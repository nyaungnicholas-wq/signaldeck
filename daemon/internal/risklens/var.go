package risklens

import (
	"math"
	"sort"
)

// DailyReturns converts a close series (oldest→newest) into simple daily
// returns r[i] = closes[i+1]/closes[i] - 1. The result has len(closes)-1
// elements. A close of zero (or fewer than two closes) yields a nil slice,
// since a division by zero return is undefined.
//
// No lookahead: return i depends only on closes i and i+1.
func DailyReturns(closes []float64) []float64 {
	if len(closes) < 2 {
		return nil
	}
	out := make([]float64, 0, len(closes)-1)
	for i := 0; i+1 < len(closes); i++ {
		prev := closes[i]
		if prev == 0 {
			return nil
		}
		out = append(out, closes[i+1]/prev-1)
	}
	return out
}

// HistoricalVaR reports the empirical Value-at-Risk and Conditional VaR (a.k.a.
// Expected Shortfall) of a return series at the given confidence.
//
// confidence 0.95 means we look at the worst 5% of days: varPct is the loss at
// the 5th percentile of returns, cvarPct is the mean loss across days at or
// beyond that threshold. Both are returned as POSITIVE loss magnitudes (a
// varPct of 0.03 means "a 3% loss"). A profitable quantile yields a negative
// magnitude (i.e. even the tail was a gain), which is reported as-is.
//
// Method: sort returns ascending, take the index at (1-confidence) using the
// "lower" empirical quantile (index = floor((1-confidence)*n), clamped), and
// negate to express loss. This is a purely descriptive, in-sample statistic —
// no distributional assumption, no lookahead. confidence is clamped to
// (0,1); an empty series yields (0,0).
func HistoricalVaR(portfolioReturns []float64, confidence float64) (varPct, cvarPct float64) {
	n := len(portfolioReturns)
	if n == 0 {
		return 0, 0
	}
	if confidence <= 0 {
		confidence = 0.0001
	}
	if confidence >= 1 {
		confidence = 0.9999
	}

	sorted := make([]float64, n)
	copy(sorted, portfolioReturns)
	sort.Float64s(sorted)

	alpha := 1 - confidence // tail mass, e.g. 0.05
	// Lower empirical quantile index: the largest index strictly inside the
	// tail. floor(alpha*n) lands on the first return just past the tail cutoff;
	// we use index = floor(alpha*n) as the VaR return, clamped to [0, n-1].
	idx := int(math.Floor(alpha * float64(n)))
	if idx >= n {
		idx = n - 1
	}
	varReturn := sorted[idx]
	varPct = -varReturn

	// CVaR: mean of all returns at or below the VaR return (the tail itself).
	// Include index idx so a single-element tail still has a defined mean.
	tailSum := 0.0
	tailCount := 0
	for i := 0; i <= idx; i++ {
		tailSum += sorted[i]
		tailCount++
	}
	cvarPct = -(tailSum / float64(tailCount))
	return varPct, cvarPct
}

// ParametricVaR reports the variance-covariance (Gaussian) Value-at-Risk of a
// return series at the given confidence, as a POSITIVE loss magnitude.
//
// Method: VaR = -(mean + z*stdev) where z is the standard-normal quantile at
// (1-confidence) (negative in the loss tail). Uses the sample standard
// deviation (n-1 denominator).
//
// LIMITATION (normality assumption): this models returns as normally
// distributed. Real market returns are fat-tailed and left-skewed, so this
// figure typically UNDERSTATES true tail risk relative to HistoricalVaR,
// especially around crashes. Prefer HistoricalVaR when the sample is large
// enough; use this as a smooth complement. An empty or single-element series
// yields 0.
func ParametricVaR(portfolioReturns []float64, confidence float64) float64 {
	n := len(portfolioReturns)
	if n < 2 {
		return 0
	}
	if confidence <= 0 {
		confidence = 0.0001
	}
	if confidence >= 1 {
		confidence = 0.9999
	}
	mean, std := meanStd(portfolioReturns)
	// z at the lower tail (1-confidence). normInvCDF(0.05) ~= -1.645.
	z := normInvCDF(1 - confidence)
	loss := -(mean + z*std)
	return loss
}

// meanStd returns the arithmetic mean and sample (n-1) standard deviation of
// vals. For n < 2 the standard deviation is 0.
func meanStd(vals []float64) (mean, std float64) {
	n := len(vals)
	if n == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	mean = sum / float64(n)
	if n < 2 {
		return mean, 0
	}
	sq := 0.0
	for _, v := range vals {
		d := v - mean
		sq += d * d
	}
	std = math.Sqrt(sq / float64(n-1))
	return mean, std
}

// normInvCDF returns the inverse of the standard-normal CDF (the quantile
// function) for p in (0,1), using the Acklam rational approximation. Its
// absolute error is < ~1.15e-9 across the domain — ample for risk display.
// p outside (0,1) is clamped to a tiny epsilon inside the interval.
func normInvCDF(p float64) float64 {
	const (
		a1 = -3.969683028665376e+01
		a2 = 2.209460984245205e+02
		a3 = -2.759285104469687e+02
		a4 = 1.383577518672690e+02
		a5 = -3.066479806614716e+01
		a6 = 2.506628277459239e+00

		b1 = -5.447609879822406e+01
		b2 = 1.615858368580409e+02
		b3 = -1.556989798598866e+02
		b4 = 6.680131188771972e+01
		b5 = -1.328068155288572e+01

		c1 = -7.784894002430293e-03
		c2 = -3.223964580411365e-01
		c3 = -2.400758277161838e+00
		c4 = -2.549732539343734e+00
		c5 = 4.374664141464968e+00
		c6 = 2.938163982698783e+00

		d1 = 7.784695709041462e-03
		d2 = 3.224671290700398e-01
		d3 = 2.445134137142996e+00
		d4 = 3.754408661907416e+00

		pLow  = 0.02425
		pHigh = 1 - 0.02425
	)
	if p <= 0 {
		p = 1e-12
	}
	if p >= 1 {
		p = 1 - 1e-12
	}
	switch {
	case p < pLow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	case p <= pHigh:
		q := p - 0.5
		r := q * q
		return (((((a1*r+a2)*r+a3)*r+a4)*r+a5)*r + a6) * q /
			(((((b1*r+b2)*r+b3)*r+b4)*r+b5)*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	}
}
