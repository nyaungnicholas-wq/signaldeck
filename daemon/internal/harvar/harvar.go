package harvar

import (
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/risklens"
)

// Standard VaR levels. Both are reported; only these two, fixed here, so the
// level cannot be chosen after seeing which one looks better.
const (
	Level5 = 0.05
	Level1 = 0.01
)

// MinTailObs is the smallest number of standardized residuals allowed in the
// tail before a quantile may be published.
//
// Reused from internal/risklens rather than re-picked. The reasoning there
// applies unchanged: a quantile is one order statistic of the tail, so its
// precision is governed by how many observations are IN the tail, not by how
// long the window is. At 1% over 500 sessions the tail holds 5 points, which
// is BELOW this floor -- so a 1% VaR from a two-year window is refused, and
// that refusal is the correct output rather than a number to round up to.
const MinTailObs = risklens.MinVaRTailObservations

// StandardizedResiduals divides each return by the volatility that was
// FORECAST for that day: z_t = r_t / sqrt(rvHat_t).
//
// This is the filtering step in filtered historical simulation. If the vol
// model is any good the z series is close to i.i.d., which is what makes its
// empirical quantile a usable shape for tomorrow's tail -- fat tails and skew
// included, without assuming a distribution.
//
// rvHat[i] must be the OUT-OF-SAMPLE forecast for day i, made before day i
// was observed. Passing an in-sample fit here would standardise each return by
// a volatility estimated partly from itself, which shrinks the tail and makes
// every VaR look better than it is. Entries that cannot be standardised are
// dropped rather than defaulted.
func StandardizedResiduals(rets, rvHat []float64) []float64 {
	n := len(rets)
	if len(rvHat) < n {
		n = len(rvHat)
	}
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		r, v := rets[i], rvHat[i]
		if math.IsNaN(r) || math.IsNaN(v) || v <= 0 || math.IsInf(r, 0) || math.IsInf(v, 0) {
			continue
		}
		z := r / math.Sqrt(v)
		if math.IsNaN(z) || math.IsInf(z, 0) {
			continue
		}
		out = append(out, z)
	}
	return out
}

// VaRES returns the one-day Value-at-Risk and Expected Shortfall as POSITIVE
// loss fractions, scaled by the current volatility forecast.
//
//	VaR = -q_level(z) * sqrt(sigma2Hat)
//	ES  = -mean(z | z <= q_level(z)) * sqrt(sigma2Hat)
//
// A VaR of 0.023 means "a loss of about 2.3% or worse on 5% of days".
//
// WHY FILTERED HISTORICAL SIMULATION AND NOT GAUSSIAN. A normal quantile at
// the 1% level systematically UNDER-states equity risk, because daily returns
// are fat-tailed. Building the design on a normal assumption would be
// constructing a straw man: it is known in advance to fail Kupiec, and failing
// a test for a reason you knew about beforehand is not evidence about the
// volatility model. The empirical quantile of the standardized residuals
// inherits whatever shape the data has and costs one extra line.
//
// ok=false when the tail is thinner than MinTailObs. Returning 0 would publish
// "this cannot lose money", which is the worst thing a risk surface can say.
func VaRES(sigma2Hat float64, z []float64, level float64) (varPct, esPct float64, ok bool) {
	if sigma2Hat <= 0 || math.IsNaN(sigma2Hat) || math.IsInf(sigma2Hat, 0) {
		return 0, 0, false
	}
	if level <= 0 || level >= 1 || len(z) == 0 {
		return 0, 0, false
	}
	// How many observations land at or below the quantile. The tail must be
	// deep enough on its own terms; a long window with a thin tail is still a
	// thin tail.
	k := int(math.Floor(level * float64(len(z))))
	if k < MinTailObs {
		return 0, 0, false
	}
	s := make([]float64, len(z))
	copy(s, z)
	sort.Float64s(s)

	q := s[k-1] // the k-th smallest: the empirical level-quantile of z
	sigma := math.Sqrt(sigma2Hat)

	var tail float64
	for i := 0; i < k; i++ {
		tail += s[i]
	}
	mean := tail / float64(k)

	varPct = -q * sigma
	esPct = -mean * sigma
	if math.IsNaN(varPct) || math.IsNaN(esPct) {
		return 0, 0, false
	}
	// ES is the mean loss GIVEN a breach, so it can never be smaller than the
	// breach threshold itself. If it is, the arithmetic is wrong, and silently
	// publishing an ES below its own VaR would be worse than refusing.
	if esPct < varPct {
		return 0, 0, false
	}
	return varPct, esPct, true
}

// Breaches marks the days where the realised loss exceeded the VaR forecast
// made for that day.
//
// A breach is rets[i] < -varPct[i]: the return fell below the negative of the
// predicted loss level. Days without a usable forecast are EXCLUDED from the
// series entirely rather than counted as non-breaches -- scoring a missing
// forecast as a pass is how a model with poor coverage gets flattered by its
// own gaps.
//
// The returned index slice says which input days survived, so a caller can tie
// a breach back to its date.
func Breaches(rets, varPct []float64) (breaches []bool, idx []int) {
	n := len(rets)
	if len(varPct) < n {
		n = len(varPct)
	}
	for i := 0; i < n; i++ {
		r, v := rets[i], varPct[i]
		if math.IsNaN(r) || math.IsNaN(v) || v <= 0 {
			continue
		}
		breaches = append(breaches, r < -v)
		idx = append(idx, i)
	}
	return breaches, idx
}
