// Package structregime holds the market-STRUCTURE regime predictors validated
// by the 2026-07-17 alpha-discovery loop — the follow-on to internal/volregime
// (same discipline, more targets). Each predicts a structural regime, never
// price direction (direction's ~52-55% ceiling was re-confirmed a fifth time
// in the same loop).
//
// # Methodology shared by every predictor here
//
// Strict walk-forward (features at t use data <= t only), NON-OVERLAPPING
// forward windows (sampling step == horizon), balanced-by-construction labels
// (the forward metric vs the TRAILING causal median of its own rolling
// series), and quarter-block-clustered bootstrap CIs over ~900 stocks /
// 7.5 years (2019-2026). Accuracy tables below are those MEASURED numbers —
// never invented, never extrapolated.
//
// # The three validated targets (measured 2026-07-17)
//
// Every percentage in the three tables below is a BACKTEST figure measured on
// 2026-07-17 by the run described above. None is a live record and none is
// graded — all seven structural claims read PENDING until their first grade,
// and README's structural table (from data/accuracy_registry.json) is the
// authority on that. tools/live_accuracy.py --scan-code matches the literal and
// cannot tell a backtest percentage from a live one, so a collision resolves
// here, as dated evidence, and never by editing a measured number:
// SUPERSEDED-SNAPSHOT.
//
// TREND (21d): will the stock still be on its current side of the 200-day SMA
// in 21 trading days? Conviction = trailing 200d percentile of |close/SMA-1|.
//
//	cumulative: all 83.3% · conv>0.5 93.1% · conv>0.8 96.2% · conv>0.9 97.2% [CI 0.965-0.978]
//	per-band (what a forecast reports): <0.5 73.1% · 0.5-0.8 90.0% · 0.8-0.9 94.6% · >=0.9 97.2%
//
// MEASURED mean forward 21d RETURN by the same bands (2026-07-24
// re-validation) — the accuracy above buys none of it:
//
//	<0.25 +0.41% · 0.25-0.5 +0.51% · 0.5-0.8 +0.58% · 0.8-0.9 +0.79% · >=0.9 -0.39%
//
// LIQUIDITY (21d): will mean daily DOLLAR VOLUME over the next 21 sessions be
// above or below its trailing-200d median? Conviction = 2*|rank-0.5| of the
// current 21d mean in its trailing distribution.
//
//	cumulative: all 71.0% · conv>0.5 79.5% · conv>0.8 85.0% · conv>0.9 87.6% [CI 0.857-0.892]
//	per-band: <0.5 59.5% · 0.5-0.8 73.9% · 0.8-0.9 80.1% · >=0.9 87.6%
//
// VOL (21d): the volregime method at a monthly horizon.
//
//	cumulative: all 62.2% · conv>0.5 67.2% · conv>0.8 70.1% · conv>0.9 72.0% [CI 0.674-0.759]
//	per-band: <0.5 55.8% · 0.5-0.8 64.3% · 0.8-0.9 66.8% · >=0.9 72.0%
//
// All three replicated 2026-07-17 by an independent re-implementation
// (different sampling offsets, rank code, and CI method) within 2 jackknife
// SEs; vol21 came back slightly BETTER (74.8% top tier) — the shipped numbers
// are the conservative measurement of record.
//
// # Honesty caveats (ship with every payload)
//
//   - LIQUIDITY: label base rate is NOT 50/50 (secular volume drift: majority
//     class 56-61% by tier) and a naive persistence rule scores the SAME
//     accuracy — the skill IS liquidity persistence. The accuracy claim holds;
//     the novelty claim would not.
//   - TREND: the predictor is trend persistence + distance; base rate 54-57%.
//     Universe is currently-tracked stocks, so delisted names are absent
//     (survivorship) — persistence of downtrends into delisting is unobserved.
//   - GEOMETRY (2026-08-03, the strongest caveat here — read it before quoting
//     any conviction tier). Both trend21 and liquidity21 ask the same question:
//     does a value stay on the same side of a slow reference line over 21 days?
//     Conviction is distance from that line. A value far from a line needs a
//     large move to cross it. So conviction predicts correctness for a reason
//     that is arithmetic, not market. Controlling for barrier distance measured
//     in units the horizon can actually move,
//
//         z = |value - reference| / sigma_of_the_21d_change
//
//     against Phi(z), the DRIFTLESS-RANDOM-WALK probability of ending on the
//     starting side (zero free parameters):
//
//       trend21    (57,158 samples, 1,305 days, survivorship-clean): accuracy
//                  tracks Phi(z) to within ~2pp in EVERY z band. The +24.5pp
//                  conviction spread falls to +6.3pp within z bands, so ~74% of
//                  it is distance. Hansen SPA out-of-sample: conviction adds
//                  NOTHING over z (p=0.204); z adds over conviction (p<0.001).
//       liquidity21 (64,847 samples, 1,419 days): WORSE. Accuracy is 5.3pp
//                  BELOW Phi(z) overall (71.1% vs 76.4%) and 6-8pp below through
//                  the middle bands, because log dollar volume mean-reverts and
//                  therefore crosses its median MORE often than a random walk
//                  would. The +28.1pp conviction spread does not merely vanish
//                  inside z bands, it INVERTS to -5.1pp: at a fixed distance,
//                  high conviction means an extreme RANK in a compressed
//                  distribution, which is a mean-reversion candidate. SPA:
//                  conviction adds nothing over z (p=0.511); z beats conviction
//                  (p<0.001). Brier: z 0.1542, conviction 0.1630.
//       vol21      (64,305 samples, 1,440 days): THE EXCEPTION, and the one
//                  worth reading carefully. 51% of the +15.7pp conviction spread
//                  survives inside z bands (+7.9pp), and NEITHER side dominates:
//                  conviction does not beat geometry (SPA p=0.073) and geometry
//                  does not beat conviction (p=0.200). Brier actually favours
//                  conviction slightly (0.2318 vs 0.2324, both 0.2315). So vol21
//                  alone carries information barrier distance does not.
//                  BUT SIZE IT HONESTLY: the pure geometric rule
//                  ("sign(rv - median) persists") scores 61.62% and the full
//                  model scores 62.16%. The entire edge is +0.54pp, and SPA
//                  cannot establish it at 5%. Note also vol21 is the furthest
//                  BELOW the driftless null of the three (-8.5pp), because vol
//                  mean-reverts hardest. Phi(z) is a poor null here; the 61.62%
//                  geometric rule is the null that matters.
//
//     The +0.54pp was then RESOLVED (same day). Holding the shipped outcome
//     fixed and varying ONLY the estimator that forms the prediction, accuracy
//     traces a smooth hump in effective window length:
//
//       ewma0.85 ~7d  61.43% | ewma0.90 ~10d 62.00% | ewma0.94 ~17d 62.16%
//       ewma0.97 ~33d 61.96% | ewma0.99 ~100d 58.87%
//       flat5d 59.36% | flat10d 60.96% | flat21d 61.62% | flat42d 60.67%
//       flat63d 61.43% | flat126d 59.69%
//
//     EWMA beats the flat window of comparable length EVERYWHERE (+1.0 to
//     +1.3pp), and 0.94 is simply nearest the optimum. That is exponential
//     weighting being a better vol nowcast than a rectangular one, which is why
//     RiskMetrics exists. It is estimation, not prediction. SPA over all 10
//     estimators against rv21: p=0.089, so nothing beats the geometric rule
//     once the search is charged for.
//
//     DO NOT "FIX" THE ESTIMATOR MISMATCH. PredictVol21 ranks EWMA(0.94) while
//     ResolveVol21At grades against flat 21d realised vol, and the signs
//     disagree 13.1% of the time. Matching the barrier to the predictor makes
//     the SAME model score 71.65% instead of 62.16%: a +9.50pp inflation, 17x
//     the entire disputed edge, bought by grading a forecast against a barrier
//     built from its own estimator. The mismatch is the CONSERVATIVE setup and
//     is deliberate. Changing it would also break comparability between the
//     backtest and the live record starting 2026-08-07.
//
//     The remaining three kinds, controlled the same day:
//
//       trend63    (18,489 samples, 1,091 dates; replicates at 69.35% vs the
//                  shipped 70.0%): GEOMETRY, same as trend21, once the null is
//                  specified correctly. Under Phi(z) it looked like the strongest
//                  kind, keeping 62% of its +26.2pp spread. That was the NULL
//                  failing, not the predictor winning: Phi(z) holds SMA200 fixed,
//                  but over H sessions the average rolls off H of its 200 points
//                  and walks toward price. See the moving-barrier null below.
//
//     THE MOVING-BARRIER NULL (2026-08-03). Same idea as Phi(z) and still zero
//     free parameters, but the barrier is allowed to move: take the real trailing
//     closes, simulate the forward window as a ZERO-DRIFT log random walk at the
//     sample's own 60d sigma, rebuild SMA200 at the horizon from (surviving real
//     closes + simulated closes), and ask whether the simulated close is on the
//     starting side. 400 paths per sample. It contains no market information and,
//     unlike Phi(z), it is a calibrated probability that can be Brier-scored and
//     used as an SPA benchmark with no fitting at all.
//
//       barrier motion is worth 1.74pp of persistence at H=21 and 7.27pp at
//       H=63 — the 21/200 vs 63/200 window turnover, exactly as predicted.
//
//                           actual   Phi(z)    gap   MOVING   gap    Brier(mov/stat)
//         trend21           82.98%   83.68%  -0.70   81.95%  +1.04   .12034 / .12072
//         trend63           69.35%   75.26%  -5.91   67.99%  +1.36   .19755 / .20095
//
//     The static null's error blows out with horizon (-0.70 -> -5.91). The moving
//     null's does not (+1.04 -> +1.36). That is the whole explanation for
//     trend63's apparent strength, and it is now measured rather than asserted.
//     Re-run against the correct null, the conviction spread collapses on both:
//     trend21 keeps 17% (+4.14pp), trend63 keeps 37% (+9.61pp), and SPA says
//     conviction adds nothing to the null in either case (p=0.198, p=0.237).
//     The zero-parameter null also out-Briers the fitted conviction model
//     (.1152 vs .1225 at 21d, .1947 vs .1970 at 63d).
//
//     Residual: actual sits ~1.0-1.4pp ABOVE the moving null, concentrated at
//     low p_moving. The null is driftless and equities drift up, so that is the
//     expected sign and size. It is not being claimed as an edge.
//       trend21-crypto     : n=110 rows over 32 DATE CLUSTERS, 7 correlated
//                  assets. Overall 93.64%, date-clustered 95% CI
//                  [84.48, 100.00]. EVERY sample sits at z >= 1.5, where the
//                  driftless null alone already predicts 91.41%. The shipped
//                  0.985 tier rests on 30 rows; the 100% bands on 15 each.
//       liquidity21-crypto : n=161 over 46 date clusters. Overall 81.37%, CI
//                  [72.67, 88.17]. Phi(z) predicts 79.30% of it. The shipped
//                  0.964 tier rests on 31 rows.
//
//     For both crypto kinds the binding problem is not geometry, it is n.
//     cryptoAccuracyFor still SERVES 0.985 and 0.964 into live payloads while
//     the comment above it concedes the top tiers "rest on 3 clusters - never
//     quoted". Serving them is quoting them. Those two tables should be
//     withdrawn or floored at the all-decisions rate until the crypto universe
//     is wider than 7 names.
//
//     CONCLUSION: none of the three has demonstrated forecasting skill.
//     trend21 reproduces the geometry, liquidity21 underperforms it, and vol21
//     beats a geometric rule by +0.54pp that does not reach significance and is
//     explainable as an estimator choice. Conviction remains usable as a
//     RELIABILITY estimate for sizing. It must never be quoted as evidence of
//     PREDICTION. Reproduce with tools/revalidate_structural.py (trend21 control
//     is built in).
//   - ACCURACY IS NOT RETURN, and at the top band they are INVERTED. The
//     2026-07-24 independent re-validation (fresh reimplementation, 968 stocks,
//     1900 trading days 2019-2026, NON-OVERLAPPING 21d/63d windows,
//     date-clustered bootstrap CIs) replicated every trend accuracy tier AND
//     measured forward return by band for the first time: trend21 conv>=0.9 has
//     the BEST hit rate and a NEGATIVE mean forward 21d return (-0.39%), while
//     the lower bands earn +0.41% to +0.79%; trend63 conv>=0.9 is worse still
//     (-1.30%). The mechanism is mechanical, not a fluke: high conviction MEANS
//     price is far from its SMA200 — already extended — and extended names
//     mean-revert. So "96% chance it stays above its 200-day average" and "this
//     basket makes money" are different claims and only the first is true. The
//     hit rate is real; the trade is not. Every trend forecast ships this
//     verbatim in Tradeability (see forwardReturnFor / TradeabilityFor).
//   - All targets: a regime call is situational awareness with a measured hit
//     rate, not a trade recommendation.
//
// # Backtest, not (yet) live — read this before quoting HistoricalAccuracy
//
// Every number in the tables above was MEASURED in a BACKTEST: computed once,
// offline, over historical bars, then frozen into accuracyFor's lookup table.
// A Forecast's HistoricalAccuracy is that lookup, stamped at predict time —
// it is not, and cannot yet be, a live/graded number. The pipeline DOES
// snapshot every forecast into regime_outcomes for later live grading
// (internal/pipeline's regime-outcome-runner) and DOES freeze the exact
// claims below into a hash-chained, falsifiable record before any of them
// resolved (internal/prereg, served at GET /api/prereg) — but as of this
// writing zero of the outstanding calls have resolved, and none can before
// firstGradableOn. A backtest number and a live number answer different
// questions and must never be read as the same thing, so every Forecast
// carries Evidence, FirstGradableOn and EvidenceCaveat rather than leaving a
// JSON consumer to infer which kind of number HistoricalAccuracy is.
package structregime

