package risklens

import (
	"strings"
	"testing"
)

// TestStressScenarios_DistinctBooksDistinctShock is the C7 regression test.
//
// BEFORE the fix, stress betas were computed against the PORTFOLIO's own return
// series. Sum_i w_i * Cov(r_i, r_p)/Var(r_p) = Cov(r_p, r_p)/Var(r_p) = 1
// identically, so marketPnL(shock) == shock for EVERY book that has ever been
// constructed: a concentrated equity book and an all-Treasury book both
// returned exactly -0.4000000000 and the UI rendered "would move about -40.0%
// (~$40,000)" for both. The number was a constant wearing a computation's
// clothes.
//
// This test fails on that build (both books return the identical -0.40) and
// passes only when the shock is translated through betas to an EXOGENOUS market
// factor, where a high-beta book must lose materially more than a low-beta one.
func TestStressScenarios_DistinctBooksDistinctShock(t *testing.T) {
	n := 240
	// Exogenous market factor: a repeating up/down pattern.
	mktRet := repeatPattern([]float64{0.01, -0.012, 0.008, -0.006, 0.014}, n)
	market := Series{Symbol: "MKT", Closes: closesFromReturns(100, mktRet)}

	// HIGH-BETA book: both names move ~1.5x the market, plus a little noise.
	hiA := scaleReturns(mktRet, 1.6)
	hiB := scaleReturns(mktRet, 1.4)
	hiHoldings := []Holding{{"HIA", 0.5}, {"HIB", 0.5}}
	hiSeries := []Series{
		{"HIA", closesFromReturns(100, hiA)},
		{"HIB", closesFromReturns(100, hiB)},
	}

	// LOW-BETA book ("all-Treasury"): tiny moves essentially uncorrelated with
	// the market factor.
	loA := repeatPattern([]float64{0.0003, -0.0002, 0.0001, 0.0002, -0.0004}, n)
	loB := repeatPattern([]float64{-0.0002, 0.0004, -0.0003, 0.0001, 0.0002}, n)
	loHoldings := []Holding{{"LOA", 0.5}, {"LOB", 0.5}}
	loSeries := []Series{
		{"LOA", closesFromReturns(100, loA)},
		{"LOB", closesFromReturns(100, loB)},
	}

	hi, err := StressScenarios(hiHoldings, hiSeries, market)
	if err != nil {
		t.Fatal(err)
	}
	lo, err := StressScenarios(loHoldings, loSeries, market)
	if err != nil {
		t.Fatal(err)
	}

	hiPnL, ok := scenarioPnL(hi, "2008 equity -40%")
	if !ok {
		t.Fatal("high-beta book: 2008 shock withheld, expected a number")
	}
	loPnL, ok := scenarioPnL(lo, "2008 equity -40%")
	if !ok {
		t.Fatal("low-beta book: 2008 shock withheld, expected a number")
	}

	if approx(hiPnL, loPnL, 1e-9) {
		t.Fatalf("C7 REGRESSION: two structurally different books produced the "+
			"same market shock (%.10f vs %.10f) — beta is being computed against "+
			"the portfolio itself, so the shock is a constant", hiPnL, loPnL)
	}
	// Direction and magnitude must both make sense: a ~1.5-beta book loses far
	// more than a ~0-beta book under a -40% equity shock.
	if hiPnL >= -0.50 || hiPnL <= -0.70 {
		t.Errorf("high-beta book PnL %.4f, expected roughly 1.5x the -40%% shock", hiPnL)
	}
	if loPnL < -0.05 || loPnL > 0.05 {
		t.Errorf("low-beta book PnL %.4f, expected near zero", loPnL)
	}
	// And neither may be the old identity value.
	if approx(hiPnL, crash2008Shock, 1e-9) || approx(loPnL, crash2008Shock, 1e-9) {
		t.Errorf("a book returned the raw shock %.4f — the beta identity is back",
			crash2008Shock)
	}
}

