// Package volregime is SignalDeck's VOLATILITY-REGIME predictor — the one
// forecast on this platform with a validated, honest edge.
//
// # What it predicts
//
// NOT price direction (proven ~52-55% ceiling — a coin flip). Instead: will a
// stock's realized volatility over the next ~quarter (63 trading days) be in the
// UPPER half of its recent distribution, or the LOWER half? Volatility is
// strongly persistent (its long memory is one of the most robust facts in
// finance), so this is genuinely forecastable.
//
// # Method (deterministic, causal, no lookahead)
//
// Compute a smooth current-vol estimate — RiskMetrics EWMA of squared daily
// returns (λ=0.94, ~33-day memory) — and RANK it within its own trailing
// 200-day distribution. A high rank means "currently much more volatile than
// usual", which persistence says will continue; a low rank means "unusually
// calm", which also persists. The rank's distance from the median IS the
// conviction: extremes are the most predictable.
//
// # Honesty: the accuracy numbers are MEASURED, not asserted
//
// AccuracyForConviction returns the accuracy this method actually achieved in a
// strict out-of-sample backtest over ~1,000 stocks and 7.5 years, graded
// WALK-FORWARD, NON-OVERLAPPING (63-day forward windows never share a day), and
// block-clustered by quarter so the sample size is the ~30 independent quarters,
// not autocorrelated days. Those measured tiers (2026-07-17):
//
//	all decisions           63.7%
//	conviction > 0.5 (57%)  69.7%
//	conviction > 0.8 (28%)  74.3%   [quarter-clustered 95% CI 0.691-0.782]
//	conviction > 0.9 (17%)  76.0%   [0.696-0.789]
//
// The predictor never claims more than it measured, and it labels a low-
// conviction call as exactly that.
//
// # Backtest, not (yet) live — read this before quoting HistoricalAccuracy
//
// "Measured" above means measured in a BACKTEST: the tiers were computed once,
// offline, over historical bars, then frozen into the AccuracyForConviction
// lookup table. Every Forecast this package returns today reads that table —
// none is built from a resolved live outcome, because no live outcome exists
// yet. Nothing in this package or its callers snapshots a Forecast and grades
// it after the fact the way internal/structregime's regime_outcomes table
// does (and that table, as of this writing, ALSO has zero resolutions — see
// internal/prereg). A backtest number and a live number answer different
// questions and must never be read as the same thing, so every Forecast is
// stamped with Evidence and EvidenceCaveat rather than leaving a JSON
// consumer to infer which kind of number HistoricalAccuracy is.
package volregime

import (
	"math"
	"sort"
)

// minHistory is the least daily returns needed: the 200-day rank window plus a
// short EWMA warm-up.
const minHistory = 230

// window is the trailing distribution the current vol is ranked within.
const window = 200

// ewmaLambda is the RiskMetrics decay (0.94 ≈ 33-day half-life).
const ewmaLambda = 0.94

// Forecast is one symbol's volatility-regime call.
type Forecast struct {
	// Regime is "elevated" (upper-half vol expected) or "calm" (lower half).
	Regime string `json:"regime"`
	// Conviction in [0,1] = |rank-0.5|*2. Extremes predict best.
	Conviction float64 `json:"conviction"`
	// HistoricalAccuracy is the accuracy AccuracyForConviction measured for
	// THIS conviction tier in the offline BACKTEST described in the package
	// doc — the honest confidence, never an invented probability, but also
	// NOT a live/graded number. See Evidence and EvidenceCaveat, which say so
	// explicitly on every Forecast rather than leaving a reader of the JSON to
	// infer it from this comment.
	HistoricalAccuracy float64 `json:"historicalAccuracy"`
	// Evidence names what HistoricalAccuracy is. Always evidenceBacktest for
	// every Forecast this package can currently produce — see the package
	// doc's "Backtest, not (yet) live" section. Reserved so a future "live"
	// value (built from a resolved, out-of-sample outcome instead of this
	// table) can never be mistaken for this one; nothing here produces that
	// value today.
	Evidence string `json:"evidence"`
	// FirstGradableOn is deliberately left empty (omitted from JSON): unlike
	// internal/structregime, whose forecasts are snapshotted for later grading
	// and carry a committed date (internal/prereg.FirstGradableOn), nothing
	// snapshots THIS predictor's calls for live grading, so there is no date
	// to honestly promise. Populated only if that changes.
	FirstGradableOn string `json:"firstGradableOn,omitempty"`
	// EvidenceCaveat is the sentence a payload should render verbatim next to
	// HistoricalAccuracy so a reader never has to infer what kind of number it
	// is — see the evidenceCaveat constant's doc.
	EvidenceCaveat string `json:"evidenceCaveat"`
	// Tier is a human label for the conviction bucket.
	Tier string `json:"tier"`
	// Rank is the current EWMA vol's percentile in its trailing window [0,1].
	Rank float64 `json:"rank"`
	// N is the number of daily returns the forecast rests on.
	N int `json:"n"`
}

