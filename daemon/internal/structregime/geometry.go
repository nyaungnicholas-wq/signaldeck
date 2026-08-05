package structregime

import "math"

// This file adds the zero-free-parameter null that conviction must beat. Measured results are that
// trend21 tracks it within ~2pp, liquidity21 runs 5.3pp BELOW it, and vol21 is the only kind
// carrying information beyond it (+0.54pp over the pure geometric rule, not significant at 5%).
// A Geometry with OK=false means the null is unmeasurable and no edge claim may be made.

// Phi returns the standard normal cumulative distribution function.
func Phi(z float64) float64 {
	if math.IsNaN(z) {
		return math.NaN()
	}
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}

// Geometry holds the arithmetic null for a trading signal: the probability that a value
// stays on the same side of a slow reference line over 21 trading days.
type Geometry struct {
	Z            float64 // |distance| / sigma of the horizon change
	PGeometry    float64 // Phi(Z): driftless-random-walk P(same side at horizon)
	SigmaHorizon float64 // the denominator actually used
	Distance     float64 // |value - reference| in the series' own units
	OK           bool    // false when sigma is unusable; other fields are then zero
}

// GeometryFrom returns a Geometry from the absolute distance and horizon sigma.
// Returns OK=false (zero struct except OK) when sigmaHorizon <= 0, or when
// distance or sigmaHorizon is non-finite.
func GeometryFrom(distance, sigmaHorizon float64) Geometry {
	if !finite(distance) || !finite(sigmaHorizon) || sigmaHorizon <= 0 {
		return Geometry{OK: false}
	}
	z := math.Abs(distance) / sigmaHorizon
	return Geometry{
		Z:            z,
		PGeometry:    Phi(z),
		SigmaHorizon: sigmaHorizon,
		Distance:     math.Abs(distance),
		OK:           true,
	}
}

// horizonSigma returns the sample standard deviation of the h-step change of series,
// measured over the trailing window observations of that change series.
// Returns ok=false if fewer than 30 usable diffs, or if the result is not finite or is <= 0.
func horizonSigma(series []float64, h int) (float64, bool) {
	n := len(series)
	if h < 0 || h >= n {
		return 0, false
	}
	diffs := make([]float64, 0, n-h)
	for i := 0; i <= n-h-1; i++ {
		if finite(series[i]) && finite(series[i+h]) {
			diffs = append(diffs, series[i+h]-series[i])
		}
	}
	if len(diffs) > window {
		diffs = diffs[len(diffs)-window:]
	}
	if len(diffs) < 30 {
		return 0, false
	}
	var sum, sumSq float64
	for _, d := range diffs {
		sum += d
		sumSq += d * d
	}
	nf := float64(len(diffs))
	variance := (sumSq - sum*sum/nf) / (nf - 1)
	if !finite(variance) || variance <= 0 {
		return 0, false
	}
	return math.Sqrt(variance), true
}

// TrendGeometry returns the trend21 null. d[i] = closes[i]/sma200[i] - 1 using the existing
// rollMean(closes, 200); entries where sma200[i] <= 0 are NaN. Returns ok=false when
// len(closes) < minHistory, when d[last] is not finite, or when horizonSigma fails.
func TrendGeometry(closes []float64) (Geometry, bool) {
	if len(closes) < minHistory {
		return Geometry{}, false
	}
	// 200 literal, NOT the `window` constant: PredictTrend uses rollMean(closes, 200)
	// and this null must measure distance from the SAME line the predictor does.
	// The two constants are both 200 today; coupling them would let a change to the
	// trailing-distribution width silently move the barrier.
	sma := rollMean(closes, 200)
	d := make([]float64, len(closes))
	for i := range closes {
		if sma[i] > 0 {
			d[i] = closes[i]/sma[i] - 1
		} else {
			d[i] = math.NaN()
		}
	}
	last := len(d) - 1
	if !finite(d[last]) {
		return Geometry{}, false
	}
	sigma, ok := horizonSigma(d, horizon)
	if !ok {
		return Geometry{}, false
	}
	return GeometryFrom(math.Abs(d[last]), sigma), true
}

// LiquidityGeometry returns the liquidity21 null. dv[i] = log(closes[i]*volumes[i]) (NaN when
// the product is <= 0). m = rollMeanNaN(dv, horizon). reference = medianOf of the trailing
// window finite entries of m ending at the last index. distance = |m[last] - reference|.
// Returns ok=false when len(closes) < minHistory, len(volumes) != len(closes), m[last] not finite,
// the trailing window has fewer than 30 finite entries, or horizonSigma fails.
func LiquidityGeometry(closes, volumes []float64) (Geometry, bool) {
	if len(closes) < minHistory || len(volumes) != len(closes) {
		return Geometry{}, false
	}
	dv := make([]float64, len(closes))
	for i := range closes {
		prod := closes[i] * volumes[i]
		if prod > 0 {
			dv[i] = math.Log(prod)
		} else {
			dv[i] = math.NaN()
		}
	}
	m := rollMeanNaN(dv, horizon)
	last := len(m) - 1
	if !finite(m[last]) {
		return Geometry{}, false
	}
	// Get the trailing window of finite entries of m up to last.
	start := last - window + 1
	if start < 0 {
		start = 0
	}
	trail := m[start : last+1]
	finTrail := make([]float64, 0, len(trail))
	for _, v := range trail {
		if finite(v) {
			finTrail = append(finTrail, v)
		}
	}
	if len(finTrail) < 30 {
		return Geometry{}, false
	}
	reference := medianOf(finTrail)
	sigma, ok := horizonSigma(m, horizon)
	if !ok {
		return Geometry{}, false
	}
	return GeometryFrom(math.Abs(m[last]-reference), sigma), true
}

// VolGeometry returns the vol21 null. ev = ewmaVol(rets). reference = medianOf of the trailing
// window finite entries of ev. distance = |ev[last] - reference|. sigma = horizonSigma(ev, horizon).
// ok=false when len(rets) < minHistory-30, ev[last] not finite, the trailing window has fewer than
// 30 finite entries, or horizonSigma fails.
func VolGeometry(rets []float64) (Geometry, bool) {
	if len(rets) < minHistory-30 {
		return Geometry{}, false
	}
	ev := ewmaVol(rets)
	last := len(ev) - 1
	if !finite(ev[last]) {
		return Geometry{}, false
	}
	start := last - window + 1
	if start < 0 {
		start = 0
	}
	trail := ev[start : last+1]
	finTrail := make([]float64, 0, len(trail))
	for _, v := range trail {
		if finite(v) {
			finTrail = append(finTrail, v)
		}
	}
	if len(finTrail) < 30 {
		return Geometry{}, false
	}
	reference := medianOf(finTrail)
	sigma, ok := horizonSigma(ev, horizon)
	if !ok {
		return Geometry{}, false
	}
	return GeometryFrom(math.Abs(ev[last]-reference), sigma), true
}

// EdgeOverGeometry returns modelAccuracy - g.PGeometry when g.OK and modelAccuracy is finite and in [0,1].
// This is the number that says whether the model beats barrier arithmetic; a negative value means
// it does not.
func EdgeOverGeometry(modelAccuracy float64, g Geometry) (float64, bool) {
	if !g.OK || !finite(modelAccuracy) || modelAccuracy < 0 || modelAccuracy > 1 {
		return 0, false
	}
	return modelAccuracy - g.PGeometry, true
}