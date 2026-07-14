package scenario

import (
	"math"
	"testing"
)

// TestFit_PerfectLinear: when asset = 2*factor exactly, OLS must recover
// Beta = 2 and R2 = 1 (a perfect fit) regardless of the factor's own shape.
func TestFit_PerfectLinear(t *testing.T) {
	factor := []float64{1, 2, 3, 4, 5, -2, -4, 0.5, 7, -3}
	asset := make([]float64, len(factor))
	for i, x := range factor {
		asset[i] = 2 * x
	}

	s := Fit(asset, factor)
	if math.Abs(s.Beta-2) > 1e-9 {
		t.Errorf("Beta = %.12f, want 2", s.Beta)
	}
	if math.Abs(s.R2-1) > 1e-9 {
		t.Errorf("R2 = %.12f, want 1", s.R2)
	}
	if s.N != len(factor) {
		t.Errorf("N = %d, want %d", s.N, len(factor))
	}
}

// TestFit_PerfectLinearWithIntercept: asset = 0.5 + 3*factor. OLS fits an
// intercept, so the slope is still exactly 3 and R2 is still 1.
func TestFit_PerfectLinearWithIntercept(t *testing.T) {
	factor := []float64{-5, -1, 0, 2, 4, 6, 9, 11}
	asset := make([]float64, len(factor))
	for i, x := range factor {
		asset[i] = 0.5 + 3*x
	}

	s := Fit(asset, factor)
	if math.Abs(s.Beta-3) > 1e-9 {
		t.Errorf("Beta = %.12f, want 3", s.Beta)
	}
	if math.Abs(s.R2-1) > 1e-9 {
		t.Errorf("R2 = %.12f, want 1", s.R2)
	}
}

// TestFit_Uncorrelated: with orthogonal series the covariance is exactly zero,
// so Beta must be 0 and R2 must be 0. x=[1,-1,1,-1] and y=[1,1,-1,-1] have
// sum(x_i*y_i)=0 about their (zero) means, a known-answer uncorrelated pair.
func TestFit_Uncorrelated(t *testing.T) {
	factor := []float64{1, -1, 1, -1}
	asset := []float64{1, 1, -1, -1}

	s := Fit(asset, factor)
	if math.Abs(s.Beta) > 1e-12 {
		t.Errorf("Beta = %.12f, want 0 (uncorrelated)", s.Beta)
	}
	if s.R2 > 1e-12 {
		t.Errorf("R2 = %.12f, want ~0 (uncorrelated)", s.R2)
	}
	if s.N != 4 {
		t.Errorf("N = %d, want 4", s.N)
	}
}

// TestFit_ZeroVarianceFactor: a constant factor has zero variance, so the slope
// is undefined. Fit must guard the divide-by-zero and return Beta 0, R2 0.
func TestFit_ZeroVarianceFactor(t *testing.T) {
	factor := []float64{3, 3, 3, 3, 3}
	asset := []float64{1, -2, 0.5, 4, -1}

	s := Fit(asset, factor)
	if s.Beta != 0 {
		t.Errorf("Beta = %v, want 0 (constant factor)", s.Beta)
	}
	if s.R2 != 0 {
		t.Errorf("R2 = %v, want 0 (constant factor)", s.R2)
	}
	if s.N != 5 {
		t.Errorf("N = %d, want 5", s.N)
	}
}

// TestFit_MismatchedLengths: the slices differ in length; Fit must use the
// shorter length. Here the first 4 factor entries pair with asset = 2*factor,
// so it must recover Beta 2 over N = 4 and ignore the trailing asset values.
func TestFit_MismatchedLengths(t *testing.T) {
	factor := []float64{1, 2, 3, 4}                  // length 4
	asset := []float64{2, 4, 6, 8, 999, -12345, 7.7} // length 7, extras ignored

	s := Fit(asset, factor)
	if s.N != 4 {
		t.Fatalf("N = %d, want 4 (min length)", s.N)
	}
	if math.Abs(s.Beta-2) > 1e-9 {
		t.Errorf("Beta = %.12f, want 2 over the paired prefix", s.Beta)
	}
	if math.Abs(s.R2-1) > 1e-9 {
		t.Errorf("R2 = %.12f, want 1 over the paired prefix", s.R2)
	}
}

// TestFit_SkipsNonFinite: NaN/Inf pairs are dropped, and the clean remainder
// (asset = 2*factor) still fits cleanly with the reduced N.
func TestFit_SkipsNonFinite(t *testing.T) {
	factor := []float64{1, math.NaN(), 3, math.Inf(1), 5, 6}
	asset := []float64{2, 10, 6, 20, math.NaN(), 12}
	// Clean pairs: (1,2), (3,6), (6,12) -> asset = 2*factor, three of them.

	s := Fit(asset, factor)
	if s.N != 3 {
		t.Fatalf("N = %d, want 3 clean pairs", s.N)
	}
	if math.Abs(s.Beta-2) > 1e-9 {
		t.Errorf("Beta = %.12f, want 2 on clean pairs", s.Beta)
	}
}

// TestFit_TooFewPoints: a single pair cannot define a slope; expect zeros.
func TestFit_TooFewPoints(t *testing.T) {
	s := Fit([]float64{5}, []float64{2})
	if s.Beta != 0 || s.R2 != 0 || s.N != 1 {
		t.Errorf("got %+v, want Beta 0, R2 0, N 1", s)
	}
}

