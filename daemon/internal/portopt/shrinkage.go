package portopt

import "math"

// ── ESTIMATION ERROR IS THE PROBLEM, NOT THE OPTIMIZATION ────────────────────
//
// Mean-variance optimization is an error-maximizing machine when its inputs are
// sample statistics from the same short window it is asked to allocate over: it
// reliably puts the most weight on whichever asset's estimate is most flattered
// by noise. Michaud (1989) named it; every practitioner since has confirmed it.
// This file supplies the two regularisers that make the inputs usable:
//
//   - Ledoit-Wolf shrinkage of the covariance toward a constant-correlation
//     target (Ledoit & Wolf, 2003/2004). Short windows produce near-singular
//     sample covariances whose inverse — which is what every optimizer actually
//     uses — amplifies the noise. Shrinking toward the average correlation
//     restores positive definiteness with an intensity derived from the data,
//     not chosen by hand.
//   - Jorion (1986) Bayes-Stein shrinkage of the expected returns toward the
//     minimum-variance portfolio's return. Sample means need centuries of data
//     to be estimated well; over one to two hundred days they are noise with a
//     sign. Shrinking them is not conservatism, it is the estimator with lower
//     expected loss.
//
// Both are pure, deterministic, stdlib-only, and report the intensity they
// chose so a reader can see how much of the answer is data and how much is the
// prior.

