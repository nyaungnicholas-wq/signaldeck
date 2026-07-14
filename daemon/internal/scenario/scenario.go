// Package scenario is a MACRO SCENARIO SIMULATION engine. Given a symbol's
// historical daily returns and a macro factor's historical daily changes (VIX
// level changes, 10y-yield changes in points, oil % moves, etc.), it estimates
// how a hypothetical SHOCK to that factor would move the symbol.
//
// The method is deliberately simple and transparent: a single-factor ordinary
// least-squares (OLS) regression of the asset's return on the factor's change.
// The slope (Beta) is "how many units of asset return we historically saw per
// one unit of factor change"; the projection is just Beta times the shock. R2
// reports how much of the asset's variance that one factor explained, so the
// caller can see how much to trust the line.
//
// LIMITATIONS (state them, don't hide them):
//
//   - Single factor, linear, in-sample. Real moves are multi-factor and
//     non-linear; a beta fitted on calm tape can misfire in a crash. Treat the
//     projection as order-of-magnitude, not a forecast.
//   - Correlation, not causation. A high R2 does not mean the factor MOVES the
//     symbol — both may be driven by something unmeasured.
//   - Extrapolation risk. A shock far larger than anything in the sample walks
//     off the edge of the data the line was fit on.
//
// HONESTY IS STRUCTURAL: when the paired history is too thin to fit a stable
// slope, or the factor explains none of the asset's variance, Estimate returns
// a result flagged Gated with a plain-English Note and withholds the projected
// move rather than emitting a number it cannot stand behind.
//
// UNITS: assetRet and factorChg are each in their own native units. Beta is
// "asset-return-units per one factor-unit". ExpectedMovePct = Beta * shock is
// therefore in the SAME units as assetRet; it is named Pct on the convention
// that daily returns are supplied in percent (e.g. 1.5 meaning +1.5%), so the
// projection reads directly as a percentage. shock is in the factor's own units
// (e.g. +10 VIX points, +0.25 for a 25bp yield move).
//
// No lookahead and deterministic: every statistic is computed only from the
// supplied slices, with no clock and no randomness.
package scenario

import (
	"fmt"
	"math"
)

// Sensitivity is the OLS regression of asset returns on factor changes.
type Sensitivity struct {
	Beta float64 // slope: asset return per 1 unit of factor change
	R2   float64 // goodness of fit in [0,1] (the square of the correlation)
	N    int     // finite paired observations actually used
}

// Fit computes the sensitivity of assetRet to factorChg by simple OLS.
//
// The two slices must be aligned; Fit pairs them up to the shorter length and
// silently drops any pair in which either value is non-finite (NaN or Inf), so
// N is the count of clean pairs, not len of the inputs.
//
// It is numerically safe: with fewer than two clean pairs, or a factor whose
// variance is zero (it never moved, so no slope is defined), it returns
// Beta 0, R2 0 rather than dividing by zero. R2 is the squared Pearson
// correlation, clamped to [0,1] against tiny floating-point overshoot.
func Fit(assetRet, factorChg []float64) Sensitivity {
	n := len(assetRet)
	if len(factorChg) < n {
		n = len(factorChg)
	}

	// Collect the finite paired observations (x = factor change, y = asset
	// return) over the common length.
	xs := make([]float64, 0, n)
	ys := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		x, y := factorChg[i], assetRet[i]
		if !isFinite(x) || !isFinite(y) {
			continue
		}
		xs = append(xs, x)
		ys = append(ys, y)
	}

	m := len(xs)
	if m < 2 {
		return Sensitivity{Beta: 0, R2: 0, N: m}
	}

	mx := mean(xs)
	my := mean(ys)

	// Sums of squares/cross-products about the means.
	var sxx, syy, sxy float64
	for i := 0; i < m; i++ {
		dx := xs[i] - mx
		dy := ys[i] - my
		sxx += dx * dx
		syy += dy * dy
		sxy += dx * dy
	}

	// Guard: a constant factor has zero variance, so its slope is undefined and
	// it explains nothing.
	if sxx == 0 {
		return Sensitivity{Beta: 0, R2: 0, N: m}
	}

	beta := sxy / sxx

	// R2 for a simple regression is the squared correlation, Sxy^2 / (Sxx*Syy).
	// A constant asset (Syy == 0) means there is nothing to explain: R2 0.
	r2 := 0.0
	if syy > 0 {
		r2 = (sxy * sxy) / (sxx * syy)
	}
	if r2 < 0 {
		r2 = 0
	}
	if r2 > 1 {
		r2 = 1
	}

	return Sensitivity{Beta: beta, R2: r2, N: m}
}

