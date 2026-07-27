package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/breakout"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	"github.com/nyaungnicholas-wq/signaldeck/internal/expectancy"
	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
	"github.com/nyaungnicholas-wq/signaldeck/internal/macrofeat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/micro"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ranking"
	"github.com/nyaungnicholas-wq/signaldeck/internal/regime"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
)

// decodeWeights parses a stored symbol_models.weights blob (component->weight).
// A malformed/empty blob yields nil, so the caller falls back to global weights.
func decodeWeights(blob string) map[string]float64 {
	if blob == "" {
		return nil
	}
	var w map[string]float64
	if err := json.Unmarshal([]byte(blob), &w); err != nil {
		return nil
	}
	return w
}

// decodeCalibration parses a stored symbol_models.calibration blob. ok=false on
// a malformed blob, so the caller falls back to the global calibration.
func decodeCalibration(blob string) (symbolagent.Calibration, bool) {
	if blob == "" {
		return symbolagent.Calibration{}, false
	}
	var c symbolagent.Calibration
	if err := json.Unmarshal([]byte(blob), &c); err != nil {
		return symbolagent.Calibration{}, false
	}
	return c, true
}

// calibrationPairLimit caps how many of the newest RESOLVED outcomes train the
// fleet-wide recalibration map for one horizon.
const calibrationPairLimit = 3000

// globalCalibration fits the fleet-wide recalibration map for one horizon from
// RESOLVED outcomes, and is the fallback for every symbol without a personal
// map.
//
// Two properties this function exists to hold (2026-07-26 review, C3):
//
//   - FIT ON THE VARIABLE IT IS APPLIED TO. The map is evaluated at the raw
//     blend probability, so it is fit on predictions.raw_prob. The previous
//     implementation fit on prediction_outcomes.prob — which UpsertPrediction
//     seeds from cal_prob — and applied the result to raw. A recalibration map
//     carries no meaning off the coordinate it was fit against.
//   - NOT RECURSIVE. cal_prob is this map's OWN output from the previous pass,
//     so training on it made every day's map a function of the day before's
//     map rather than of realized outcomes. Training on raw_prob paired with
//     the realized direction breaks that loop: the only feedback left is
//     through the legs, which are graded separately.
//
// ok=false means there is not enough evidence to correct anything (no resolved
// history, or ensemble.Calibrate refused the fit) — the caller then publishes
// the raw probability uncorrected rather than an invented one.
func globalCalibration(ctx context.Context, st *store.Store, h md.Horizon) (func(float64) float64, bool, error) {
	raws, ups, err := st.ResolvedRawPredictionPairs(ctx, h, calibrationPairLimit)
	if err != nil {
		return nil, false, err
	}
	if len(raws) == 0 {
		return nil, false, nil
	}
	pairs := make([]ensemble.Pair, len(raws))
	for i := range raws {
		pairs[i] = ensemble.Pair{Pred: raws[i], Actual: ups[i]}
	}
	mapFn, calibrated := ensemble.Calibrate(pairs)
	if !calibrated {
		return nil, false, nil
	}
	return mapFn, true, nil
}

// predHorizons are the horizons the ensemble predicts (forecast + expectancy
// both cover these).
var predHorizons = []md.Horizon{md.H1d, md.H1w}

// ── prequential-majority benchmark (tracked-benchmark wave) ─────────────
//
// The registry's null — the walk-forward majority-follower — is the baseline
// the ensemble keeps losing to, and until now it existed only inside the
// offline grader. Here it becomes a first-class TRACKED predictor: every pass
// commits its constant guess for the same symbols at the same ts, namespaced
// under "<horizon>#pm" in prediction_outcomes, so the registry grades it with
// the identical dedup / survivorship / day-clustered rules as the ensemble
// while every other reader (calibration fits, dashboards, the ledger) stays
// blind to it — they all filter on exact horizon values.

// benchmarkSuffix namespaces benchmark rows inside prediction_outcomes.
const benchmarkSuffix = "#pm"

// benchmarkHorizon maps an ensemble horizon to its benchmark namespace.
func benchmarkHorizon(h md.Horizon) md.Horizon { return h + benchmarkSuffix }

// benchmarkMajorityEpoch bounds the evidence the live majority-follower may
// lean on. Rows before it were graded against a survivor-seeded universe; a
// benchmark fed that evidence would be a null in name only.
//
// It reads the ONE boundary rather than restating it: a second copy of the same
// instant is a second place it can drift out of step with the registry.
const benchmarkMajorityEpoch = store.SurvivorshipEpoch

func horizonSecs(h md.Horizon) int64 {
	if h == md.H1w {
		return 7 * 86400
	}
	return 86400
}

