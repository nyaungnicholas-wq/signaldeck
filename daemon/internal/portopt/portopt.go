// Package portopt is a small, dependency-free mean-variance portfolio
// optimizer (Markowitz) for long-only allocations.
//
// Given aligned per-asset return series it builds a sample covariance matrix
// and produces two classic allocations, both constrained to be long-only
// (weights >= 0, summing to 1):
//
//   - MinVariance: the global minimum-variance portfolio via the closed-form
//     inverse-covariance solution, clipped to the long-only simplex.
//   - MaxSharpe: the maximum-Sharpe portfolio via projected gradient ascent on
//     the long-only simplex (no closed form exists under the sign constraint).
//
// Every function is pure: numbers in, numbers out. No I/O, no persistence, no
// clock, no randomness, no goroutines. It depends only on the standard library;
// the small amount of linear algebra it needs (matrix inversion, matrix-vector
// products, quadratic forms, simplex projection) is implemented here.
//
// HONESTY NOTES (this is the brand):
//   - ESTIMATES, NOT TRUTH. Expected returns and covariances are noisy sample
//     statistics estimated from the supplied history. Mean-variance optimization
//     is notoriously sensitive to these inputs — small changes in the estimated
//     mean can swing weights a lot. Treat the output as one reasonable
//     allocation given this history, not a forecast or a guarantee.
//   - APPROXIMATE LONG-ONLY MIN-VARIANCE. The long-only minimum-variance
//     portfolio is a quadratic program. Rather than solve the QP, MinVariance
//     uses the exact unconstrained closed form (weights proportional to
//     Sigma^-1 * 1) and then clips negative weights to zero and renormalizes.
//     This is a documented approximation, not the certified constrained optimum;
//     when clipping occurs it is recorded in Result.Note.
//   - BEST-FOUND, NOT CERTIFIED. MaxSharpe runs deterministic projected gradient
//     ascent for a fixed number of iterations and returns the best weights it
//     visited. The long-only Sharpe objective over the simplex is well-behaved,
//     so this reliably finds the tangency portfolio in practice, but no global
//     optimality certificate is produced.
//   - NEVER NaN/Inf. Singular or ill-conditioned covariance, shape mismatches,
//     zero-variance portfolios, or too few assets all fall back gracefully to
//     an equal-weight allocation with an explanatory Result.Note. Divisions are
//     guarded; the package never returns NaN or Inf weights.
package portopt

import (
	"math"
	"sort"
)

// Method names reported in Result.Method.
const (
	// MethodMinVariance marks a minimum-variance allocation.
	MethodMinVariance = "min_variance"
	// MethodMaxSharpe marks a maximum-Sharpe allocation.
	MethodMaxSharpe = "max_sharpe"
	// MethodEqualWeightFallback marks a graceful equal-weight fallback.
	MethodEqualWeightFallback = "equal_weight_fallback"
)

// Result is an optimized allocation. Weights are aligned with Symbols, are
// long-only (each >= 0) and sum to 1 (except the degenerate empty-input case,
// which yields no weights). ExpRet, Vol and Sharpe are computed for the chosen
// weights when the necessary inputs are available.
type Result struct {
	Symbols []string
	Weights []float64 // long-only, sum to 1
	ExpRet  float64   // portfolio expected return (if expRet provided)
	Vol     float64   // portfolio volatility (sqrt wᵀΣw)
	Sharpe  float64   // (ExpRet - rf) / Vol
	Method  string    // "min_variance" | "max_sharpe" | "equal_weight_fallback"
	Note    string
}

// Covariance returns the NxN sample covariance matrix of the given aligned
// return series (returns[i] is asset i's series). Series are expected to share
// the same length; if they differ, the common leading length (the minimum
// across series) is used defensively. The estimator is the unbiased sample
// covariance with an (T-1) denominator, where T is that common length.
//
// With fewer than two observations there is no sample covariance to form, so an
// all-zero matrix is returned rather than a division by zero.
func Covariance(returns [][]float64) [][]float64 {
	n := len(returns)
	cov := make([][]float64, n)
	for i := range cov {
		cov[i] = make([]float64, n)
	}
	if n == 0 {
		return cov
	}

	// Common (minimum) length across series, so unequal input can't panic.
	t := len(returns[0])
	for _, r := range returns {
		if len(r) < t {
			t = len(r)
		}
	}
	if t < 2 {
		return cov // not enough data for a sample covariance
	}

	means := make([]float64, n)
	for i := 0; i < n; i++ {
		s := 0.0
		for k := 0; k < t; k++ {
			s += returns[i][k]
		}
		means[i] = s / float64(t)
	}

	den := float64(t - 1)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			s := 0.0
			for k := 0; k < t; k++ {
				s += (returns[i][k] - means[i]) * (returns[j][k] - means[j])
			}
			c := s / den
			cov[i][j] = c
			cov[j][i] = c
		}
	}
	return cov
}