import (
	"fmt"
	"math"
	"sort"
)

// Kind identifies a validated regime target.
type Kind string

const (
	KindTrend21     Kind = "trend21"
	KindLiquidity21 Kind = "liquidity21"
	KindVol21       Kind = "vol21"
)

const (
	window     = 200  // trailing distribution for ranks / medians
	minHistory = 260  // SMA200 + warm-up
	ewmaLambda = 0.94 // RiskMetrics decay (vol target)
	horizon    = 21   // trading days ahead, all three targets
)

// Forecast is one symbol's structural-regime call.
type Forecast struct {
	Kind        Kind `json:"kind"`
	HorizonDays int  `json:"horizonDays"`
	// Regime: trend21 "uptrend"/"downtrend" · liquidity21 "active"/"quiet" ·
	// vol21 "elevated"/"calm".
	Regime     string  `json:"regime"`
	Conviction float64 `json:"conviction"`
	// HistoricalAccuracy is the accuracy accuracyFor measured for THIS
	// conviction tier in the offline BACKTEST described in the package doc —
	// the honest confidence, but NOT a live/graded number. See Evidence and
	// EvidenceCaveat, which say so explicitly on every Forecast rather than
	// leaving a reader of the JSON to infer it from this comment.
	HistoricalAccuracy float64 `json:"historicalAccuracy"`
	// Evidence names what HistoricalAccuracy is. Always evidenceBacktest for
	// every Forecast this package can currently produce — see the package
	// doc's "Backtest, not (yet) live" section. Reserved so a future "live"
	// value (built from a resolved regime_outcomes row instead of this table)
	// can never be mistaken for this one; nothing here produces that value
	// today.
	Evidence string `json:"evidence"`
	// FirstGradableOn is the earliest date this platform's structural claims
	// can have a resolved live outcome — mirrors internal/prereg.FirstGradableOn
	// exactly (TestFirstGradableOnMatchesPrereg asserts the two never drift).
	// Before it, EVERY HistoricalAccuracy on this platform is a backtest
	// claim, however it is labeled elsewhere.
	FirstGradableOn string `json:"firstGradableOn"`
	// EvidenceCaveat is the sentence a payload should render verbatim next to
	// HistoricalAccuracy so a reader never has to infer what kind of number it
	// is — see the evidenceCaveat constant's doc.
	EvidenceCaveat string  `json:"evidenceCaveat"`
	Tier           string  `json:"tier"`
	Rank           float64 `json:"rank"`
	N              int     `json:"n"`
	// EvidenceRows and EvidenceClusters say how much measurement stands behind
	// HistoricalAccuracy: the sample THIS conviction tier was measured on, and
	// the independent quarter blocks behind its CI. Both zero (and omitted from
	// the JSON) when the loop did not record them — see EvidenceSizeFor, which
	// refuses to invent a number for the kinds that did not.
	//
	// The point is that 0.985 on 68 rows across 4 quarters and 0.972 on the
	// ~900-stock / 7.5-year equity table are not the same kind of number, and
	// nothing in this payload previously let a reader tell them apart.
	EvidenceRows     int `json:"evidenceRows,omitempty"`
	EvidenceClusters int `json:"evidenceClusters,omitempty"`
	// Tradeability states the MEASURED mean forward return of THIS conviction
	// band in plain English — including the case the accuracy number hides, a
	// top band that is the most accurate and the least profitable (package doc,
	// 2026-07-24 re-validation). Empty for kinds whose forward return was never
	// measured; never inferred from another kind's table.
	Tradeability string `json:"tradeability,omitempty"`
}

