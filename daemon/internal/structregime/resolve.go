// Resolution helpers for the LIVE regime-forecast grading loop (credibility
// wave). Each Resolve*At recomputes the REALIZED regime label for a call made
// at bar index t, using EXACTLY the math the history walkers in report.go and
// the accuracy loops used — exported from this package so the outcome worker
// can never drift into subtly-different arithmetic.
//
// Causality contract: everything "at call time" (trailing medians, ranks) is
// computed from indices <= t only; the forward window is indices t+1..t+h.
// Indices beyond t+h are never read, so resolving is lookahead-free by
// construction (tests assert truncation invariance).
package structregime

import "math"

// Resolution is a realized regime label plus the one measured number behind it
// (the postmortem's "key number" — what actually happened, in the metric the
// call was about).
type Resolution struct {
	Actual   string  // realized regime label, same vocabulary as the call
	KeyName  string  // which measured number KeyValue is
	KeyValue float64 // the realized number (never invented)
}

// ResolveTrendAt resolves a trend21/trend63 call made at bar index t of closes:
// is the close still on the same side of the SMA200 at t+horizonDays?
// KeyValue = the realized close/SMA200 distance in PERCENT at the horizon.
// ok=false when the forward bar is missing or either distance is degenerate
// (an honest "cannot grade", left unresolved by the caller).
func ResolveTrendAt(closes []float64, t, horizonDays int) (Resolution, bool) {
	n := len(closes)
	if t < 0 || t+horizonDays >= n {
		return Resolution{}, false
	}
	sma := rollMean(closes, 200)
	if sma[t] <= 0 || sma[t+horizonDays] <= 0 {
		return Resolution{}, false
	}
	d0 := closes[t]/sma[t] - 1
	d1 := closes[t+horizonDays]/sma[t+horizonDays] - 1
	if !finite(d0) || !finite(d1) || d0 == 0 || d1 == 0 {
		return Resolution{}, false
	}
	act := "downtrend"
	if d1 > 0 {
		act = "uptrend"
	}
	return Resolution{Actual: act, KeyName: "sma200_distance_pct_at_horizon", KeyValue: d1 * 100}, true
}