// SampleCovariance returns the RAW NxN sample covariance matrix of the given
// aligned return series (returns[i] is asset i's series), with the unbiased
// (T-1) denominator. Series are expected to share the same length; if they
// differ, the common leading length is used defensively.
//
// This is the estimator the package used everywhere before the hostile review.
// It is kept exported because it is the honest baseline to compare against and
// because a caller may legitimately want the unregularised matrix — but it is
// NOT what the solvers should be fed from a short window. Use Covariance.
//
// With fewer than two observations there is no sample covariance to form, so an
// all-zero matrix is returned rather than a division by zero.
func SampleCovariance(returns [][]float64) [][]float64 {
	n := len(returns)
	cov := make([][]float64, n)
	for i := range cov {
		cov[i] = make([]float64, n)
	}
	if n == 0 {
		return cov
	}

	t := commonLength(returns)
	if t < 2 {
		return cov // not enough data for a sample covariance
	}

	means := meansOf(returns, t)
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

// Covariance returns the covariance estimate this package uses everywhere: the
// Ledoit-Wolf shrinkage estimator with a constant-correlation target, on the
// unbiased (T-1) scale.
//
// It is deliberately NOT the raw sample covariance. Feeding a short-window
// sample covariance to an optimizer is the mechanism behind the "error
// maximization" finding: the matrix is near-singular, its inverse magnifies the
// smallest eigenvalue's noise, and the resulting weights concentrate on
// whichever pair of assets happened to look least correlated. Shrinkage costs a
// small bias and removes most of that variance. SampleCovariance is still
// available for callers who explicitly want the unregularised matrix.
//
// The target keeps every sample VARIANCE and replaces every sample CORRELATION
// with the cross-sectional average correlation, so the diagonal is untouched
// and only the co-movement structure is regularised.
func Covariance(returns [][]float64) [][]float64 {
	cov, _ := CovarianceShrinkage(returns)
	return cov
}

// CovarianceShrinkage returns the Ledoit-Wolf estimator together with the
// shrinkage intensity delta in [0,1] it selected (0 = pure sample covariance,
// 1 = pure constant-correlation target). The intensity is derived from the data
// by minimising expected squared Frobenius loss — it is not a tuning knob.
//
// Method (Ledoit & Wolf 2003, "Improved estimation of the covariance matrix of
// stock returns with an application to portfolio selection", section 3.3):
// with y the demeaned returns and S the 1/T sample covariance,
//
//	pi   = sum_ij  (1/T) sum_t (y_it*y_jt - s_ij)^2
//	rho  = sum_i pi_ii + sum_{i!=j} (rbar/2) *
//	       ( sqrt(s_jj/s_ii)*theta_ii,ij + sqrt(s_ii/s_jj)*theta_jj,ij )
//	gamma= sum_ij (f_ij - s_ij)^2
//	delta= clamp( (pi - rho) / gamma / T , 0, 1 )
//
// The returned matrix is rescaled by T/(T-1) so its diagonal remains the
// unbiased sample variances the rest of the package expects; scaling a convex
// combination by a constant leaves the combination unchanged.
//
// Degenerate inputs (fewer than two assets or two observations, a zero-variance
// asset, gamma = 0) fall back to delta = 0 and the sample covariance, which is
// the correct answer when there is no dispersion to shrink.
func CovarianceShrinkage(returns [][]float64) (cov [][]float64, delta float64) {
	n := len(returns)
	sample := SampleCovariance(returns)
	if n < 2 {
		return sample, 0
	}
	T := commonLength(returns)
	if T < 2 {
		return sample, 0
	}
	ft := float64(T)

	// Demeaned returns and the 1/T sample covariance the LW formulas are
	// defined on (the package's own estimator uses 1/(T-1)).
	means := meansOf(returns, T)
	y := make([][]float64, n)
	for i := 0; i < n; i++ {
		y[i] = make([]float64, T)
		for t := 0; t < T; t++ {
			y[i][t] = returns[i][t] - means[i]
		}
	}
	s := make([][]float64, n)
	for i := range s {
		s[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			acc := 0.0
			for t := 0; t < T; t++ {
				acc += y[i][t] * y[j][t]
			}
			v := acc / ft
			s[i][j], s[j][i] = v, v
		}
	}
	for i := 0; i < n; i++ {
		if s[i][i] <= 0 {
			return sample, 0 // a flat asset: no correlation structure to shrink
		}
	}

	// rbar: average sample correlation across the off-diagonal.
	rbar, pairs := 0.0, 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			rbar += s[i][j] / math.Sqrt(s[i][i]*s[j][j])
			pairs++
		}
	}
	if pairs == 0 {
		return sample, 0
	}
	rbar /= float64(pairs)

	// Constant-correlation target F.
	f := make([][]float64, n)
	for i := range f {
		f[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		f[i][i] = s[i][i]
		for j := i + 1; j < n; j++ {
			v := rbar * math.Sqrt(s[i][i]*s[j][j])
			f[i][j], f[j][i] = v, v
		}
	}

	// pi_ij: asymptotic variance of the sample covariance entries.
	pi := make([][]float64, n)
	for i := range pi {
		pi[i] = make([]float64, n)
	}
	piSum := 0.0
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			acc := 0.0
			for t := 0; t < T; t++ {
				d := y[i][t]*y[j][t] - s[i][j]
				acc += d * d
			}
			v := acc / ft
			pi[i][j], pi[j][i] = v, v
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			piSum += pi[i][j]
		}
	}

	// rho: covariance between the sample entries and the target's estimation
	// error. theta_kk,ij = (1/T) sum_t (y_kt^2 - s_kk)(y_it*y_jt - s_ij).
	rho := 0.0
	for i := 0; i < n; i++ {
		rho += pi[i][i]
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			thetaII, thetaJJ := 0.0, 0.0
			for t := 0; t < T; t++ {
				cross := y[i][t]*y[j][t] - s[i][j]
				thetaII += (y[i][t]*y[i][t] - s[i][i]) * cross
				thetaJJ += (y[j][t]*y[j][t] - s[j][j]) * cross
			}
			thetaII /= ft
			thetaJJ /= ft
			rho += rbar / 2 * (math.Sqrt(s[j][j]/s[i][i])*thetaII + math.Sqrt(s[i][i]/s[j][j])*thetaJJ)
		}
	}

	// gamma: squared Frobenius distance between target and sample.
	gamma := 0.0
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			d := f[i][j] - s[i][j]
			gamma += d * d
		}
	}
	if gamma <= 0 {
		return sample, 0 // sample already equals the target
	}

	delta = (piSum - rho) / gamma / ft
	if delta < 0 {
		delta = 0
	}
	if delta > 1 {
		delta = 1
	}

	// Combine on the 1/T scale, then rescale to the unbiased (T-1) scale so the
	// diagonal matches SampleCovariance exactly.
	scale := ft / float64(T-1)
	out := make([][]float64, n)
	for i := range out {
		out[i] = make([]float64, n)
		for j := range out[i] {
			out[i][j] = (delta*f[i][j] + (1-delta)*s[i][j]) * scale
		}
	}
	return out, delta
}

