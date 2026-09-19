package harrv

import "math"

// The nulls. A forecast without the thing it must beat is not evidence, and on
// this platform that is not a stylistic preference: every predictor that has
// ever failed here failed against a null, and the ones that looked strongest
// (trend21, liquidity21) turned out to be reproducing a zero-parameter
// geometric rule. structregime.go's own conclusion is that "none of the three
// has demonstrated forecasting skill".
//
// Both nulls are computed AT CALL TIME and stored beside the forecast, never
// reconstructed afterwards. A null computed after the outcome is known is
// hindsight, and this repository already had to build a quarantine manifest
// once because that happened.

// RiskMetricsLambda is the RiskMetrics EWMA decay. Same 0.94 that
// internal/volregime already uses, reused rather than re-picked: a second
// spelling of a constant is a second thing to keep in step.
const RiskMetricsLambda = 0.94

// RWAt is the random-walk null: the coming window's variance is today's.
//
// It forecasts the SAME estimand as the model it is compared against -- at
// h > 1 that is the mean over the next h sessions, and "today's value" is the
// naive answer to that question too. A null answering a different question
// than the model is not a comparison.
//
// It is the weakest of the two and it is included because it is the honest
// floor -- but note it is a POOR null against a noisy daily proxy. A single
// Garman-Klass estimate can land near zero, and QLIKE divides by the forecast,
// so one such day dominates the mean loss. Measured on a 150-symbol probe the
// RW mean QLIKE came out at 16,110 against HAR's 0.78, which says nothing
// about forecasting and everything about the proxy's noise. EWMA is the null
// that matters.
func RWAt(rv []float64, t int) (float64, bool) {
	if t < 0 || t >= len(rv) {
		return 0, false
	}
	v := rv[t]
	if math.IsNaN(v) || v <= 0 {
		return 0, false
	}
	return v, true
}

// EWMAAt is the RiskMetrics null: an exponentially weighted mean of past
// variance, seeded from the first available observation and carried forward
// across holes.
//
// THIS IS THE NULL THAT MATTERS. Exponential weighting is a genuinely good
// volatility nowcast -- that is why RiskMetrics exists -- and this repository
// has already measured that EWMA beats a flat window of comparable length
// everywhere by 1.0 to 1.3pp, calling it "estimation, not prediction". Beating
// a flat window is therefore not evidence of anything. Beating EWMA would be.
//
// Reads indices <= t only.
func EWMAAt(rv []float64, t int, lambda float64) (float64, bool) {
	if t < 0 || t >= len(rv) || lambda <= 0 || lambda >= 1 {
		return 0, false
	}
	var ew float64
	seeded := false
	for i := 0; i <= t; i++ {
		v := rv[i]
		if math.IsNaN(v) || v <= 0 {
			continue // a hole updates nothing; it does not reset the state
		}
		if !seeded {
			ew, seeded = v, true
			continue
		}
		ew = lambda*ew + (1-lambda)*v
	}
	if !seeded || ew <= 0 {
		return 0, false
	}
	return ew, true
}

// FlatAt is the rectangular-window null over the last w observations. Reported
// alongside, never as the headline: see EWMAAt for why beating it is cheap.
func FlatAt(rv []float64, t, w int) (float64, bool) {
	return RollingMean(rv, t, w, w/2)
}
