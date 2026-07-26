package portopt

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// These tests encode the hostile review's "error-maximization" finding: the
// optimizer estimated expected returns AND covariance on the SAME window with
// no shrinkage, then published the Sharpe that overweighting produced. Three
// things have to become true, and each has a test below:
//
//  1. the covariance the package hands the solvers is REGULARISED, not the raw
//     sample matrix that a short window makes near-singular;
//  2. the default entry point does not tilt on sample means, because sample
//     means over a short window are almost pure noise (the honest no-view
//     answer is equal expected returns, i.e. minimum variance);
//  3. any Sharpe that IS reported is labelled in-sample, in the field name, so
//     it cannot be read as an out-of-sample result.

// TestCovariance_IsShrunkTowardConstantCorrelation: the package's canonical
// estimator must pull the off-diagonal correlations toward their average
// (Ledoit-Wolf constant-correlation target) while leaving the variances alone —
// the diagonal of the target IS the sample diagonal, so shrinkage must not move
// it. The raw estimator stays available as SampleCovariance.
func TestCovariance_IsShrunkTowardConstantCorrelation(t *testing.T) {
	returns := noisyPanel(8, 24)

	sample := SampleCovariance(returns)
	shrunk := Covariance(returns)

	// Variances untouched: the constant-correlation target has f_ii = s_ii.
	for i := range sample {
		if !approx(shrunk[i][i], sample[i][i], 1e-12) {
			t.Errorf("variance[%d] moved: shrunk %.10f vs sample %.10f", i, shrunk[i][i], sample[i][i])
		}
	}

	// Correlation dispersion must fall: that is what the shrinkage buys.
	sSpread := corrSpread(sample)
	shSpread := corrSpread(shrunk)
	if shSpread >= sSpread {
		t.Errorf("correlation spread not reduced: sample %.4f -> shrunk %.4f", sSpread, shSpread)
	}

	cov, delta := CovarianceShrinkage(returns)
	if delta <= 0 || delta > 1 {
		t.Errorf("shrinkage intensity = %.4f, want (0,1] on a short noisy panel", delta)
	}
	for i := range cov {
		for j := range cov[i] {
			if !approx(cov[i][j], shrunk[i][j], 1e-12) {
				t.Fatalf("CovarianceShrinkage disagrees with Covariance at [%d][%d]", i, j)
			}
		}
	}
}

// TestMinVariance_SurvivesASingularSampleWindow is the practical consequence.
// With more assets than observations the sample covariance is rank-deficient,
// invert() refuses it, and MinVariance silently degraded to equal weight —
// "the optimizer" returning 1/N while claiming to have optimized. The shrunk
// estimator is positive definite, so a real solve happens.
func TestMinVariance_SurvivesASingularSampleWindow(t *testing.T) {
	returns := noisyPanel(8, 5) // 8 assets, 5 observations: sample cov is singular

	if _, ok := invert(SampleCovariance(returns)); ok {
		t.Fatal("precondition failed: the sample covariance of an 8x5 panel should be singular")
	}
	symbols := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	r := MinVariance(symbols, Covariance(returns))
	if r.Method != MethodMinVariance {
		t.Fatalf("Method = %q (Note=%q), want a real min-variance solve", r.Method, r.Note)
	}
	if hasNaNorInf(r.Weights) || !approx(sumWeights(r.Weights), 1, tol) {
		t.Fatalf("weights bad: %v", r.Weights)
	}
}

