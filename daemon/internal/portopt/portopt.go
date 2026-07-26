// Package portopt is a small, dependency-free mean-variance portfolio
// optimizer (Markowitz) for long-only allocations.
//
// Given aligned per-asset return series it builds a SHRUNK covariance matrix
// (Ledoit-Wolf, see shrinkage.go) and produces long-only allocations (weights
// >= 0, summing to 1):
//
//   - MinVariance: the global minimum-variance portfolio via the closed-form
//     inverse-covariance solution, clipped to the long-only simplex.
//   - MaxSharpe: the SAFE DEFAULT. It takes no view on expected returns — see
//     its doc for why a mean vector handed to a library cannot be distinguished
//     from in-window noise — so it returns the minimum-variance allocation and
//     says so in the Note.
//   - TangencyWithViews: the classic maximum-Sharpe solve, for callers who can
//     assert their expected returns are EXOGENOUS. Projected gradient ascent on
//     the long-only simplex (no closed form exists under the sign constraint).
//   - MaxSharpeFromReturns: the honest history-only path. It knows T, so it
//     shrinks the covariance (Ledoit-Wolf) and the means (Jorion Bayes-Stein)
//     by data-derived intensities before solving, and reports both.
//
// Every function is pure: numbers in, numbers out. No I/O, no persistence, no
// clock, no randomness, no goroutines. It depends only on the standard library;
// the small amount of linear algebra it needs (matrix inversion, matrix-vector
// products, quadratic forms, simplex projection) is implemented here.
//
// HONESTY NOTES (this is the brand):
//   - ESTIMATION ERROR IS THE DOMINANT RISK, AND IT IS REGULARISED, NOT JUST
//     DISCLOSED. Expected returns and covariances estimated on the same short
//     window the optimizer allocates over make it an error-maximizing machine:
//     it concentrates on whichever estimate noise flattered most. This package
//     therefore SHRINKS both inputs (see shrinkage.go) rather than printing a
//     caveat beside an unshrunk answer. Covariance() is the Ledoit-Wolf
//     estimator, not the raw sample matrix; MaxSharpe takes no view on returns
//     unless the caller states the views are exogenous.
//   - IN-SAMPLE IS SAID IN THE FIELD NAME. Every statistic a Result reports is
//     measured on the estimation window. The Sharpe field is SharpeInSample and
//     is nil when there is nothing to compute it from — a 0 Sharpe is a claim,
//     not an absence.
//   - APPROXIMATE LONG-ONLY MIN-VARIANCE. The long-only minimum-variance
//     portfolio is a quadratic program. Rather than solve the QP, MinVariance
//     uses the exact unconstrained closed form (weights proportional to
//     Sigma^-1 * 1) and then clips negative weights to zero and renormalizes.
//     This is a documented approximation, not the certified constrained optimum;
//     when clipping occurs it is recorded in Result.Note.
//   - BEST-FOUND, NOT CERTIFIED. TangencyWithViews runs deterministic projected
//     gradient ascent for a fixed number of iterations and returns the best
//     weights it visited. The long-only Sharpe objective over the simplex is
//     well-behaved, so this reliably finds the tangency portfolio in practice,
//     but no global optimality certificate is produced.
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
	// MethodMaxSharpe marks a maximum-Sharpe (tangency) allocation built on
	// views the caller asserted are exogenous.
	MethodMaxSharpe = "max_sharpe"
	// MethodMinVarianceNoView marks the no-view default: expected returns were
	// fully shrunk to their cross-sectional mean, so the maximum-Sharpe
	// portfolio IS the minimum-variance portfolio.
	MethodMinVarianceNoView = "min_variance_no_view"
	// MethodEqualWeightFallback marks a graceful equal-weight fallback.
	MethodEqualWeightFallback = "equal_weight_fallback"
)