// ResolveLiquidityAt resolves a liquidity21 call made at bar index t: mean log
// dollar volume over the 21 forward sessions vs the trailing-200d median of the
// rolling-21d mean AT CALL TIME (computed causally from indices <= t, exactly
// like LiquidityHistory). KeyValue = forward mean minus that causal median (log
// points). ok=false on missing forward bars, an undefined median, or an exact
// tie (the engine defines no label on a tie).
func ResolveLiquidityAt(closes, volumes []float64, t int) (Resolution, bool) {
	n := len(closes)
	if len(volumes) != n || t < 0 || t+horizon >= n {
		return Resolution{}, false
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
	m := rollMeanNaN(dv[:t+1], horizon) // causal: rolling mean from bars <= t only
	med := medianOf(m[max(0, t-window) : t+1])
	var s float64
	cnt := 0
	for _, x := range dv[t+1 : t+1+horizon] {
		if finite(x) {
			s += x
			cnt++
		}
	}
	if cnt < horizon || !finite(med) {
		return Resolution{}, false
	}
	fwd := s / float64(cnt)
	if fwd == med {
		return Resolution{}, false // tie: the engine defines no label
	}
	act := "quiet"
	if fwd > med {
		act = "active"
	}
	return Resolution{Actual: act, KeyName: "fwd21d_mean_log_dollar_vol_minus_trailing_median", KeyValue: fwd - med}, true
}

// ResolveVol21At resolves a vol21 call made at RETURN index t (ret index i
// resolves at bar i+1): realized vol of the 21 forward returns vs the
// trailing-200d median of the rolling-21d realized vol AT CALL TIME — the
// exact Vol21History arithmetic. KeyValue = forward realized vol minus that
// causal median (daily units). ok=false on missing forward returns, an
// undefined median, or an exact tie.
func ResolveVol21At(rets []float64, t int) (Resolution, bool) {
	n := len(rets)
	if t < 0 || t+horizon >= n {
		return Resolution{}, false
	}
	// rolling 21d realized vol, causal per index (rv[i] uses rets[i-20..i])
	hi := t + horizon
	rv := make([]float64, hi+1)
	for i := range rv {
		rv[i] = math.NaN()
	}
	for i := horizon; i <= hi; i++ {
		var sum, sq float64
		cnt := 0
		for _, x := range rets[i-horizon+1 : i+1] {
			if finite(x) {
				sum += x
				sq += x * x
				cnt++
			}
		}
		if cnt == horizon {
			mean := sum / float64(cnt)
			rv[i] = math.Sqrt(sq/float64(cnt) - mean*mean)
		}
	}
	med := medianOf(rv[max(0, t-window) : t+1])
	fwd := rv[hi] // covers rets[t+1..t+21] — exactly the 21 forward returns
	if !finite(med) || !finite(fwd) || fwd == med {
		return Resolution{}, false
	}
	act := "calm"
	if fwd > med {
		act = "elevated"
	}
	return Resolution{Actual: act, KeyName: "fwd21d_realized_vol_minus_trailing_median_daily", KeyValue: fwd - med}, true
}

// ── NAIVE-PERSISTENCE NULL (2026-07-27) ──────────────────────────────────────
//
// Every structural predictor here answers a persistence question, so the only
// baseline that can falsify one is the "nothing changes" guess: the label the
// CURRENT state already carries at the call bar. Until now the grader had no
// such null for structural kinds and could therefore never return a failing
// verdict for them — while this package's own liquidity caveat states that
// naive persistence scores the SAME accuracy. These helpers compute that guess
// from the same causal arithmetic the resolvers use (indices <= t only), so it
// is measured, never asserted equal to the call.
//
// ok=false is an honest "no baseline here" — the caller freezes NULL rather
// than guessing, and a NULL naive label is excluded from the benchmark tally
// instead of being scored as a miss.

// NaiveTrendAt is the at-call-time side of the SMA200 at bar t — the guess that
// the current trend simply persists over the horizon.
func NaiveTrendAt(closes []float64, t int) (string, bool) {
	if t < 0 || t >= len(closes) {
		return "", false
	}
	sma := rollMean(closes, 200)
	if sma[t] <= 0 {
		return "", false
	}
	d := closes[t]/sma[t] - 1
	if !finite(d) || d == 0 {
		return "", false
	}
	if d > 0 {
		return "uptrend", true
	}
	return "downtrend", true
}

// NaiveLiquidityAt is the at-call-time liquidity side at bar t: the causal
// rolling-21d mean log dollar volume vs its trailing-200d median — the same
// two numbers ResolveLiquidityAt compares the FORWARD window against.
func NaiveLiquidityAt(closes, volumes []float64, t int) (string, bool) {
	n := len(closes)
	if len(volumes) != n || t < 0 || t >= n {
		return "", false
	}
	dv := make([]float64, t+1)
	for i := 0; i <= t; i++ {
		x := closes[i] * volumes[i]
		if x > 0 {
			dv[i] = math.Log(x)
		} else {
			dv[i] = math.NaN()
		}
	}
	m := rollMeanNaN(dv, horizon)
	cur := m[t]
	med := medianOf(m[max(0, t-window) : t+1])
	if !finite(cur) || !finite(med) || cur == med {
		return "", false
	}
	if cur > med {
		return "active", true
	}
	return "quiet", true
}

// NaiveVol21At is the at-call-time volatility half at RETURN index t: the
// causal rolling-21d realized vol vs its trailing-200d median — the same pair
// ResolveVol21At compares the forward window against. Deliberately NOT the
// predictor's EWMA rank, so the benchmark is an independent baseline rather
// than a copy of the model.
func NaiveVol21At(rets []float64, t int) (string, bool) {
	if t < horizon-1 || t >= len(rets) {
		return "", false
	}
	rv := make([]float64, t+1)
	for i := range rv {
		rv[i] = math.NaN()
	}
	for i := horizon - 1; i <= t; i++ {
		var sum, sq float64
		cnt := 0
		for _, x := range rets[i-horizon+1 : i+1] {
			if finite(x) {
				sum += x
				sq += x * x
				cnt++
			}
		}
		if cnt == horizon {
			mean := sum / float64(cnt)
			rv[i] = math.Sqrt(sq/float64(cnt) - mean*mean)
		}
	}
	cur := rv[t]
	med := medianOf(rv[max(0, t-window) : t+1])
	if !finite(cur) || !finite(med) || cur == med {
		return "", false
	}
	if cur > med {
		return "elevated", true
	}
	return "calm", true
}
