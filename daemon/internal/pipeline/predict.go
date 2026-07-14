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

// predHorizons are the horizons the ensemble predicts (forecast + expectancy
// both cover these).
var predHorizons = []md.Horizon{md.H1d, md.H1w}

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
const featureVersion = 7

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
// extra carries cross-cutting feature maps (Stage 6): crypto microstructure
// (micro_*) for crypto symbols and FRED VIX (vix_*) for all symbols. Each is
// merged verbatim; absence of a map means those features are simply not present
// for this row (absence is information, not zero).
func buildFeatureVector(sc md.Score, c ensemble.Components, raw, cal float64, nUsed int, regimeLbl string, rankPct *float64, sentN int, extra ...map[string]float64) map[string]float64 {
	vec := map[string]float64{
		"pressure_score": c.PressureScore,
		"pred_raw":       raw,
		"pred_cal":       cal,
		"n_used":         float64(nUsed),
	}
	for _, comp := range sc.Components {
		vec["comp_"+comp.Name] = comp.Contrib
	}
	if c.ExpectancyHitRate != nil {
		vec["expectancy_hit_rate"] = *c.ExpectancyHitRate
	}
	if c.ForecastProb != nil {
		vec["forecast_prob"] = *c.ForecastProb
	}
	if c.ForecastLift != nil {
		vec["forecast_lift"] = *c.ForecastLift
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
	// CROSS-SECTIONAL ALPHA wave (featureVersion 5) — market-wide new-source
	// features (pc_total / cot_spx_net), loaded ONCE per pass like vix_*.
	// Best-effort + gate-honoring: stale or absent sources mean the fields are
	// simply absent from every vector this pass (see alphaxfeat.go).
	alphaMktMap := alphaMarketFeatures(ctx, w.St, time.Now())
	// CADENCE SPLIT (live-everything wave): hot set + crypto every run; the
	// broad universe every 10m while the market is open / once per UTC day
	// closed (see universecadence.go — universe bars are minute-live now).
	doUniverse, universeCursor := universeDue(ctx, w.St, "predict_universe_day", time.Now())
	n, featErrs := 0, 0
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
				// (>=MinCalibrationPairs, real spread). An unfitted personal
				// map is the identity, so without this guard a just-graduated
				// symbol would emit an UNcalibrated prob while a still-learning
				// one gets the global calibration — a quality regression. When
				// unfitted, fall through to the global-calibration branch.
				if cal, ok := decodeCalibration(pm.Calibration); ok && cal.Fitted {
					personalCal, usePersonalCal = cal.Map(), true
				}
			}
			raw, nUsed := ensemble.WeightedProbability(c, wts)
			cal := raw
			if usePersonalCal {
				// Personal tier: recalibrate with the symbol's OWN isotonic map.
				cal = personalCal(raw)
			} else if probs, ups, err := w.St.ResolvedPredictionPairs(ctx, h, 3000); err == nil && len(probs) > 0 {
				pairs := make([]ensemble.Pair, len(probs))
				for i := range probs {
					pairs[i] = ensemble.Pair{Pred: probs[i], Actual: ups[i]}
				}
				if mapFn, calibrated := ensemble.Calibrate(pairs); calibrated {
					cal = mapFn(raw)
				}
			}
			comps, _ := json.Marshal(c)
			if err := w.St.UpsertPrediction(ctx, store.Prediction{
				SymbolID: s.ID, Horizon: h, Ts: ts,
				RawProb: raw, CalProb: cal, NUsed: nUsed, Components: string(comps),
			}); err != nil {
				return "", err
			}
			n++
			// Feature store: persist the full input vector this prediction
			// used. Failure must NOT fail the prediction — log + dq metric.
			var rankPct *float64
			if pct, ok := rankPcts[s.ID]; ok {
				rankPct = &pct
			}
			vec := buildFeatureVector(sc, c, raw, cal, nUsed, regimeLbls[s.ID], rankPct, sentN, microMap, vixMap, newsMap, alphaSymMap, alphaMktMap, idxMap)
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
			if _, lerr := w.St.AppendLedger(ctx, store.LedgerEntry{
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
		pending, err := w.St.UnresolvedPredictions(ctx, h, now-horizonSecs(h), 1500)
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
			if err := w.St.ResolvePrediction(ctx, p.SymbolID, h, p.Ts, fwd.Close/base.Close-1); err != nil {
				return "", err
			}
			resolved++
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
	fmt.Sscanf(last, "%d", &lastTs)
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
