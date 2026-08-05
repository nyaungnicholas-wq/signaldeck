package riskgate

import (
	"math"
	"strings"
	"testing"
)

// healthyBook is a book in good standing with plenty of cash and no exposure.
func healthyBook() Book {
	return Book{
		Equity:           100_000,
		Cash:             100_000,
		CurrentDrawdown:  0,
		DrawdownKnown:    true,
		DailyPnLFrac:     0,
		DailyKnown:       true,
		OpenPositions:    0,
		ExposureBySector: map[string]float64{},
	}
}

// goodEdge is a measured, positive, well-sampled edge.
func goodEdge() Edge {
	return Edge{WinRate: 0.6, PayoffRatio: 2.0, Trips: 50, Valid: true}
}

func hasBreach(d Decision, want string) bool {
	for _, b := range d.Breaches {
		if b == want {
			return true
		}
	}
	return false
}

func reasonsJoined(d Decision) string { return strings.Join(d.Reasons, " | ") }

// THE most important rule in the package: a de-risking trade is never blocked,
// no matter how bad the book's condition is.
func TestExitIsNeverGated(t *testing.T) {
	ruined := Book{
		Equity:          1, // effectively wiped out
		Cash:            0,
		CurrentDrawdown: 0.99, DrawdownKnown: true,
		DailyPnLFrac: -0.90, DailyKnown: true,
		OpenPositions: 999,
	}
	d := Evaluate(ruined, Request{Symbol: "AAPL", Action: Exit}, Edge{}, Defaults())
	if !d.Allow {
		t.Fatalf("an exit must ALWAYS be allowed; got refusal: %s", reasonsJoined(d))
	}
	if len(d.Breaches) != 0 {
		t.Errorf("an exit should breach nothing, got %v", d.Breaches)
	}
}

// The drawdown circuit breaker halts new entries at the limit and stays out of
// the way below it.
func TestDrawdownBreakerHaltsEntriesAtLimit(t *testing.T) {
	lim := Defaults()
	b := healthyBook()

	b.CurrentDrawdown = lim.MaxDrawdown - 0.01
	if d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), lim); !d.Allow {
		t.Errorf("just under the limit should still trade: %s", reasonsJoined(d))
	}

	b.CurrentDrawdown = lim.MaxDrawdown
	d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), lim)
	if d.Allow {
		t.Error("at the drawdown limit new entries must be refused")
	}
	if !hasBreach(d, "max-drawdown") {
		t.Errorf("want a max-drawdown breach, got %v", d.Breaches)
	}
	if d.Notional != 0 {
		t.Errorf("a refusal must carry no size, got %v", d.Notional)
	}
}

// A recovered book is not a halted book — the breaker reads CURRENT drawdown.
func TestRecoveredBookIsNotHalted(t *testing.T) {
	b := healthyBook()
	b.CurrentDrawdown = 0 // fell hard once, fully recovered
	if d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), Defaults()); !d.Allow {
		t.Errorf("a recovered book must be allowed to trade: %s", reasonsJoined(d))
	}
}

func TestDailyLossLimitClosesTheSession(t *testing.T) {
	lim := Defaults()
	b := healthyBook()
	b.DailyPnLFrac = -lim.MaxDailyLoss

	d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), lim)
	if d.Allow {
		t.Error("at the daily loss limit new entries must be refused")
	}
	if !hasBreach(d, "max-daily-loss") {
		t.Errorf("want a max-daily-loss breach, got %v", d.Breaches)
	}

	// A profitable day is not a loss, however large.
	b.DailyPnLFrac = +0.50
	if d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), lim); !d.Allow {
		t.Errorf("a big UP day must not trip the loss limit: %s", reasonsJoined(d))
	}
}