// TestMaxSharpe_DoesNotTiltOnSampleMeans is THE regression for the finding.
//
// Five assets, identical covariance, and one asset whose sample mean is higher
// purely by luck. The old MaxSharpe put essentially the whole book on it —
// error maximization by construction. With no way to know whether the means it
// was handed are exogenous views or in-window noise, the default must assume
// the latter: equal expected returns, which makes the maximum-Sharpe portfolio
// the minimum-variance portfolio.
func TestMaxSharpe_DoesNotTiltOnSampleMeans(t *testing.T) {
	symbols := []string{"A", "B", "C", "D", "E"}
	expRet := []float64{0, 0, 0, 0, 0.002} // E just got lucky this window
	cov := diagCov(5, 0.0004)              // identical, uncorrelated risk

	r := MaxSharpe(symbols, expRet, cov, 0)

	maxW := 0.0
	for _, w := range r.Weights {
		if w > maxW {
			maxW = w
		}
	}
	if maxW > 1.5/5 {
		t.Errorf("max weight %.4f on identical-risk assets: still tilting on a noise mean (weights=%v)", maxW, r.Weights)
	}
	if r.Method == MethodMaxSharpe {
		t.Errorf("Method = %q: the no-view default must not present itself as a max-Sharpe solve", r.Method)
	}
	for _, want := range []string{"shrunk", "minimum-variance"} {
		if !strings.Contains(strings.ToLower(r.Note), want) {
			t.Errorf("Note %q must say the means were %q", r.Note, want)
		}
	}
}