// featureVersion stamps every persisted feature vector so the layout can
// evolve without corrupting the historical training set: bump it whenever a
// field is added/removed/rescaled, and train per version.
// v2: + sentiment_score / sentiment_n (learning-flywheel wave).
// v3 (Stage 6): + crypto microstructure (micro_*) for crypto symbols; + FRED VIX
// cross-asset features (vix_*) for all symbols; + gated model-leg probs
// (gbm_prob / meanrev_prob) recorded when they cleared their OOS gate.
// v4 (news-trends wave): + news_vol_z — today's headline count standardized
// vs the symbol's own trailing-30d baseline (news-trends worker). Absent when
// the baseline gate is unmet or the row is stale — absence is information,
// not zero. The GBM leg trains per version (LabeledFeaturesBySymbolVersion),
// so v4 rows accumulate their own labeled set and the OOS-lift gate decides
// whether the new field ever influences the live blend.
// v5 (cross-sectional alpha wave): + the DATA-EXPANSION sources as gated,
// freshness-bounded fields (see alphaxfeat.go) — per-symbol short_int_dtc,
// funding_rate (crypto), stocktwits_bull_ratio, wiki_z, tv_reco; market-wide
// pc_total + cot_spx_net (loaded once per pass, like vix_*). Every field is
// absent when its source is unavailable/stale/thin — absence is information,
// not zero. Same honesty contract as v4: nothing here touches a live output
// until a measured OOS-lift gate says it earned it.
// v6 (alphax-leg wave): + alphax_prob — the pooled cross-sectional leg's
// P(beat same-day universe median), recorded ONLY when its stored OOS lift
// cleared the gate (same treatment gbm_prob/meanrev_prob got at v3). Bumped
// for the same reason v3 bumped for gbm_prob: the GBM leg trains per version,
// so the new field must accumulate its own labeled set rather than dilute
// absent-vs-zero across the v5 rows.
// v7 (candlestick-patterns + model-fed indicators wave): + five model-fed
// technical-indicator signals (stoch_k, adx14, cci20, bb_pctb, supertrend_dir
// — see internal/indicators) and pattern_bias (the net Bias of the candlestick
// patterns firing on the latest bar — see internal/candles, indicatorfeat.go).
// These join the vector so the GBM/alphax legs LEARN whether they have edge —
// the OOS-lift gate is the referee; nothing is trusted on faith. None is a
// model output, so none is on the self-reference exclusion lists. Each is
// absent when unavailable (thin history, a degenerate window, or no pattern
// firing). Bumped for the same reason every prior field-adding wave bumped:
// the per-symbol GBM trains per version, so v7 rows accumulate their own
// labeled set rather than dilute absent-vs-zero across the v6 rows.
// v9 (alpha-source wave): + two documented-edge sources that were ingested but
// never reached the model — insider_net_ratio (Form 4 open-market net-buy ratio
// over 90d, scale-free in [-1,1]) and short_vol_z (daily Reg SHO short-sale
// VOLUME ratio z vs the symbol's own baseline; distinct from bi-monthly short
// INTEREST). Both stocks-only, gated + freshness-bounded (alphaxfeat.go), absent
// when unavailable. Same honesty contract: the OOS-lift gate is the referee —
// neither touches a live output until it earns it. Bumped so the per-symbol GBM
// accumulates a clean v9 labeled set instead of diluting absent-vs-zero.
// v10 (TradingView webhook wave): + tv_webhook_signal — the direction of the
// latest TradingView Pine alert this symbol PUSHED to our webhook (the only
// ToS-compliant TV data path; the TV MCP is assistant-only, unreachable by the
// daemon). Sparse + weak (public technical crossings), so absence is the norm
// and the OOS-lift gate will almost certainly find it immaterial — it earns its
// way in or it doesn't, same contract as every other external source.
// v11 (macro-breadth wave): + the REST of the free FRED panel, which had been
// ingested (~104k observations across twelve series) with exactly one series
// (VIXCLS) ever reaching the model. Adds macro_<key>_pct / macro_<key>_chg for
// the yield curve (DGS10/DGS2/T10Y2Y/T10Y3M), the policy rate (DFF), risk
// appetite (BAMLH0A0HYM2 high-yield spread, NFCI financial conditions) and oil
// (DCOILWTICO) — see internal/macrofeat. Market-wide, so identical across
// symbols at a given ts; their value is TEMPORAL, letting the tree condition a
// symbol's own features on the prevailing macro state. Two deliberate
// restrictions: the REVISED series (CPIAUCSL/M2SL/UNRATE) are excluded because
// only current values are stored and feeding a restated number is lookahead;
// and levels are encoded as trailing PERCENTILE + squashed CHANGE rather than
// raw, because a raw level teaches a tree to split on an era instead of a
// state. Absent when a series is missing/thin/degenerate. Same contract as every
// prior source wave: the OOS-lift gate is the referee, nothing is trusted on
// faith. Bumped so the per-symbol GBM accumulates a clean v11 labeled set
// instead of diluting absent-vs-zero across v10 rows.
// v12 (informational-component repair): comp_vol_regime is REMOVED and
// comp_vol_regime_value added. The old key stored the score component's
// Contrib, which for a weight-0 component is algebraically zero — measured
// over the live store at v11, 248,390 rows with min = max = 0. The column
// carried no market state; its only variation was the derived "__has" bit,
// which says a 90-bar window exists, so a tree splitting on it learns recency.
// The Bollinger-width percentile it discards is genuinely informative (it is
// the input to the one independently replicated edge in the platform) and is
// distinct from bb_pctb, which is band POSITION, not band WIDTH. Renamed
// rather than redefined in place: alphax pools every version from v3 up, so a
// key that silently changed meaning would be pooled across the change. Bumped
// for the standard reason — the per-symbol GBM trains per version, so v12 rows
// accumulate their own labeled set instead of diluting absent-vs-zero across
// v11. Cost: the per-symbol GBM restarts from an empty v12 history (v11 held
// 7,070 rows) and legs stay gated out until v12 clears its own OOS-lift gate.
const featureVersion = 12