// maxSaneReturn is the wild-move guard: a single-day |simple return| above
// this inside the prediction window means the series is either corrupted by an
// unadjusted split (the 2026-07-17 inspection found ~130 live symbols with
// 2x-44x one-day "jumps" from incremental fetches straddling reverse splits)
// or in a news regime the persistence statistics were not measured to cover.
// Either way the honest output is NO forecast.
const maxSaneReturn = 0.65

// firstGradableOn mirrors internal/prereg.FirstGradableOn exactly: the date
// the earliest pre-registered structural claim (internal/prereg.Specs) can
// produce a resolved live verdict. Duplicated here rather than imported, on
// purpose — prereg exists to freeze a claim independently of the code that
// produces it, so the prediction path deliberately does not import back into
// the audit package (the mirror image of prereg_test.go, which imports
// structregime to check ITS frozen numbers match, never the reverse).
// TestFirstGradableOnMatchesPrereg keeps the two dates from drifting apart
// silently, the same discipline TestFrozenClaimsMatchLivePredictors already
// applies to the accuracy numbers.
const firstGradableOn = "2026-08-07"

// evidenceBacktest/evidenceLive name what a Forecast's HistoricalAccuracy
// actually is (Forecast.Evidence). Every Forecast this package returns today
// is evidenceBacktest — read from accuracyFor's offline lookup table, never
// from grading a resolved regime_outcomes row. evidenceLive is reserved for a
// Forecast built FROM such a resolved row; nothing in this package
// constructs one, so the constant exists only so a future caller that does
// can use the same vocabulary instead of inventing a second one.
const (
	evidenceBacktest = "backtest"
	evidenceLive     = "live"
)