// An unarmed breaker allows the trade but SAYS it is unarmed — a young book must
// not read as a clean pass.
func TestUnmeasurableBreakersSaySoRatherThanPassSilently(t *testing.T) {
	b := healthyBook()
	b.DrawdownKnown = false
	b.DailyKnown = false
	d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), Defaults())
	if !d.Allow {
		t.Fatalf("a book with no history yet should still be able to open: %s", reasonsJoined(d))
	}
	r := reasonsJoined(d)
	if !strings.Contains(r, "unarmed") {
		t.Errorf("want the decision to disclose the unarmed breakers, got: %s", r)
	}
}

func TestPositionCountCap(t *testing.T) {
	lim := Defaults()
	b := healthyBook()
	b.OpenPositions = lim.MaxPositions
	d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), lim)
	if d.Allow {
		t.Error("at the position cap a new NAME must be refused")
	}
	if !hasBreach(d, "max-positions") {
		t.Errorf("want a max-positions breach, got %v", d.Breaches)
	}
}

// The core veto: a measured negative edge is refused outright, not sized down.
func TestNegativeEdgeIsRefusedNotShrunk(t *testing.T) {
	// 40% win rate at 1:1 payoff -> f* = 0.4 - 0.6/1 = -0.2.
	bad := Edge{WinRate: 0.40, PayoffRatio: 1.0, Trips: 100, Valid: true}
	d := Evaluate(healthyBook(), Request{Symbol: "X", Action: Enter}, bad, Defaults())
	if d.Allow {
		t.Fatalf("a negative-expectancy trade must be refused, got notional %v", d.Notional)
	}
	if !hasBreach(d, "no-edge") {
		t.Errorf("want a no-edge breach, got %v", d.Breaches)
	}
	if !strings.Contains(reasonsJoined(d), "negative edge sized small is still a negative edge") {
		t.Errorf("the refusal should explain itself, got: %s", reasonsJoined(d))
	}
}

// A thin record does not produce an invented Kelly fraction — it falls back to
// the equal slice and labels itself.
func TestThinRecordFallsBackToEqualSliceAndLabelsIt(t *testing.T) {
	lim := Defaults()
	thin := Edge{WinRate: 0.9, PayoffRatio: 5, Trips: lim.MinEdgeTrips - 1, Valid: true}
	d := Evaluate(healthyBook(), Request{Symbol: "X", Action: Enter}, thin, lim)
	if !d.Allow {
		t.Fatalf("a thin record should still trade the equal slice: %s", reasonsJoined(d))
	}
	if d.Sizing != SizingEqualSlice {
		t.Errorf("want %q sizing, got %q", SizingEqualSlice, d.Sizing)
	}
	if !strings.Contains(reasonsJoined(d), "NOT by measured odds") {
		t.Errorf("the fallback must not be mistakable for measured odds: %s", reasonsJoined(d))
	}
	// A flattering thin sample must not size ABOVE the equal slice.
	want := 100_000 / float64(lim.MaxPositions)
	if d.Notional > want+1e-9 {
		t.Errorf("thin-sample size %v exceeded the equal slice %v", d.Notional, want)
	}
}

// An edge the caller could not even measure behaves the same way.
func TestInvalidEdgeUsesEqualSlice(t *testing.T) {
	d := Evaluate(healthyBook(), Request{Symbol: "X", Action: Enter}, Edge{Valid: false}, Defaults())
	if !d.Allow || d.Sizing != SizingEqualSlice {
		t.Errorf("want an equal-slice allow, got allow=%v sizing=%q", d.Allow, d.Sizing)
	}
}

