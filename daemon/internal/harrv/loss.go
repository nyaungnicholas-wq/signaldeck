package harrv

import "math"

// Loss functions and the test that compares them.
//
// Both losses are "robust" in Patton's (2011) sense: with a conditionally
// unbiased but noisy variance proxy, they rank forecasts the same way in
// expectation as the true (unobservable) variance would. That property is the
// only reason a daily proxy can arbitrate anything at all, and it is why the
// loss set is fixed HERE, before any result exists, rather than chosen later
// from whichever one flatters the model.

// QLIKE(actual, forecast) = a/h - ln(a/h) - 1.
//
// Zero when they agree, positive otherwise, and ASYMMETRIC: under-forecasting
// is punished much harder than over-forecasting, because a/h blows up as h
// falls. That asymmetry is the right shape for a risk tool -- being wrong low
// about volatility is the expensive direction -- and it is also why the
// lognormal retransform in PredictAt is mandatory rather than cosmetic.
//
// ok=false on any non-positive input. Never returns a number it cannot justify.
func QLIKE(actual, forecast float64) (float64, bool) {
	if actual <= 0 || forecast <= 0 ||
		math.IsNaN(actual) || math.IsNaN(forecast) ||
		math.IsInf(actual, 0) || math.IsInf(forecast, 0) {
		return 0, false
	}
	r := actual / forecast
	return r - math.Log(r) - 1, true
}

// MSE on variance LEVELS.
//
// Reported because a result that holds under only one loss is not a result,
// but never as the headline: squared error on variance is dominated by a
// handful of crisis days, so its sampling distribution has fat tails and its
// test has poor power. QLIKE is the headline.
func MSE(actual, forecast float64) (float64, bool) {
	if math.IsNaN(actual) || math.IsNaN(forecast) ||
		math.IsInf(actual, 0) || math.IsInf(forecast, 0) {
		return 0, false
	}
	d := actual - forecast
	return d * d, true
}

// DMResult is a Diebold-Mariano comparison of two competing forecasts.
//
// Negative Mean means the FIRST model has lower loss, i.e. it is better.
type DMResult struct {
	Mean float64 // mean loss differential, L(model) - L(null)
	T    float64 // HLN-corrected t statistic
	N    int     // number of INDEPENDENT observations (see below)
	OK   bool
}

// DieboldMariano tests whether a series of loss differentials has zero mean,
// with a Newey-West HAC variance and the Harvey-Leybourne-Newbold small-sample
// correction.
//
// THE UNIT OF OBSERVATION IS THE CALLER'S RESPONSIBILITY AND IT IS THE WHOLE
// BALLGAME. `d` must already be one value PER DAY, cross-sectionally averaged
// over symbols -- never one value per (symbol, day). A panel of ~758 symbols
// over ~2,000 sessions is roughly 970,000 forecasts, and pooling them as
// independent returns a t-statistic in the hundreds for any model at all,
// because all symbols share one market shock each day. This repository has
// already written the rule down: "408 forecasts resolving in one cluster are
// ONE market observation, however many symbols they cover"
// (internal/prereg/prereg.go).
//
// The HLN correction only ever widens the interval. It is applied
// unconditionally so it cannot be dropped later for being inconvenient.
func DieboldMariano(d []float64, lag int) DMResult {
	n := len(d)
	if n < 8 || lag < 0 {
		return DMResult{N: n}
	}
	if lag >= n {
		lag = n - 1
	}
	mean := 0.0
	for _, v := range d {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return DMResult{N: n}
		}
		mean += v
	}
	mean /= float64(n)

	// Newey-West long-run variance with Bartlett weights.
	gamma := func(k int) float64 {
		s := 0.0
		for i := k; i < n; i++ {
			s += (d[i] - mean) * (d[i-k] - mean)
		}
		return s / float64(n)
	}
	lrv := gamma(0)
	for k := 1; k <= lag; k++ {
		lrv += 2 * (1 - float64(k)/float64(lag+1)) * gamma(k)
	}
	if lrv <= 0 {
		return DMResult{Mean: mean, N: n}
	}
	stat := mean / math.Sqrt(lrv/float64(n))

	// Harvey-Leybourne-Newbold: at horizon 1 the factor reduces to
	// sqrt((n+1-2h+h(h-1)/n)/n) with h=1, i.e. sqrt((n-1)/n).
	stat *= math.Sqrt(float64(n-1) / float64(n))
	return DMResult{Mean: mean, T: stat, N: n, OK: true}
}

// NeweyWestLag is the standard automatic bandwidth, floor(4*(n/100)^(2/9)).
// Registered as a rule rather than chosen per run, so the bandwidth cannot
// become another free parameter to search over.
func NeweyWestLag(n int) int {
	if n <= 0 {
		return 0
	}
	return int(4 * math.Pow(float64(n)/100.0, 2.0/9.0))
}