// evidenceCaveat is Forecast.EvidenceCaveat's fixed text. Deliberately names
// no Kind or accuracy tier of its own: trend63 and the crypto kinds build
// their Forecast by copying PredictTrend/PredictLiquidity's output and only
// overwriting Kind, HistoricalAccuracy and Tradeability (see trend63.go,
// crypto.go), so a caveat that named "trend21" here would ship unchanged on a
// trend63 or crypto row and be wrong.
const evidenceCaveat = "BACKTEST CLAIM, not a live measurement: HistoricalAccuracy is a walk-" +
	"forward backtest lookup, frozen and hash-chained BEFORE any of this predictor's forecasts " +
	"resolved — see GET /api/prereg (internal/prereg) for the exact claim this call will be " +
	"checked against. First gradable " + firstGradableOn + " — before that date this number has " +
	"zero live resolutions behind it, whatever it is labeled elsewhere. Once resolutions exist, " +
	"GET /api/track-record carries the live-vs-claimed comparison."

// wildClose reports whether the trailing `lookback` closes contain a 1-day
// move beyond maxSaneReturn.
func wildClose(closes []float64, lookback int) bool {
	lo := len(closes) - lookback
	if lo < 1 {
		lo = 1
	}
	for i := lo; i < len(closes); i++ {
		if closes[i-1] > 0 && closes[i] > 0 {
			r := closes[i]/closes[i-1] - 1
			if r > maxSaneReturn || r < -maxSaneReturn {
				return true
			}
		}
	}
	return false
}