// ledgerModelVersion stamps each hash-chained ledger entry with the version of
// the prediction MODEL/pipeline that produced it (Stage 3 tamper-evident
// ledger). Bump when the ensemble/calibration/feature pipeline changes in a way
// that changes emitted probabilities, so an auditor can see which model era a
// committed prediction belongs to. Independent of featureVersion (that stamps
// the training-set layout; this stamps the committed prediction).
const ledgerModelVersion = 1

// Sentiment feature gates: the daily aggregate joins the blend only when it
// rests on at least sentimentMinHeadlines rated headlines and is at most
// sentimentMaxAgeDays old. Thin or stale sentiment is ABSENT, not zero.
const (
	sentimentMinHeadlines = 3
	sentimentMaxAgeDays   = 3
)

// buildFeatureVector assembles the EXACT inputs used for one prediction into
// a flat name->value map (the feature store's row payload). Optional signals
// are simply absent — absence is information, not zero. The regime label is
// one-hot encoded ("regime_<label>"=1) and the prediction's own raw +
// calibrated probabilities are included so the labeled set can grade the
// calibration layer itself.
//
// The four A7 shortcut keys (forecast_prob / forecast_lift /
// expectancy_hit_rate / n_used) are NOT written at all. They were only ever
// gated out of training by gbm.SelfReferentialKey, no non-test reader ever
// consumed them back out of a row, and cmd/selfref-ablation measured their
// restoration as worthless (+0.026 mean lift, zero admission-gate flips), so
// they are deleted at the source rather than built and then banned. No
// featureVersion bump: the trained layout is unchanged, because these keys
// never survived the exclusion predicate anyway.
// extra carries cross-cutting feature maps (Stage 6): crypto microstructure
// (micro_*) for crypto symbols and FRED VIX (vix_*) for all symbols. Each is
// merged verbatim; absence of a map means those features are simply not present
// for this row (absence is information, not zero).
func buildFeatureVector(sc md.Score, c ensemble.Components, raw, cal float64, regimeLbl string, rankPct *float64, sentN int, extra ...map[string]float64) map[string]float64 {
	vec := map[string]float64{
		"pressure_score": c.PressureScore,
		"pred_raw":       raw,
		"pred_cal":       cal,
	}
	for _, comp := range sc.Components {
		// A weight-0 component is INFORMATIONAL: its Contrib is Norm × Weight,
		// so storing it writes an algebraic constant into every row, and the
		// derived "__has" bit becomes the column's only variation — encoding
		// "enough history exists", a data-completeness proxy a tree can split
		// on to learn recency. Store the READING instead, under its own key so
		// no existing key's meaning changes for readers pooling old versions.
		if comp.Weight == 0 {
			vec["comp_"+comp.Name+"_value"] = comp.Value
			continue
		}
		vec["comp_"+comp.Name] = comp.Contrib
	}
	if c.SentimentScore != nil {
		vec["sentiment_score"] = *c.SentimentScore
		vec["sentiment_n"] = float64(sentN)
	}
	// STAGE 6 gated model legs: recorded ONLY when they cleared their OOS gate
	// (LegProbabilities semantics), so the labeled training set (and the adaptive
	// attribution reading it back) sees exactly the leg the live blend used.
	if c.GBMProb != nil && c.GBMLift != nil && *c.GBMLift > 0 {
		vec["gbm_prob"] = *c.GBMProb
	}
	if c.MeanRevProb != nil && c.MeanRevLift != nil && *c.MeanRevLift > 0 {
		vec["meanrev_prob"] = *c.MeanRevProb
	}
	if c.AlphaXProb != nil && c.AlphaXLift != nil && *c.AlphaXLift > 0 {
		vec["alphax_prob"] = *c.AlphaXProb
	}
	if regimeLbl != "" {
		vec["regime_"+regimeLbl] = 1
	}
	if rankPct != nil {
		vec["rank_pct"] = *rankPct
	}
	// Merge cross-cutting feature maps (Stage 6 micro_* / vix_*; v4 news_vol_z).
	for _, m := range extra {
		for k, v := range m {
			vec[k] = v
		}
	}
	return vec
}

// ── PredictionRunner: the calibrated ensemble ───────────────────────────

// PredictionRunner fuses the pressure score, expectancy tendency, the
// backtested forecast, and (when fresh and deep enough) the daily sentiment
// aggregate into ONE probability — blended with per-regime weights LEARNED
// from resolved outcomes when the honesty gate allows, static equal prior
// otherwise — then CALIBRATES it against the symbol's own realized history.
// The flagship honest prediction.
type PredictionRunner struct {
	St *store.Store
}

func (w *PredictionRunner) Name() string            { return "prediction-runner" }
func (w *PredictionRunner) Interval() time.Duration { return 10 * time.Minute }