// TestEstimate_ExpectedMove: on a usable fit, ExpectedMovePct must equal
// Beta*shock exactly and the result must not be gated. asset = 2*factor gives
// Beta 2; a +10 shock therefore projects a +20 move.
func TestEstimate_ExpectedMove(t *testing.T) {
	factor := []float64{1, 2, 3, 4, 5, -2, -4, 0.5, 7, -3}
	asset := make([]float64, len(factor))
	for i, x := range factor {
		asset[i] = 2 * x
	}

	const shock = 10.0
	imp := Estimate("TEST", asset, factor, shock, "FACTOR +10", 5)

	if imp.Gated {
		t.Fatalf("unexpected gate: %s", imp.Note)
	}
	if math.Abs(imp.ExpectedMovePct-imp.Beta*shock) > 1e-9 {
		t.Errorf("ExpectedMovePct = %.6f, want Beta*shock = %.6f", imp.ExpectedMovePct, imp.Beta*shock)
	}
	if math.Abs(imp.ExpectedMovePct-20) > 1e-9 {
		t.Errorf("ExpectedMovePct = %.6f, want 20 (Beta 2 * shock 10)", imp.ExpectedMovePct)
	}
	if imp.Symbol != "TEST" || imp.ShockLabel != "FACTOR +10" {
		t.Errorf("passthrough fields wrong: symbol=%q label=%q", imp.Symbol, imp.ShockLabel)
	}
	if imp.Note == "" {
		t.Error("expected a descriptive Note on a usable fit")
	}
}

// TestEstimate_GatedThinData: N below minN must gate, withhold the projection
// (ExpectedMovePct = 0), and carry an explanatory Note.
func TestEstimate_GatedThinData(t *testing.T) {
	factor := []float64{1, 2, 3, 4, 5}
	asset := []float64{2, 4, 6, 8, 10} // Beta would be 2, but only 5 points.

	imp := Estimate("THIN", asset, factor, 10, "FACTOR +10", 30)

	if !imp.Gated {
		t.Fatal("expected Gated=true when N (5) < minN (30)")
	}
	if imp.ExpectedMovePct != 0 {
		t.Errorf("ExpectedMovePct = %v, want 0 when gated", imp.ExpectedMovePct)
	}
	if imp.Note == "" {
		t.Error("expected a Note explaining the gate")
	}
	if imp.N != 5 {
		t.Errorf("N = %d, want 5 (reported even when gated)", imp.N)
	}
}

// TestEstimate_GatedZeroR2: when the factor explains none of the asset's
// variance (R2 == 0), the beta is signal-free; Estimate must gate and withhold
// the projection even though the sample is large enough.
func TestEstimate_GatedZeroR2(t *testing.T) {
	// Orthogonal pattern repeated to clear a small minN while keeping R2 == 0.
	var factor, asset []float64
	for i := 0; i < 10; i++ {
		factor = append(factor, 1, -1, 1, -1)
		asset = append(asset, 1, 1, -1, -1)
	}

	imp := Estimate("ORTH", asset, factor, 10, "FACTOR +10", 5)

	if !imp.Gated {
		t.Fatalf("expected Gated=true when R2=0 (got R2=%v)", imp.R2)
	}
	if imp.ExpectedMovePct != 0 {
		t.Errorf("ExpectedMovePct = %v, want 0 when gated on zero R2", imp.ExpectedMovePct)
	}
	if imp.Note == "" {
		t.Error("expected a Note explaining the zero-R2 gate")
	}
}

// TestEstimate_NegativeShock: a negative shock through a positive beta projects
// a negative move, and the identity ExpectedMovePct == Beta*shock still holds.
func TestEstimate_NegativeShock(t *testing.T) {
	factor := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	asset := make([]float64, len(factor))
	for i, x := range factor {
		asset[i] = 1.5 * x
	}

	const shock = -4.0
	imp := Estimate("NEG", asset, factor, shock, "FACTOR -4", 4)

	if imp.Gated {
		t.Fatalf("unexpected gate: %s", imp.Note)
	}
	want := imp.Beta * shock // 1.5 * -4 = -6
	if math.Abs(imp.ExpectedMovePct-want) > 1e-9 {
		t.Errorf("ExpectedMovePct = %.6f, want %.6f", imp.ExpectedMovePct, want)
	}
	if imp.ExpectedMovePct >= 0 {
		t.Errorf("ExpectedMovePct = %.6f, want negative", imp.ExpectedMovePct)
	}
}

// TestStdDev checks the sample (n-1) standard deviation on a known set and the
// small-sample guard.
func TestStdDev(t *testing.T) {
	// values 2,4,4,4,5,5,7,9 have sample stdev 2.138... (variance 32/7).
	xs := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	got := StdDev(xs)
	want := math.Sqrt(32.0 / 7.0)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("StdDev = %.9f, want %.9f", got, want)
	}
	if StdDev([]float64{42}) != 0 {
		t.Error("StdDev of a single element should be 0")
	}
	if StdDev(nil) != 0 {
		t.Error("StdDev of empty should be 0")
	}
}

// TestMean checks the arithmetic mean helper and its empty guard.
func TestMean(t *testing.T) {
	if got := mean([]float64{1, 2, 3, 4}); math.Abs(got-2.5) > 1e-12 {
		t.Errorf("mean = %v, want 2.5", got)
	}
	if got := mean(nil); got != 0 {
		t.Errorf("mean(nil) = %v, want 0", got)
	}
}