// MinVariance returns the long-only minimum-variance weights for cov.
//
// It computes the exact unconstrained global minimum-variance portfolio,
// weights proportional to Sigma^-1 * 1 (normalized to sum to 1), then clips any
// negative weights to zero and renormalizes so the result is long-only. If cov
// is singular or ill-conditioned, has a shape that does not match symbols, or
// there are too few assets to optimize, it falls back to equal weight and
// records why in the returned Note. Result.Vol is sqrt(wᵀ·cov·w); ExpRet and
// Sharpe are left at zero because no expected returns are supplied.
func MinVariance(symbols []string, cov [][]float64) Result {
	n := len(symbols)
	if n == 0 {
		return Result{Symbols: symbols, Weights: []float64{}, Method: MethodEqualWeightFallback, Note: "no assets to optimize"}
	}
	if n == 1 {
		return Result{
			Symbols: symbols,
			Weights: []float64{1},
			Vol:     math.Sqrt(clampNonNeg(covAt(cov, 0, 0))),
			Method:  MethodEqualWeightFallback,
			Note:    "single asset: nothing to optimize, allocated 100%",
		}
	}
	if !isSquare(cov, n) {
		return equalWeightResult(symbols, nil, cov, 0, "covariance shape does not match symbols; used equal weight")
	}

	inv, ok := invert(cov)
	if !ok {
		return equalWeightResult(symbols, nil, cov, 0, "covariance is singular or ill-conditioned; used equal weight")
	}

	// Unconstrained global minimum variance: w ∝ Σ⁻¹·1.
	ones := make([]float64, n)
	for i := range ones {
		ones[i] = 1
	}
	raw := matVec(inv, ones)
	sum := 0.0
	for _, x := range raw {
		sum += x
	}
	if math.Abs(sum) < 1e-15 {
		return equalWeightResult(symbols, nil, cov, 0, "min-variance weights undefined (degenerate covariance); used equal weight")
	}
	w := make([]float64, n)
	for i := range raw {
		w[i] = raw[i] / sum
	}

	// Long-only: clip negatives to zero and renormalize.
	clipped, numClipped, allZero := clipRenorm(w)
	if allZero {
		return equalWeightResult(symbols, nil, cov, 0, "long-only clipping removed all weight; used equal weight")
	}

	res := Result{
		Symbols: symbols,
		Weights: clipped,
		Vol:     math.Sqrt(clampNonNeg(quadForm(clipped, cov))),
		Method:  MethodMinVariance,
	}
	if numClipped > 0 {
		res.Note = "clipped negative weights to zero and renormalized for long-only"
	}
	return res
}

// MaxSharpe returns long-only weights maximizing (expRet·w - rf)/sqrt(wᵀΣw) via
// projected gradient ascent on the simplex. rf is the per-period risk-free rate
// (expressed in the same units as expRet, e.g. per-day if returns are daily).
//
// The search is fully deterministic: it starts from equal weight, takes a fixed
// number of gradient steps with a decaying, scale-normalized step size,
// projects onto the long-only simplex after each step, and returns the
// best-Sharpe weights encountered. If the inputs are shape-mismatched, the
// starting portfolio has zero variance, or there are too few assets, it falls
// back to equal weight with an explanatory Note. Result reports ExpRet, Vol and
// Sharpe for the chosen weights.
func MaxSharpe(symbols []string, expRet []float64, cov [][]float64, rf float64) Result {
	n := len(symbols)
	if n == 0 {
		return Result{Symbols: symbols, Weights: []float64{}, Method: MethodEqualWeightFallback, Note: "no assets to optimize"}
	}
	if n == 1 {
		w := []float64{1}
		er := safeIndex(expRet, 0)
		vol := math.Sqrt(clampNonNeg(covAt(cov, 0, 0)))
		return Result{
			Symbols: symbols,
			Weights: w,
			ExpRet:  er,
			Vol:     vol,
			Sharpe:  sharpe(er, vol, rf),
			Method:  MethodEqualWeightFallback,
			Note:    "single asset: nothing to optimize, allocated 100%",
		}
	}
	if !isSquare(cov, n) || len(expRet) != n {
		return equalWeightResult(symbols, expRet, cov, rf, "inputs shape mismatch; used equal weight")
	}

	w := equalWeights(n)
	if quadForm(w, cov) <= 0 {
		return equalWeightResult(symbols, expRet, cov, rf, "zero-variance portfolio; Sharpe undefined, used equal weight")
	}

	const (
		iters = 500
		step0 = 0.1
	)
	best := append([]float64(nil), w...)
	bestS := sharpeAt(w, expRet, cov, rf)
	for t := 0; t < iters; t++ {
		g, ok := sharpeGradient(w, expRet, cov, rf)
		if !ok {
			break // hit a zero-variance point; stop and keep best
		}
		gmax := infNorm(g)
		if gmax < 1e-15 {
			break // stationary
		}
		// Scale-normalized, decaying step: bound the pre-projection move to at
		// most `frac` in any coordinate so behavior is independent of the units
		// of returns/covariance, then shrink it to settle near the optimum.
		frac := step0 * (1 - float64(t)/float64(iters))
		nw := make([]float64, n)
		for i := 0; i < n; i++ {
			nw[i] = w[i] + frac/gmax*g[i]
		}
		w = projectSimplex(nw)
		if s := sharpeAt(w, expRet, cov, rf); s > bestS {
			bestS = s
			copy(best, w)
		}
	}

	er := dot(expRet, best)
	vol := math.Sqrt(clampNonNeg(quadForm(best, cov)))
	return Result{
		Symbols: symbols,
		Weights: best,
		ExpRet:  er,
		Vol:     vol,
		Sharpe:  sharpe(er, vol, rf),
		Method:  MethodMaxSharpe,
	}
}