// Fractional Kelly sizes from the record and is then bounded by the
// single-position cap.
func TestFractionalKellySizesAndIsCappedPerPosition(t *testing.T) {
	lim := Defaults()
	// p=0.6, b=2 -> f* = 0.6 - 0.4/2 = 0.4; quarter Kelly -> 0.10 of equity,
	// which is exactly the position cap, so it must not exceed it.
	d := Evaluate(healthyBook(), Request{Symbol: "X", Action: Enter}, goodEdge(), lim)
	if !d.Allow {
		t.Fatalf("want an allow: %s", reasonsJoined(d))
	}
	if d.Sizing != SizingFractionalKelly {
		t.Errorf("want fractional-kelly sizing, got %q", d.Sizing)
	}
	cap := lim.MaxPositionWeight * 100_000
	if d.Notional > cap+1e-9 {
		t.Errorf("notional %v exceeded the per-position cap %v", d.Notional, cap)
	}

	// A very strong edge must still be trimmed to the cap, and say so.
	strong := Edge{WinRate: 0.9, PayoffRatio: 5, Trips: 200, Valid: true}
	d = Evaluate(healthyBook(), Request{Symbol: "X", Action: Enter}, strong, lim)
	if d.Notional > cap+1e-9 {
		t.Errorf("a strong edge must still respect the cap, got %v > %v", d.Notional, cap)
	}
	if !hasBreach(d, "max-position-weight") {
		t.Errorf("want the trim disclosed as a breach, got %v", d.Breaches)
	}
}

// Sector concentration: a full sector is refused, a partly-full one is trimmed
// to its headroom.
func TestSectorConcentrationRefusesFullAndTrimsPartial(t *testing.T) {
	lim := Defaults()

	full := healthyBook()
	full.ExposureBySector = map[string]float64{"Technology": lim.MaxSectorWeight * full.Equity}
	d := Evaluate(full, Request{Symbol: "MSFT", Sector: "Technology", Action: Enter}, goodEdge(), lim)
	if d.Allow {
		t.Error("a sector already at its cap must be refused")
	}
	if !hasBreach(d, "max-sector-weight") {
		t.Errorf("want a max-sector-weight breach, got %v", d.Breaches)
	}

	// Leave $1,000 of headroom under the cap and expect exactly that.
	partial := healthyBook()
	partial.ExposureBySector = map[string]float64{"Technology": lim.MaxSectorWeight*partial.Equity - 1_000}
	d = Evaluate(partial, Request{Symbol: "MSFT", Sector: "Technology", Action: Enter}, goodEdge(), lim)
	if !d.Allow {
		t.Fatalf("headroom remains, so this should trade: %s", reasonsJoined(d))
	}
	if math.Abs(d.Notional-1_000) > 1e-6 {
		t.Errorf("want the trade trimmed to the $1,000 headroom, got %v", d.Notional)
	}

	// A different sector is unaffected by Technology being full.
	d = Evaluate(full, Request{Symbol: "XOM", Sector: "Energy", Action: Enter}, goodEdge(), lim)
	if !d.Allow {
		t.Errorf("a full Technology book must not block Energy: %s", reasonsJoined(d))
	}
}

// An unclassified name is still bounded by the position cap; it just has no
// sector to concentrate.
func TestUnclassifiedSectorStillBoundedByPositionCap(t *testing.T) {
	lim := Defaults()
	d := Evaluate(healthyBook(), Request{Symbol: "???", Sector: "", Action: Enter}, goodEdge(), lim)
	if !d.Allow {
		t.Fatalf("an unclassified name should still be tradable: %s", reasonsJoined(d))
	}
	if d.Notional > lim.MaxPositionWeight*100_000+1e-9 {
		t.Errorf("notional %v exceeded the position cap", d.Notional)
	}
}

// The book can never deploy cash it does not hold.
func TestClampedToCashOnHand(t *testing.T) {
	lim := Defaults()
	b := healthyBook()
	// Above the minimum ticket but well below the Kelly size, so the clamp is
	// what binds.
	b.Cash = 5_000
	d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), lim)
	if !d.Allow {
		t.Fatalf("a reduced cash balance is still tradable: %s", reasonsJoined(d))
	}
	if d.Notional > 5_000+1e-9 {
		t.Errorf("notional %v exceeded cash on hand", d.Notional)
	}

	b.Cash = 0
	d = Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), lim)
	if d.Allow {
		t.Error("with no cash there is nothing to deploy")
	}
}

