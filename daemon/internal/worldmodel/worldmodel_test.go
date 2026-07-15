package worldmodel

import (
	"strings"
	"testing"
)

// driverByKey finds a classified driver in a graph, failing the test if absent.
func driverByKey(t *testing.T, g Graph, key string) DriverState {
	t.Helper()
	for _, d := range g.Drivers {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("driver %q not present in graph", key)
	return DriverState{}
}

// affectedBySymbol finds a symbol in a propagation result, failing if absent.
func affectedBySymbol(t *testing.T, p Propagation, sym string) Affected {
	t.Helper()
	for _, a := range p.Affected {
		if a.Symbol == sym {
			return a
		}
	}
	t.Fatalf("symbol %q not among affected: %+v", sym, p.Affected)
	return Affected{}
}

// (1) A representative macro map classifies each live-fed driver correctly, and a
// series deliberately left out of the map stays unknown/Known=false.
func TestBuild_ClassifiesStates(t *testing.T) {
	macro := map[string]float64{
		"VIXCLS":       32,    // -> high
		"T10Y2Y":       -0.5,  // -> inverted
		"DGS10":        4.6,   // -> high
		"BAMLH0A0HYM2": 6.2,   // -> wide
		"NFCI":         0.4,   // -> tight
		"UNRATE":       3.6,   // -> low
		"CPIAUCSL":     315.0, // index level -> presence flag only
		"DFF":          5.0,   // no modeled driver; must be ignored, not crash
		// DCOILWTICO intentionally omitted -> oil unknown.
	}
	g := Build(macro)

	cases := []struct {
		key       string
		wantState string
		wantValue float64
	}{
		{"volatility", "high", 32},
		{"yield_curve", "inverted", -0.5},
		{"rates", "high", 4.6},
		{"credit_spreads", "wide", 6.2},
		{"financial_conditions", "tight", 0.4},
		{"unemployment", "low", 3.6},
	}
	for _, c := range cases {
		d := driverByKey(t, g, c.key)
		if !d.Known {
			t.Errorf("%s: Known=false, want true", c.key)
		}
		if d.State != c.wantState {
			t.Errorf("%s: State=%q, want %q", c.key, d.State, c.wantState)
		}
		if d.Value != c.wantValue {
			t.Errorf("%s: Value=%v, want %v", c.key, d.Value, c.wantValue)
		}
	}

	// Omitted series -> unknown, never fabricated.
	oil := driverByKey(t, g, "oil")
	if oil.Known || oil.State != "unknown" || oil.Value != 0 {
		t.Errorf("oil (series omitted) = %+v, want unknown/Known=false/Value=0", oil)
	}
	if oil.Series != "DCOILWTICO" {
		t.Errorf("oil.Series=%q, want DCOILWTICO (the feed is named even when absent)", oil.Series)
	}

	// CPIAUCSL is an index level: presence flag only, with a loud caveat note.
	infl := driverByKey(t, g, "inflation")
	if !infl.Known || infl.State != "elevated" {
		t.Errorf("inflation with CPIAUCSL present = %+v, want Known/elevated presence flag", infl)
	}
	if !strings.Contains(infl.Note, "not a YoY") {
		t.Errorf("inflation.Note missing index-vs-rate caveat: %q", infl.Note)
	}
}

// (2) Structural drivers (no live series) are always unknown, and a strong-dollar
// world isn't invented from an unrelated series.
func TestBuild_StructuralDriversUnknown(t *testing.T) {
	g := Build(map[string]float64{"VIXCLS": 18})
	for _, key := range []string{"geopolitical_risk", "growth", "usd", "consumer", "housing"} {
		d := driverByKey(t, g, key)
		if d.Known || d.State != "unknown" || d.Series != "" {
			t.Errorf("structural driver %s = %+v, want unknown/Known=false/Series=\"\"", key, d)
		}
	}
}

// (3) An empty (and nil) macro map never panics and leaves every live-fed driver
// Known=false.
func TestBuild_EmptyMapNoPanicAllUnknown(t *testing.T) {
	for _, macro := range []map[string]float64{{}, nil} {
		g := Build(macro)
		if len(g.Drivers) == 0 {
			t.Fatal("Build produced no drivers")
		}
		for _, d := range g.Drivers {
			if d.Known {
				t.Errorf("driver %s Known=true on empty map, want false", d.Key)
			}
			if d.State != "unknown" {
				t.Errorf("driver %s State=%q on empty map, want unknown", d.Key, d.State)
			}
		}
	}
}

// (4) The required 12 drivers are all present as kind="driver" nodes.
func TestBuild_RequiredDriversPresent(t *testing.T) {
	g := Build(nil)
	kind := map[string]string{}
	for _, n := range g.Nodes {
		kind[n.ID] = n.Kind
	}
	required := []string{
		"geopolitical_risk", "oil", "inflation", "growth", "rates", "yield_curve",
		"credit_spreads", "financial_conditions", "volatility", "usd", "unemployment", "consumer",
	}
	for _, id := range required {
		if kind[id] != KindDriver {
			t.Errorf("driver %q: node kind=%q, want %q", id, kind[id], KindDriver)
		}
	}
}

// (5) No dangling edges: every edge endpoint exists as a node, and every
// classified driver is also a kind="driver" node.
func TestBuild_NoDanglingEdges(t *testing.T) {
	g := Build(nil)
	isNode := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		isNode[n.ID] = true
	}
	for _, e := range g.Edges {
		if !isNode[e.From] {
			t.Errorf("edge From=%q has no node", e.From)
		}
		if !isNode[e.To] {
			t.Errorf("edge To=%q has no node", e.To)
		}
		if e.Sign != 1 && e.Sign != -1 {
			t.Errorf("edge %s->%s has non-directional Sign=%d", e.From, e.To, e.Sign)
		}
	}
	driverNode := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Kind == KindDriver {
			driverNode[n.ID] = true
		}
	}
	for _, d := range g.Drivers {
		if !driverNode[d.Key] {
			t.Errorf("classified driver %q is not a kind=driver node", d.Key)
		}
	}
}