// --- internal helpers ------------------------------------------------------

// equalWeights returns the length-n equal-weight vector (each 1/n). For n <= 0
// it returns an empty slice.
func equalWeights(n int) []float64 {
	if n <= 0 {
		return []float64{}
	}
	w := make([]float64, n)
	v := 1.0 / float64(n)
	for i := range w {
		w[i] = v
	}
	return w
}

// equalWeightResult builds an equal-weight fallback Result, filling in Vol from
// cov when its shape matches and ExpRet/Sharpe from expRet when supplied.
func equalWeightResult(symbols []string, expRet []float64, cov [][]float64, rf float64, note string) Result {
	n := len(symbols)
	w := equalWeights(n)
	res := Result{
		Symbols: symbols,
		Weights: w,
		Method:  MethodEqualWeightFallback,
		Note:    note,
	}
	if isSquare(cov, n) {
		res.Vol = math.Sqrt(clampNonNeg(quadForm(w, cov)))
	}
	if len(expRet) == n {
		res.ExpRet = dot(expRet, w)
		res.Sharpe = sharpe(res.ExpRet, res.Vol, rf)
	}
	return res
}

// clipRenorm returns a copy of w with negative entries set to zero and the
// remainder rescaled to sum to 1. numClipped counts the negatives removed;
// allZero is true when nothing positive remained (so the caller must fall back).
func clipRenorm(w []float64) (out []float64, numClipped int, allZero bool) {
	out = make([]float64, len(w))
	sum := 0.0
	for i, x := range w {
		if x < 0 {
			numClipped++
			continue // out[i] stays 0
		}
		out[i] = x
		sum += x
	}
	if sum <= 0 {
		return out, numClipped, true
	}
	for i := range out {
		out[i] /= sum
	}
	return out, numClipped, false
}

// sharpe returns (expRet - rf)/vol, guarding a non-positive vol by returning 0.
func sharpe(expRet, vol, rf float64) float64 {
	if vol <= 0 {
		return 0
	}
	return (expRet - rf) / vol
}

// sharpeAt evaluates the Sharpe ratio of weights w. A non-positive-variance
// portfolio has an undefined Sharpe and returns -Inf so it is never selected as
// the best point during ascent.
func sharpeAt(w, expRet []float64, cov [][]float64, rf float64) float64 {
	v := quadForm(w, cov)
	if v <= 0 {
		return math.Inf(-1)
	}
	return (dot(expRet, w) - rf) / math.Sqrt(v)
}

// sharpeGradient returns the gradient of the Sharpe ratio with respect to w:
//
//	∇S = μ/σ - (μᵀw - rf)·Σw/σ³,   σ = sqrt(wᵀΣw)
//
// ok is false when the portfolio variance is (near) zero, where the gradient is
// undefined.
func sharpeGradient(w, expRet []float64, cov [][]float64, rf float64) (grad []float64, ok bool) {
	sigma2 := quadForm(w, cov)
	if sigma2 <= 1e-18 {
		return nil, false
	}
	sigma := math.Sqrt(sigma2)
	num := dot(expRet, w) - rf // μᵀw - rf
	sw := matVec(cov, w)       // Σw
	sigma3 := sigma2 * sigma
	g := make([]float64, len(w))
	for i := range g {
		g[i] = expRet[i]/sigma - num*sw[i]/sigma3
	}
	return g, true
}

// dot returns the inner product of a and b over their common leading length.
func dot(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	s := 0.0
	for i := 0; i < n; i++ {
		s += a[i] * b[i]
	}
	return s
}

// infNorm returns the maximum absolute value in v (0 for an empty vector).
func infNorm(v []float64) float64 {
	m := 0.0
	for _, x := range v {
		if a := math.Abs(x); a > m {
			m = a
		}
	}
	return m
}