// TestStressScenarios_WithheldWithoutMarketFactor: with no usable exogenous
// factor there is no honest market-shock number. The old code silently
// substituted beta 1.0; the fix must withhold and say why, because a fabricated
// -40% reads to a user as a measurement.
func TestStressScenarios_WithheldWithoutMarketFactor(t *testing.T) {
	n := 100
	aRet := repeatPattern([]float64{0.01, -0.02, 0.015, -0.03, 0.02}, n)
	holdings := []Holding{{"A", 1}}
	series := []Series{{"A", closesFromReturns(100, aRet)}}

	scen, err := StressScenarios(holdings, series, Series{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range scen {
		if s.Name != "2008 equity -40%" && s.Name != "COVID-2020 -34%" {
			continue
		}
		if s.PnLPct != nil {
			t.Errorf("%q published %.4f with no market factor supplied; must be withheld",
				s.Name, *s.PnLPct)
		}
		if !s.Withheld || s.Detail == "" {
			t.Errorf("%q must be marked withheld with a stated reason, got %+v", s.Name, s)
		}
	}
	// The empirical scenarios need no factor and must still be published.
	if _, ok := scenarioPnL(scen, "worst historical day"); !ok {
		t.Error("worst historical day must survive a missing market factor")
	}
}

// TestStressScenarios_MismatchedMarketWindowWithheld: a market series that does
// not cover the holdings' window cannot produce a beta. Withhold rather than
// truncate silently — a beta fitted on a different window is not the beta the
// label claims.
func TestStressScenarios_MismatchedMarketWindowWithheld(t *testing.T) {
	n := 100
	aRet := repeatPattern([]float64{0.01, -0.02, 0.015, -0.03, 0.02}, n)
	mktRet := repeatPattern([]float64{0.01, -0.01}, n-10) // shorter window
	holdings := []Holding{{"A", 1}}
	series := []Series{{"A", closesFromReturns(100, aRet)}}
	market := Series{Symbol: "MKT", Closes: closesFromReturns(100, mktRet)}

	scen, err := StressScenarios(holdings, series, market)
	if err != nil {
		t.Fatal(err)
	}
	if pnl, ok := scenarioPnL(scen, "2008 equity -40%"); ok {
		t.Errorf("published %.4f from a misaligned market window; must be withheld", pnl)
	}
}

// TestStressScenarios_ZeroBetaHoldingIsMeasuredNotAssumed: a flat-price holding
// has a genuine beta of 0 (its covariance with the factor is 0), and that zero
// must dampen the shock. Under the old model the same book got the full -40%.
func TestStressScenarios_ZeroBetaHoldingIsMeasuredNotAssumed(t *testing.T) {
	n := 240
	mktRet := repeatPattern([]float64{0.01, -0.012, 0.008, -0.006, 0.014}, n)
	market := Series{Symbol: "MKT", Closes: closesFromReturns(100, mktRet)}

	holdings := []Holding{{"MOVER", 0.5}, {"FLAT", 0.5}}
	series := []Series{
		{"MOVER", closesFromReturns(100, scaleReturns(mktRet, 2.0))},
		{"FLAT", closesFromReturns(100, repeatPattern([]float64{0}, n))},
	}
	scen, err := StressScenarios(holdings, series, market)
	if err != nil {
		t.Fatal(err)
	}
	pnl, ok := scenarioPnL(scen, "2008 equity -40%")
	if !ok {
		t.Fatal("expected a published shock")
	}
	// 0.5*2.0 + 0.5*0.0 = book beta 1.0 -> -40%: the flat leg really does halve
	// the exposure of the beta-2 leg.
	if !approx(pnl, -0.40, 1e-6) {
		t.Errorf("PnL %.6f, want -0.40 (half a beta-2 name, half a beta-0 name)", pnl)
	}

	betas, err := MarketBetas(holdings, series, market)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"MOVER": 2.0, "FLAT": 0.0}
	for _, b := range betas {
		if b.Beta == nil {
			t.Fatalf("%s beta withheld (%s); both are estimable here", b.Symbol, b.Reason)
		}
		if !approx(*b.Beta, want[b.Symbol], 1e-9) {
			t.Errorf("%s beta=%.6f want %.2f", b.Symbol, *b.Beta, want[b.Symbol])
		}
	}
}

// TestStressScenarios_ImpossibleLongOnlyLossWithheld: a beta-3 long-only book
// under a -40% shock computes to -120%, a loss bigger than the capital in the
// book. The house rule is that a surface gates rather than prints an
// arithmetically impossible return.
func TestStressScenarios_ImpossibleLongOnlyLossWithheld(t *testing.T) {
	n := 240
	mktRet := repeatPattern([]float64{0.01, -0.012, 0.008, -0.006, 0.014}, n)
	market := Series{Symbol: "MKT", Closes: closesFromReturns(100, mktRet)}
	holdings := []Holding{{"LEV3", 1.0}}
	series := []Series{{"LEV3", closesFromReturns(100, scaleReturns(mktRet, 3.0))}}

	scen, err := StressScenarios(holdings, series, market)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range scen {
		if s.Name != "2008 equity -40%" {
			continue
		}
		if s.PnLPct != nil {
			t.Fatalf("published %.4f (a %.0f%% loss on a long-only book)", *s.PnLPct, *s.PnLPct*100)
		}
		if !s.Withheld || !strings.Contains(s.Detail, "larger than the capital") {
			t.Errorf("expected an impossible-loss withholding, got %+v", s)
		}
	}
	// The -34% COVID shock at beta 3 is -102%, also impossible; the milder
	// empirical scenarios must still publish.
	if _, ok := scenarioPnL(scen, "worst historical day"); !ok {
		t.Error("empirical scenario must survive the sanity gate")
	}
}

// TestMarketBetas_NeverDefaultsToOne pins the specific substitution that made
// C7 possible: when the factor is unusable, every beta must come back nil with
// a reason. A silent 1.0 is indistinguishable in the payload from a measured
// 1.0.
func TestMarketBetas_NeverDefaultsToOne(t *testing.T) {
	n := 100
	holdings := []Holding{{"A", 0.7}, {"B", 0.3}}
	series := []Series{
		{"A", closesFromReturns(100, repeatPattern([]float64{0.01, -0.02}, n))},
		{"B", closesFromReturns(100, repeatPattern([]float64{-0.01, 0.03}, n))},
	}
	cases := map[string]Series{
		"absent":       {},
		"flat_factor":  {Symbol: "MKT", Closes: repeatPattern([]float64{100}, n+1)},
		"short_factor": {Symbol: "MKT", Closes: closesFromReturns(100, repeatPattern([]float64{0.01}, n-5))},
	}
	for name, mkt := range cases {
		t.Run(name, func(t *testing.T) {
			betas, err := MarketBetas(holdings, series, mkt)
			if err != nil {
				t.Fatal(err)
			}
			if len(betas) != len(holdings) {
				t.Fatalf("got %d betas want %d", len(betas), len(holdings))
			}
			for _, b := range betas {
				if b.Beta != nil {
					t.Errorf("%s got beta %.4f from an unusable factor", b.Symbol, *b.Beta)
				}
				if b.Reason == "" {
					t.Errorf("%s withheld without a stated reason", b.Symbol)
				}
			}
		})
	}
}

// scaleReturns multiplies every return by k, giving a series with a known beta
// of k to the source series.
func scaleReturns(rets []float64, k float64) []float64 {
	out := make([]float64, len(rets))
	for i, r := range rets {
		out[i] = r * k
	}
	return out
}

// scenarioPnL finds a scenario by name and reports its P&L, or ok=false when it
// is absent or withheld.
func scenarioPnL(ss []Scenario, name string) (float64, bool) {
	for _, s := range ss {
		if s.Name == name {
			if s.PnLPct == nil {
				return 0, false
			}
			return *s.PnLPct, true
		}
	}
	return 0, false
}