// A remnant left by the composition of several caps is REFUSED, not filled. This
// is the flaw the pipeline test surfaced: each cap trimmed politely and the last
// candidate was handed four tenths of a cent.
func TestDustIsRefusedNotFilled(t *testing.T) {
	lim := Defaults()
	b := healthyBook()
	b.Cash = 3 // $3 on a $100,000 book
	d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), lim)
	if d.Allow {
		t.Errorf("a $%.2f position must be refused, not filled", d.Notional)
	}
	if !hasBreach(d, "min-ticket") {
		t.Errorf("want a min-ticket breach, got %v", d.Breaches)
	}

	// A nearly-full sector leaves dust too, and that must be refused for the
	// same reason.
	full := healthyBook()
	full.ExposureBySector = map[string]float64{
		"Technology": lim.MaxSectorWeight*full.Equity - 4, // $4 of headroom
	}
	d = Evaluate(full, Request{Symbol: "MSFT", Sector: "Technology", Action: Enter}, goodEdge(), lim)
	if d.Allow {
		t.Errorf("$4 of sector headroom is not a position; got notional %v", d.Notional)
	}
	if !hasBreach(d, "min-ticket") {
		t.Errorf("want a min-ticket breach, got %v", d.Breaches)
	}
}

func TestUnusableEquityIsRefused(t *testing.T) {
	for _, eq := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		b := healthyBook()
		b.Equity = eq
		if d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), Defaults()); d.Allow {
			t.Errorf("equity %v must not be sizable", eq)
		}
	}
}

func TestFullKellyFormulaAndDomain(t *testing.T) {
	// f* = p - (1-p)/b
	cases := []struct{ p, b, want float64 }{
		{0.6, 2.0, 0.4},    // 0.6 - 0.4/2
		{0.5, 1.0, 0.0},    // a coin flip at even odds is no edge at all
		{0.4, 1.0, -0.2},   // negative: the bet loses
		{0.75, 3.0, 0.667}, // 0.75 - 0.25/3
	}
	for _, c := range cases {
		got, ok := FullKelly(c.p, c.b)
		if !ok {
			t.Fatalf("FullKelly(%v,%v) unexpectedly withheld", c.p, c.b)
		}
		if math.Abs(got-c.want) > 1e-3 {
			t.Errorf("FullKelly(%v,%v) = %v, want %v", c.p, c.b, got, c.want)
		}
	}
	// Out-of-domain inputs are withheld, never substituted.
	for _, c := range []struct{ p, b float64 }{
		{-0.1, 2}, {1.1, 2}, {0.6, 0}, {0.6, -1},
		{math.NaN(), 2}, {0.6, math.NaN()}, {0.6, math.Inf(1)},
	} {
		if _, ok := FullKelly(c.p, c.b); ok {
			t.Errorf("FullKelly(%v,%v) should be withheld", c.p, c.b)
		}
	}
	// Bounded at the whole book.
	if f, ok := FullKelly(1.0, 10); !ok || f != 1 {
		t.Errorf("a certainty should bound at 1, got %v (ok=%v)", f, ok)
	}
}

// A partial envelope must inherit the documented limits, never "no limit".
func TestWithDefaultsFillsUnsetLimits(t *testing.T) {
	b := healthyBook()
	b.CurrentDrawdown = 0.5 // deep in drawdown
	// Pass a zero-valued Limits: a naive implementation would read MaxDrawdown=0
	// as "halt immediately" or as "no limit". It must behave like Defaults.
	d := Evaluate(b, Request{Symbol: "X", Action: Enter}, goodEdge(), Limits{})
	if d.Allow {
		t.Error("a zero Limits must inherit the default drawdown halt, not disable it")
	}
	if !hasBreach(d, "max-drawdown") {
		t.Errorf("want the default drawdown limit enforced, got %v", d.Breaches)
	}
}

// ── Correlation cap ────────────────────────────────────────────────────────

