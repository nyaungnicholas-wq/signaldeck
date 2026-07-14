package portopt

import (
	"math"
	"testing"
)

const tol = 1e-9

// sumWeights is a small helper for the sum-to-one invariant.
func sumWeights(w []float64) float64 {
	s := 0.0
	for _, x := range w {
		s += x
	}
	return s
}

// hasNaNorInf reports whether any weight is NaN or Inf.
func hasNaNorInf(w []float64) bool {
	for _, x := range w {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return true
		}
	}
	return false
}

func approx(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// --- Covariance -------------------------------------------------------------

func TestCovariance(t *testing.T) {
	// Two series with hand-computed sample (n-1) covariance.
	//   A = [1,2,3]  mean 2  dev [-1,0,1]
	//   B = [1,3,2]  mean 2  dev [-1,1,0]
	//   cov_AA = (1+0+1)/2 = 1
	//   cov_BB = (1+1+0)/2 = 1
	//   cov_AB = (1+0+0)/2 = 0.5
	got := Covariance([][]float64{{1, 2, 3}, {1, 3, 2}})
	want := [][]float64{{1, 0.5}, {0.5, 1}}
	if len(got) != 2 || len(got[0]) != 2 {
		t.Fatalf("shape = %dx?, want 2x2", len(got))
	}
	for i := range want {
		for j := range want[i] {
			if !approx(got[i][j], want[i][j], tol) {
				t.Errorf("cov[%d][%d] = %v, want %v", i, j, got[i][j], want[i][j])
			}
		}
	}
	// Symmetry.
	if got[0][1] != got[1][0] {
		t.Errorf("covariance not symmetric: %v vs %v", got[0][1], got[1][0])
	}

	// Degenerate: fewer than two observations -> all-zero, no panic/NaN.
	z := Covariance([][]float64{{1}, {2}})
	if z[0][0] != 0 || z[0][1] != 0 || z[1][1] != 0 {
		t.Errorf("short-series covariance = %v, want all zeros", z)
	}
}

// --- MinVariance ------------------------------------------------------------

func TestMinVariance(t *testing.T) {
	tests := []struct {
		name    string
		symbols []string
		cov     [][]float64
		// wantWeights: if non-nil, exact expected weights within tol.
		wantWeights []float64
		wantMethod  string
		// checks run when wantWeights is nil.
		check func(t *testing.T, r Result)
	}{
		{
			// (1) Two uncorrelated assets, equal variance -> 50/50.
			name:        "equal_variance_uncorrelated",
			symbols:     []string{"A", "B"},
			cov:         [][]float64{{0.04, 0}, {0, 0.04}},
			wantWeights: []float64{0.5, 0.5},
			wantMethod:  MethodMinVariance,
		},
		{
			// (2) One asset far riskier -> min-variance tilts to the low-vol one.
			// cov = diag(0.01, 0.04); Σ⁻¹1 ∝ (100, 25) -> (0.8, 0.2).
			name:        "unequal_variance_tilts_low",
			symbols:     []string{"LOW", "HIGH"},
			cov:         [][]float64{{0.01, 0}, {0, 0.04}},
			wantWeights: []float64{0.8, 0.2},
			wantMethod:  MethodMinVariance,
		},
		{
			// Three uncorrelated assets, variances 1,2,4 -> weights ∝ (1,1/2,1/4).
			name:        "three_assets_inverse_variance",
			symbols:     []string{"A", "B", "C"},
			cov:         [][]float64{{1, 0, 0}, {0, 2, 0}, {0, 0, 4}},
			wantWeights: []float64{4.0 / 7, 2.0 / 7, 1.0 / 7},
			wantMethod:  MethodMinVariance,
		},
		{
			// (4) Singular covariance (duplicate identical series) -> fallback.
			name:       "singular_fallback",
			symbols:    []string{"A", "B"},
			cov:        [][]float64{{0.04, 0.04}, {0.04, 0.04}},
			wantMethod: MethodEqualWeightFallback,
			check: func(t *testing.T, r Result) {
				if !approx(sumWeights(r.Weights), 1, tol) {
					t.Errorf("fallback weights sum = %v, want 1", sumWeights(r.Weights))
				}
				if !approx(r.Weights[0], 0.5, tol) || !approx(r.Weights[1], 0.5, tol) {
					t.Errorf("fallback weights = %v, want [0.5 0.5]", r.Weights)
				}
				if r.Note == "" {
					t.Error("expected a Note on singular fallback")
				}
				if hasNaNorInf(r.Weights) {
					t.Errorf("weights contain NaN/Inf: %v", r.Weights)
				}
			},
		},
		{
			// Long-only clipping: correlated assets whose unconstrained min-var
			// solution has a negative weight; it must be clipped and renormed.
			name:       "clips_negative_weight",
			symbols:    []string{"A", "B"},
			cov:        [][]float64{{0.01, 0.02}, {0.02, 0.09}},
			wantMethod: MethodMinVariance,
			check: func(t *testing.T, r Result) {
				for i, w := range r.Weights {
					if w < 0 {
						t.Errorf("weight[%d] = %v is negative (long-only violated)", i, w)
					}
				}
				if !approx(sumWeights(r.Weights), 1, tol) {
					t.Errorf("weights sum = %v, want 1", sumWeights(r.Weights))
				}
			},
		},
		{
			name:        "single_asset",
			symbols:     []string{"ONLY"},
			cov:         [][]float64{{0.04}},
			wantWeights: []float64{1},
			wantMethod:  MethodEqualWeightFallback,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := MinVariance(tc.symbols, tc.cov)
			if r.Method != tc.wantMethod {
				t.Errorf("Method = %q, want %q (Note=%q)", r.Method, tc.wantMethod, r.Note)
			}
			if hasNaNorInf(r.Weights) {
				t.Fatalf("weights contain NaN/Inf: %v", r.Weights)
			}
			if tc.wantWeights != nil {
				if len(r.Weights) != len(tc.wantWeights) {
					t.Fatalf("len(weights) = %d, want %d", len(r.Weights), len(tc.wantWeights))
				}
				for i := range tc.wantWeights {
					if !approx(r.Weights[i], tc.wantWeights[i], tol) {
						t.Errorf("weight[%d] = %v, want %v", i, r.Weights[i], tc.wantWeights[i])
					}
				}
				if !approx(sumWeights(r.Weights), 1, tol) {
					t.Errorf("weights sum = %v, want 1", sumWeights(r.Weights))
				}
			}
			if tc.check != nil {
				tc.check(t, r)
			}
		})
	}
}