func (w *PredictionRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	ts := time.Now().Truncate(time.Minute).Unix()
	// One-query-per-fleet context for the feature vectors (regime label +
	// cross-sectional ranking percentile). Best-effort: an error only means
	// those features are absent from this pass's vectors.
	regimeLbls, err := w.St.RegimeLabels(ctx)
	if err != nil {
		regimeLbls = map[int64]string{}
	}
	rankPcts, err := w.St.RankingPercentiles(ctx)
	if err != nil {
		rankPcts = map[int64]float64{}
	}
	// Adaptive per-regime weights, loaded ONCE per run. Absent/invalid data
	// simply means adaptive.Pick falls through to the static equal prior —
	// the exact pre-flywheel behavior.
	var learned adaptive.Weights
	if raw, err := w.St.GetMeta(ctx, adaptive.MetaKey); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &learned); err != nil {
			learned = adaptive.Weights{}
		}
	}
	// STAGE 6 — FRED VIX cross-asset feature, loaded ONCE per pass (the VIX level
	// is market-wide, identical for every symbol at this ts). Present for ALL
	// symbols when a VIX observation exists; absent (nil) otherwise. Best-effort:
	// a read error or no data simply means the vix_* features are omitted.
	var vixMap map[string]float64
	if vix, ok, err := w.St.LatestVIX(ctx); err == nil && ok && vix > 0 {
		vixMap = macrofeat.FromVIX(vix).Map()
	}
	// MACRO-BREADTH WAVE (featureVersion 11) — the rest of the free FRED panel
	// (curve/policy/credit/conditions/oil), loaded ONCE per pass for the same
	// reason vix_* is: these are market-wide levels, identical for every symbol
	// at this ts. Best-effort throughout — an unreadable or thin series simply
	// contributes no keys this pass. Revised series are excluded at the
	// macrofeat layer, not here, so the admissibility rule lives with the
	// encoding it protects.
	macroMap := macroPanelFeatures(ctx, w.St)
	// CROSS-SECTIONAL ALPHA wave (featureVersion 5) — market-wide new-source
	// features (pc_total / cot_spx_net), loaded ONCE per pass like vix_*.
	// Best-effort + gate-honoring: stale or absent sources mean the fields are
	// simply absent from every vector this pass (see alphaxfeat.go).
	alphaMktMap := alphaMarketFeatures(ctx, w.St, time.Now())
	// CADENCE SPLIT (live-everything wave): hot set + crypto every run; the
	// broad universe every 10m while the market is open / once per UTC day
	// closed (see universecadence.go — universe bars are minute-live now).
	doUniverse, universeCursor := universeDue(ctx, w.St, "predict_universe_day", time.Now())
	// Fleet-wide fallback calibration, fit ONCE per horizon per pass. The
	// resolved-outcome set it trains on cannot change mid-pass (this pass only
	// writes UNresolved rows), so hoisting is output-identical and drops one
	// 3000-row join per symbol.
	globalCal := map[md.Horizon]func(float64) float64{}
	for _, h := range predHorizons {
		if fn, ok, err := globalCalibration(ctx, w.St, h); err == nil && ok {
			globalCal[h] = fn
		} else if err != nil {
			slog.Warn("global calibration: fit failed, publishing uncorrected probabilities", "horizon", h, "err", err)
		}
	}
	// PREQUENTIAL-MAJORITY BENCHMARK: the constant guess is decided ONCE per
	// pass per horizon, from the deduplicated resolved record over UTC days
	// strictly before today — committed before today's outcome can exist, so
	// the benchmark never sees the move it will be graded on. A failed read
	// skips the benchmark this pass rather than inventing a guess.
	benchProb := map[md.Horizon]float64{}
	todayUTC := time.Now().UTC().Unix() / 86400
	for _, h := range predHorizons {
		if p, err := w.St.PrequentialMajorityProb(ctx, h, todayUTC, benchmarkMajorityEpoch); err == nil {
			benchProb[h] = p
		} else {
			slog.Warn("prequential-majority benchmark: majority read failed — skipping this pass", "horizon", h, "err", err)
		}
	}
	n, featErrs, staleCals := 0, 0, 0
	for _, s := range syms {
		hot := s.Market == md.Crypto || s.Stream
		if !hot && !doUniverse {
			continue // daily-only universe symbol already predicted today
		}
		daily, minute, err := loadBars(ctx, w.St, s.ID)
		if err != nil {
			return "", err
		}
		states := expectancy.CurrentStateKeys(daily, minute)
		forecasts, err := w.St.Forecasts(ctx, s.ID)
		if err != nil {
			return "", err
		}
		// STAGE 6 — the symbol's gated model legs (GBM + mean-reversion), trained
		// by the gbm-trainer worker. Loaded once per symbol; the leg is fed into
		// the blend below ONLY when its stored lift > 0 (LegProbabilities gate).
		modelFcs, err := w.St.ModelForecasts(ctx, s.ID)
		if err != nil {
			return "", err
		}
		// STAGE 6 — crypto MICROSTRUCTURE features (per crypto symbol, shared
		// across horizons): derived from the last ~2 minutes of 1s snapshots at
		// or before now. Absent for non-crypto or when the book window is thin.
		var microMap map[string]float64
		if s.Market == md.Crypto {
			now := time.Now().Unix()
			if snaps, err := w.St.Snaps(ctx, s.ID, now-120, now+1, 121); err == nil {
				if mf, ok := micro.Extract(snaps); ok {
					microMap = mf.Map()
				}
			}
		}
		// NEWS-TRENDS (featureVersion 4) — news_vol_z: the symbol's freshest
		// stored news-volume z (<=2 UTC days old), computed by the news-trends
		// worker against the symbol's OWN trailing-30d baseline. Best-effort and
		// gate-honoring: no fresh row, a NULL z (baseline gate unmet) or a read
		// error all mean the field is simply ABSENT from the vector.
		var newsMap map[string]float64
		if z, ok, err := w.St.LatestNewsTrendZ(ctx, s.ID, 2); err == nil && ok {
			newsMap = map[string]float64{"news_vol_z": z}
		}
		// CROSS-SECTIONAL ALPHA wave (featureVersion 5) — per-symbol
		// new-source features (short_int_dtc / funding_rate /
		// stocktwits_bull_ratio / wiki_z / tv_reco), each gate-honoring and
		// freshness-bounded; absent fields stay absent (see alphaxfeat.go).
		alphaSymMap := alphaSymbolFeatures(ctx, w.St, s, time.Now())
		// CANDLESTICK-PATTERNS + MODEL-FED INDICATORS wave (featureVersion 7) —
		// model-fed technical-indicator signals + candlestick pattern_bias,
		// derived from THIS symbol's daily bars (shared across horizons). Each
		// field is absent when unavailable (see indicatorfeat.go).
		idxMap := indicatorPatternFeatures(daily)
		// TREND-STRUCTURE wave (featureVersion 8) — geometric trend read (class,
		// slope, distance-to-support/resistance, channel) as learnable features;
		// see trendfeat.go. Absent-when-uncomputable, gated by OOS lift.
		trendMap := trendFeatures(daily)
		// Sentiment feature (per symbol, shared across horizons): the latest
		// daily aggregate, only when fresh (<=3 days) AND resting on enough
		// headlines (n>=3). Best-effort — a read error means "absent".
		var sentScore *float64
		sentN := 0
		if mean, sn, ok, err := w.St.LatestSentiment(ctx, s.ID, sentimentMaxAgeDays); err == nil && ok && sn >= sentimentMinHeadlines {
			sentScore, sentN = &mean, sn
		}
		// Global learned weights for THIS symbol's regime cell (the fallback
		// when the symbol has no personal model of its own).
		regimeWts, _ := adaptive.Pick(learned, regimeLbls[s.ID])
		for _, h := range predHorizons {
			sc, ok, err := w.St.LatestScore(ctx, s.ID, h)
			if err != nil {
				return "", err
			}
			if !ok {
				continue
			}
			c := ensemble.Components{PressureScore: sc.Score, SentimentScore: sentScore}
			// Expectancy hit rate for the current state.
			if rows, err := w.St.Expectancy(ctx, s.ID, h); err == nil {
				if row, ok := expectancy.Lookup(rows, states[h]); ok {
					hr := row.HitRate
					c.ExpectancyHitRate = &hr
				}
			}
			// Forecast prob + lift (lift gates whether it is trusted).
			for _, f := range forecasts {
				if f.Horizon == h {
					p, l := f.Prob, f.Lift
					c.ForecastProb, c.ForecastLift = &p, &l
				}
			}
			// STAGE 6 gated model legs. Each carries its stored OOS lift; the
			// ensemble includes the leg ONLY when lift > 0 (LegProbabilities). An
			// absent leg (never trained, too little history, or a row older than
			// maxModelForecastAgeSecs — M3 freshness cap) contributes nothing —
			// the blend is exactly the pre-Stage-6 blend.
			if p, l, ok := modelLegProbLift(modelFcs, h, store.ModelGBM, ts); ok {
				c.GBMProb, c.GBMLift = &p, &l
			}
			if p, l, ok := modelLegProbLift(modelFcs, h, store.ModelMeanRev, ts); ok {
				c.MeanRevProb, c.MeanRevLift = &p, &l
			}
			// Cross-sectional alpha leg (alphax-leg wave): rows exist in
			// model_forecasts ONLY while the pooled model's OOS lift > 0 (the
			// AlphaXTrainer deletes them on a gated regrade), and the ensemble
			// re-applies the same lift>0 gate. NOTE the category nuance: this
			// prob is RELATIVE (beat the same-day universe median), blended as
			// a directional tilt — see ensemble.Components.AlphaXProb.
			if p, l, ok := modelLegProbLift(modelFcs, h, store.ModelAlphaX, ts); ok {
				c.AlphaXProb, c.AlphaXLift = &p, &l
			}
			// PRESSURE LEG GATE: the pressure score is the platform's oldest base
			// leg, but the resolved record shows its fixed-weight directional call
			// is anti-predictive at 1d/1w. The pressure-trainer grades it
			// walk-forward and stores the OOS lift like a model leg; here we inject
			// only the LIFT (the leg's PROB stays live from sc.Score above). A
			// MEASURED lift <= 0 benches the leg in LegProbabilities; an unmeasured
			// leg (no fresh row) is kept — fail-safe against a cold trainer.
			if _, l, ok := modelLegProbLift(modelFcs, h, store.ModelPressure, ts); ok {
				c.PressureLift = &l
			}
			// PER-SYMBOL AGENTS: pick weights + calibration by tier order
			//   personal(symbol) -> global-regime -> global -> static.
			// When this symbol has EARNED a personal model (tier=personal), use
			// ITS weights AND its own prequential calibration; otherwise fall
			// back to the global per-regime weights and the global calibration
			// (the exact pre-per-symbol behavior). No feature capture changes;
			// no leakage introduced (the personal calibration was fit only on
			// this symbol's already-resolved pairs, never on the live point).
			wts := regimeWts
			usePersonalCal := false
			var personalCal func(float64) float64
			if pm, ok, err := w.St.SymbolModel(ctx, s.ID, h); err == nil && ok && pm.Tier == symbolagent.TierPersonal {
				if pw := decodeWeights(pm.Weights); len(pw) > 0 {
					wts = pw
				}
				// Only take the personal calibration when it actually FITTED
				// (>=MinCalibrationPairs, real spread) AND its persisted knots
				// still pass ensemble.ValidateKnots. 495 of 928 stored maps were
				// non-monotone before the isotonic fix (2026-07-26 review, C3);
				// those rows survive until the hourly per-symbol learner refits
				// them, and serving one inverts the published probability
				// against the raw input. A REFUSED map falls through to the
				// global calibration — a coarser correction, never a scrambled
				// one. An unfitted personal map falls through for the same
				// reason (it would otherwise emit an UNcalibrated prob while a
				// still-learning symbol gets the global map).
				if cal, ok := decodeCalibration(pm.Calibration); ok && cal.Fitted {
					if fn, err := ensemble.MapFromKnotsChecked(cal.KX, cal.KY); err == nil {
						personalCal, usePersonalCal = fn, true
					} else {
						staleCals++
					}
				}
			}
			raw, nUsed := ensemble.WeightedProbability(c, wts)
			cal := raw
			if usePersonalCal {
				// Personal tier: recalibrate with the symbol's OWN isotonic map.
				cal = personalCal(raw)
			} else if fn, ok := globalCal[h]; ok {
				// Fleet-wide fallback, fit on raw_prob against realized
				// outcomes — the SAME variable it is applied to here.
				cal = fn(raw)
			}
			comps, _ := json.Marshal(c)
			if err := w.St.UpsertPrediction(ctx, store.Prediction{
				SymbolID: s.ID, Horizon: h, Ts: ts,
				RawProb: raw, CalProb: cal, NUsed: nUsed, Components: string(comps),
			}); err != nil {
				return "", err
			}
			n++
			// Benchmark row for the SAME (symbol, ts): identical universe,
			// identical resolution path, identical dedup keys — the registry
			// grades both under one set of rules, which is the entire point.
			// Best-effort: the benchmark must never break the prediction it
			// exists to hold to account.
			if bp, ok := benchProb[h]; ok {
				if err := w.St.SeedBenchmarkOutcome(ctx, s.ID, benchmarkHorizon(h), ts, bp); err != nil {
					slog.Warn("prequential-majority benchmark: seed failed", "symbol", s.Symbol, "horizon", h, "err", err)
				}
			}
			// Feature store: persist the full input vector this prediction
			// used. Failure must NOT fail the prediction — log + dq metric.
			var rankPct *float64
			if pct, ok := rankPcts[s.ID]; ok {
				rankPct = &pct
			}
			vec := buildFeatureVector(sc, c, raw, cal, regimeLbls[s.ID], rankPct, sentN, microMap, vixMap, macroMap, newsMap, alphaSymMap, alphaMktMap, idxMap, trendMap)
			if err := w.St.InsertFeatures(ctx, s.ID, h, ts, featureVersion, vec); err != nil {
				featErrs++
				slog.Warn("feature store: persist failed", "symbol", s.Symbol, "horizon", h, "err", err)
				sid := s.ID
				_ = w.St.InsertDQ(ctx, md.DQEvent{
					SymbolID: &sid, Ts: time.Now().Unix(),
					Kind: "feature_store_error", Detail: fmt.Sprintf("horizon %s: %v", h, err),
				})
			}
			// STAGE 3 — append-only, hash-chained prediction ledger. Commit the
			// prediction's identity to the tamper-evident chain AFTER the
			// prediction + feature vector are persisted, and BEFORE any outcome
			// can exist (the resolver runs on its own cadence). feature_hash is
			// the sha256 of the SAME vector we just wrote, so the committed hash
			// is reproducible from the persisted row. Best-effort: a ledger
			// failure logs + records a dq event but MUST NOT fail the prediction
			// (the prediction is already durably written above).
			if entry, lerr := w.St.AppendLedger(ctx, store.LedgerEntry{
				PredictedAt:  time.Now().Unix(),
				SymbolID:     s.ID,
				Horizon:      h,
				BarTs:        ts,
				RawProb:      raw,
				CalProb:      cal,
				FeatureHash:  store.HashFeatureVector(vec),
				ModelVersion: ledgerModelVersion,
			}); lerr != nil {
				slog.Warn("prediction ledger: append failed", "symbol", s.Symbol, "horizon", h, "err", lerr)
				sid := s.ID
				_ = w.St.InsertDQ(ctx, md.DQEvent{
					SymbolID: &sid, Ts: time.Now().Unix(),
					Kind: "ledger_append_error", Detail: fmt.Sprintf("horizon %s: %v", h, lerr),
				})
			} else {
				// Lineage spine (Layers 2+8): tie the ledgered prediction to
				// each MODEL LEG that actually contributed to its blend (nil
				// pointer = leg absent or gated off, so no edge — an edge
				// means "this leg's probability was in the mix"). Idempotent
				// upserts, best-effort: a lineage failure must not fail the
				// prediction (already durably ledgered above).
				for _, leg := range []struct {
					name string
					on   bool
				}{
					{store.ModelGBM, c.GBMProb != nil},
					{store.ModelMeanRev, c.MeanRevProb != nil},
					{store.ModelAlphaX, c.AlphaXProb != nil},
				} {
					if !leg.on {
						continue
					}
					_ = lineage.Link(ctx, w.St, lineage.Edge{
						SrcKind:  lineage.KindPrediction,
						SrcID:    fmt.Sprintf("%d", entry.Seq),
						DstKind:  lineage.KindModel,
						DstID:    leg.name + ":" + string(h),
						EdgeKind: lineage.EdgeGeneratedBy, MetaJSON: lineage.RevMeta(),
					})
				}
				// Prediction attribution (Layer 6): persist the top-N named
				// parts of THIS ledgered prediction's raw blend (comp_*
				// components + legs, probability deltas from the 0.5 prior).
				// Best-effort like the ledger append itself.
				WriteLedgerAttribution(ctx, w.St, entry.Seq, s.ID, h, ts, sc, c, wts)
			}
		}
	}
	if doUniverse {
		// Only after a clean full pass, so a mid-run error retries next minute.
		_ = w.St.SetMeta(ctx, "predict_universe_day", universeCursor)
	}
	detail := fmt.Sprintf("wrote %d predictions", n)
	if featErrs > 0 {
		detail += fmt.Sprintf(" (%d feature-vector write(s) failed — see dq)", featErrs)
	}
	if staleCals > 0 {
		// Visible, not silent: these symbols are on the global calibration
		// because their stored per-symbol map failed the monotonicity
		// assertion. The count must fall to 0 as the per-symbol learner refits.
		detail += fmt.Sprintf(" (%d stale non-monotone per-symbol map(s) refused — using global calibration until refit)", staleCals)
		slog.Warn("calibration: refused stale per-symbol maps", "count", staleCals)
	}
	return detail, nil
}