// TradingDaysPerYear is the annualization factor for daily volatility. Exported
// because every consumer that converts this predictor's output into an
// annualized vol (internal/options) must use the SAME constant — a mismatched
// annualization silently shifts every implied-vs-forecast comparison.
const TradingDaysPerYear = 252

// evidenceBacktest is the only value Forecast.Evidence currently takes — see
// the package doc's "Backtest, not (yet) live" section. Defined as a named
// constant (rather than a literal "backtest" repeated at each call site) so a
// future live-grading path has one place to introduce its counterpart.
const evidenceBacktest = "backtest"

// evidenceCaveat is Forecast.EvidenceCaveat's fixed text. Deliberately names
// no conviction tier or accuracy number of its own, so it stays correct
// regardless of which tier a given Forecast landed in.
const evidenceCaveat = "BACKTEST CLAIM, not a live measurement: HistoricalAccuracy is a walk-" +
	"forward backtest lookup (AccuracyForConviction), computed offline before any forecast from " +
	"this predictor was graded against what actually happened. No live grading loop is wired to " +
	"this quarterly predictor yet, so no first-gradable date is promised here. Treat this number " +
	"as the platform's best backtest evidence for this predictor, not a live track record."

// maxSaneReturn guards against unadjusted-split corruption: a one-day |simple
// return| above this inside the prediction window (found on ~130 live symbols
// by the 2026-07-17 inspection — 2x-44x "jumps" where incremental fetches
// straddled reverse splits) poisons the EWMA and its trailing rank. The honest
// output for such a series is NO forecast.
const maxSaneReturn = 0.65

// Predict produces the volatility-regime forecast from a symbol's daily returns
// (oldest first). ok=false when there is too little history or the window
// contains a wild (likely split-corrupted) move — honest absences, never a
// guess. Pure and causal: only the supplied (past) returns are used.
func Predict(rets []float64) (Forecast, bool) {
	if len(rets) < minHistory {
		return Forecast{}, false
	}
	for _, r := range rets[len(rets)-minHistory:] {
		if r > maxSaneReturn || r < -maxSaneReturn {
			return Forecast{}, false
		}
	}
	ev := ewmaVol(rets)
	cur := ev[len(ev)-1]
	win := ev[len(ev)-window:]
	rank := frac(win, cur)
	conv := math.Abs(rank-0.5) * 2

	regime := "calm"
	if rank > 0.5 {
		regime = "elevated"
	}
	return Forecast{
		Regime:             regime,
		Conviction:         conv,
		HistoricalAccuracy: AccuracyForConviction(conv),
		Evidence:           evidenceBacktest,
		EvidenceCaveat:     evidenceCaveat,
		Tier:               tierName(conv),
		Rank:               rank,
		N:                  len(rets),
	}, true
}

// AccuracyForConviction maps a conviction to the MEASURED out-of-sample accuracy
// of this method (see package doc). Monotone non-decreasing; never exceeds the
// measured ceiling; a coin-flip conviction returns ~0.5 (no claimed skill).
func AccuracyForConviction(conv float64) float64 {
	switch {
	case conv >= 0.9:
		return 0.76
	case conv >= 0.8:
		return 0.74
	case conv >= 0.5:
		return 0.70
	case conv >= 0.25:
		return 0.62
	default:
		return 0.55
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
	case conv >= 0.25:
		return "low conviction"
	default:
		return "no measurable edge"
	}
}

