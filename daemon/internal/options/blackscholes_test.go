package options

import (
	"math"
	"testing"
)

func approx(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.6f, want %.6f (tol %g)", what, got, want, tol)
	}
}

// The textbook case: S=K=100, T=1y, r=5%, no dividend, vol=20%. These values are
// the standard Black-Scholes reference, so a drift in the formula shows up here
// before it reaches a user's screen.
func TestBlackScholesReferenceValues(t *testing.T) {
	in := Inputs{Spot: 100, Strike: 100, T: 1, Rate: 0.05, Vol: 0.20}
	c, p := Call(in), Put(in)
	approx(t, c.Price, 10.450584, 1e-5, "call")
	approx(t, p.Price, 5.573526, 1e-5, "put")
	approx(t, c.Delta, 0.636831, 1e-5, "call delta")
	approx(t, p.Delta, -0.363169, 1e-5, "put delta")
	approx(t, c.Gamma, 0.018762, 1e-5, "gamma")
	approx(t, c.Vega, 0.375240, 1e-5, "vega per point")
	if c.Theta >= 0 || p.Theta >= 0 {
		t.Fatalf("long premium should decay: call theta %v, put theta %v", c.Theta, p.Theta)
	}
}

// Put-call parity is the identity the whole model must satisfy:
// C - P = S*e^{-qT} - K*e^{-rT}. It catches sign and discounting errors that
// individual price checks can miss.
func TestPutCallParity(t *testing.T) {
	for _, in := range []Inputs{
		{Spot: 100, Strike: 90, T: 0.5, Rate: 0.04, DivYield: 0.02, Vol: 0.3},
		{Spot: 42, Strike: 55, T: 2, Rate: 0.01, DivYield: 0, Vol: 0.8},
		{Spot: 1000, Strike: 1000, T: 0.08, Rate: 0.05, DivYield: 0.03, Vol: 0.15},
	} {
		c, p := Call(in), Put(in)
		want := in.Spot*math.Exp(-in.DivYield*in.T) - in.Strike*math.Exp(-in.Rate*in.T)
		approx(t, c.Price-p.Price, want, 1e-9, "parity")
		// Delta parity: call delta - put delta = e^{-qT}.
		approx(t, c.Delta-p.Delta, math.Exp(-in.DivYield*in.T), 1e-9, "delta parity")
		// Gamma and vega are side-independent.
		approx(t, c.Gamma, p.Gamma, 1e-12, "gamma parity")
		approx(t, c.Vega, p.Vega, 1e-12, "vega parity")
	}
}

// Vega must equal the numerical derivative of price with respect to vol, scaled
// to one vol point — this is the number the whole vol-edge surface leans on.
func TestVegaMatchesNumericalDerivative(t *testing.T) {
	in := Inputs{Spot: 120, Strike: 110, T: 0.75, Rate: 0.03, DivYield: 0.01, Vol: 0.35}
	const h = 1e-6
	up, dn := in, in
	up.Vol += h
	dn.Vol -= h
	num := (Call(up).Price - Call(dn).Price) / (2 * h) / 100
	approx(t, Call(in).Vega, num, 1e-6, "vega vs numeric")
}

// An expired or zero-vol contract has no optionality: value is intrinsic and
// every convexity Greek is exactly zero, not NaN from dividing by sqrt(0).
func TestExpiredAndZeroVolAreFiniteIntrinsic(t *testing.T) {
	expired := Inputs{Spot: 110, Strike: 100, T: 0, Rate: 0.05, Vol: 0.3}
	c := Call(expired)
	approx(t, c.Price, 10, 1e-12, "expired call intrinsic")
	if c.Gamma != 0 || c.Vega != 0 {
		t.Fatalf("expired contract should have zero convexity, got gamma %v vega %v", c.Gamma, c.Vega)
	}
	if math.IsNaN(c.Price) || math.IsNaN(c.Delta) {
		t.Fatal("expired contract produced NaN")
	}
	zeroVol := Inputs{Spot: 100, Strike: 100, T: 1, Rate: 0, Vol: 0}
	if v := Put(zeroVol).Price; v != 0 || math.IsNaN(v) {
		t.Fatalf("zero-vol ATM put should be worth 0, got %v", v)
	}
}

func TestStraddleBreakevens(t *testing.T) {
	in := Inputs{Spot: 100, Strike: 100, T: 0.25, Rate: 0.04, Vol: 0.4}
	st := PriceStraddle(in)
	approx(t, st.Price, st.Call.Price+st.Put.Price, 1e-12, "straddle price")
	approx(t, st.UpperBreakeven-st.LowerBreakeven, 2*st.Price, 1e-12, "breakeven width")
	approx(t, st.BreakevenMovePct, st.Price/in.Spot, 1e-12, "breakeven pct")
	if st.Vega <= 0 || st.Theta >= 0 {
		t.Fatalf("long straddle should be long vega / short theta, got vega %v theta %v", st.Vega, st.Theta)
	}
	if math.Abs(st.NetDelta) > 0.15 {
		t.Fatalf("ATM straddle should be near delta-neutral, got %v", st.NetDelta)
	}
	// Price is strictly increasing in vol — the property the IV inversion needs.
	hi := in
	hi.Vol = 0.5
	if PriceStraddle(hi).Price <= st.Price {
		t.Fatal("straddle price must increase with vol")
	}
}