// wildRet is wildClose for a returns series.
func wildRet(rets []float64, lookback int) bool {
	lo := len(rets) - lookback
	if lo < 0 {
		lo = 0
	}
	for _, r := range rets[lo:] {
		if r > maxSaneReturn || r < -maxSaneReturn {
			return true
		}
	}
	return false
}

// PredictTrend forecasts whether the stock stays on its current side of the
// 200-day SMA for the next 21 sessions. Pure and causal; ok=false on thin
// history or a wild-move-contaminated window (honest absences).
func PredictTrend(closes []float64) (Forecast, bool) {
	if len(closes) < minHistory || wildClose(closes, minHistory) {
		return Forecast{}, false
	}
	sma := rollMean(closes, 200)
	n := len(closes)
	cur := closes[n-1]/sma[n-1] - 1
	if !finite(cur) || cur == 0 {
		return Forecast{}, false
	}
	absd := make([]float64, n)
	for i := range closes {
		if sma[i] > 0 {
			absd[i] = math.Abs(closes[i]/sma[i] - 1)
		} else {
			absd[i] = math.NaN()
		}
	}
	// conviction = percentile of today's |distance| in its trailing window
	conv := frac(absd[max(0, n-1-window):n-1], math.Abs(cur))
	regime := "downtrend"
	if cur > 0 {
		regime = "uptrend"
	}
	return Forecast{
		Kind: KindTrend21, HorizonDays: horizon, Regime: regime,
		Conviction:         conv,
		HistoricalAccuracy: accuracyFor(KindTrend21, conv),
		Evidence:           evidenceBacktest,
		FirstGradableOn:    firstGradableOn,
		EvidenceCaveat:     evidenceCaveat,
		Tier:               tierName(conv),
		Rank:               conv,
		N:                  n,
		Tradeability:       TradeabilityFor(KindTrend21, conv),
	}, true
}