// ShrinkMeans applies Jorion (1986) Bayes-Stein shrinkage to the sample mean
// vector, pulling every asset's mean toward the return of the global
// minimum-variance portfolio. It returns the shrunk views and the intensity w
// in [0,1] that was applied (1 = no view at all: every asset gets the same
// expected return).
//
//	mu_g = (1' S^-1 mu) / (1' S^-1 1)                 -- the shrinkage target
//	w    = (N+2) / ( (N+2) + T * (mu-mu_g)' S^-1 (mu-mu_g) )
//	mu_BS= (1-w)*mu + w*mu_g
//
// w -> 1 when the cross-sectional dispersion of the sample means is small
// relative to what sampling noise alone would produce, which over a few hundred
// daily observations is nearly always. That is not a defect of the estimator;
// it is the honest reading of the data.
//
// It shrinks FULLY (w = 1) whenever the covariance cannot be inverted, T is not
// large enough relative to N for the quadratic form to be meaningful, or fewer
// than four assets are supplied — in each case the dispersion cannot be
// distinguished from noise, and no view is the answer that does not fabricate
// one.
func ShrinkMeans(mu []float64, cov [][]float64, T int) (shrunk []float64, w float64) {
	n := len(mu)
	if n == 0 {
		return nil, 1
	}
	flat := func() ([]float64, float64) {
		g := 0.0
		for _, m := range mu {
			g += m
		}
		g /= float64(n)
		out := make([]float64, n)
		for i := range out {
			out[i] = g
		}
		return out, 1
	}
	if n < 4 || T <= n+2 || !isSquare(cov, n) {
		return flat()
	}
	inv, ok := invert(cov)
	if !ok {
		return flat()
	}

	ones := make([]float64, n)
	for i := range ones {
		ones[i] = 1
	}
	invOnes := matVec(inv, ones)
	den := dot(ones, invOnes)
	if math.Abs(den) < 1e-15 {
		return flat()
	}
	muG := dot(ones, matVec(inv, mu)) / den

	d := make([]float64, n)
	for i := range d {
		d[i] = mu[i] - muG
	}
	q := dot(d, matVec(inv, d))
	if !(q > 0) || math.IsInf(q, 0) {
		return flat()
	}

	nPlus2 := float64(n + 2)
	w = nPlus2 / (nPlus2 + float64(T)*q)
	if w < 0 {
		w = 0
	}
	if w > 1 {
		w = 1
	}
	shrunk = make([]float64, n)
	for i := range shrunk {
		shrunk[i] = (1-w)*mu[i] + w*muG
	}
	return shrunk, w
}

// commonLength is the minimum series length across returns (0 for empty), so
// unequal input can never index out of range.
func commonLength(returns [][]float64) int {
	if len(returns) == 0 {
		return 0
	}
	t := len(returns[0])
	for _, r := range returns {
		if len(r) < t {
			t = len(r)
		}
	}
	return t
}

// meansOf returns each series' mean over its leading t observations.
func meansOf(returns [][]float64, t int) []float64 {
	means := make([]float64, len(returns))
	if t <= 0 {
		return means
	}
	for i := range returns {
		s := 0.0
		for k := 0; k < t; k++ {
			s += returns[i][k]
		}
		means[i] = s / float64(t)
	}
	return means
}