// A name that moves with the book is more of the bet already held. It is
// refused, not trimmed: a smaller slice of the same exposure is the same
// mistake in a less honest package.
func TestCorrelationCapRefusesRatherThanTrims(t *testing.T) {
	d := Evaluate(healthyBook(), Request{
		Symbol: "AAA", Action: Enter, CorrToBook: 0.85, HasCorr: true,
	}, goodEdge(), Defaults())
	if d.Allow {
		t.Fatalf("0.85 correlation must be refused at the 0.80 cap: %s", reasonsJoined(d))
	}
	if d.Notional != 0 {
		t.Fatalf("a refusal must size nothing, got %.2f", d.Notional)
	}
	if !hasBreach(d, "max-corr-to-book") {
		t.Fatalf("breaches = %v, want max-corr-to-book", d.Breaches)
	}

	// Just under the cap still trades.
	if ok := Evaluate(healthyBook(), Request{
		Symbol: "AAA", Action: Enter, CorrToBook: 0.79, HasCorr: true,
	}, goodEdge(), Defaults()); !ok.Allow {
		t.Fatalf("0.79 correlation is under the cap and must trade: %s", reasonsJoined(ok))
	}
}

// Unmeasured correlation allows, but says the cap is UNARMED rather than
// reading as a clean pass — the same doctrine as the drawdown breaker.
func TestUnmeasuredCorrelationLeavesTheCapUnarmed(t *testing.T) {
	d := Evaluate(healthyBook(), Request{Symbol: "AAA", Action: Enter}, goodEdge(), Defaults())
	if !d.Allow {
		t.Fatalf("unmeasured correlation must not block: %s", reasonsJoined(d))
	}
	if !strings.Contains(reasonsJoined(d), "correlation cap unarmed") {
		t.Fatalf("an unarmed cap must say so, reasons = %s", reasonsJoined(d))
	}
}

// The correlation refusal outranks sizing: it must fire before any notional is
// computed, so no caller can find a number to use on a refused candidate.
func TestCorrelationRefusalCarriesNoSizing(t *testing.T) {
	d := Evaluate(healthyBook(), Request{
		Symbol: "AAA", Action: Enter, CorrToBook: 0.99, HasCorr: true,
	}, goodEdge(), Defaults())
	if d.Sizing != SizingNone {
		t.Fatalf("sizing = %q, want none on a correlation refusal", d.Sizing)
	}
}

// ── Gross exposure ─────────────────────────────────────────────────────────

func TestGrossExposureCapRefusesFullAndTrimsPartial(t *testing.T) {
	// Fully deployed: no unlevered room left.
	full := healthyBook()
	full.GrossExposure, full.GrossKnown = 100_000, true
	d := Evaluate(full, Request{Symbol: "AAA", Action: Enter}, goodEdge(), Defaults())
	if d.Allow {
		t.Fatalf("a fully deployed book must refuse: %s", reasonsJoined(d))
	}
	if !hasBreach(d, "max-gross-exposure") {
		t.Fatalf("breaches = %v, want max-gross-exposure", d.Breaches)
	}

	// Partially deployed: trimmed to the headroom, not refused. $2k of room is
	// still above the $500 minimum ticket.
	part := healthyBook()
	part.GrossExposure, part.GrossKnown = 98_000, true
	p := Evaluate(part, Request{Symbol: "AAA", Action: Enter}, goodEdge(), Defaults())
	if !p.Allow {
		t.Fatalf("$2,000 of headroom must still trade: %s", reasonsJoined(p))
	}
	if p.Notional > 2_000+1e-9 {
		t.Fatalf("notional %.2f exceeds the $2,000 gross headroom", p.Notional)
	}
	if !hasBreach(p, "max-gross-exposure") {
		t.Fatalf("a trim must name the limit that bound it, breaches = %v", p.Breaches)
	}
}

func TestUnmeasuredGrossLeavesTheCapUnarmed(t *testing.T) {
	d := Evaluate(healthyBook(), Request{Symbol: "AAA", Action: Enter}, goodEdge(), Defaults())
	if !strings.Contains(reasonsJoined(d), "gross-exposure cap unarmed") {
		t.Fatalf("an unmeasured gross must leave the cap unarmed and say so: %s", reasonsJoined(d))
	}
}