// PredictLiquidity forecasts whether mean daily dollar volume over the next 21
// sessions sits above (active) or below (quiet) its trailing-200d median.
func PredictLiquidity(closes, volumes []float64) (Forecast, bool) {
	n := len(closes)
	if n < minHistory || len(volumes) != n || wildClose(closes, minHistory) {
		return Forecast{}, false
	}
	dv := make([]float64, n)
	for i := range closes {
		x := closes[i] * volumes[i]
		if x > 0 {
			dv[i] = math.Log(x)
		} else {
			dv[i] = math.NaN()
		}
	}
	m := rollMeanNaN(dv, horizon)
	cur := m[n-1]
	if !finite(cur) {
		return Forecast{}, false
	}
	rank := frac(m[max(0, n-1-window):n-1], cur)
	conv := math.Abs(rank-0.5) * 2
	regime := "quiet"
	if rank > 0.5 {
		regime = "active"
	}
	return Forecast{
		Kind: KindLiquidity21, HorizonDays: horizon, Regime: regime,
		Conviction:         conv,
		HistoricalAccuracy: accuracyFor(KindLiquidity21, conv),
		Evidence:           evidenceBacktest,
		FirstGradableOn:    firstGradableOn,
		EvidenceCaveat:     evidenceCaveat,
		Tier:               tierName(conv),
		Rank:               rank,
		N:                  n,
	}, true
}

// PredictVol21 is the volregime method at a monthly horizon: EWMA vol ranked
// in its trailing distribution, forecasting next-21d realized vol vs its
// trailing median.
func PredictVol21(rets []float64) (Forecast, bool) {
	if len(rets) < minHistory-30 || wildRet(rets, minHistory) {
		return Forecast{}, false
	}
	ev := ewmaVol(rets)
	n := len(ev)
	rank := frac(ev[max(0, n-1-window):n-1], ev[n-1])
	conv := math.Abs(rank-0.5) * 2
	regime := "calm"
	if rank > 0.5 {
		regime = "elevated"
	}
	return Forecast{
		Kind: KindVol21, HorizonDays: horizon, Regime: regime,
		Conviction:         conv,
		HistoricalAccuracy: accuracyFor(KindVol21, conv),
		Evidence:           evidenceBacktest,
		FirstGradableOn:    firstGradableOn,
		EvidenceCaveat:     evidenceCaveat,
		Tier:               tierName(conv),
		Rank:               rank,
		N:                  n,
	}, true
}

// accuracyFor maps (kind, conviction) to the MEASURED out-of-sample accuracy
// of the conviction BAND the forecast falls in — never the cumulative or
// whole-population number, which would overstate a low-conviction call (the
// all-decisions 83.3% for trend contains the 97.2% top decile; the below-0.5
// band alone scores 73.1%). Band values are exact arithmetic decompositions
// of the measured cumulative tiers (2026-07-17 loop, independently
// re-verified same day). Monotone non-decreasing in conviction.
func accuracyFor(k Kind, conv float64) float64 {
	type bands struct{ lo, b50, b80, b90 float64 }
	var t bands
	switch k {
	case KindTrend21:
		t = bands{0.731, 0.900, 0.946, 0.972}
	case KindLiquidity21:
		t = bands{0.595, 0.739, 0.801, 0.876}
	case KindVol21:
		t = bands{0.558, 0.643, 0.668, 0.720}
	case KindTrend63:
		// CUMULATIVE tiers, not per-band decompositions — the 63d loop did not
		// record band shares, so each value is the measured accuracy of calls
		// AT OR ABOVE that conviction floor (see trend63.go; caveat shipped in
		// the /api/regimes payload).
		t = bands{0.700, 0.777, 0.817, 0.837}
	default:
		return 0.5
	}
	switch {
	case conv >= 0.9:
		return t.b90
	case conv >= 0.8:
		return t.b80
	case conv >= 0.5:
		return t.b50
	default:
		return t.lo
	}
}

func tierName(conv float64) string {
	switch {
	case conv >= 0.9:
		return "very-high conviction"
	case conv >= 0.8:
		return "high conviction"
	case conv >= 0.5:
		return "moderate conviction"
	default:
		return "low conviction"
	}
}

// ── grading (re-derive the accuracy on a caller's data; honesty surface) ──

// GradeTrend walk-forward-scores the trend predictor on one symbol's closes,
// non-overlapping at the 21d horizon: (correct, total) above the conviction
// floor.
func GradeTrend(closes []float64, minConv float64) (correct, total int) {
	n := len(closes)
	sma := rollMean(closes, 200)
	absd := make([]float64, n)
	dist := make([]float64, n)
	for i := range closes {
		if sma[i] > 0 {
			dist[i] = closes[i]/sma[i] - 1
			absd[i] = math.Abs(dist[i])
		} else {
			dist[i] = math.NaN()
			absd[i] = math.NaN()
		}
	}
	for t := minHistory; t+horizon < n; t += horizon {
		d0, d1 := dist[t], dist[t+horizon]
		if !finite(d0) || !finite(d1) || d0 == 0 || d1 == 0 {
			continue
		}
		conv := frac(absd[max(0, t-window):t], math.Abs(d0))
		if conv < minConv {
			continue
		}
		total++
		if (d0 > 0) == (d1 > 0) {
			correct++
		}
	}
	return correct, total
}

