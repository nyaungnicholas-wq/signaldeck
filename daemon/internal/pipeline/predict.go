package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/breakout"
	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
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

// crossSectionRecord is one pass's measured cross-section, stamped with the UTC
// day it describes. Stored in meta rather than derived from the predictions
// table because a withheld pass writes no predictions, and a gate that can only
// read published rows can never observe its own release.
type crossSectionRecord struct {
	Day string `json:"day"`
	ensemble.CrossSection
}

const crossSectionMetaPrefix = "crosssection:"

func loadCrossSection(ctx context.Context, st *store.Store, h md.Horizon) (*crossSectionRecord, error) {
	raw, err := st.GetMeta(ctx, crossSectionMetaPrefix+string(h))
	if err != nil || raw == "" {
		return nil, err
	}
	var rec crossSectionRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// saveCrossSection records what this pass would publish. Best-effort: failing to
// store the shape must never fail the pass that produced it, and a missing
// record reads as "nothing to gate on" rather than as a collapse.
func saveCrossSection(ctx context.Context, st *store.Store, h md.Horizon, day string, cs ensemble.CrossSection) {
	blob, err := json.Marshal(crossSectionRecord{Day: day, CrossSection: cs})
	if err != nil {
		return
	}
	if err := st.SetMeta(ctx, crossSectionMetaPrefix+string(h), string(blob)); err != nil {
		slog.Warn("cross-section gate: could not record this pass's shape",
			"horizon", h, "err", err)
	}
}

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

// calibrationPairLimit caps how many of the newest INDEPENDENT resolved
// outcomes train the fleet-wide recalibration map for one horizon.
// ResolvedRawPredictionPairs now returns one row per (symbol, trading day), so this
// is a memory guard rather than the statistical window: at ~1,000 symbols a day
// it admits roughly 40 trading days. The old value of 3,000 was chosen when the
// query returned every intraday re-score, and it bought TWO calendar days.
const calibrationPairLimit = 40000

// calibrationMinDays is the number of DISTINCT TRADING days the fit must see before
// it is allowed to correct anything.
//
// Rows inside one day share one market move, so days — not rows — are the unit
// of evidence here. A three-parameter map fit on two days has ~two independent
// observations behind it: it cannot learn a probability map, it can only
// memorise which way the market went, and applying that to a fresh day injects
// a large confident bias with no forecasting content. That is exactly what the
// live record caught (see ResolvedRawPredictionPairs).
//
// 10 matches the evidence floor the rest of the platform already uses — the
// accuracy registry's min_distinct_blocks and canary.ReadmitMinDistinctDays —
// so no surface has a laxer bar for FITTING a map than for BELIEVING one.
const calibrationMinDays = 10

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
	raws, ups, days, err := st.ResolvedRawPredictionPairs(ctx, h, calibrationPairLimit)
	if err != nil {
		return nil, false, err
	}
	if len(raws) == 0 {
		return nil, false, nil
	}
	// EVIDENCE FLOOR, counted in days rather than rows. The pairs are already
	// deduped to one per (symbol, trading day) by the store, but a handful of days
	// can still carry thousands of rows, and it is the day count that says how
	// much independent evidence is behind the fit. Below the floor, publish the
	// raw probability uncorrected: an uncorrected number is honestly
	// uncalibrated, whereas a map fit on two market moves is confidently wrong.
	distinctDays := map[int64]struct{}{}
	for _, d := range days {
		distinctDays[d] = struct{}{}
	}
	if len(distinctDays) < calibrationMinDays {
		slog.Info("calibration: refusing to fit — too few independent days",
			"horizon", h, "days", len(distinctDays), "need", calibrationMinDays,
			"rows", len(raws))
		return nil, false, nil
	}
	// ResolvedRawPredictionPairs returns rows ORDER BY day DESC, so reversing
	// puts them in chronological order. Ts carries the REAL trading-day number, not
	// an ordinal: CalibrateRanking splits its holdout on a Ts boundary, and a
	// split that lands mid-day would put the same market move on both sides of
	// the train/test line — which is how a Brier gate that exists to catch a bad
	// map ends up measuring in-sample.
	pairs := make([]ensemble.Pair, len(raws))
	for i := range raws {
		src := len(raws) - 1 - i // oldest first
		pairs[i] = ensemble.Pair{Pred: raws[src], Actual: ups[src], Ts: days[src]}
	}

	// CalibrateRanking, not Calibrate: isotonic is only WEAKLY monotone, so its
	// flat blocks map every raw probability in a range to one identical value.
	// Measured 2026-08-02, that collapsed seven distinct per-symbol crypto
	// probabilities to a single 0.4635 and 322 stocks to six distinct values —
	// a cross-sectional ranking reduced to one market-wide call.
	//
	// It only preserves the ranking when doing so costs nothing: isotonic still
	// ships whenever it is significantly better out-of-sample. `ranked` records
	// which happened, so the collapse is reportable rather than silent.
	mapFn, calibrated, ranked := ensemble.CalibrateRanking(pairs)
	if !calibrated {
		return nil, false, nil
	}
	if !ranked {
		// A MAP THAT COLLAPSED THE RANKING IS REFUSED, not merely logged.
		//
		// ranked=false means isotonic won the held-out Brier comparison, and
		// isotonic's pool-adjacent-violators blocks map whole ranges of raw
		// scores onto one identical value. Applied across the fleet, that turns
		// N per-symbol probabilities into a handful of distinct numbers — in
		// practice ONE, sitting a hair below 0.5 — and the `prob > 0.5` rule
		// every reader thresholds at then converts it into a UNANIMOUS
		// directional call.
		//
		// Measured over the live resolved record (31 UTC days, 15,998
		// independent symbol-days), a rank-collapsing map shipped on 21 of the
		// 21 days it could be fitted, and its up-call rate was 0.0%-0.4% while
		// 48%-70% of symbols actually rose. What the platform was publishing was
		// not a thousand forecasts: it was ONE market-direction bet, inferred
		// from the trailing base rate, replicated across every symbol and graded
		// as a thousand independent predictions. When the regime matched the
		// trailing rate it scored well; when the regime flipped it scored 29.4%
		// against a 70.6% up-day, and the registry duly recorded "significantly
		// worse than the naive baseline".
		//
		// Publishing the RAW probability uncorrected costs measured accuracy on
		// the pooled record (-0.8pp at 1d, -2.5pp at 1w) and it is still the
		// right call, for the reason the rest of this codebase already accepts:
		// the collapsed map earns those points by silently BECOMING the naive
		// majority baseline while presenting itself as a per-symbol forecaster.
		// An uncalibrated number that varies per symbol is honestly uncertain; a
		// calibrated number identical for every symbol is a market call wearing
		// a forecast's clothes, and it costs 17pp the day the market turns.
		slog.Warn("calibration: refusing a rank-collapsing map — publishing raw "+
			"probabilities uncorrected rather than one market-wide call replicated "+
			"across every symbol", "horizon", h)
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

// rankGate resolves one leg's admission edge for a symbol: the per-symbol
// Wilson lower bound on its out-of-sample AUC, VETOED to a bench whenever the
// leg's FLEET AUC is at or below chance. The veto is one-directional by design
// — a good fleet never promotes a bad symbol, only a bad fleet demotes a good
// one — because at ~1,000 symbols the tail of a null leg clears a 90% bound
// about 100 times by chance, and that tail is exactly what a per-symbol gate
// would otherwise admit.
// MEASURED THE WAY THE LEG IS USED. `vetoed` carries the day-clustered
// WITHIN-DAY cross-sectional AUC verdict (fleetveto.go) rather than an
// n-weighted mean of per-symbol AUCs. The old statistic answered a different
// question and answered it noisily: on the live 1d pressure record the two
// disagreed enough to flip the verdict — 0.3614, vetoed outright, versus 0.4636
// with a 95% interval of [0.4113, 0.5158] that contains chance.
//
// A leg is vetoed only when the WHOLE interval sits at or below 0.5. One that
// straddles chance, or has too few days to measure, falls through to the
// per-symbol Wilson bound below and is judged there.
func rankGate(vetoed map[string]bool, leg string, h md.Horizon, auc float64, nEval int) (float64, bool) {
	if vetoed[leg+"|"+string(h)] {
		return -1, true
	}
	return clusterstat.RankEdge(auc, nEval)
}

// requireMeasuredLegs reports whether the blend runs in PRODUCTION mode, where
// every leg must carry a measured positive edge, rather than COLD-START mode,
// where an ungraded leg is kept so a cold or erroring trainer cannot blank the
// platform.
//
// Default ON, which INVERTS the historical default. Cold start was the right
// contract while the trainers were new and most legs were ungraded. It is the
// wrong one now that they are graded, because the only legs it still admits on
// absence of evidence are precisely the ones no trainer has ever managed to
// grade. On the live record that is the sentiment leg: numeric on ~3% of rows,
// measured within-day AUC 0.3805 — ranking BACKWARDS — and admitted on ~25% of
// the current day's rows for no reason but that nothing assigns SentimentLift.
//
// The graded legs are unaffected either way. Pressure and expectancy carry
// RankEdge entries, and RankEdge overrides the lift gate, so this flag reaches
// only the ungraded remainder. That is the whole point: it closes the door that
// says absence of evidence is evidence of edge, and touches nothing else.
//
// SIGNALDECK_COLD_START_LEGS=1 restores the old fail-safe without a rebuild.
// That is deliberately the rollback path for this change: if a trainer outage
// ever does thin emission, recovery is one environment variable and a restart
// rather than a revert, rebuild and redeploy.
func requireMeasuredLegs() bool {
	switch os.Getenv("SIGNALDECK_COLD_START_LEGS") {
	case "1", "true", "TRUE", "yes":
		return false
	}
	return true
}

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
	// HMM volatility labels, recorded alongside the rule-based ones. These go
	// into the feature vector unconditionally so a graded comparison against
	// regime_state accumulates from today, but they only KEY the adaptive
	// weight cells when hmmCellsEnabled() says so — see that function.
	hmmLbls, err := w.St.HMMRegimeLabels(ctx)
	if err != nil {
		hmmLbls = map[int64]string{}
	}
	rankPcts, err := w.St.RankingPercentiles(ctx)
	if err != nil {
		rankPcts = map[int64]float64{}
	}
	// FLEET RANKING VETO, read once. Measured as the day-clustered WITHIN-DAY
	// cross-sectional AUC — the ranking a leg is actually asked to perform —
	// rather than an n-weighted mean of per-symbol AUCs, which answers "does
	// this symbol's score predict its own moves over time" and answers it with
	// per-symbol estimates whose standard error is near 0.1. See fleetveto.go.
	// Best-effort: on an unreadable cross-section nothing is vetoed and the
	// per-symbol bound stays in charge.
	vetoed := fleetVetoes(ctx, w.St, predHorizons)

	// Fill the settled-move key on rows that predate the column, a bounded batch
	// per pass so it converges without a migration framework and never stalls a
	// sweep. settle_ts is the true independence unit — two predictions share an
	// outcome exactly when they share a base bar — and trading_day(ts) is not
	// that unit off a 24/7 market: measured 2026-08-08, folding the stock record
	// on the settled move instead of the calendar day removes 27.9% phantom
	// observations (10,722 -> 7,732) while leaving crypto at 0.0%.
	// All three outcome tables that carry the key are drained on the same pass and
	// the same budget: they share one derivation, so letting one lag behind would
	// mean two surfaces disagreeing about what one observation is — the exact
	// drift the shared md.SettleDay implementation exists to prevent.
	for _, bf := range []struct {
		name string
		fn   func(context.Context, int) (int64, error)
	}{
		{"prediction_outcomes", w.St.BackfillSettleTs},
		{"score_outcomes", w.St.BackfillScoreSettleTs},
		{"confluence_outcomes", w.St.BackfillConfluenceSettleTs},
	} {
		if n, err := bf.fn(ctx, settleBackfillBatch); err != nil {
			slog.Warn("settle_ts backfill failed; day-clustered counts stay on the calendar day",
				"table", bf.name, "err", err)
		} else if n > 0 {
			slog.Info("settle_ts backfilled", "table", bf.name, "rows", n)
		}
	}
	// Read once per pass, not per symbol: the mode is a deploy-time decision and
	// re-reading it mid-sweep could split one pass across two contracts.
	strictLegs := requireMeasuredLegs()
	// Newest trading day already carrying an EVIDENCE row per symbol, so a
	// legless blend is recorded once a day rather than on all ~138 passes. One
	// query per horizon, mutated in place as this pass writes.
	evidenceDay := map[md.Horizon]map[int64]int64{}
	for _, h := range predHorizons {
		m, err := w.St.EvidenceDayBySymbol(ctx, h)
		if err != nil {
			m = map[int64]int64{}
		}
		evidenceDay[h] = m
	}
	// CROSS-SECTIONAL FEATURES, computed ONCE for the whole universe (a
	// percentile needs the cross-section, so it cannot be built inside the
	// per-symbol loop below). These are the four factors measured to rank the
	// cross-section — liquidity, low-vol, 12-1 momentum, 1-day reversal — none
	// of which existed in the alphax feature set that grades AUC 0.501. See
	// xsfeatures.go for the measurement and its limits.
	// CROSS-SECTION DISPERSION GATE. Measured on the PREVIOUS pass's published
	// probabilities, per horizon, before anything is written this pass.
	//
	// Between 2026-07-27 and 2026-08-04 the fleet-wide calibration map collapsed
	// and 329 symbols were handed 5-14 distinct probabilities; the 0.5 threshold
	// turned that into a near-unanimous market call which was then stored,
	// resolved and graded as ~330 independent per-symbol forecasts. The registry
	// published FAILED/retire=true on what was really 11 market calls.
	//
	// A degenerate cross-section carries no usable call AND no usable ranking —
	// that is what "no dispersion" means — so this refuses the upsert outright,
	// the same answer AdmittedProbability already gives for a legless blend: no
	// forecast, rather than a forecast that means nothing.
	//
	// It lags by one DAY, measured on the prior day's deduped cross-section —
	// the same unit the registry grades. Every observed episode ran 6-8
	// consecutive days, so the lag still gates them from the second day on. On
	// an empty table there is nothing to measure and the pass publishes: a cold
	// start must not be indistinguishable from a collapse.
	today := time.Now().UTC().Format("2006-01-02")
	gated := map[md.Horizon]string{}
	for _, h := range predHorizons {
		// MEASURED FROM THE TABLE, NOT FROM A STORED SAMPLE.
		//
		// This used to read a meta record that saveCrossSection overwrote once
		// per pass, so the gate judged a whole day from whatever the LAST pass
		// of that day happened to emit. Measured 2026-08-08 the record read
		// n=12 on a 329-symbol day: the distinct rule then needs only 6, and
		// the spread cleared its floor by 0.00002. A thin pass silently
		// disarmed the gate, and the collapses of 08-06 and 08-07 — both of
		// which the spread rule catches on the real cross-section — published.
		//
		// The prior day is COMPLETE, so reading it from the table carries none
		// of the risk the one-pass record existed to avoid (a gate judging the
		// sweep it is producing), while measuring the whole cross-section
		// instead of a sample of it.
		// A STORE ERROR IS NOT A CLEAN CROSS-SECTION. `continue` here means "do
		// not gate", i.e. publish, and both reads used to fold their error into a
		// benign data shape -- so a contended pool on a day whose cross-section
		// HAD collapsed published the whole day ungated, with no log and no dq
		// event. The justifying comment below only ever covered the benign half.
		// THE PRIOR DAY IS DERIVED, NOT READ BACK FROM THE RECORD WE JUST WROTE.
		//
		// This loaded the meta record and skipped when rec.Day >= today. But
		// saveCrossSection stamps that record with TODAY at the end of every
		// pass, and runProbs is populated even for a gated horizon, so the
		// stamp landed on pass one and every later pass of the day short-
		// circuited: the gate could fire ONCE per UTC day, at whatever hour the
		// first pass ran, and the remaining ~137 passes -- the entire 09:30-16:00
		// ET session -- published ungated. A six-day collapse withheld six
		// passes out of roughly 830. The one gating pass logged a warning, which
		// reads exactly like the gate working.
		//
		// Walking back from today finds the most recent day that actually
		// published, so weekends and holidays are skipped by data rather than by
		// arithmetic, and the answer no longer depends on our own write.
		var probs []float64
		priorDay := ""
		for back := 1; back <= 5 && priorDay == ""; back++ {
			cand := time.Now().UTC().AddDate(0, 0, -back).Format("2006-01-02")
			got, perr := w.St.PublishedCrossSection(ctx, string(h), cand)
			if perr != nil {
				w.gateReadFailed(ctx, h, "published cross-section unreadable for "+cand, perr)
				break
			}
			if len(got) > 0 {
				priorDay, probs = cand, got
			}
		}
		if priorDay == "" {
			// Nothing published in the last five days is a cold start, not a
			// collapse; a cold start must not be indistinguishable from one.
			continue
		}
		rec := &crossSectionRecord{Day: priorDay}
		measured := ensemble.MeasureCrossSection(probs)
		if ok, reason := measured.Usable(); !ok {
			rec.CrossSection = measured
			gated[h] = reason
			slog.Warn("cross-section gate: refusing to publish this horizon",
				"horizon", h, "priorDay", rec.Day, "reason", reason,
				"n", rec.N, "distinct", rec.Distinct, "spread", rec.Spread,
				"agreement", rec.Agreement)
		}
	}

	xsFeats := crossSectionalFeatures(ctx, w.St, syms)
	if len(xsFeats) == 0 {
		slog.Info("cross-sectional features unavailable this pass — universe too " +
			"thin or the batched bar read failed; the alphax leg sees the old feature set")
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
	todayUTC := md.TradingDay(time.Now().UTC().Unix())
	for _, h := range predHorizons {
		p, ok, err := w.St.PrequentialMajorityProb(ctx, h, todayUTC, benchmarkMajorityEpoch)
		switch {
		case err != nil:
			slog.Warn("prequential-majority benchmark: majority read failed — skipping this pass", "horizon", h, "err", err)
		case !ok:
			// Empty or exactly tied prior: the majority-follower has no side to
			// take, so it commits nothing this pass rather than a 0.5 that the
			// graders would score as a confident DOWN call.
			slog.Info("prequential-majority benchmark: no majority in the prior record — no benchmark row this pass", "horizon", h)
		default:
			benchProb[h] = p
		}
	}
	n, featErrs, staleCals, noLegs, gatedRows := 0, 0, 0, 0, 0
	// This pass's emitted probabilities per horizon, published or withheld.
	runProbs := map[md.Horizon][]float64{}
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
		regimeWts, _ := adaptive.Pick(learned, cellKey(regimeLbls[s.ID], hmmLbls[s.ID]))
		for _, h := range predHorizons {
			sc, ok, err := w.St.LatestScore(ctx, s.ID, h)
			if err != nil {
				return "", err
			}
			if !ok {
				continue
			}
			c := ensemble.Components{
				PressureScore:       sc.Score,
				SentimentScore:      sentScore,
				RequireMeasuredLegs: strictLegs,
			}
			// RANKING GATE. Every leg below already carries a graded
			// out-of-sample AUC; until now admission was decided on
			// Lift = Accuracy - BaseRate, which is threshold-dependent and so
			// tracks the grading window's base rate rather than the leg's
			// information. See clusterstat.RankEdge for the measurement that
			// motivated the change. A leg with no graded AUC gets no entry and
			// keeps its historical lift behaviour exactly.
			c.RankEdge = map[string]float64{}
			// Expectancy hit rate for the current state.
			if rows, err := w.St.Expectancy(ctx, s.ID, h); err == nil {
				if row, ok := expectancy.Lookup(rows, states[h]); ok {
					hr := row.HitRate
					c.ExpectancyHitRate = &hr
				}
			}
			// The expectancy leg's grade lives in model_forecasts like the model
			// legs, written by ExpectancyTrainer. Without it this leg is admitted
			// on availability alone — and with pressure and alphax now benched on
			// measured ranking, it would be the leg carrying most of the blend.
			if e, ok := modelLegRankEdge(vetoed, modelFcs, h, store.ModelExpectancy, ts); ok {
				c.RankEdge[ensemble.LegExpectancy] = e
			}
			// Forecast prob + lift (lift gates whether it is trusted).
			for _, f := range forecasts {
				if f.Horizon == h {
					p, l := f.Prob, f.Lift
					c.ForecastProb, c.ForecastLift = &p, &l
					if e, ok := rankGate(vetoed, ensemble.LegForecast, h, f.AUC, f.NEval); ok {
						c.RankEdge[ensemble.LegForecast] = e
					}
				}
			}
			// STAGE 6 gated model legs. Each carries its stored OOS lift; the
			// ensemble includes the leg ONLY when lift > 0 (LegProbabilities). An
			// absent leg (never trained, too little history, or a row older than
			// maxModelForecastAgeSecs — M3 freshness cap) contributes nothing —
			// the blend is exactly the pre-Stage-6 blend.
			if p, l, ok := modelLegProbLift(modelFcs, h, store.ModelGBM, ts); ok {
				c.GBMProb, c.GBMLift = &p, &l

				if e, ok := modelLegRankEdge(vetoed, modelFcs, h, store.ModelGBM, ts); ok {

					c.RankEdge[ensemble.LegGBM] = e

				}
			}
			if p, l, ok := modelLegProbLift(modelFcs, h, store.ModelMeanRev, ts); ok {
				c.MeanRevProb, c.MeanRevLift = &p, &l

				if e, ok := modelLegRankEdge(vetoed, modelFcs, h, store.ModelMeanRev, ts); ok {

					c.RankEdge[ensemble.LegMeanRev] = e

				}
			}
			// Cross-sectional alpha leg (alphax-leg wave): rows exist in
			// model_forecasts ONLY while the pooled model's OOS lift > 0 (the
			// AlphaXTrainer deletes them on a gated regrade), and the ensemble
			// re-applies the same lift>0 gate. NOTE the category nuance: this
			// prob is RELATIVE (beat the same-day universe median), blended as
			// a directional tilt — see ensemble.Components.AlphaXProb.
			if p, l, ok := modelLegProbLift(modelFcs, h, store.ModelAlphaX, ts); ok {
				c.AlphaXProb, c.AlphaXLift = &p, &l

				if e, ok := modelLegRankEdge(vetoed, modelFcs, h, store.ModelAlphaX, ts); ok {

					c.RankEdge[ensemble.LegAlphaX] = e

				}
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

				if e, ok := modelLegRankEdge(vetoed, modelFcs, h, store.ModelPressure, ts); ok {

					c.RankEdge[ensemble.LegPressure] = e

				}
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
			// basis names the tier these weights came from. Recorded per
			// prediction because the fallback chain is the most likely thing to
			// be silently carrying the fleet: a blend running on the static
			// prior looks identical, after the fact, to one weighted by
			// measured skill.
			basis := "static"
			if len(regimeWts) > 0 {
				basis = "regime"
			}
			usePersonalCal := false
			var personalCal func(float64) float64
			if pm, ok, err := w.St.SymbolModel(ctx, s.ID, h); err == nil && ok && pm.Tier == symbolagent.TierPersonal {
				if pw := decodeWeights(pm.Weights); len(pw) > 0 {
					wts, basis = pw, "personal"
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
					fn, err := ensemble.MapFromKnotsChecked(cal.KX, cal.KY)
					switch {
					case err != nil:
						staleCals++
					case !ensemble.KnotsDiscriminate(cal.KY):
						// COLLAPSED PERSONAL MAP. The monotonicity check above
						// passes a fully flat map — ties are not inversions — so
						// a symbol whose stored isotonic fit degenerated to one
						// block would publish the same probability regardless of
						// what its legs computed. That is the per-symbol twin of
						// the fleet-wide collapse refused in globalCalibration,
						// and it is counted with the other refused maps so the
						// detail line already reports it.
						staleCals++
					default:
						personalCal, usePersonalCal = fn, true
					}
				}
			}
			raw, nUsed, admitted := ensemble.AdmittedProbability(c, wts)
			// persistFeatures writes the INPUT VECTOR this pass saw and returns it
			// for the ledger's feature hash. Failure must NOT fail the prediction —
			// log + dq metric.
			//
			// Called on EVERY path, including the two that publish nothing. The
			// vector records what the inputs WERE, which is true whether or not a
			// forecast was emitted from them, and it is what the leg trainers read
			// to produce the grades that decide admission. Skipping it on a
			// withheld row is the same self-sealing trap the evidence row exists to
			// avoid, one surface over: bench a leg (or gate a horizon), stop
			// recording the inputs, and the trainers starve of exactly the data
			// that could re-admit it. Measured 2026-08-07: the 1d cross-section
			// gate had already driven that day's 1d feature corpus to 23 of 329
			// symbols while 1w, ungated, kept 328.
			persistFeatures := func(cal float64) map[string]float64 {
				var rankPct *float64
				if pct, ok := rankPcts[s.ID]; ok {
					rankPct = &pct
				}
				// xsMap is nil when this symbol had no computable cross-section
				// (too few peers, or a leg uncomputable for it). buildFeatureVector
				// skips nil maps, so the feature is ABSENT rather than defaulted —
				// a fabricated 0.5 would place the symbol at the median of a
				// cross-section it was never ranked against.
				var xsMap map[string]float64
				if f, ok := xsFeats[s.ID]; ok {
					xsMap = f.vec()
				}
				// hmm_<label>=1 rides in as one more cross-cutting feature map, so
				// the vector records what the HMM said at prediction time whether
				// or not it currently keys the weight cells.
				var hmmMap map[string]float64
				if l := hmmLbls[s.ID]; l != "" {
					hmmMap = map[string]float64{"hmm_" + l: 1}
				}
				vec := buildFeatureVector(sc, c, raw, cal, cellKey(regimeLbls[s.ID], hmmLbls[s.ID]), rankPct, sentN, microMap, vixMap, macroMap, newsMap, alphaSymMap, alphaMktMap, idxMap, trendMap, xsMap, hmmMap)
				if err := w.St.InsertFeatures(ctx, s.ID, h, ts, featureVersion, vec); err != nil {
					featErrs++
					slog.Warn("feature store: persist failed", "symbol", s.Symbol, "horizon", h, "err", err)
					sid := s.ID
					_ = w.St.InsertDQ(ctx, md.DQEvent{
						SymbolID: &sid, Ts: time.Now().Unix(),
						Kind: "feature_store_error", Detail: fmt.Sprintf("horizon %s: %v", h, err),
					})
				}
				return vec
			}

			if !admitted {
				// NO LEG SURVIVED ADMISSION — record the EVIDENCE, publish nothing.
				//
				// The row is written with nUsed=0, which store.UpsertPrediction
				// reads as "not a forecast": it writes no prediction_outcomes row,
				// so this can never be graded, never enter a calibration fit and
				// never reach the live record. Every reader of the predictions
				// table filters n_used > 0, so it is never served either.
				//
				// What it DOES keep is components — what each leg said at this
				// instant. That is the only surface the leg graders read (the
				// expectancy leg has no other one at all), so skipping the write
				// entirely would have made the gate self-sealing: bench a leg,
				// stop recording it, and the evidence that could ever re-admit it
				// stops accruing with it. A withheld forecast must not also
				// withhold the measurement that reopens the question.
				// ONE evidence row per symbol per trading day. Consumers dedup
				// to that unit regardless, so the rest would be pure volume —
				// ~40,200 rows a day at 1d against a 363,355-row table.
				day := md.TradingDay(ts)
				if prev, seen := evidenceDay[h][s.ID]; !seen || prev < day {
					comps, _ := json.Marshal(c)
					if err := w.St.UpsertPrediction(ctx, store.Prediction{
						SymbolID: s.ID, Horizon: h, Ts: ts,
						RawProb: raw, CalProb: raw, NUsed: 0, Components: string(comps),
						Weights: "{}", Basis: basis,
					}); err != nil {
						return "", err
					}
					evidenceDay[h][s.ID] = day
				}
				// The 0.5 stored here is NOT calibrated and NOT a call. An empty
				// blend returns 0.5 from WeightedProbability, and on the wire a
				// stored 0.5 is indistinguishable from a real coin-flip forecast:
				// it would inherit the calibration map, land on one side of the
				// 0.5 threshold and be graded as a confident directional call.
				// 1,202 live rows carried exactly that shape. So the calibration
				// map is deliberately NOT applied and cal_prob is left equal to
				// raw — the column has to hold something, and an uncalibrated
				// number that no surface reads is the honest thing to put there.
				// cal is deliberately raw here, matching the CalProb stored above:
				// no calibration map was applied to a legless blend, so the vector
				// must not claim one was.
				persistFeatures(raw)
				noLegs++
				continue
			}
			cal := raw
			if usePersonalCal {
				// Personal tier: recalibrate with the symbol's OWN isotonic map.
				cal = personalCal(raw)
			} else if fn, ok := globalCal[h]; ok {
				// Fleet-wide fallback, fit on raw_prob against realized
				// outcomes — the SAME variable it is applied to here.
				cal = fn(raw)
			}
			// SHADOW MEASUREMENT — recorded whether or not this row is published.
			//
			// The gate reads what the fleet WOULD publish, never what it did. If it
			// read stored rows instead, withholding a horizon would erase the only
			// evidence that could ever reopen it: the newest day carrying rows
			// would stay the collapsed one forever and the gate would latch shut.
			// Measuring here, after calibration and before the write, is what makes
			// the refusal self-releasing — the pass keeps reporting its own shape
			// while publishing nothing.
			runProbs[h] = append(runProbs[h], cal)

			if _, isGated := gated[h]; isGated {
				// Yesterday's cross-section was degenerate. Skip the upsert exactly
				// as a legless blend does: the row that would be written here is the
				// one that gets graded as an independent forecast, and it is not one.
				//
				// The INPUTS are still recorded. Withholding the forecast is the
				// point; withholding the feature vector too would make the gate
				// latch shut, since the trainers whose grades reopen the horizon
				// read exactly this table.
				persistFeatures(cal)
				gatedRows++
				continue
			}
			comps, _ := json.Marshal(c)
			// WeightedProbability falls through to the equal-weight mean when
			// the weight map carries no positive mass over the AVAILABLE legs,
			// so a non-empty map is not proof the weights were used. Record the
			// map that was PASSED and let the audit compare it against the legs
			// that were actually present.
			wjson, _ := json.Marshal(wts)
			if len(wts) == 0 {
				wjson = []byte("{}")
			}
			if err := w.St.UpsertPrediction(ctx, store.Prediction{
				SymbolID: s.ID, Horizon: h, Ts: ts,
				RawProb: raw, CalProb: cal, NUsed: nUsed, Components: string(comps),
				Weights: string(wjson), Basis: basis,
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
			vec := persistFeatures(cal)
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
	if noLegs > 0 {
		// Reported, never silent. A rising count is the honest signal that the
		// legs have gone cold fleet-wide — which is exactly the condition that
		// used to be laundered into thousands of 0.5 "predictions".
		detail += fmt.Sprintf(" (%d symbol-horizon(s) had no admitted leg — no forecast published)", noLegs)
	}
	// Record every horizon's shape, INCLUDING the withheld ones. This is the
	// only write a gated horizon makes, and it is what lets the next pass see
	// that the cross-section recovered.
	for _, h := range predHorizons {
		if probs := runProbs[h]; len(probs) > 0 {
			saveCrossSection(ctx, w.St, h, today, ensemble.MeasureCrossSection(probs))
		}
	}
	if gatedRows > 0 {
		// A horizon whose previous cross-section had collapsed. Loud on purpose:
		// this is the counter that would have been non-zero for eight straight
		// trading days in the 2026-07-27..08-04 episode, and nothing at the time
		// was counting it.
		hs := make([]string, 0, len(gated))
		for h, reason := range gated {
			hs = append(hs, fmt.Sprintf("%s: %s", h, reason))
		}
		sort.Strings(hs)
		detail += fmt.Sprintf(" (%d row(s) withheld by the cross-section gate — %s)",
			gatedRows, strings.Join(hs, "; "))
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
			pending, err := w.St.UnresolvedPredictions(ctx, hh, now-horizonSecs(h), horizonSecs(h), 1500)
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
				// SETTLEMENT GUARD — the forward bar must be FINISHED.
				//
				// `now < target` was the only time check, and target is the next
				// session's bar STAMP (ET midnight). Ingest creates that bar at
				// the open, so any resolver pass during the session found a bar
				// whose Close was the live price, froze it as a close-to-close
				// label, and never revisited it.
				//
				// Measured on 4,000 resolved 1d stock rows: 37.8% were frozen
				// before their forward bar's 16:00 ET close, and those disagree
				// with the FINAL close 9.7% of the time against 2.9% for rows
				// frozen afterwards — about 3.7% of the whole 1d record labeled
				// against a price that had not happened yet. 1w freezes
				// mid-session only 0.9% of the time, which is exactly why it
				// agrees with a recomputation 99.8% of the time and 1d only 94.2%.
				//
				// The test is "a LATER bar exists", not a clock offset: a
				// successor bar can only appear once the next session has begun,
				// so it settles the previous one without this code needing to
				// know exchange hours, half-days, DST or crypto's 24h day.
				//
				// Cost: a label lands one session later, and the final bar of a
				// symbol that stops printing never resolves — the same answer
				// UnresolvedPredictions already gives for dead symbols, and an
				// unknown outcome is better than a confident wrong one.
				if _, settled, err := w.St.BarAtOrAfter(ctx, p.SymbolID, md.TF1d, fwd.Ts+1); err != nil {
					return "", err
				} else if !settled {
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

// gateReadFailed records that the cross-section collapse gate could not read its
// own evidence for one horizon.
//
// The gate's decision on a failed read is to PUBLISH -- refusing on a transient
// database error would wedge the predictor shut on something that is not
// evidence, which is the same fail-open contract CollapsedGradingWindow states.
// That is defensible only if the failure is VISIBLE: an unreadable prior day is
// then an unexamined day rather than a clean one, and nothing downstream can
// tell the difference unless this says so. A dq event makes it countable, and
// the sibling best-effort path thirty lines below already logs its ambiguity.
func (w *PredictionRunner) gateReadFailed(ctx context.Context, h md.Horizon, what string, err error) {
	slog.Warn("cross-section gate: evidence unreadable, horizon published UNGATED",
		"horizon", string(h), "what", what, "err", err)
	_ = w.St.InsertDQ(ctx, md.DQEvent{
		Ts:   time.Now().Unix(),
		Kind: "crosssection_gate_unread",
		Detail: "horizon " + string(h) + " published without the collapse gate: " + what +
			": " + err.Error(),
	})
}
