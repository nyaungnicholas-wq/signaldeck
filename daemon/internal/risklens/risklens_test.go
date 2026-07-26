package risklens

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func approx(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol
}

// ---- DailyReturns ----------------------------------------------------------

func TestDailyReturns(t *testing.T) {
	tests := []struct {
		name   string
		closes []float64
		want   []float64
	}{
		{"simple", []float64{100, 110, 99}, []float64{0.10, -0.10}},
		{"flat", []float64{50, 50, 50}, []float64{0, 0}},
		{"single", []float64{100}, nil},
		{"empty", nil, nil},
		{"zero_close", []float64{0, 100, 110}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DailyReturns(tc.closes)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if !approx(got[i], tc.want[i], 1e-12) {
					t.Errorf("[%d]=%.6f want %.6f", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// ---- HistoricalVaR: see var_test.go for the gate and the hand-computed case --

// ---- ParametricVaR ---------------------------------------------------------

func TestParametricVaR(t *testing.T) {
	// Returns with mean 0 and known stdev. Use a symmetric set.
	// {-0.02,-0.01,0,0.01,0.02}: mean=0, sample var = (0.0004+0.0001+0+0.0001+0.0004)/4
	//   = 0.001/4 = 0.00025, std = 0.0158113883.
	// z(0.05) ~= -1.6448536; VaR = -(0 + z*std) = 1.6448536*0.0158113883 ~= 0.02600658.
	rets := []float64{-0.02, -0.01, 0, 0.01, 0.02}
	got := ParametricVaR(rets, 0.95)
	if got == nil {
		t.Fatal("ParametricVaR withheld on a 5-return series")
	}
	want := 1.6448536269514722 * 0.015811388300841896
	if !approx(*got, want, 1e-6) {
		t.Errorf("ParametricVaR=%.8f want %.8f", *got, want)
	}
	// Degenerate: nil, never 0 — a 0 would render as "no risk".
	if v := ParametricVaR(nil, 0.95); v != nil {
		t.Errorf("nil series -> %.6f want withheld", *v)
	}
	if v := ParametricVaR([]float64{0.01}, 0.95); v != nil {
		t.Errorf("single return -> %.6f want withheld", *v)
	}
}

func TestNormInvCDF(t *testing.T) {
	tests := []struct {
		p    float64
		want float64
	}{
		{0.5, 0.0},
		{0.975, 1.959964},
		{0.025, -1.959964},
		{0.95, 1.644854},
		{0.05, -1.644854},
		{0.99, 2.326348},
	}
	for _, tc := range tests {
		got := normInvCDF(tc.p)
		if !approx(got, tc.want, 1e-4) {
			t.Errorf("normInvCDF(%.3f)=%.6f want %.6f", tc.p, got, tc.want)
		}
	}
}

// ---- helpers to build test series ------------------------------------------

// closesFromReturns builds a close series starting at base that realizes the
// given daily returns exactly (so DailyReturns round-trips).
func closesFromReturns(base float64, rets []float64) []float64 {
	cs := make([]float64, len(rets)+1)
	cs[0] = base
	for i, r := range rets {
		cs[i+1] = cs[i] * (1 + r)
	}
	return cs
}

// repeatPattern tiles pat until it has at least n elements, returning exactly n.
func repeatPattern(pat []float64, n int) []float64 {
	out := make([]float64, 0, n)
	for len(out) < n {
		out = append(out, pat...)
	}
	return out[:n]
}

// ---- PortfolioReturns ------------------------------------------------------

func TestPortfolioReturns(t *testing.T) {
	// Two assets, 60/40. A returns +0.01 daily, B returns -0.01 daily.
	n := 80
	aRet := repeatPattern([]float64{0.01}, n)
	bRet := repeatPattern([]float64{-0.01}, n)
	holdings := []Holding{{"A", 0.6}, {"B", 0.4}}
	series := []Series{
		{"A", closesFromReturns(100, aRet)},
		{"B", closesFromReturns(100, bRet)},
	}
	got, err := PortfolioReturns(holdings, series)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("len=%d want %d", len(got), n)
	}
	// Each day: 0.6*0.01 + 0.4*(-0.01) = 0.006 - 0.004 = 0.002.
	for i, r := range got {
		if !approx(r, 0.002, 1e-12) {
			t.Fatalf("day %d = %.6f want 0.002", i, r)
		}
	}
}

func TestPortfolioReturns_WeightNormalization(t *testing.T) {
	// Unnormalized weights (sum 3) must normalize to 0.5/0.5 -> not 2/1.
	n := 70
	aRet := repeatPattern([]float64{0.02}, n)
	bRet := repeatPattern([]float64{0.04}, n)
	holdings := []Holding{{"A", 2}, {"B", 2}} // sum 4 -> 0.5/0.5
	series := []Series{
		{"A", closesFromReturns(100, aRet)},
		{"B", closesFromReturns(100, bRet)},
	}
	got, err := PortfolioReturns(holdings, series)
	if err != nil {
		t.Fatal(err)
	}
	// 0.5*0.02 + 0.5*0.04 = 0.03.
	if !approx(got[0], 0.03, 1e-12) {
		t.Errorf("day0=%.6f want 0.03 (weights should be normalized)", got[0])
	}
}

func TestPortfolioReturns_Validation(t *testing.T) {
	good := closesFromReturns(100, repeatPattern([]float64{0.01}, 70))
	short := closesFromReturns(100, repeatPattern([]float64{0.01}, 10)) // 11 closes < 60
	tests := []struct {
		name     string
		holdings []Holding
		series   []Series
		wantErr  error
	}{
		{"no_holdings", nil, []Series{{"A", good}}, ErrNoHoldings},
		{"no_series", []Holding{{"A", 1}}, nil, ErrNoSeries},
		{"short", []Holding{{"A", 1}}, []Series{{"A", short}}, ErrShortSeries},
		{"unequal", []Holding{{"A", 0.5}, {"B", 0.5}},
			[]Series{{"A", good}, {"B", good[:len(good)-1]}}, ErrUnequalLength},
		{"missing", []Holding{{"A", 0.5}, {"Z", 0.5}},
			[]Series{{"A", good}, {"B", good}}, ErrMissingSeries},
		{"zero_weight", []Holding{{"A", 0}}, []Series{{"A", good}}, ErrZeroWeight},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PortfolioReturns(tc.holdings, tc.series)
			if err != tc.wantErr {
				t.Errorf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

// ---- Single-asset portfolio VaR == that asset's VaR ------------------------

func TestSingleAssetPortfolioVaR(t *testing.T) {
	// 240 returns — past MinVaRTailObservations at 95%, so the VaR is publishable
	// and the two sides are actually comparable.
	rets := repeatPattern([]float64{
		-0.05, 0.02, -0.03, 0.01, 0.04, -0.02, 0.03, -0.01, 0.00, -0.06,
	}, 240)
	closes := closesFromReturns(100, rets)
	holdings := []Holding{{"SOLO", 1.0}}
	series := []Series{{"SOLO", closes}}

	// Portfolio returns should equal the asset's own returns (single 100% asset).
	port, err := PortfolioReturns(holdings, series)
	if err != nil {
		t.Fatal(err)
	}
	assetRets := DailyReturns(closes)
	if len(port) != len(assetRets) {
		t.Fatalf("len mismatch %d vs %d", len(port), len(assetRets))
	}
	for i := range port {
		if !approx(port[i], assetRets[i], 1e-12) {
			t.Fatalf("day %d port=%.8f asset=%.8f", i, port[i], assetRets[i])
		}
	}
	// Therefore VaR of the portfolio equals VaR of the asset.
	pv, pc, pg := HistoricalVaR(port, 0.95)
	av, ac, _ := HistoricalVaR(assetRets, 0.95)
	if pv == nil || av == nil {
		t.Fatalf("VaR withheld at n=%d: %s", pg.N, pg.Reason)
	}
	if !approx(*pv, *av, 1e-12) || !approx(*pc, *ac, 1e-12) {
		t.Errorf("portfolio VaR (%.6f,%.6f) != asset VaR (%.6f,%.6f)", *pv, *pc, *av, *ac)
	}
}

// ---- RiskContributions -----------------------------------------------------

func TestRiskContributions_SumTo100(t *testing.T) {
	// Three assets with distinct, non-trivially-correlated return patterns.
	n := 90
	aRet := repeatPattern([]float64{0.02, -0.01, 0.015, -0.02, 0.01}, n)
	bRet := repeatPattern([]float64{-0.01, 0.02, -0.015, 0.01, -0.005}, n)
	cRet := repeatPattern([]float64{0.005, 0.005, -0.02, 0.03, -0.01}, n)
	holdings := []Holding{{"A", 0.5}, {"B", 0.3}, {"C", 0.2}}
	series := []Series{
		{"A", closesFromReturns(100, aRet)},
		{"B", closesFromReturns(100, bRet)},
		{"C", closesFromReturns(100, cRet)},
	}
	contribs, err := RiskContributions(holdings, series)
	if err != nil {
		t.Fatal(err)
	}
	if len(contribs) != 3 {
		t.Fatalf("got %d contributions", len(contribs))
	}
	sum := 0.0
	for _, c := range contribs {
		sum += c.PctOfRisk
		if c.Vol <= 0 {
			t.Errorf("%s vol=%.6f should be > 0", c.Symbol, c.Vol)
		}
	}
	if !approx(sum, 100, 1e-6) {
		t.Errorf("PctOfRisk sum=%.6f want 100", sum)
	}
}

func TestRiskContributions_SingleAssetIs100(t *testing.T) {
	rets := repeatPattern([]float64{0.01, -0.02, 0.03, -0.01}, 70)
	series := []Series{{"A", closesFromReturns(100, rets)}}
	contribs, err := RiskContributions([]Holding{{"A", 1}}, series)
	if err != nil {
		t.Fatal(err)
	}
	if len(contribs) != 1 || !approx(contribs[0].PctOfRisk, 100, 1e-9) {
		t.Errorf("single asset PctOfRisk=%v want 100", contribs)
	}
}

// ---- Diversification: uncorrelated vs correlated ---------------------------

// portVol is a small helper computing the stdev of a portfolio's returns.
func portVol(t *testing.T, holdings []Holding, series []Series) float64 {
	t.Helper()
	port, err := PortfolioReturns(holdings, series)
	if err != nil {
		t.Fatal(err)
	}
	_, std := meanStd(port)
	return std
}

func TestDiversification_UncorrelatedBeatsCorrelated(t *testing.T) {
	n := 120
	// Two return streams with equal volatility.
	// Correlated case: B == A (perfectly correlated) -> portfolio vol == asset vol.
	// Uncorrelated case: B is A's pattern phase-shifted so their product averages
	// to ~0 -> portfolio vol < weighted-average vol.
	aPat := []float64{0.03, -0.03}
	aRet := repeatPattern(aPat, n)

	// Perfectly correlated B: identical to A.
	corrB := repeatPattern(aPat, n)
	// (Near) uncorrelated B: a different, phase-orthogonal pattern with the same
	// magnitude set so |B| distribution matches A but sign co-movement ~cancels.
	uncorrB := repeatPattern([]float64{0.03, 0.03, -0.03, -0.03}, n)

	holdings := []Holding{{"A", 0.5}, {"B", 0.5}}

	aClose := closesFromReturns(100, aRet)
	corrSeries := []Series{{"A", aClose}, {"B", closesFromReturns(100, corrB)}}
	uncorrSeries := []Series{{"A", aClose}, {"B", closesFromReturns(100, uncorrB)}}

	// Weighted-average of the individual vols (both have equal vol here).
	_, volA := meanStd(DailyReturns(aClose))
	_, volUncorrB := meanStd(DailyReturns(closesFromReturns(100, uncorrB)))
	weightedAvgVol := 0.5*volA + 0.5*volUncorrB

	corrVol := portVol(t, holdings, corrSeries)
	uncorrVol := portVol(t, holdings, uncorrSeries)

	// Diversification benefit: uncorrelated portfolio vol strictly below the
	// weighted average of component vols.
	if !(uncorrVol < weightedAvgVol) {
		t.Errorf("uncorrelated portfolio vol %.6f should be < weighted-avg vol %.6f",
			uncorrVol, weightedAvgVol)
	}
	// And uncorrelated should beat (be lower than) the perfectly-correlated case.
	if !(uncorrVol < corrVol) {
		t.Errorf("uncorrelated vol %.6f should be < correlated vol %.6f", uncorrVol, corrVol)
	}
	// Sanity: perfectly-correlated portfolio vol ~= component vol (no benefit).
	if !approx(corrVol, weightedAvgVol, 1e-9) {
		t.Errorf("correlated vol %.6f should ~= weighted-avg vol %.6f (no diversification)",
			corrVol, weightedAvgVol)
	}
}

// ---- StressScenarios -------------------------------------------------------

func TestStressScenarios(t *testing.T) {
	n := 100
	// A and B are built as leveraged versions of the market factor so the book
	// carries a real, positive beta and the market shocks are losses.
	mktRet := repeatPattern([]float64{0.01, -0.02, 0.015, -0.03, 0.02}, n)
	aRet := repeatPattern([]float64{0.012, -0.024, 0.018, -0.036, 0.024}, n)
	bRet := repeatPattern([]float64{0.005, -0.01, 0.008, -0.015, 0.01}, n)
	holdings := []Holding{{"A", 0.6}, {"B", 0.4}}
	series := []Series{
		{"A", closesFromReturns(100, aRet)},
		{"B", closesFromReturns(100, bRet)},
	}
	market := Series{Symbol: "MKT", Closes: closesFromReturns(100, mktRet)}
	scen, err := StressScenarios(holdings, series, market)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := map[string]bool{
		"2008 equity -40%":     false,
		"COVID-2020 -34%":      false,
		"3-sigma down day":     false,
		"worst historical day": false,
	}
	for _, s := range scen {
		if _, ok := wantNames[s.Name]; !ok {
			t.Errorf("unexpected scenario %q", s.Name)
			continue
		}
		wantNames[s.Name] = true
		if s.Detail == "" {
			t.Errorf("%q has empty Detail", s.Name)
		}
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("missing scenario %q", name)
		}
	}

	// The worst-historical-day P&L must equal the worst realized portfolio day.
	port, _ := PortfolioReturns(holdings, series)
	worst := math.Inf(1)
	for _, r := range port {
		if r < worst {
			worst = r
		}
	}
	for _, s := range scen {
		if s.PnLPct == nil {
			t.Errorf("%q withheld; every scenario is computable here (%s)", s.Name, s.Detail)
			continue
		}
		if s.Name == "worst historical day" {
			if !approx(*s.PnLPct, worst, 1e-12) {
				t.Errorf("worst-day PnL=%.6f want %.6f", *s.PnLPct, worst)
			}
		}
		// Market shocks should be losses (negative) for a long, positive-beta book.
		if (s.Name == "2008 equity -40%" || s.Name == "COVID-2020 -34%") && *s.PnLPct >= 0 {
			t.Errorf("%q PnL=%.6f expected negative for long book", s.Name, *s.PnLPct)
		}
	}
}

// ---- Summary contains the key numbers --------------------------------------

func TestSummary_ContainsKeyNumbers(t *testing.T) {
	n := 240 // long enough to clear the VaR tail gate
	aRet := repeatPattern([]float64{0.02, -0.04, 0.01, -0.02, 0.03}, n)
	bRet := repeatPattern([]float64{-0.01, 0.015, -0.02, 0.01, -0.005}, n)
	mktRet := repeatPattern([]float64{0.01, -0.02, 0.005, -0.01, 0.015}, n)
	holdings := []Holding{{"AAA", 0.7}, {"BBB", 0.3}}
	series := []Series{
		{"AAA", closesFromReturns(100, aRet)},
		{"BBB", closesFromReturns(100, bRet)},
	}
	market := Series{Symbol: "MKT", Closes: closesFromReturns(100, mktRet)}
	report, err := Analyze(holdings, series, market, 0.95, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if report.HistVaRPct == nil {
		t.Fatalf("VaR withheld at n=%d: %s", report.VaRGate.N, report.VaRGate.Reason)
	}
	s := Summary(report)

	// Must mention the VaR percent, "95%", "VaR", the notional, and the top driver.
	varStr := fmt.Sprintf("%.2f%%", *report.HistVaRPct*100)
	mustContain := []string{
		"95% VaR",
		varStr,
		"VaR",
		"per $100,000",
	}
	for _, sub := range mustContain {
		if !strings.Contains(s, sub) {
			t.Errorf("summary missing %q\n---\n%s", sub, s)
		}
	}
	// Top driver symbol should appear.
	top, _ := topDriver(report.Contributions)
	if !strings.Contains(s, top.Symbol) {
		t.Errorf("summary missing top driver %q\n---\n%s", top.Symbol, s)
	}
	// Sentence count between 2 and 4 (period-delimited, roughly).
	sentences := strings.Count(s, ". ") + strings.Count(s, ".")
	if sentences < 2 {
		t.Errorf("summary should be 2-4 sentences, got %q", s)
	}
}

func TestSummary_DefaultsNotional(t *testing.T) {
	// Report with zero notional/confidence should default gracefully.
	f := func(v float64) *float64 { return &v }
	r := Report{
		HistVaRPct:  f(0.03),
		HistCVaRPct: f(0.05),
		ParamVaRPct: f(0.028),
		Contributions: []Contribution{
			{Symbol: "X", PctOfRisk: 80, Weight: 0.5, Vol: 0.02},
			{Symbol: "Y", PctOfRisk: 20, Weight: 0.5, Vol: 0.01},
		},
		Scenarios: []Scenario{{Name: "worst historical day", PnLPct: f(-0.07)}},
	}
	s := Summary(r)
	if !strings.Contains(s, "per $100,000") {
		t.Errorf("default notional not applied: %s", s)
	}
	if !strings.Contains(s, "95% VaR") {
		t.Errorf("default confidence not applied: %s", s)
	}
	if !strings.Contains(s, "X") {
		t.Errorf("top driver X missing: %s", s)
	}
}

// TestSummary_WithheldVaRSaysSo: when the VaR is gated the prose must state
// that plainly. Rendering a withheld figure as 0.00% would tell a user the book
// cannot lose money — the exact failure mode the gate exists to prevent.
func TestSummary_WithheldVaRSaysSo(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	r := Report{
		Confidence: 0.95,
		VaRGate: VaRGate{
			Withheld: true,
			Reason:   "a 95% historical VaR needs at least 10 return days in the loss tail; this 59-day window puts only 3 there",
		},
		Contributions: []Contribution{{Symbol: "X", PctOfRisk: 100, Weight: 1, Vol: 0.02}},
		Scenarios: []Scenario{
			{Name: "2008 equity -40%", Withheld: true, Detail: "WITHHELD — no exogenous market factor supplied."},
			{Name: "worst historical day", PnLPct: f(-0.07)},
		},
	}
	s := Summary(r)
	if !strings.Contains(s, "not publishing") || !strings.Contains(s, "loss tail") {
		t.Errorf("withheld VaR not disclosed in prose: %s", s)
	}
	if strings.Contains(s, "0.00%") {
		t.Errorf("withheld VaR rendered as a zero loss: %s", s)
	}
	// The worst-case sentence must come from the computed scenario, not the
	// withheld one.
	if !strings.Contains(s, "worst historical day") {
		t.Errorf("worst computed scenario missing: %s", s)
	}
	if strings.Contains(s, "2008 equity") {
		t.Errorf("withheld scenario used as the worst case: %s", s)
	}
}

// ---- humanMoney ------------------------------------------------------------

func TestHumanMoney(t *testing.T) {
	tests := []struct {
		v    float64
		want string
	}{
		{0, "0.00"},
		{5.5, "5.50"},
		{100, "100"},
		{1000, "1,000"},
		{100000, "100,000"},
		{1234567, "1,234,567"},
		{-3000, "3,000"},
	}
	for _, tc := range tests {
		if got := humanMoney(tc.v); got != tc.want {
			t.Errorf("humanMoney(%.2f)=%q want %q", tc.v, got, tc.want)
		}
	}
}