// Shrinkage records how far each noisy input was pulled toward its prior, so a
// reader can see how much of the allocation is data and how much is the
// regulariser. Nil intensities mean "not applicable on this path" (e.g. the
// caller supplied a covariance matrix, so this package never saw the returns
// the Ledoit-Wolf intensity is derived from).
type Shrinkage struct {
	CovarianceIntensity *float64 `json:",omitempty"` // Ledoit-Wolf delta in [0,1]
	CovarianceTarget    string   `json:",omitempty"`
	MeanIntensity       *float64 `json:",omitempty"` // Bayes-Stein w in [0,1]; 1 = no view
	MeanTarget          string   `json:",omitempty"`
	// Views are the expected returns actually optimized on, after shrinkage.
	Views []float64 `json:",omitempty"`
}

// Result is an optimized allocation. Weights are aligned with Symbols, are
// long-only (each >= 0) and sum to 1 (except the degenerate empty-input case,
// which yields no weights).
//
// Every reported statistic is measured on the SAME window that produced the
// inputs — InSample is true and SharpeInSample is named for it. A Sharpe
// computed on the window an optimizer just fitted is a fit statistic; reporting
// it as "Sharpe" beside an allocation invites it to be read as the allocation's
// expected performance, which was precisely the review's complaint.
type Result struct {
	Symbols []string
	Weights []float64 // long-only, sum to 1
	ExpRet  float64   // portfolio expected return over the estimation window
	Vol     float64   // portfolio volatility (sqrt wᵀΣw) over the estimation window
	// SharpeInSample is (ExpRet - rf)/Vol for these weights ON THE ESTIMATION
	// WINDOW. It is nil — never 0 — when no expected returns were supplied or
	// the volatility is not positive.
	SharpeInSample *float64 `json:",omitempty"`
	// InSample is always true for this package and is carried in the payload so
	// a consumer that only reads JSON cannot miss it.
	InSample  bool
	Method    string // see the Method* constants
	Note      string
	Shrinkage *Shrinkage `json:",omitempty"`
}

// withSharpe fills ExpRet/Vol/SharpeInSample for weights w, withholding the
// Sharpe (nil) when it cannot be formed.
func (r Result) withSharpe(expRet []float64, cov [][]float64, rf float64) Result {
	r.InSample = true
	if !isSquare(cov, len(r.Weights)) {
		return r
	}
	r.Vol = math.Sqrt(clampNonNeg(quadForm(r.Weights, cov)))
	if len(expRet) != len(r.Weights) {
		return r
	}
	r.ExpRet = dot(expRet, r.Weights)
	if r.Vol > 0 {
		s := (r.ExpRet - rf) / r.Vol
		r.SharpeInSample = &s
	}
	return r
}

// Covariance and its shrinkage machinery live in shrinkage.go.

// MinVariance returns the long-only minimum-variance weights for cov.
//
// It computes the exact unconstrained global minimum-variance portfolio,
// weights proportional to Sigma^-1 * 1 (normalized to sum to 1), then clips any
// negative weights to zero and renormalizes so the result is long-only. If cov
// is singular or ill-conditioned, has a shape that does not match symbols, or
// there are too few assets to optimize, it falls back to equal weight and
// records why in the returned Note. Result.Vol is sqrt(wᵀ·cov·w); ExpRet is
// zero and SharpeInSample is nil (withheld) because no expected returns are
// supplied — there is nothing to compute a Sharpe from, and a 0 would be a
// claim rather than an absence.
func MinVariance(symbols []string, cov [][]float64) Result {
	return minVariance(symbols, cov, nil, 0, MethodMinVariance, "")
}

