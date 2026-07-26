package risklens

import (
	"fmt"
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

// MinVaRTailObservations is the smallest number of returns allowed at or below
// the VaR quantile before a historical VaR may be published.
//
// WHY THIS EXISTS: a historical VaR is a single order statistic of the loss
// tail, so its precision is governed by how many observations are IN that tail,
// not by how many days the window spans. At the package's 60-close minimum a
// 95% VaR sat on a tail of three points — three coin flips deciding a number
// the UI rendered as a dollar figure. Ten is the smallest tail at which the
// estimate stops being dominated by which single day happened to be worst; at
// 95% confidence it requires ~180 return days, which the live 400-bar fetch
// comfortably supplies.
const MinVaRTailObservations = 10

// HistoricalVaR reports the empirical Value-at-Risk and Conditional VaR (a.k.a.
// Expected Shortfall) of a return series at the given confidence, together with
// the gate that admitted or refused them.
//
// confidence 0.95 means we look at the worst 5% of days: varPct is the loss at
// the 5th percentile of returns, cvarPct is the mean loss across days at or
// beyond that threshold. Both are POSITIVE loss magnitudes (a varPct of 0.03
// means "a 3% loss"). A profitable quantile yields a negative magnitude (i.e.
// even the tail was a gain), which is reported as-is.
//
// Method: sort returns ascending, take the index at (1-confidence) using the
// "lower" empirical quantile (index = floor((1-confidence)*n), clamped), and
// negate to express loss. This is a purely descriptive, in-sample statistic —
// no distributional assumption, no lookahead. confidence is clamped to (0,1).
//
// WITHHOLDING: when the loss tail holds fewer than MinVaRTailObservations
// points, both figures come back nil with gate.Reason set. Callers must render
// that as "—". Returning 0 would publish "this portfolio cannot lose money",
// which is the single worst thing a risk surface can say.
func HistoricalVaR(portfolioReturns []float64, confidence float64) (varPct, cvarPct *float64, gate VaRGate) {
	n := len(portfolioReturns)
	if confidence <= 0 {
		confidence = 0.0001
	}
	if confidence >= 1 {
		confidence = 0.9999
	}
	alpha := 1 - confidence // tail mass, e.g. 0.05

	// Return days needed for the tail to reach the floor: the tail holds
	// floor(alpha*n)+1 points, so floor(alpha*n) >= MinVaRTailObservations-1.
	needN := int(math.Ceil(float64(MinVaRTailObservations-1) / alpha))
	gate = VaRGate{
		Confidence: confidence,
		N:          n,
		MinTailN:   MinVaRTailObservations,
		NeedN:      needN,
	}

	// Lower empirical quantile index: the largest index strictly inside the
	// tail. floor(alpha*n) lands on the first return just past the tail cutoff;
	// we use index = floor(alpha*n) as the VaR return, clamped to [0, n-1].
	idx := int(math.Floor(alpha * float64(n)))
	if idx >= n {
		idx = n - 1
	}
	if n > 0 {
		gate.TailN = idx + 1
	}

	if gate.TailN < MinVaRTailObservations {
		gate.Withheld = true
		gate.Reason = fmt.Sprintf(
			"a %.0f%% historical VaR needs at least %d return days in the loss tail; this %d-day window puts only %d there (about %d days are required at this confidence)",
			confidence*100, MinVaRTailObservations, n, gate.TailN, needN)
		return nil, nil, gate
	}

	sorted := make([]float64, n)
	copy(sorted, portfolioReturns)
	sort.Float64s(sorted)

	v := -sorted[idx]

	// CVaR: mean of all returns at or below the VaR return (the tail itself).
	// Include index idx so the tail mean covers exactly gate.TailN points.
	tailSum := 0.0
	for i := 0; i <= idx; i++ {
		tailSum += sorted[i]
	}
	cv := -(tailSum / float64(gate.TailN))
	return &v, &cv, gate
}

// MinParametricVaRObservations is the smallest return sample from which a
// Gaussian VaR may be published.
//
// WHY THIS EXISTS AND WHY 49: the parametric VaR is a scale estimate, so its
// precision is governed by the precision of the sample standard deviation:
// SE(sigma-hat)/sigma ~= 1/sqrt(2(n-1)). Keeping the published figure inside
// +/-20% of its own point estimate at 95% confidence needs
// 1.96/sqrt(2(n-1)) <= 0.20, i.e. n >= 49. Twenty percent is already the
// loosest band anyone would put on a risk number; below 49 observations the
// figure is not a measurement. The old floor was n >= 2, at which the same
// interval is +/-196%.
const MinParametricVaRObservations = 49

// jarqueBeraCritical1pct is the chi-squared(2) critical value at the 1% level.
// The Jarque-Bera statistic is asymptotically chi-squared with 2 d.o.f., so a
// sample scoring above this rejects normality at 1%.
const jarqueBeraCritical1pct = 9.21034

// ParamVaRGate records the sample and the distributional test a parametric VaR
// was published from — or refused on. It exists because the Gaussian VaR makes
// exactly ONE claim (returns are normal) and that claim is testable: a gate
// that only counts observations would keep shipping a number whose sole
// assumption the same data falsifies.
type ParamVaRGate struct {
	Confidence float64
	N          int // return observations (one per day) in the window
	MinN       int // floor required to publish

	Skew              float64 // sample skewness (0 under normality)
	ExcessKurtosis    float64 // sample excess kurtosis (0 under normality)
	JarqueBera        float64 // JB statistic, chi-squared(2) under normality
	JBCritical        float64 // 1% critical value the statistic is tested against
	NormalityRejected bool    // JB > JBCritical

	Withheld bool
	Reason   string `json:",omitempty"`
	// Assumes states the model's assumption in the payload so a reader sees it
	// beside the number, not in a doc comment they will never open.
	Assumes string `json:",omitempty"`
}

// paramVaRAssumes is the one-line assumption that must travel with every
// published Gaussian VaR.
const paramVaRAssumes = "assumes normally distributed daily returns; real markets are fat-tailed, so a normal-model VaR understates the true tail"

// ParametricVaR reports the variance-covariance (Gaussian) Value-at-Risk of a
// return series at the given confidence, as a POSITIVE loss magnitude, together
// with the gate that admitted or refused it.
//
// Method: VaR = -(mean + z*stdev) where z is the standard-normal quantile at
// (1-confidence) (negative in the loss tail). Uses the sample standard
// deviation (n-1 denominator).
//
// WITHHOLDING — two independent gates, either of which returns nil:
//
//   - SAMPLE. Fewer than MinParametricVaRObservations returns cannot pin the
//     scale estimate the whole figure rests on. See that constant's derivation.
//   - NORMALITY. The Gaussian VaR's only claim is that returns are normal. A
//     sample rejecting normality at 1% by Jarque-Bera has falsified it, and the
//     failure is one-directional: fat tails make this number too SMALL, so
//     shipping it beside the caveat would publish a understatement dressed as a
//     risk limit. This gate matters most exactly when it fires — once
//     HistoricalVaR started withholding below its tail floor, the parametric
//     figure became the only VaR on screen, i.e. the understating one filled the
//     hole left by the honest one.
//
// A withheld figure is nil, never 0: a zero VaR reads as "this portfolio cannot
// lose money". Callers must render nil as "—" and show gate.Reason.
func ParametricVaR(portfolioReturns []float64, confidence float64) (*float64, ParamVaRGate) {
	n := len(portfolioReturns)
	if confidence <= 0 {
		confidence = 0.0001
	}
	if confidence >= 1 {
		confidence = 0.9999
	}
	gate := ParamVaRGate{
		Confidence: confidence,
		N:          n,
		MinN:       MinParametricVaRObservations,
		JBCritical: jarqueBeraCritical1pct,
	}

	if n < MinParametricVaRObservations {
		gate.Withheld = true
		gate.Reason = fmt.Sprintf(
			"a normal-model VaR needs at least %d return days to pin the volatility it is built from; this window has %d, where that volatility's own 95%% interval is about +/-%.0f%% of itself",
			MinParametricVaRObservations, n, relSDErrorPct(n))
		return nil, gate
	}

	mean, std := meanStd(portfolioReturns)
	if std <= 0 {
		gate.Withheld = true
		gate.Reason = "the window has zero return variance, so a normal-model VaR is undefined"
		return nil, gate
	}

	gate.Skew, gate.ExcessKurtosis = skewExcessKurtosis(portfolioReturns, mean, std)
	gate.JarqueBera = float64(n) / 6 *
		(gate.Skew*gate.Skew + gate.ExcessKurtosis*gate.ExcessKurtosis/4)
	gate.NormalityRejected = gate.JarqueBera > jarqueBeraCritical1pct
	if gate.NormalityRejected {
		gate.Withheld = true
		gate.Reason = fmt.Sprintf(
			"this window is not normally distributed (Jarque-Bera %.1f > %.2f, the 1%% critical value; skew %.2f, excess kurtosis %.2f), and the normal model's error runs one way — it UNDERSTATES the tail. Use the historical VaR",
			gate.JarqueBera, jarqueBeraCritical1pct, gate.Skew, gate.ExcessKurtosis)
		return nil, gate
	}

	// z at the lower tail (1-confidence). normInvCDF(0.05) ~= -1.645.
	z := normInvCDF(1 - confidence)
	loss := -(mean + z*std)
	gate.Assumes = paramVaRAssumes
	return &loss, gate
}

// relSDErrorPct is the half-width, in percent of the estimate, of the 95%
// interval around a sample standard deviation at n observations:
// 1.96/sqrt(2(n-1)). Used to state the imprecision in the refusal reason
// rather than asserting a bare floor.
func relSDErrorPct(n int) float64 {
	if n < 2 {
		return 999
	}
	return 1.96 / math.Sqrt(2*float64(n-1)) * 100
}

// skewExcessKurtosis returns the sample skewness and EXCESS kurtosis (kurtosis
// minus 3, so both are 0 under normality) using the population moment forms the
// Jarque-Bera statistic is defined on. std must be the sample (n-1) standard
// deviation the caller already computed.
func skewExcessKurtosis(vals []float64, mean, std float64) (skew, excessKurt float64) {
	n := len(vals)
	if n < 2 || std <= 0 {
		return 0, 0
	}
	var m3, m4 float64
	for _, v := range vals {
		d := (v - mean) / std
		d3 := d * d * d
		m3 += d3
		m4 += d3 * d
	}
	fn := float64(n)
	return m3 / fn, m4/fn - 3
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