// --- MaxSharpe --------------------------------------------------------------

func TestMaxSharpe(t *testing.T) {
	// (3) Asset A: higher expected return AND lower vol than B, uncorrelated.
	// Long-only tangency (rf=0) ∝ Σ⁻¹μ = (μ0/σ0², μ1/σ1²) = (3.75, 0.3125)
	// -> ~ (0.923, 0.077), so A should dominate.
	symbols := []string{"A", "B"}
	expRet := []float64{0.0015, 0.0005}
	cov := [][]float64{{0.0004, 0}, {0, 0.0016}}
	rf := 0.0

	r := MaxSharpe(symbols, expRet, cov, rf)

	if r.Method != MethodMaxSharpe {
		t.Fatalf("Method = %q, want %q (Note=%q)", r.Method, MethodMaxSharpe, r.Note)
	}
	if hasNaNorInf(r.Weights) {
		t.Fatalf("weights contain NaN/Inf: %v", r.Weights)
	}
	if len(r.Weights) != 2 {
		t.Fatalf("len(weights) = %d, want 2", len(r.Weights))
	}
	// Long-only and fully invested.
	for i, w := range r.Weights {
		if w < 0 {
			t.Errorf("weight[%d] = %v is negative", i, w)
		}
	}
	if !approx(sumWeights(r.Weights), 1, tol) {
		t.Errorf("weights sum = %v, want 1", sumWeights(r.Weights))
	}
	// More weight on the higher-return / lower-vol asset.
	if r.Weights[0] <= r.Weights[1] {
		t.Errorf("weights = %v, want more on A (index 0)", r.Weights)
	}
	// Should land near the analytical tangency (~0.923).
	if !approx(r.Weights[0], 0.9231, 5e-3) {
		t.Errorf("weight[0] = %v, want ≈0.923", r.Weights[0])
	}
	// Reported stats are consistent and finite.
	wantVol := math.Sqrt(quadForm(r.Weights, cov))
	if !approx(r.Vol, wantVol, tol) {
		t.Errorf("Vol = %v, want %v", r.Vol, wantVol)
	}
	wantSharpe := sharpe(r.ExpRet, r.Vol, rf)
	if !approx(r.Sharpe, wantSharpe, tol) {
		t.Errorf("Sharpe = %v, want %v", r.Sharpe, wantSharpe)
	}
	if r.Sharpe <= 0 {
		t.Errorf("Sharpe = %v, want positive for these inputs", r.Sharpe)
	}

	// The optimized Sharpe must beat both single-asset Sharpes' worse pick and
	// the equal-weight portfolio (sanity that ascent actually improved things).
	ew := equalWeights(2)
	ewSharpe := sharpeAt(ew, expRet, cov, rf)
	if r.Sharpe < ewSharpe-tol {
		t.Errorf("optimized Sharpe %v worse than equal-weight %v", r.Sharpe, ewSharpe)
	}

	// Degenerate: zero-variance covariance -> graceful equal-weight fallback.
	deg := MaxSharpe(symbols, expRet, [][]float64{{0, 0}, {0, 0}}, rf)
	if deg.Method != MethodEqualWeightFallback {
		t.Errorf("zero-variance Method = %q, want %q", deg.Method, MethodEqualWeightFallback)
	}
	if deg.Note == "" {
		t.Error("expected a Note on zero-variance fallback")
	}
	if hasNaNorInf(deg.Weights) {
		t.Errorf("degenerate weights contain NaN/Inf: %v", deg.Weights)
	}
}