// minVariance is the shared long-only minimum-variance solve. expRet/rf are
// used only to fill the in-sample statistics; they never influence the weights.
// method/extraNote let the no-view MaxSharpe path label itself honestly.
func minVariance(symbols []string, cov [][]float64, expRet []float64, rf float64, method, extraNote string) Result {
	n := len(symbols)
	join := func(base string) string {
		if extraNote == "" {
			return base
		}
		if base == "" {
			return extraNote
		}
		return extraNote + " " + base
	}
	if n == 0 {
		return Result{Symbols: symbols, Weights: []float64{}, InSample: true,
			Method: MethodEqualWeightFallback, Note: join("no assets to optimize")}
	}
	if n == 1 {
		r := Result{
			Symbols: symbols,
			Weights: []float64{1},
			Method:  MethodEqualWeightFallback,
			Note:    join("single asset: nothing to optimize, allocated 100%"),
		}
		return r.withSharpe(expRet, cov, rf)
	}
	if !isSquare(cov, n) {
		return equalWeightResult(symbols, expRet, cov, rf, join("covariance shape does not match symbols; used equal weight"))
	}

	inv, ok := invert(cov)
	if !ok {
		return equalWeightResult(symbols, expRet, cov, rf, join("covariance is singular or ill-conditioned; used equal weight"))
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
		return equalWeightResult(symbols, expRet, cov, rf, join("min-variance weights undefined (degenerate covariance); used equal weight"))
	}
	w := make([]float64, n)
	for i := range raw {
		w[i] = raw[i] / sum
	}

	// Long-only: clip negatives to zero and renormalize.
	clipped, numClipped, allZero := clipRenorm(w)
	if allZero {
		return equalWeightResult(symbols, expRet, cov, rf, join("long-only clipping removed all weight; used equal weight"))
	}

	res := Result{Symbols: symbols, Weights: clipped, Method: method}
	if numClipped > 0 {
		res.Note = "clipped negative weights to zero and renormalized for long-only"
	}
	res.Note = join(res.Note)
	return res.withSharpe(expRet, cov, rf)
}

// noViewNote is the sentence a no-view allocation carries. It has to say what
// happened to the caller's expected returns, because the weights no longer
// depend on them at all.
const noViewNote = "expected returns were fully shrunk to their cross-sectional mean: sample means over a window this short are not distinguishable from noise, and tilting on them maximizes estimation error rather than return. With no view, the maximum-Sharpe portfolio IS the minimum-variance portfolio. Supply exogenous views to TangencyWithViews, or use MaxSharpeFromReturns to shrink by a data-derived intensity."

// MaxSharpe is the SAFE DEFAULT entry point, and it deliberately does not tilt
// on the expected returns it is handed.
//
// THE FINDING THIS IMPLEMENTS: the optimizer estimated expected returns and
// covariance on the same window and then reported the Sharpe that overweighting
// produced. Given only a mean vector and a covariance matrix, this function
// cannot tell an exogenous view from a sample mean computed on the very window
// the covariance came from — and the caller that shipped the defect was passing
// the latter. So the default assumes the latter: expRet is fully shrunk to its
// cross-sectional mean, which makes the tangency portfolio the minimum-variance
// portfolio, and the Note says so.
//
// expRet is still used to report the in-sample ExpRet and SharpeInSample OF THE
// CHOSEN WEIGHTS. Those are fit statistics on the estimation window, named for
// it, and the Sharpe is withheld (nil) rather than zeroed when it cannot be
// formed.
//
// Callers with genuinely exogenous views want TangencyWithViews. Callers who
// have the return history want MaxSharpeFromReturns, which knows T and can
// therefore shrink by a derived intensity instead of shrinking all the way.
func MaxSharpe(symbols []string, expRet []float64, cov [][]float64, rf float64) Result {
	if len(symbols) > 1 && (!isSquare(cov, len(symbols)) || len(expRet) != len(symbols)) {
		return equalWeightResult(symbols, expRet, cov, rf, "inputs shape mismatch; used equal weight")
	}
	full := 1.0
	res := minVariance(symbols, cov, expRet, rf, MethodMinVarianceNoView, noViewNote)
	res.Shrinkage = &Shrinkage{
		MeanIntensity: &full,
		MeanTarget:    "cross-sectional mean of the supplied expected returns (no view)",
	}
	return res
}

