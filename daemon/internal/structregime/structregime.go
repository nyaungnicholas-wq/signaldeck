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
// TREND (21d): will the stock still be on its current side of the 200-day SMA
// in 21 trading days? Conviction = trailing 200d percentile of |close/SMA-1|.
//
//	cumulative: all 83.3% · conv>0.5 93.1% · conv>0.8 96.2% · conv>0.9 97.2% [CI 0.965-0.978]
//	per-band (what a forecast reports): <0.5 73.1% · 0.5-0.8 90.0% · 0.8-0.9 94.6% · >=0.9 97.2%
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
//   - All targets: a regime call is situational awareness with a measured hit
//     rate, not a trade recommendation.
package structregime

import (
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
	// HistoricalAccuracy is the MEASURED walk-forward accuracy at THIS
	// conviction tier (package doc) — the honest confidence.
	HistoricalAccuracy float64 `json:"historicalAccuracy"`
	Tier               string  `json:"tier"`
	Rank               float64 `json:"rank"`
	N                  int     `json:"n"`
}

// maxSaneReturn is the wild-move guard: a single-day |simple return| above
// this inside the prediction window means the series is either corrupted by an
// unadjusted split (the 2026-07-17 inspection found ~130 live symbols with
// 2x-44x one-day "jumps" from incremental fetches straddling reverse splits)
// or in a news regime the persistence statistics were not measured to cover.
// Either way the honest output is NO forecast.
const maxSaneReturn = 0.65

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
		Tier:               tierName(conv),
		Rank:               conv,
		N:                  n,
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