// GradeLiquidity walk-forward-scores the liquidity predictor, non-overlapping.
func GradeLiquidity(closes, volumes []float64, minConv float64) (correct, total int) {
	n := len(closes)
	if len(volumes) != n {
		return 0, 0
	}
	dv := make([]float64, n)
	for i := range closes {
		x := closes[i] * volumes[i]
		if x > 0 {
			dv[i] = math.Log(x)
		} else {
			dv[i] = math.NaN()
		}
	}
	m := rollMeanNaN(dv, horizon)
	for t := minHistory; t+horizon < n; t += horizon {
		if !finite(m[t]) {
			continue
		}
		med := medianOf(m[max(0, t-window) : t+1])
		var s float64
		cnt := 0
		for _, x := range dv[t+1 : t+1+horizon] {
			if finite(x) {
				s += x
				cnt++
			}
		}
		if cnt < horizon || !finite(med) || s/float64(cnt) == med {
			continue
		}
		rank := frac(m[max(0, t-window):t], m[t])
		conv := math.Abs(rank-0.5) * 2
		if conv < minConv {
			continue
		}
		total++
		if (rank > 0.5) == (s/float64(cnt) > med) {
			correct++
		}
	}
	return correct, total
}

// ── math (dependency-free) ──

func ewmaVol(r []float64) []float64 {
	out := make([]float64, len(r))
	var v float64
	for i, x := range r {
		v = ewmaLambda*v + (1-ewmaLambda)*x*x
		out[i] = math.Sqrt(v)
	}
	return out
}

// rollMean is the trailing w-mean requiring a full window (NaN before).
func rollMean(x []float64, w int) []float64 {
	out := make([]float64, len(x))
	var s float64
	for i := range x {
		s += x[i]
		if i >= w {
			s -= x[i-w]
		}
		if i >= w-1 {
			out[i] = s / float64(w)
		} else {
			out[i] = math.NaN()
		}
	}
	return out
}

// rollMeanNaN is rollMean tolerating NaNs (full finite window required).
func rollMeanNaN(x []float64, w int) []float64 {
	out := make([]float64, len(x))
	for i := range out {
		out[i] = math.NaN()
	}
	for i := w - 1; i < len(x); i++ {
		var s float64
		ok := true
		for _, v := range x[i-w+1 : i+1] {
			if !finite(v) {
				ok = false
				break
			}
			s += v
		}
		if ok {
			out[i] = s / float64(w)
		}
	}
	return out
}

// frac is the percentile of v in the finite window values, counting ties as
// half (so a flat series ranks 0.5 = zero conviction, an honest no-signal).
func frac(win []float64, v float64) float64 {
	below, eq, n := 0, 0, 0
	for _, x := range win {
		if !finite(x) {
			continue
		}
		n++
		switch {
		case x < v:
			below++
		case x == v:
			eq++
		}
	}
	if n == 0 || !finite(v) {
		return 0.5
	}
	return (float64(below) + 0.5*float64(eq)) / float64(n)
}