// TangencyWithViews returns long-only weights maximizing (views·w - rf)/sqrt(wᵀΣw)
// via projected gradient ascent on the simplex. rf is the per-period risk-free
// rate (expressed in the same units as views, e.g. per-day if returns are
// daily).
//
// CONTRACT — READ BEFORE CALLING: `views` must be EXOGENOUS expected returns.
// Trailing sample means computed on the same window as cov are not views; they
// are noise, and this solver will faithfully concentrate the book on whichever
// asset that noise flattered. That is the error-maximization the review found.
// If all you have is history, call MaxSharpeFromReturns.
//
// The search is fully deterministic: it starts from equal weight, takes a fixed
// number of gradient steps with a decaying, scale-normalized step size,
// projects onto the long-only simplex after each step, and returns the
// best-Sharpe weights encountered. If the inputs are shape-mismatched, the
// starting portfolio has zero variance, or there are too few assets, it falls
// back to equal weight with an explanatory Note.
func TangencyWithViews(symbols []string, views []float64, cov [][]float64, rf float64) Result {
	n := len(symbols)
	if n == 0 {
		return Result{Symbols: symbols, Weights: []float64{}, InSample: true,
			Method: MethodEqualWeightFallback, Note: "no assets to optimize"}
	}
	if n == 1 {
		r := Result{
			Symbols: symbols,
			Weights: []float64{1},
			Method:  MethodEqualWeightFallback,
			Note:    "single asset: nothing to optimize, allocated 100%",
		}
		return r.withSharpe(views, cov, rf)
	}
	if !isSquare(cov, n) || len(views) != n {
		return equalWeightResult(symbols, views, cov, rf, "inputs shape mismatch; used equal weight")
	}

	w := equalWeights(n)
	if quadForm(w, cov) <= 0 {
		return equalWeightResult(symbols, views, cov, rf, "zero-variance portfolio; Sharpe undefined, used equal weight")
	}

	const (
		iters = 500
		step0 = 0.1
	)
	best := append([]float64(nil), w...)
	bestS := sharpeAt(w, views, cov, rf)
	for t := 0; t < iters; t++ {
		g, ok := sharpeGradient(w, views, cov, rf)
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
		if s := sharpeAt(w, views, cov, rf); s > bestS {
			bestS = s
			copy(best, w)
		}
	}

	res := Result{Symbols: symbols, Weights: best, Method: MethodMaxSharpe}
	return res.withSharpe(views, cov, rf)
}

// MaxSharpeFromReturns is the honest mean-variance path when history is all you
// have. Because it receives the raw aligned return series it knows T, so it can
// shrink BOTH inputs by intensities derived from the data instead of asserting
// them:
//
//   - covariance: Ledoit-Wolf toward a constant-correlation target;
//   - expected returns: Jorion (1986) Bayes-Stein toward the minimum-variance
//     portfolio's return.
//
// It then runs the same tangency solve on those shrunk estimates and reports
// both intensities and the shrunk views in Result.Shrinkage, so a reader can
// see how much of the allocation survived regularisation. The reported Sharpe
// is still an in-sample fit statistic and is named accordingly.
func MaxSharpeFromReturns(symbols []string, returns [][]float64, rf float64) Result {
	n := len(symbols)
	if n == 0 || len(returns) != n {
		return equalWeightResult(symbols, nil, nil, rf, "no aligned return series for these symbols; used equal weight")
	}
	cov, covDelta := CovarianceShrinkage(returns)
	T := commonLength(returns)
	raw := meansOf(returns, T)
	views, meanW := ShrinkMeans(raw, cov, T)

	var res Result
	if meanW >= 1 {
		// Fully shrunk: every asset carries the same expected return, so the
		// tangency portfolio collapses onto minimum variance. Solve it directly
		// rather than asking gradient ascent to rediscover it.
		res = minVariance(symbols, cov, views, rf, MethodMinVarianceNoView,
			"Bayes-Stein shrank the sample means all the way to the minimum-variance portfolio's return: their cross-sectional dispersion is within what sampling noise alone produces at this sample size.")
	} else {
		res = TangencyWithViews(symbols, views, cov, rf)
	}
	res.Shrinkage = &Shrinkage{
		CovarianceIntensity: &covDelta,
		CovarianceTarget:    "constant correlation (Ledoit-Wolf)",
		MeanIntensity:       &meanW,
		MeanTarget:          "minimum-variance portfolio return (Jorion Bayes-Stein)",
		Views:               views,
	}
	return res
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
// cov when its shape matches and ExpRet/SharpeInSample from expRet when
// supplied.
func equalWeightResult(symbols []string, expRet []float64, cov [][]float64, rf float64, note string) Result {
	res := Result{
		Symbols: symbols,
		Weights: equalWeights(len(symbols)),
		Method:  MethodEqualWeightFallback,
		Note:    note,
	}
	return res.withSharpe(expRet, cov, rf)
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
