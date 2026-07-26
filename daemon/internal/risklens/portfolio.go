package risklens

import "math"

// normalizeWeights returns a copy of holdings with weights rescaled to sum to
// 1. Negative weights (shorts) are permitted and preserved; normalization uses
// the sum of raw weights. If that sum is zero it returns ErrZeroWeight.
func normalizeWeights(holdings []Holding) ([]Holding, error) {
	if len(holdings) == 0 {
		return nil, ErrNoHoldings
	}
	sum := 0.0
	for _, h := range holdings {
		sum += h.Weight
	}
	if sum == 0 {
		return nil, ErrZeroWeight
	}
	out := make([]Holding, len(holdings))
	for i, h := range holdings {
		out[i] = Holding{Symbol: h.Symbol, Weight: h.Weight / sum}
	}
	return out, nil
}

// alignSeries validates the series set and returns, for the given holdings, the
// per-holding return matrix (one []float64 of returns per holding, in holdings
// order) alongside the normalized holdings. It enforces: at least one holding
// and series, each holding has a matching series, every series shares the same
// length, that length is >= MinCloses, and that every close is a finite
// positive price.
//
// The CLOSE-VALUE check is load-bearing, not hygiene. DailyReturns returns nil
// for a series containing a zero close (the division is undefined), so a book
// whose second holding carried one produced rets = [[n returns], nil] and every
// caller below sized its loop from rets[0] and then indexed rets[i][t] — an
// index-out-of-range panic on the risk endpoint. Rejecting the input here,
// with a named error, is the fix; a defensive length check at each call site
// would only convert the panic into a portfolio silently missing a leg.
func alignSeries(holdings []Holding, series []Series) (norm []Holding, rets [][]float64, err error) {
	if len(holdings) == 0 {
		return nil, nil, ErrNoHoldings
	}
	if len(series) == 0 {
		return nil, nil, ErrNoSeries
	}

	byName := make(map[string]Series, len(series))
	length := -1
	for _, s := range series {
		if length == -1 {
			length = len(s.Closes)
		} else if len(s.Closes) != length {
			return nil, nil, ErrUnequalLength
		}
		byName[s.Symbol] = s
	}
	if length < MinCloses {
		return nil, nil, ErrShortSeries
	}

	norm, err = normalizeWeights(holdings)
	if err != nil {
		return nil, nil, err
	}

	rets = make([][]float64, len(norm))
	for i, h := range norm {
		s, ok := byName[h.Symbol]
		if !ok {
			return nil, nil, ErrMissingSeries
		}
		for _, c := range s.Closes {
			// A zero close makes the return undefined; a negative or non-finite
			// one is not a price at all, and DailyReturns would happily turn it
			// into a finite, entirely fictional return.
			if !(c > 0) || math.IsInf(c, 0) {
				return nil, nil, ErrNonPositiveClose
			}
		}
		r := DailyReturns(s.Closes)
		if len(r) != length-1 {
			// Unreachable given the checks above; kept so a future change to
			// DailyReturns cannot silently reintroduce a ragged return matrix.
			return nil, nil, ErrNonPositiveClose
		}
		rets[i] = r
	}
	return norm, rets, nil
}

// PortfolioReturns computes the portfolio's weighted daily return series from
// holdings and aligned price series. Weights are normalized to sum to 1, then
// each day's portfolio return is the weighted sum of holding returns.
//
// The result has (seriesLength-1) elements. No lookahead: day t's portfolio
// return uses only day t's holding returns. Assumes constant weights across the
// window (no rebalancing drift modeled) — an honest simplification.
func PortfolioReturns(holdings []Holding, series []Series) ([]float64, error) {
	norm, rets, err := alignSeries(holdings, series)
	if err != nil {
		return nil, err
	}
	if len(rets) == 0 || len(rets[0]) == 0 {
		return nil, ErrShortSeries
	}
	m := len(rets[0]) // number of return days
	out := make([]float64, m)
	for i, h := range norm {
		w := h.Weight
		for t := 0; t < m; t++ {
			out[t] += w * rets[i][t]
		}
	}
	return out, nil
}

// RiskContributions decomposes total portfolio variance into each holding's
// share. For holding i the (percent) risk contribution is
//
//	RC_i = w_i * (Cov(r_i, r_portfolio)) / Var(r_portfolio)
//
// i.e. weight times the covariance of the holding with the portfolio, over
// portfolio variance. These marginal contributions sum exactly to 1 by
// construction (sum_i w_i*Cov(r_i, r_p) = Cov(r_p, r_p) = Var(r_p)); we scale
// to percent so PctOfRisk sums to ~100. Each Contribution also carries the
// holding's normalized weight and its own daily-return volatility.
//
// No lookahead — every covariance is computed over the full supplied window.
// If portfolio variance is zero (e.g. all-flat prices) risk is split by weight
// as a graceful fallback.
func RiskContributions(holdings []Holding, series []Series) ([]Contribution, error) {
	norm, rets, err := alignSeries(holdings, series)
	if err != nil {
		return nil, err
	}

	// Portfolio return series and its variance.
	m := len(rets[0])
	port := make([]float64, m)
	for i, h := range norm {
		for t := 0; t < m; t++ {
			port[t] += h.Weight * rets[i][t]
		}
	}
	_, portStd := meanStd(port)
	portVar := portStd * portStd

	out := make([]Contribution, len(norm))
	if portVar == 0 {
		// Degenerate: no variance to attribute. Fall back to weight share so
		// the percentages still sum to 100 and stay interpretable.
		for i, h := range norm {
			_, vol := meanStd(rets[i])
			out[i] = Contribution{
				Symbol:    h.Symbol,
				PctOfRisk: math.Abs(h.Weight) * 100, // will renormalize below
				Weight:    h.Weight,
				Vol:       vol,
			}
		}
		renormPct(out)
		return out, nil
	}

	for i, h := range norm {
		cov := covariance(rets[i], port)
		rc := h.Weight * cov / portVar // marginal contribution, sums to 1
		_, vol := meanStd(rets[i])
		out[i] = Contribution{
			Symbol:    h.Symbol,
			PctOfRisk: rc * 100,
			Weight:    h.Weight,
			Vol:       vol,
		}
	}
	return out, nil
}

// renormPct rescales the PctOfRisk field so the (absolute) magnitudes sum to
// 100. Used only in the degenerate zero-variance fallback.
func renormPct(cs []Contribution) {
	sum := 0.0
	for _, c := range cs {
		sum += math.Abs(c.PctOfRisk)
	}
	if sum == 0 {
		return
	}
	for i := range cs {
		cs[i].PctOfRisk = cs[i].PctOfRisk / sum * 100
	}
}

// covariance returns the sample (n-1) covariance of two equal-length series.
// Series of differing length are truncated to the shorter; length < 2 yields 0.
func covariance(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 2 {
		return 0
	}
	ma, mb := 0.0, 0.0
	for i := 0; i < n; i++ {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(n)
	mb /= float64(n)
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += (a[i] - ma) * (b[i] - mb)
	}
	return sum / float64(n-1)
}