// clampNonNeg returns x, or 0 when x is a small negative produced by floating
// error, so sqrt never sees a negative argument. Genuinely large negatives
// (which should not occur for a valid covariance quadratic form) are clamped
// too rather than yielding NaN.
func clampNonNeg(x float64) float64 {
	if x < 0 {
		return 0
	}
	return x
}

// isSquare reports whether m is an n×n matrix.
func isSquare(m [][]float64, n int) bool {
	if len(m) != n {
		return false
	}
	for _, row := range m {
		if len(row) != n {
			return false
		}
	}
	return true
}

// covAt safely reads m[i][j], returning 0 when out of range.
func covAt(m [][]float64, i, j int) float64 {
	if i < 0 || i >= len(m) || j < 0 || j >= len(m[i]) {
		return 0
	}
	return m[i][j]
}

// safeIndex returns v[i], or 0 when out of range.
func safeIndex(v []float64, i int) float64 {
	if i < 0 || i >= len(v) {
		return 0
	}
	return v[i]
}

// invert returns the inverse of the square matrix a via Gauss-Jordan
// elimination with partial pivoting. ok is false when a is not square or is
// singular / numerically too close to singular to invert. The input is not
// modified.
func invert(a [][]float64) (inv [][]float64, ok bool) {
	n := len(a)
	if n == 0 {
		return nil, false
	}
	// Working copy of a and an identity to transform into the inverse.
	m := make([][]float64, n)
	inv = make([][]float64, n)
	for i := 0; i < n; i++ {
		if len(a[i]) != n {
			return nil, false
		}
		m[i] = make([]float64, n)
		copy(m[i], a[i])
		inv[i] = make([]float64, n)
		inv[i][i] = 1
	}

	const eps = 1e-12
	for col := 0; col < n; col++ {
		// Partial pivot: largest-magnitude entry at or below the diagonal.
		pivot := col
		maxAbs := math.Abs(m[col][col])
		for r := col + 1; r < n; r++ {
			if av := math.Abs(m[r][col]); av > maxAbs {
				maxAbs = av
				pivot = r
			}
		}
		if maxAbs < eps {
			return nil, false // singular
		}
		if pivot != col {
			m[col], m[pivot] = m[pivot], m[col]
			inv[col], inv[pivot] = inv[pivot], inv[col]
		}
		// Scale the pivot row so the pivot becomes 1.
		pv := m[col][col]
		for j := 0; j < n; j++ {
			m[col][j] /= pv
			inv[col][j] /= pv
		}
		// Eliminate the pivot column from every other row.
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			factor := m[r][col]
			if factor == 0 {
				continue
			}
			for j := 0; j < n; j++ {
				m[r][j] -= factor * m[col][j]
				inv[r][j] -= factor * inv[col][j]
			}
		}
	}
	return inv, true
}

// matVec returns the matrix-vector product m·v, using common leading lengths so
// mismatched dimensions cannot panic.
func matVec(m [][]float64, v []float64) []float64 {
	out := make([]float64, len(m))
	for i := range m {
		s := 0.0
		row := m[i]
		k := len(row)
		if len(v) < k {
			k = len(v)
		}
		for j := 0; j < k; j++ {
			s += row[j] * v[j]
		}
		out[i] = s
	}
	return out
}

// quadForm returns the quadratic form wᵀ·m·w.
func quadForm(w []float64, m [][]float64) float64 {
	mv := matVec(m, w)
	return dot(w, mv)
}

// projectSimplex returns the Euclidean projection of v onto the probability
// simplex {x : xᵢ >= 0, Σxᵢ = 1}, i.e. the closest point on the long-only,
// fully-invested simplex. It uses the exact sort-based algorithm of Duchi et
// al. (2008). An empty input yields an empty output.
func projectSimplex(v []float64) []float64 {
	n := len(v)
	if n == 0 {
		return []float64{}
	}
	// Sort a copy in descending order.
	u := make([]float64, n)
	copy(u, v)
	sort.Sort(sort.Reverse(sort.Float64Slice(u)))

	// Largest rho such that u[rho-1] - (cumsum(u)[rho-1] - 1)/rho > 0.
	css := 0.0
	rho := 0
	cssAtRho := 0.0
	for j := 0; j < n; j++ {
		css += u[j]
		if u[j]-(css-1)/float64(j+1) > 0 {
			rho = j + 1
			cssAtRho = css
		}
	}
	// rho is always >= 1 (the j=0 test is u[0]-(u[0]-1)=1 > 0), so the divide
	// below is safe.
	theta := (cssAtRho - 1) / float64(rho)

	w := make([]float64, n)
	for i := range v {
		if x := v[i] - theta; x > 0 {
			w[i] = x
		}
	}
	return w
}