// ── grading (the honesty surface: prove the accuracy on a caller's data) ──

// Grade walk-forward-scores this method on one symbol's full return history,
// NON-OVERLAPPING at the given horizon, returning (correct, total) directional
// calls above the given conviction floor. Used by tests and the honesty API to
// re-derive the accuracy on live data rather than trust a constant.
func Grade(rets []float64, horizon int, minConv float64) (correct, total int) {
	rv := rollStd(rets, horizon)
	ev := ewmaVol(rets)
	for i := window + 20; i+horizon < len(rets); i += horizon {
		w := ev[i-window : i]
		rank := frac(w, ev[i])
		conv := math.Abs(rank-0.5) * 2
		if conv < minConv {
			continue
		}
		thr := median(rvSlice(rv, i-window, i))
		pred := rank > 0.5
		act := rv[i+horizon] > thr
		total++
		if pred == act {
			correct++
		}
	}
	return correct, total
}

// Instance is one historical walk-forward occurrence of the vol-regime call
// on a symbol (non-overlapping 63d windows), for the per-signal report page.
type Instance struct {
	Ts         int64   `json:"ts"`
	Regime     string  `json:"regime"`
	Conviction float64 `json:"conviction"`
	Actual     string  `json:"actual"`
	Correct    bool    `json:"correct"`
	// ForwardVol is the ANNUALIZED realized volatility that actually followed
	// this call over the horizon (daily stdev x sqrt(252)). The regime label is
	// binary; this is the level behind it, and it is what an options surface
	// needs to compare a forecast against an implied vol (internal/options).
	ForwardVol float64 `json:"forwardVol"`
}

// History replays the predictor over one symbol's returns (ts aligned to
// rets) and returns every non-overlapping instance with its resolution,
// oldest first — the same walk Grade counts, made visible.
func History(ts []int64, rets []float64, horizon int) []Instance {
	if len(ts) != len(rets) || horizon <= 0 {
		return nil
	}
	rv := rollStd(rets, horizon)
	ev := ewmaVol(rets)
	var out []Instance
	for i := window + 20; i+horizon < len(rets); i += horizon {
		wild := false
		lo := i - minHistory
		if lo < 0 {
			lo = 0
		}
		for _, r := range rets[lo:i] {
			if r > maxSaneReturn || r < -maxSaneReturn {
				wild = true
				break
			}
		}
		if wild {
			continue
		}
		w := ev[i-window : i]
		rank := frac(w, ev[i])
		conv := math.Abs(rank-0.5) * 2
		thr := median(rvSlice(rv, i-window, i))
		pred, act := "calm", "calm"
		if rank > 0.5 {
			pred = "elevated"
		}
		if rv[i+horizon] > thr {
			act = "elevated"
		}
		out = append(out, Instance{Ts: ts[i], Regime: pred, Conviction: conv,
			Actual: act, Correct: pred == act,
			ForwardVol: rv[i+horizon] * math.Sqrt(TradingDaysPerYear)})
	}
	return out
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

// rollStd is the trailing w-day realized vol (stdev of returns) at each index.
func rollStd(r []float64, w int) []float64 {
	out := make([]float64, len(r))
	for i := range r {
		lo := i - w + 1
		if lo < 0 {
			lo = 0
		}
		out[i] = stdev(r[lo : i+1])
	}
	return out
}

func rvSlice(rv []float64, lo, hi int) []float64 {
	if lo < 0 {
		lo = 0
	}
	return rv[lo:hi]
}

func frac(win []float64, v float64) float64 {
	if len(win) == 0 {
		return 0.5
	}
	c := 0
	for _, x := range win {
		if x < v {
			c++
		}
	}
	return float64(c) / float64(len(win))
}

func median(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	a := append([]float64(nil), x...)
	sort.Float64s(a)
	return a[len(a)/2]
}

func stdev(r []float64) float64 {
	if len(r) < 2 {
		return 0
	}
	var m float64
	for _, x := range r {
		m += x
	}
	m /= float64(len(r))
	var v float64
	for _, x := range r {
		v += (x - m) * (x - m)
	}
	return math.Sqrt(v / float64(len(r)-1))
}