// PredictionResolver grades past predictions (feeds the calibration curve).
type PredictionResolver struct {
	St *store.Store
}

func (w *PredictionResolver) Name() string            { return "prediction-resolver" }
func (w *PredictionResolver) Interval() time.Duration { return 10 * time.Minute }

func (w *PredictionResolver) Run(ctx context.Context) (string, error) {
	now := time.Now().Unix()
	resolved := 0
	for _, h := range predHorizons {
		// The prequential-majority benchmark rows ("<horizon>#pm") resolve
		// through the exact same path on the exact same horizon clock —
		// identical grading rules is the entire point of tracking the
		// benchmark as a predictor.
		for _, hh := range []md.Horizon{h, benchmarkHorizon(h)} {
			pending, err := w.St.UnresolvedPredictions(ctx, hh, now-horizonSecs(h), 1500)
			if err != nil {
				return "", err
			}
			for _, p := range pending {
				base, okB, err := w.St.BarAtOrBefore(ctx, p.SymbolID, md.TF1d, p.Ts)
				if err != nil {
					return "", err
				}
				if !okB {
					continue
				}
				target := base.Ts + horizonSecs(h)
				if now < target {
					continue
				}
				fwd, okF, err := w.St.BarAtOrAfter(ctx, p.SymbolID, md.TF1d, target)
				if err != nil {
					return "", err
				}
				if !okF || base.Close <= 0 || fwd.Ts-target > 3*horizonSecs(h) {
					continue
				}
				if err := w.St.ResolvePrediction(ctx, p.SymbolID, hh, p.Ts, fwd.Close/base.Close-1); err != nil {
					return "", err
				}
				resolved++
			}
		}
	}
	return fmt.Sprintf("resolved %d predictions", resolved), nil
}