// Exits stay ungated with both new caps at their worst possible reading.
func TestNewCapsNeverBlockAnExit(t *testing.T) {
	b := healthyBook()
	b.GrossExposure, b.GrossKnown = 10_000_000, true
	d := Evaluate(b, Request{Symbol: "AAA", Action: Exit, CorrToBook: 1.0, HasCorr: true}, Edge{}, Defaults())
	if !d.Allow {
		t.Fatalf("an exit must never be gated, got refusal: %s", reasonsJoined(d))
	}
}

func TestWithDefaultsFillsTheNewLimits(t *testing.T) {
	got := Limits{MaxDrawdown: 0.1}.withDefaults()
	if got.MaxCorrToBook != Defaults().MaxCorrToBook {
		t.Fatalf("MaxCorrToBook = %v, want the default", got.MaxCorrToBook)
	}
	if got.MaxGrossExposure != Defaults().MaxGrossExposure {
		t.Fatalf("MaxGrossExposure = %v, want the default", got.MaxGrossExposure)
	}
}

// ── Admit: risk as a precondition, not a property of a candidate ───────────

// A halted book refuses admission regardless of which candidate asks. This is
// the property that makes risk a GATE: no caller can route around a tripped
// breaker by finding a better trade.
func TestAdmitRefusesAHaltedBookForEveryCandidate(t *testing.T) {
	halted := healthyBook()
	halted.CurrentDrawdown = 0.25 // past the 0.20 breaker

	a := Admit(halted, Defaults())
	if a.Allow {
		t.Fatalf("a book 25%% below peak must not be admitted: %s", reasonsJoined(a))
	}
	if !hasBreach(a, "max-drawdown") {
		t.Fatalf("breaches = %v, want max-drawdown", a.Breaches)
	}

	// And no candidate, however good, gets past Evaluate either.
	for _, sym := range []string{"AAA", "BBB", "CCC"} {
		d := Evaluate(halted, Request{Symbol: sym, Action: Enter, CorrToBook: 0.0, HasCorr: true}, goodEdge(), Defaults())
		if d.Allow {
			t.Fatalf("%s was admitted into a halted book", sym)
		}
	}
}

// Admission is not a size, and must never be mistaken for one.
func TestAdmitCarriesNoSize(t *testing.T) {
	a := Admit(healthyBook(), Defaults())
	if !a.Allow {
		t.Fatalf("a healthy book must be admitted: %s", reasonsJoined(a))
	}
	if a.Notional != 0 || a.Sizing != SizingNone {
		t.Fatalf("admission must carry no size, got notional=%.2f sizing=%q", a.Notional, a.Sizing)
	}
}

// Admit and Evaluate must never disagree about what a halted book is: every
// book-wide breaker refused by Admit is refused by Evaluate with the same
// breach.
func TestAdmitAndEvaluateAgreeOnEveryBookWideBreaker(t *testing.T) {
	cases := map[string]func(Book) Book{
		"max-drawdown":       func(b Book) Book { b.CurrentDrawdown = 0.30; return b },
		"max-daily-loss":     func(b Book) Book { b.DailyPnLFrac = -0.10; return b },
		"max-positions":      func(b Book) Book { b.OpenPositions = 10; return b },
		"max-gross-exposure": func(b Book) Book { b.GrossExposure, b.GrossKnown = b.Equity, true; return b },
		"equity":             func(b Book) Book { b.Equity = 0; return b },
	}
	for breach, mut := range cases {
		b := mut(healthyBook())
		a := Admit(b, Defaults())
		e := Evaluate(b, Request{Symbol: "AAA", Action: Enter}, goodEdge(), Defaults())
		if a.Allow || e.Allow {
			t.Fatalf("%s: admit.Allow=%v evaluate.Allow=%v, want both false", breach, a.Allow, e.Allow)
		}
		if !hasBreach(a, breach) || !hasBreach(e, breach) {
			t.Fatalf("%s: admit=%v evaluate=%v, both must name it", breach, a.Breaches, e.Breaches)
		}
	}
}