// (6) The flagship chain: oil_up flows oil→inflation→rates, lifts financials
// symbols, and — through the INVERSE rates→housing edge — pushes homebuilders
// down. This is the sign-multiplication check: same shock, opposite effects,
// decided by an inverse edge on one path.
func TestPropagate_OilUp(t *testing.T) {
	p, ok := Propagate("oil_up")
	if !ok {
		t.Fatal("Propagate(oil_up) ok=false, want true")
	}

	// Financials symbols go up, via oil→inflation→rates→financials (+ throughout).
	for _, sym := range []string{"XLF", "JPM", "BAC"} {
		a := affectedBySymbol(t, p, sym)
		if a.Effect != "up" {
			t.Errorf("%s effect=%q, want up", sym, a.Effect)
		}
		if a.Market != "stocks" {
			t.Errorf("%s market=%q, want stocks", sym, a.Market)
		}
	}

	// The financials path must contain the flagship chain oil→inflation→rates.
	xlf := affectedBySymbol(t, p, "XLF")
	if !strings.Contains(xlf.Path, "oil→inflation→rates") {
		t.Errorf("XLF path=%q, want it to contain oil→inflation→rates", xlf.Path)
	}

	// Homebuilder symbols go DOWN even though the shock is oil UP: the inverse
	// rates→housing edge flips the direction on that branch.
	for _, sym := range []string{"XHB", "DHI", "LEN"} {
		a := affectedBySymbol(t, p, sym)
		if a.Effect != "down" {
			t.Errorf("%s effect=%q, want down (flipped by rates→housing inverse edge)", sym, a.Effect)
		}
		if !strings.Contains(a.Path, "housing") {
			t.Errorf("%s path=%q, want it to route through housing", sym, a.Path)
		}
	}

	// The chain of driver/sector effects records the up-move through rates.
	var sawRatesUp bool
	for _, s := range p.Chain {
		if s.Node == "rates" && s.Effect == "up" {
			sawRatesUp = true
		}
	}
	if !sawRatesUp {
		t.Errorf("chain missing rates=up step: %+v", p.Chain)
	}
}

// (7) growth_down hits cyclicals and energy to the downside.
func TestPropagate_GrowthDown(t *testing.T) {
	p, ok := Propagate("growth_down")
	if !ok {
		t.Fatal("Propagate(growth_down) ok=false, want true")
	}
	// Energy (XLE) and industrials (XLI) are cyclicals: down when growth falls.
	for _, sym := range []string{"XLE", "XLI", "XLB"} {
		a := affectedBySymbol(t, p, sym)
		if a.Effect != "down" {
			t.Errorf("%s effect=%q on growth_down, want down", sym, a.Effect)
		}
	}
}

// (8) An unknown shock key returns ok=false and a zero result.
func TestPropagate_UnknownShock(t *testing.T) {
	p, ok := Propagate("nope_not_a_shock")
	if ok {
		t.Errorf("Propagate(unknown) ok=true, want false")
	}
	if len(p.Chain) != 0 || len(p.Affected) != 0 {
		t.Errorf("Propagate(unknown) returned non-empty result: %+v", p)
	}
}

// (9) Shocks lists the supported keys, sorted and complete.
func TestShocks(t *testing.T) {
	got := Shocks()
	want := []string{"credit_stress", "growth_down", "inflation_up", "oil_up", "rates_up", "vol_up"}
	if len(got) != len(want) {
		t.Fatalf("Shocks()=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Shocks()=%v, want %v", got, want)
		}
	}
	// Every listed shock must actually propagate.
	for _, s := range got {
		if _, ok := Propagate(s); !ok {
			t.Errorf("listed shock %q does not propagate", s)
		}
	}
}