// ── RegimeRunner: label + change detection ──────────────────────────────

type RegimeRunner struct {
	St *store.Store
}

func (w *RegimeRunner) Name() string            { return "regime-runner" }
func (w *RegimeRunner) Interval() time.Duration { return 30 * time.Minute }

func (w *RegimeRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	n, changes := 0, 0
	for _, s := range syms {
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			return "", err
		}
		st, ok := regime.Classify(daily)
		if !ok {
			continue
		}
		ts := daily[len(daily)-1].Ts
		if err := w.St.UpsertRegime(ctx, s.ID, ts, string(st.Label), st.Strength, st.Note); err != nil {
			return "", err
		}
		n++
	}
	// Count recent changes surfaced this pass (informational).
	if cs, err := w.St.RecentRegimeChanges(ctx, 5); err == nil {
		changes = len(cs)
	}
	return fmt.Sprintf("classified %d symbols (%d recent changes on record)", n, changes), nil
}

// ── RankingRunner: cross-sectional relative strength ────────────────────

type RankingRunner struct {
	St *store.Store
}

func (w *RankingRunner) Name() string            { return "ranking-runner" }
func (w *RankingRunner) Interval() time.Duration { return time.Hour }

func (w *RankingRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	var metrics []ranking.Metric
	idBySym := map[string]int64{}
	for _, s := range syms {
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			return "", err
		}
		m, ok := ranking.FromBars(s.Symbol, daily)
		if !ok {
			continue
		}
		metrics = append(metrics, m)
		idBySym[s.Symbol] = s.ID
	}
	if len(metrics) < 2 {
		return "not enough symbols to rank", nil
	}
	ranked := ranking.RelativeStrength(metrics)
	ts := time.Now().Truncate(time.Minute).Unix()
	rows := make([]struct {
		SymbolID     int64
		Score        float64
		Rank         int
		Ret1M, Ret3M float64
	}, 0, len(ranked))
	for _, r := range ranked {
		rows = append(rows, struct {
			SymbolID     int64
			Score        float64
			Rank         int
			Ret1M, Ret3M float64
		}{idBySym[r.Symbol], r.Score, r.Rank, r.Return1M, r.Return3M})
	}
	if err := w.St.ReplaceRanking(ctx, ts, rows); err != nil {
		return "", err
	}
	return fmt.Sprintf("ranked %d symbols", len(ranked)), nil
}