func medianOf(x []float64) float64 {
	a := make([]float64, 0, len(x))
	for _, v := range x {
		if finite(v) {
			a = append(a, v)
		}
	}
	if len(a) == 0 {
		return math.NaN()
	}
	sort.Float64s(a)
	return a[len(a)/2]
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ── forward return by conviction band (2026-07-24 re-validation) ──

// forwardReturnFor is accuracyFor's parallel: it maps (kind, conviction) to the
// MEASURED mean forward return, in percent, of the conviction BAND the forecast
// falls in — the number that decides whether the hit rate is worth anything.
// Same band cutoffs as accuracyFor, and the same rule: measured or absent,
// never invented. ok=false for every kind whose forward return the 2026-07-24
// re-validation did not measure (liquidity21, vol21, both crypto kinds) and for
// trend63's bands below 0.9, which it did not report.
//
// Unlike accuracyFor this is NOT monotone in conviction — trend21 rises to
// +0.79% at 0.8-0.9 and then INVERTS to -0.39% at >=0.9. That inversion is the
// finding, not a typo: conviction is distance from the SMA200, so the most
// confident band is by construction the most extended one, and extended names
// mean-revert over the next month.
func forwardReturnFor(k Kind, conv float64) (pct float64, ok bool) {
	type bands struct{ lo, b50, b80, b90 float64 }
	nm := math.NaN() // not measured — refuse rather than interpolate
	var t bands
	switch k {
	case KindTrend21:
		// The re-validation split the sub-0.5 region into 0.00-0.25 (+0.41%) and
		// 0.25-0.50 (+0.51%); the band served here is the LOWER of the two,
		// because a share-weighted blend of them was never measured.
		//
		// TOP BAND CORRECTED 2026-07-26 from -0.39% to -0.76%. An adversarial
		// review claimed the disclosed downside understated the measured loss by
		// 5x (-2.05%). An independent replication run here against the live bars
		// — 976 stock symbols, conviction >= 0.9, NON-OVERLAPPING 21-session
		// forward windows, one call per (symbol, day), n = 18,850 — measured
		// -0.76% with a 95% interval of [-1.19%, -0.32%], alongside a 95.17%
		// persistence accuracy that corroborates the accuracy ladder.
		//
		// So the reviewer's DIRECTION replicates and their MAGNITUDE does not:
		// -2.05% sits outside this interval, and their method was not stated, so
		// it could not be reproduced. The served number is the measurement, not
		// the worse unreproduced figure and not the kinder original — adopting an
		// unverified number because it flatters nobody would be the same failure
		// as keeping one that flatters us. The interval is what to quote; the
		// point estimate is a mean over a right-skewed distribution whose median
		// is only -0.05%, i.e. the loss lives in a tail, not in the typical call.
		t = bands{0.41, 0.58, 0.79, -0.76}
	case KindTrend63:
		// Only the top band was reported at the quarterly horizon.
		t = bands{nm, nm, nm, -1.30}
	default:
		return 0, false
	}
	var v float64
	switch {
	case conv >= 0.9:
		v = t.b90
	case conv >= 0.8:
		v = t.b80
	case conv >= 0.5:
		v = t.b50
	default:
		v = t.lo
	}
	if !finite(v) {
		return 0, false
	}
	return v, true
}

// TradeabilityFor renders forwardReturnFor as the sentence that ships with the
// forecast. Exported because the store does not persist the string (it is a
// pure function of kind + conviction), so every read surface derives it from
// the row's OWN kind — a trend21 number can never end up labelling a trend63 or
// crypto row. Empty string means the forward return was not measured, which is
// the honest output rather than a reassuring guess.
func TradeabilityFor(k Kind, conv float64) string {
	pct, ok := forwardReturnFor(k, conv)
	if !ok {
		return ""
	}
	h := horizon
	if k == KindTrend63 {
		h = horizon63
	}
	if pct <= 0 {
		return fmt.Sprintf("NOT A TRADE: the measured mean forward %dd return in this "+
			"conviction band is %+.2f%% — this band has the HIGHEST hit rate and the WORST "+
			"return. High conviction means price is far from its 200-day average, i.e. already "+
			"extended, and extended names mean-revert. Accuracy here is a persistence "+
			"statistic, NOT a profitable trade: higher accuracy does NOT mean higher return. "+
			"Measured 2026-07-24 over 968 stocks / 1900 trading days with non-overlapping "+
			"forward windows.", h, pct)
	}
	return fmt.Sprintf("The measured mean forward %dd return in this conviction band is "+
		"%+.2f%% (2026-07-24, 968 stocks / 1900 trading days, non-overlapping forward "+
		"windows). That is a measurement of what this band did on average, not advice and "+
		"not an expectation for any one symbol — and accuracy is a separate axis from "+
		"return: the HIGHEST-accuracy band of this kind has a NEGATIVE mean forward return.",
		h, pct)
}

// AccuracyForTest exposes the band-accuracy tables to the pre-registration
// package, which must compare what a predictor CLAIMS today against the claim
// frozen before its forecasts resolved. Exported for that check specifically:
// the alternative is duplicating the tables in prereg, and two copies of the
// same numbers is precisely the drift the check exists to catch.
//
// It routes crypto kinds to the crypto table, because those accuracies were
// measured on a different (much smaller, bear-dominated) sample and quoting an
// equity number for them would be the same class of error the check prevents.
func AccuracyForTest(k Kind, conv float64) float64 {
	switch k {
	case KindTrendCrypto21, KindLiquidityCrypto21:
		return cryptoAccuracyFor(k, conv)
	default:
		return accuracyFor(k, conv)
	}
}

// EvidenceCaveatText exposes the fixed caveat sentence so a surface OUTSIDE
// this package (the MCP server) can ship it verbatim beside a stored accuracy
// rather than paraphrasing it. A paraphrase of a caveat is how a caveat gets
// softer with every copy.
func EvidenceCaveatText() string { return evidenceCaveat }

// FirstGradableOnDate exposes the mirrored first-gradable date for the same
// reason.
func FirstGradableOnDate() string { return firstGradableOn }