// Impact is the estimated effect of a shock on one symbol.
type Impact struct {
	Symbol          string
	Beta            float64 // fitted sensitivity (asset return per factor unit)
	R2              float64 // goodness of fit in [0,1]
	N               int     // finite paired observations used
	ShockLabel      string  // human string for the shock, e.g. "VIX +10"
	ExpectedMovePct float64 // Beta * shock; 0 when Gated (no projection made)
	Gated           bool    // true when N < minN or the fit carries no signal
	Note            string  // plain-English explanation of the result
}

// Estimate fits the sensitivity of assetRet to factorChg, then projects the
// shock through it.
//
// minN gates thin data (30 is a reasonable floor for a daily-return regression;
// it is raised to 2 internally since OLS needs at least two points). shock is
// expressed in the factor's own units (e.g. +10 VIX points, +0.25 for a 25bp
// yield move). label is a human string naming the shock, echoed as ShockLabel.
//
// The result is Gated (with ExpectedMovePct forced to 0 and a Note saying why)
// when either the clean paired sample is smaller than minN, or the factor
// explains none of the asset's variance (R2 == 0 — a constant factor, a
// constant asset, or exactly-zero correlation). In those cases the fitted Beta
// and R2 are still reported for transparency, but no move is projected. When
// the fit is usable, Note summarises the beta, R2, sample size and projection.
func Estimate(symbol string, assetRet, factorChg []float64, shock float64, label string, minN int) Impact {
	s := Fit(assetRet, factorChg)

	imp := Impact{
		Symbol:          symbol,
		Beta:            s.Beta,
		R2:              s.R2,
		N:               s.N,
		ShockLabel:      label,
		ExpectedMovePct: s.Beta * shock,
	}

	if minN < 2 {
		minN = 2 // a regression is undefined with fewer than two points
	}

	switch {
	case s.N < minN:
		imp.Gated = true
		imp.ExpectedMovePct = 0
		imp.Note = fmt.Sprintf(
			"Too few paired observations (%d < %d) to fit a stable sensitivity; no move projected.",
			s.N, minN)
	case s.R2 == 0:
		imp.Gated = true
		imp.ExpectedMovePct = 0
		imp.Note = fmt.Sprintf(
			"Factor explains none of %s's variance (R2=0), so the fitted beta carries no signal; no move projected.",
			symbol)
	default:
		imp.Note = fmt.Sprintf(
			"Beta %.4f (R2 %.2f, N=%d): %s implies an estimated %+.2f%% move. In-sample, single-factor estimate.",
			s.Beta, s.R2, s.N, label, imp.ExpectedMovePct)
	}

	return imp
}

// StdDev returns the sample (n-1 denominator) standard deviation of xs. It is a
// convenience for callers who want to size a shock in the factor's own volatility
// units (e.g. "a 2-sigma VIX move" = 2 * StdDev(vixChanges)). Fewer than two
// elements yield 0.
func StdDev(xs []float64) float64 {
	n := len(xs)
	if n < 2 {
		return 0
	}
	m := mean(xs)
	var sq float64
	for _, v := range xs {
		d := v - m
		sq += d * d
	}
	return math.Sqrt(sq / float64(n-1))
}

// mean returns the arithmetic mean of xs, or 0 for an empty slice.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range xs {
		s += v
	}
	return s / float64(len(xs))
}

// isFinite reports whether v is a usable real number (neither NaN nor Inf).
func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