// ── BreakoutRunner: trend-creation events + correlation breaks ──────────

type BreakoutRunner struct {
	St *store.Store
}

func (w *BreakoutRunner) Name() string            { return "breakout-runner" }
func (w *BreakoutRunner) Interval() time.Duration { return 15 * time.Minute }

func (w *BreakoutRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	inserted := 0
	var series []breakout.Series
	for _, s := range syms {
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			return "", err
		}
		closes := make([]float64, len(daily))
		tss := make([]int64, len(daily))
		for i, b := range daily {
			closes[i] = b.Close
			tss[i] = b.Ts
		}
		series = append(series, breakout.Series{Symbol: s.Symbol, Closes: closes, Ts: tss})
		for _, sig := range breakout.Detect(daily) {
			// Dedup: skip if we already logged this kind at/after this ts.
			last, err := w.St.LastBreakoutTs(ctx, s.ID, sig.Kind)
			if err != nil {
				return "", err
			}
			if last >= sig.Ts {
				continue
			}
			sid := s.ID
			if err := w.St.InsertBreakout(ctx, &sid, sig.Ts, sig.Kind, sig.Detail, sig.Strength); err != nil {
				return "", err
			}
			inserted++
		}
	}
	// Correlation breaks across the whole watchlist (recent 20d vs base 90d).
	// Throttle to every ~6h so the feed doesn't repeat the same pair each pass.
	now := time.Now()
	last, _ := w.St.GetMeta(ctx, "corr_break_last")
	lastTs := int64(0)
	_, _ = fmt.Sscanf(last, "%d", &lastTs)
	if now.Unix()-lastTs > 6*3600 {
		for _, b := range breakout.CorrelationBreaks(series, 20, 90) {
			detail := fmt.Sprintf("%s vs %s: recent r=%.2f, base r=%.2f (change %.2f)", b.A, b.B, b.RecentR, b.BaseR, b.Delta)
			if err := w.St.InsertBreakout(ctx, nil, now.Unix(), "correlation_break", detail, b.Delta); err != nil {
				return "", err
			}
			inserted++
		}
		_ = w.St.SetMeta(ctx, "corr_break_last", fmt.Sprintf("%d", now.Unix()))
	}
	return fmt.Sprintf("%d event(s) logged", inserted), nil
}

