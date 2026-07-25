package options

import (
	"math"
	"testing"
)

// Round trip: price at a known vol, invert, get the vol back. Run across the
// moneyness range where Newton's method would stall on a vanishing vega — this
// package uses bisection precisely so those cases still resolve.
func TestImpliedVolRoundTrip(t *testing.T) {
	base := Inputs{Spot: 100, T: 0.5, Rate: 0.04, DivYield: 0.01}
	for _, k := range []float64{50, 80, 100, 120, 200} {
		for _, vol := range []float64{0.05, 0.2, 0.65, 1.5} {
			for _, isCall := range []bool{true, false} {
				in := base
				in.Strike, in.Vol = k, vol
				priced := Price(in, isCall)
				if priced.Price <= 0 {
					continue // worthless quote: nothing to invert, covered below
				}
				got, ok := ImpliedVol(priced.Price, in, isCall)
				if !ok {
					// Legitimate only where the quote genuinely does not pin a
					// vol down (vega ~ 0); anywhere else a refusal is a bug.
					if priced.Vega > vegaFloorRel*in.Spot {
						t.Fatalf("K=%v vol=%v call=%v: refused a price with real vega %g", k, vol, isCall, priced.Vega)
					}
					continue
				}
				if math.Abs(got-vol) > 1e-4 {
					t.Fatalf("K=%v call=%v: implied %.6f, want %.6f", k, isCall, got, vol)
				}
			}
		}
	}
}

// A quote below intrinsic (or above the underlying) is unpriceable — the honest
// answer is a refusal, not a clamped vol that looks like a real number.
func TestImpliedVolRefusesUnpriceableQuotes(t *testing.T) {
	in := Inputs{Spot: 100, Strike: 80, T: 1, Rate: 0.05}
	if _, ok := ImpliedVol(5, in, true); ok {
		t.Fatal("accepted a call quote below intrinsic")
	}
	if _, ok := ImpliedVol(500, in, true); ok {
		t.Fatal("accepted a call quote above any in-range vol")
	}
	if _, ok := ImpliedVol(-1, in, true); ok {
		t.Fatal("accepted a negative price")
	}
	if _, ok := ImpliedVol(5, Inputs{Spot: 100, Strike: 100, T: 0}, true); ok {
		t.Fatal("accepted an expired contract")
	}
}

// A quote so deep in the money that its value is insensitive to volatility does
// not identify an implied vol — every vol in a wide range reproduces the same
// price. Refusing is the honest answer; returning whichever vol the search
// landed on would look like a measurement.
func TestImpliedVolRefusesNonIdentifiableQuote(t *testing.T) {
	in := Inputs{Spot: 100, Strike: 20, T: 0.25, Rate: 0.04, Vol: 0.05}
	p := Call(in).Price
	if _, ok := ImpliedVol(p, in, true); ok {
		t.Fatalf("returned an implied vol for a quote with vega %g", Call(in).Vega)
	}
}

func TestImpliedVolStraddleRoundTrip(t *testing.T) {
	in := Inputs{Spot: 250, Strike: 250, T: 0.1, Rate: 0.045, DivYield: 0.005, Vol: 0.42}
	p := PriceStraddle(in).Price
	got, ok := ImpliedVolStraddle(p, in)
	if !ok {
		t.Fatal("straddle inversion refused its own price")
	}
	if math.Abs(got-0.42) > 1e-4 {
		t.Fatalf("straddle implied %.6f, want 0.42", got)
	}
	if _, ok := ImpliedVolStraddle(0.0001, in); ok {
		t.Fatal("accepted a straddle quote below the zero-vol floor")
	}
}
