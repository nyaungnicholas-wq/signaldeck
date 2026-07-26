package papertrade

import "math"

// ── DOWNSIDE-ONLY RISK ──────────────────────────────────────────────────────
//
// Sharpe divides by the standard deviation of ALL returns, which charges a book
// for its upside surprises as if they were risk. For a long/flat signal book
// that is the wrong denominator: the whole design intent is asymmetry — be long
// when the signal fires, be flat otherwise — so the return distribution is
// supposed to be skewed, and a metric that penalizes skew reports the design as
// a defect.
//
// Sortino replaces the denominator with the deviation of the returns that
// actually hurt (Sortino & Van Der Meer, 1991). Two choices here are worth
// stating rather than burying, because implementations differ and the difference
// is large:
//
//   - The target return (MAR) is ZERO, not the mean. "Below zero" is the loss a
//     reader means by downside; measuring against the sample's own mean would
//     make a book's threshold move with its luck.
//   - The downside deviation divides the sum of squared shortfalls by the FULL
//     observation count, not by the count of negative marks. Dividing by the
//     negatives alone measures "how bad were the bad days", which is a different
//     question and makes a book with rare-but-severe losses look safer than one
//     with frequent small ones. The full-count convention is the one Sortino
//     defines and the one that keeps Sortino comparable to Sharpe.
//
// Both statistics are WITHHELD rather than defaulted when the sample cannot
// support them, matching Summary's discipline: a Sortino with no losing mark is
// division by zero, and reporting it as "infinite skill" would be the most
// flattering possible reading of too little data.

// Sortino is the annualized downside-deviation-adjusted return of an equity
// curve (target 0, risk-free 0).
//
// ok is false when the curve is too short (fewer than minSharpeMarks marks, the
// same floor Sharpe uses) or when no mark was negative — in which case the
// denominator is zero and the ratio is not a number a reader should be shown.
func Sortino(curve []EquityPoint) (float64, bool) {
	if len(curve) < minSharpeMarks {
		return 0, false
	}
	rets := equityReturns(curve)
	if len(rets) < 2 {
		return 0, false
	}
	dd, ok := downsideDeviation(rets)
	if !ok {
		return 0, false
	}
	var sum float64
	for _, r := range rets {
		sum += r
	}
	mean := sum / float64(len(rets))
	mpy := marksPerYear(curve)
	if mpy <= 0 {
		mpy = 252
	}
	v := mean / dd * math.Sqrt(mpy)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// downsideDeviation is the root-mean-square of the returns below zero, divided
// by the FULL sample count (see the package note above). ok is false when no
// return was negative, because the deviation is then zero and every ratio built
// on it is undefined rather than excellent.
func downsideDeviation(rets []float64) (float64, bool) {
	if len(rets) == 0 {
		return 0, false
	}
	var ss float64
	neg := 0
	for _, r := range rets {
		if r < 0 {
			ss += r * r
			neg++
		}
	}
	if neg == 0 || ss <= 0 {
		return 0, false
	}
	dd := math.Sqrt(ss / float64(len(rets)))
	if dd <= 0 || math.IsNaN(dd) || math.IsInf(dd, 0) {
		return 0, false
	}
	return dd, true
}

// CurrentDrawdown is the equity curve's drawdown AS OF ITS LAST MARK, as a
// positive fraction of the running peak — distinct from Summary.MaxDrawdown,
// which is the worst drawdown the curve ever reached.
//
// The distinction is the whole point of a circuit breaker: a book that fell 30%
// and fully recovered has a 30% max drawdown and a 0% current drawdown, and
// halting it today for a wound it already healed would be a bug, not caution.
// ok is false for an empty curve or a non-positive peak, where the fraction has
// no meaning.
func CurrentDrawdown(curve []EquityPoint) (float64, bool) {
	if len(curve) == 0 {
		return 0, false
	}
	peak := math.Inf(-1)
	for _, p := range curve {
		if p.Equity > peak {
			peak = p.Equity
		}
	}
	if peak <= 0 || math.IsInf(peak, 0) {
		return 0, false
	}
	last := curve[len(curve)-1].Equity
	dd := 1 - last/peak
	if dd < 0 {
		dd = 0 // the last mark IS the peak
	}
	if math.IsNaN(dd) || math.IsInf(dd, 0) {
		return 0, false
	}
	return dd, true
}
