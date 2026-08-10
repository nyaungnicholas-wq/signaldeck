package clusterstat

import "math"

// DayClusteredAUC measures a leg's CROSS-SECTIONAL ranking skill: for each
// trading day it computes the AUC of the leg's score against the realized
// direction ACROSS SYMBOLS, then summarises those daily numbers with a
// day-clustered interval.
//
// WHY THIS EXISTS, and why it is not the same as the number it replaces.
//
// The fleet veto read an n-weighted mean of PER-SYMBOL AUCs — one AUC per
// symbol, each estimated over that symbol's own ~30 resolved days. Two things
// are wrong with that for this purpose.
//
// First it answers the wrong question. A leg is used to rank symbols AGAINST
// EACH OTHER on a given day; "does this symbol's pressure predict its own moves
// over time" is a different property, and a fleet can be good at one and
// hopeless at the other.
//
// Second it is a noisy statistic averaged as though it were a precise one. An
// AUC on ~30 observations has a standard error near 0.1; averaging a thousand
// of them weights the noise by n without ever accounting for it. Measured on
// the live 1d pressure record 2026-08-08, the two estimators disagree materially:
//
//	n-weighted mean of per-symbol AUCs   0.3614   -> "strongly backwards"
//	day-clustered within-day AUC         0.4636   95% CI [0.4113, 0.5158]
//
// The first vetoed the leg outright. The second cannot distinguish it from
// chance. Gating a fleet on the first is gating on an artifact of the estimator.
//
// The interval is a t-style normal interval over the DAILY AUCs — the day is the
// independence unit, because every symbol on one day shares one market move.
// Fewer than MinDaysForInterval days yields ok=false: an interval over two
// numbers is not an interval, and the caller must treat that as "unmeasured"
// rather than as either verdict.
func DayClusteredAUC(daily []float64) (mean, lo, hi float64, ok bool) {
	if len(daily) < MinDaysForInterval {
		return 0, 0, 0, false
	}
	var sum float64
	for _, a := range daily {
		sum += a
	}
	mean = sum / float64(len(daily))

	var ss float64
	for _, a := range daily {
		ss += (a - mean) * (a - mean)
	}
	// Sample standard deviation, then the standard error of the daily mean.
	sd := math.Sqrt(ss / float64(len(daily)-1))
	se := sd / math.Sqrt(float64(len(daily)))

	lo, hi = mean-1.959963984540054*se, mean+1.959963984540054*se
	return mean, math.Max(0, lo), math.Min(1, hi), true
}

// MinDaysForInterval is the number of distinct trading days required before a
// day-clustered interval is reported at all.
//
// Ten matches the accuracy registry's floor and internal/forecastmon's, so the
// gate that BENCHES a leg and the surface that GRADES it cannot disagree about
// how much evidence is enough.
const MinDaysForInterval = 10

// VetoOnDayClusteredAUC reports whether a leg should be benched fleet-wide.
//
// It vetoes only when the leg is MEASURABLY not better than chance — the whole
// interval sits at or below 0.5 — rather than whenever a point estimate does.
// That keeps the veto one-directional, which is its original design: a good
// fleet never promotes a bad symbol, only a demonstrably bad fleet demotes a
// good one. An interval STRADDLING 0.5 is not a demonstration; the per-symbol
// rank edge, which carries its own Wilson lower bound, decides those.
//
// Unmeasured (too few days) never vetoes. Absence of evidence is not evidence of
// backwardness, exactly as it is not evidence of edge.
func VetoOnDayClusteredAUC(daily []float64) (veto bool, mean float64, measured bool) {
	m, _, hi, ok := DayClusteredAUC(daily)
	if !ok {
		return false, 0, false
	}
	return hi <= 0.5, m, true
}
