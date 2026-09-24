package clusterstat

import "math"

// DayClusteredAUCLag computes the mean AUC and a Newey-West corrected confidence
// interval that accounts for autocorrelation induced by overlapping label
// horizons. For a horizon of H days the labels of consecutive days overlap
// in H-1 sessions, making daily AUCs serially correlated; the naive
// sd/sqrtn therefore overstates precision. The Bartlett-weighted (Newey-West)
// long-run variance with lag = H-1 (here supplied as `lag`) yields a
// consistent estimator of the variance of the sample mean. When lag = 0 the
// formula reduces to the original DayClusteredAUC, reproducing the
// independence assumption.
func DayClusteredAUCLag(daily []float64, lag int) (mean, lo, hi float64, ok bool) {
	n := len(daily)
	if n < MinDaysForInterval {
		return 0, 0, 0, false
	}
	if lag < 0 {
		lag = 0
	} else if lag >= n {
		lag = n - 1
	}

	var sum float64
	for _, v := range daily {
		sum += v
	}
	mean = sum / float64(n)

	denom := float64(n - 1)
	gamma := make([]float64, lag+1)
	for k := 0; k <= lag; k++ {
		var acc float64
		for t := k; t < n; t++ {
			acc += (daily[t] - mean) * (daily[t-k] - mean)
		}
		gamma[k] = acc / denom
	}

	v := gamma[0]
	if lag > 0 {
		var sumK float64
		for k := 1; k <= lag; k++ {
			weight := 1.0 - float64(k)/float64(lag+1)
			sumK += weight * gamma[k]
		}
		v += 2.0 * sumK
	}
	if v < 0 {
		v = 0
	}

	se := math.Sqrt(v / float64(n))
	const z = 1.959963984540054
	lo = mean - z*se
	hi = mean + z*se
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return mean, lo, hi, true
}

// VetoOnDayClusteredAUCLag returns true if the upper bound of the
// Newey-West corrected interval is at or below 0.5, using the same rule as
// VetoOnDayClusteredAUC.
func VetoOnDayClusteredAUCLag(daily []float64, lag int) (veto bool, mean float64, measured bool) {
	m, _, hi, ok := DayClusteredAUCLag(daily, lag)
	if !ok {
		return false, 0, false
	}
	return hi <= 0.5, m, true
}
