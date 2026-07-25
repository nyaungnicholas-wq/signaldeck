package options

import "math"

// ivBounds are the volatility search range: 0.01% to 500% annualized. Anything
// outside is a quote error or a mispriced input, and returning ok=false is the
// honest answer.
const (
	ivLow  = 0.0001
	ivHigh = 5.0
	ivTol  = 1e-8
	// vegaFloorRel is the identifiability gate, as a fraction of spot: a deep
	// in- or out-of-the-money quote is almost insensitive to volatility, so many
	// different vols reproduce the same price to the penny and no implied vol is
	// recoverable. Below this vega the answer is "not identifiable from this
	// quote", which is true, rather than whichever vol the search happened to
	// land on, which would look like a measurement.
	vegaFloorRel = 1e-8
)

// ImpliedVol inverts Black-Scholes for the volatility that reproduces a market
// price. in.Vol is ignored. ok=false when the price violates the no-arbitrage
// bounds or lies outside the searchable vol range — no vol is invented for an
// unpriceable quote.
//
// Bisection, not Newton: price is strictly increasing in vol, so bisection
// always converges, while Newton's vega denominator collapses for deep
// in/out-of-the-money contracts — exactly the quotes most likely to be typed in.
func ImpliedVol(marketPrice float64, in Inputs, isCall bool) (float64, bool) {
	if marketPrice <= 0 || in.Spot <= 0 || in.Strike <= 0 || in.T <= 0 {
		return 0, false
	}
	lo, hi := ivLow, ivHigh
	pLo := priceAt(in, lo, isCall)
	pHi := priceAt(in, hi, isCall)
	// Below the zero-vol floor the quote is below intrinsic (arbitrage or bad
	// input); above the ceiling no vol in range reproduces it.
	if marketPrice < pLo-ivTol || marketPrice > pHi+ivTol {
		return 0, false
	}
	sol := (lo + hi) / 2
	for i := 0; i < 200; i++ {
		sol = (lo + hi) / 2
		p := priceAt(in, sol, isCall)
		if math.Abs(p-marketPrice) < ivTol || hi-lo < ivTol {
			break
		}
		if p < marketPrice {
			lo = sol
		} else {
			hi = sol
		}
	}
	return sol, identifiable(in, sol, isCall)
}

// identifiable reports whether the solved vol is actually pinned down by the
// quote — see vegaFloorRel.
func identifiable(in Inputs, vol float64, isCall bool) bool {
	in.Vol = vol
	return Price(in, isCall).Vega > vegaFloorRel*in.Spot
}

func priceAt(in Inputs, vol float64, isCall bool) float64 {
	in.Vol = vol
	return Price(in, isCall).Price
}

// ImpliedVolStraddle inverts a straddle quote — the single number a vol trader
// usually has, since a straddle is quoted as one price. ok=false on the same
// unpriceable conditions as ImpliedVol.
func ImpliedVolStraddle(marketPrice float64, in Inputs) (float64, bool) {
	if marketPrice <= 0 || in.Spot <= 0 || in.Strike <= 0 || in.T <= 0 {
		return 0, false
	}
	lo, hi := ivLow, ivHigh
	sLo := straddleAt(in, lo)
	sHi := straddleAt(in, hi)
	if marketPrice < sLo-ivTol || marketPrice > sHi+ivTol {
		return 0, false
	}
	sol := (lo + hi) / 2
	for i := 0; i < 200; i++ {
		sol = (lo + hi) / 2
		p := straddleAt(in, sol)
		if math.Abs(p-marketPrice) < ivTol || hi-lo < ivTol {
			break
		}
		if p < marketPrice {
			lo = sol
		} else {
			hi = sol
		}
	}
	q := in
	q.Vol = sol
	return sol, PriceStraddle(q).Vega > vegaFloorRel*in.Spot
}

func straddleAt(in Inputs, vol float64) float64 {
	in.Vol = vol
	return PriceStraddle(in).Price
}