// --- Linear algebra helpers -------------------------------------------------

func TestInvert(t *testing.T) {
	tests := []struct {
		name string
		a    [][]float64
		want [][]float64 // nil means expect singular (ok=false)
	}{
		{
			// Known 2x2 inverse: A=[[4,7],[2,6]], det=10, A⁻¹=[[.6,-.7],[-.2,.4]].
			name: "known_2x2",
			a:    [][]float64{{4, 7}, {2, 6}},
			want: [][]float64{{0.6, -0.7}, {-0.2, 0.4}},
		},
		{
			// Diagonal 3x3 -> reciprocal diagonal.
			name: "diagonal_3x3",
			a:    [][]float64{{2, 0, 0}, {0, 4, 0}, {0, 0, 5}},
			want: [][]float64{{0.5, 0, 0}, {0, 0.25, 0}, {0, 0, 0.2}},
		},
		{
			name: "singular",
			a:    [][]float64{{1, 2}, {2, 4}},
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inv, ok := invert(tc.a)
			if tc.want == nil {
				if ok {
					t.Fatalf("expected singular, got inverse %v", inv)
				}
				return
			}
			if !ok {
				t.Fatalf("invert failed on non-singular matrix")
			}
			for i := range tc.want {
				for j := range tc.want[i] {
					if !approx(inv[i][j], tc.want[i][j], 1e-9) {
						t.Errorf("inv[%d][%d] = %v, want %v", i, j, inv[i][j], tc.want[i][j])
					}
				}
			}
			// Verify A·A⁻¹ ≈ I as an independent cross-check.
			n := len(tc.a)
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					s := 0.0
					for k := 0; k < n; k++ {
						s += tc.a[i][k] * inv[k][j]
					}
					want := 0.0
					if i == j {
						want = 1
					}
					if !approx(s, want, 1e-9) {
						t.Errorf("(A·A⁻¹)[%d][%d] = %v, want %v", i, j, s, want)
					}
				}
			}
		})
	}
}

func TestProjectSimplex(t *testing.T) {
	tests := []struct {
		name string
		in   []float64
		want []float64
	}{
		{"already_on_simplex", []float64{0.2, 0.5, 0.3}, []float64{0.2, 0.5, 0.3}},
		{"vertex", []float64{5, -1, -2}, []float64{1, 0, 0}},
		{"negative_pulled_up", []float64{-1, 2, 0}, []float64{0, 1, 0}},
		{"two_equal", []float64{1, 1}, []float64{0.5, 0.5}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := projectSimplex(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if !approx(got[i], tc.want[i], tol) {
					t.Errorf("out[%d] = %v, want %v", i, got[i], tc.want[i])
				}
				if got[i] < 0 {
					t.Errorf("out[%d] = %v is negative", i, got[i])
				}
			}
			if !approx(sumWeights(got), 1, tol) {
				t.Errorf("sum = %v, want 1", sumWeights(got))
			}
		})
	}
}

// End-to-end: covariance built from returns then optimized, no NaN anywhere.
func TestPipelineFromReturns(t *testing.T) {
	returns := [][]float64{
		{0.01, -0.02, 0.015, 0.00, 0.005, -0.01},
		{-0.005, 0.01, -0.02, 0.03, -0.01, 0.02},
		{0.02, 0.00, 0.01, -0.015, 0.005, 0.00},
	}
	symbols := []string{"X", "Y", "Z"}
	cov := Covariance(returns)

	mv := MinVariance(symbols, cov)
	if mv.Method != MethodMinVariance {
		t.Errorf("MinVariance method = %q, want %q (Note=%q)", mv.Method, MethodMinVariance, mv.Note)
	}
	if hasNaNorInf(mv.Weights) || !approx(sumWeights(mv.Weights), 1, tol) {
		t.Errorf("MinVariance weights bad: %v", mv.Weights)
	}
	for i, w := range mv.Weights {
		if w < 0 {
			t.Errorf("MinVariance weight[%d] = %v negative", i, w)
		}
	}

	expRet := []float64{0.001, 0.0008, 0.0012}
	ms := MaxSharpe(symbols, expRet, cov, 0.0)
	if ms.Method != MethodMaxSharpe {
		t.Errorf("MaxSharpe method = %q, want %q (Note=%q)", ms.Method, MethodMaxSharpe, ms.Note)
	}
	if hasNaNorInf(ms.Weights) || !approx(sumWeights(ms.Weights), 1, tol) {
		t.Errorf("MaxSharpe weights bad: %v", ms.Weights)
	}
	for i, w := range ms.Weights {
		if w < 0 {
			t.Errorf("MaxSharpe weight[%d] = %v negative", i, w)
		}
	}
}