// ─────────────────────────────────────────────────────────────────────────
// MACRO-BREADTH WAVE (appended block).

// macroPanelLookback is how many observations of each FRED series are loaded.
// Enough to cover macrofeat's trailing percentile window with room for the
// change lookback, and small enough that eight series cost one cheap read each.
const macroPanelLookback = macrofeat.PercentileWindow + 60

// macroPanelFeatures loads the admissible FRED series and encodes them as
// market-wide model features.
//
// Best-effort by design: this runs inside the prediction pass, and a macro read
// failing must degrade the vector, never the prediction. A series that errors,
// is missing, or is too thin contributes no keys — absence is already
// distinguished from zero everywhere downstream.
func macroPanelFeatures(ctx context.Context, st *store.Store) map[string]float64 {
	hist := make(map[string][]macrofeat.Point, len(macrofeat.AdmissibleSeries))
	for _, s := range macrofeat.AdmissibleSeries {
		pts, err := st.MacroSeries(ctx, s.ID, macroPanelLookback)
		if err != nil || len(pts) == 0 {
			continue
		}
		conv := make([]macrofeat.Point, 0, len(pts))
		for _, p := range pts {
			conv = append(conv, macrofeat.Point{Ts: p.Ts, Value: p.Value})
		}
		hist[s.ID] = conv
	}
	if len(hist) == 0 {
		return nil
	}
	return macrofeat.FromSeries(hist)
}