// TestResult_SharpeIsLabelledInSample: the review's second half — "report the
// in-sample Sharpe as in-sample, or withhold it". A field called `Sharpe`
// beside an allocation reads as the allocation's expected Sharpe. It is a fit
// statistic on the window that produced the inputs, and the field name is the
// only place a JSON consumer will ever learn that.
func TestResult_SharpeIsLabelledInSample(t *testing.T) {
	r := MaxSharpe([]string{"A", "B"}, []float64{0.001, 0.0005},
		[][]float64{{0.0004, 0}, {0, 0.0009}}, 0)

	if r.SharpeInSample == nil {
		t.Fatal("SharpeInSample is nil; expected the in-sample fit statistic for these weights")
	}
	if !r.InSample {
		t.Error("InSample must be true — every statistic here is measured on the estimation window")
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"SharpeInSample"`) {
		t.Errorf("payload has no SharpeInSample field: %s", s)
	}
	if strings.Contains(s, `"Sharpe":`) {
		t.Errorf("payload still ships a bare \"Sharpe\" field: %s", s)
	}
}

// TestMinVariance_WithholdsSharpe: min-variance is computed without expected
// returns, so there is no Sharpe to report. nil, never 0 — a 0 Sharpe is a
// claim, not an absence.
func TestMinVariance_WithholdsSharpe(t *testing.T) {
	r := MinVariance([]string{"A", "B"}, [][]float64{{0.0004, 0}, {0, 0.0009}})
	if r.SharpeInSample != nil {
		t.Errorf("SharpeInSample = %.6f, want withheld (no expected returns were supplied)", *r.SharpeInSample)
	}
}

// TestMaxSharpeFromReturns_ShrinksBothEstimates is the honest full-history
// path: given raw returns it knows T, so it can shrink the covariance
// (Ledoit-Wolf) AND the means (Jorion Bayes-Stein) and then run the tangency
// solve on estimates that have been pulled back toward their grand mean.
func TestMaxSharpeFromReturns_ShrinksBothEstimates(t *testing.T) {
	returns := noisyPanel(6, 60)
	symbols := []string{"A", "B", "C", "D", "E", "F"}

	r := MaxSharpeFromReturns(symbols, returns, 0)
	if r.Shrinkage == nil {
		t.Fatal("Shrinkage not reported; the intensities are the whole point of this entry point")
	}
	if r.Shrinkage.CovarianceIntensity == nil || *r.Shrinkage.CovarianceIntensity <= 0 || *r.Shrinkage.CovarianceIntensity > 1 {
		t.Errorf("covariance intensity = %v, want (0,1]", r.Shrinkage.CovarianceIntensity)
	}
	if r.Shrinkage.MeanIntensity == nil || *r.Shrinkage.MeanIntensity < 0 || *r.Shrinkage.MeanIntensity > 1 {
		t.Errorf("mean intensity = %v, want [0,1]", r.Shrinkage.MeanIntensity)
	}

	// Shrunk means must be a strict contraction toward the grand mean: the
	// spread of the shrunk view set is smaller than the spread of the raw
	// sample means it started from.
	raw := make([]float64, len(returns))
	for i, s := range returns {
		raw[i] = meanOf(s)
	}
	shrunkViews := r.Shrinkage.Views
	if len(shrunkViews) != len(raw) {
		t.Fatalf("Views len %d, want %d", len(shrunkViews), len(raw))
	}
	if spread(shrunkViews) >= spread(raw) {
		t.Errorf("views not contracted: raw spread %.6g -> shrunk %.6g", spread(raw), spread(shrunkViews))
	}

	// And the allocation must be less concentrated than the tangency portfolio
	// built on the RAW estimates — the concentration is the error maximization.
	rawTangency := TangencyWithViews(symbols, raw, SampleCovariance(returns), 0)
	if maxOf(r.Weights) >= maxOf(rawTangency.Weights) {
		t.Errorf("shrunk allocation (max weight %.4f) is not less concentrated than the raw one (%.4f)",
			maxOf(r.Weights), maxOf(rawTangency.Weights))
	}
}

// TestTangencyWithViews_KeepsTheSolver proves the tangency math itself was not
// thrown away: when a caller supplies genuinely EXOGENOUS views, the solver
// still finds the analytical long-only tangency portfolio.
func TestTangencyWithViews_KeepsTheSolver(t *testing.T) {
	// A: higher expected return AND lower vol than B, uncorrelated.
	// Long-only tangency (rf=0) ∝ Σ⁻¹μ = (3.75, 0.3125) -> ~(0.923, 0.077).
	r := TangencyWithViews([]string{"A", "B"}, []float64{0.0015, 0.0005},
		[][]float64{{0.0004, 0}, {0, 0.0016}}, 0)
	if r.Method != MethodMaxSharpe {
		t.Fatalf("Method = %q, want %q (Note=%q)", r.Method, MethodMaxSharpe, r.Note)
	}
	if !approx(r.Weights[0], 0.9231, 5e-3) {
		t.Errorf("weight[0] = %v, want ~0.923", r.Weights[0])
	}
}

// --- deterministic fixtures (no RNG anywhere in this repo's test paths) -----

// noisyPanel builds n asset return series of length T whose sample means and
// correlations differ only by construction noise — the situation in which
// mean-variance optimization maximizes estimation error.
func noisyPanel(n, T int) [][]float64 {
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		s := make([]float64, T)
		for t := 0; t < T; t++ {
			// Two incommensurable frequencies per asset: deterministic, but with
			// no repeating relationship across assets, so the sample covariance
			// picks up spurious structure exactly as a real short window does.
			s[t] = 0.01*math.Sin(float64(t+1)*(0.7+0.31*float64(i))) +
				0.006*math.Cos(float64(t+1)*(1.9+0.17*float64(i)))
		}
		out[i] = s
	}
	return out
}

// diagCov returns an n x n diagonal covariance with identical variances.
func diagCov(n int, v float64) [][]float64 {
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n)
		m[i][i] = v
	}
	return m
}

// corrSpread is max - min over the off-diagonal correlations implied by cov.
func corrSpread(cov [][]float64) float64 {
	lo, hi := math.Inf(1), math.Inf(-1)
	for i := range cov {
		for j := range cov[i] {
			if i == j {
				continue
			}
			d := math.Sqrt(cov[i][i] * cov[j][j])
			if d <= 0 {
				continue
			}
			c := cov[i][j] / d
			if c < lo {
				lo = c
			}
			if c > hi {
				hi = c
			}
		}
	}
	return hi - lo
}

func meanOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func spread(xs []float64) float64 {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, x := range xs {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	return hi - lo
}

func maxOf(xs []float64) float64 {
	m := math.Inf(-1)
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}
