package confluence

import (
	"math"
	"testing"
)

// THE CELUW CASE, END TO END.
//
// CELUW closed at $0.0003 on 2026-07-16 and $0.0019 on 2026-07-17. The
// underlying rose 533%; a short of one unit of unconstrained notional therefore
// returned -533%. Both of those are true and both must remain visible. What must
// NOT happen is that -5.33 entering an average described as account performance.
//
// MUTATION CHECK: delete the MinPrice clause in Evaluate and this test fails
// with Tradable true — a sub-penny warrant re-enters the account book.
func TestEvaluate_CELUWStaysVisibleButIsNotAccountPerformance(t *testing.T) {
	got := Evaluate(Trade{
		Direction: -1, EntryPx: 0.0003, ExitPx: 0.0019,
		HighPx: 0.0019, Short: ShortUnknown, Warrant: true,
	}, DefaultConstraints())

	// The market fact survives intact. Neither leg is clipped or winsorised.
	if math.Abs(got.RawUnderlying-5.3333) > 0.001 {
		t.Fatalf("rawUnderlying = %.4f, want +5.3333. The genuine CELUW move must stay visible", got.RawUnderlying)
	}
	if math.Abs(got.RawTrade-(-5.3333)) > 0.001 {
		t.Fatalf("rawTrade = %.4f, want -5.3333 — the unconstrained short return is a real number and must be published as one", got.RawTrade)
	}
	// But the account never took this position.
	if got.Tradable {
		t.Fatal("a $0.0003 instrument is reported as tradable at size; that is how -533% became an 'expectancy'")
	}
	if got.Constrained != 0 || got.CapitalAtRisk != 0 || got.Weight != 0 {
		t.Fatalf("an untradable trade contributed to the account book: %+v", got)
	}
	if got.Reason == "" {
		t.Fatal("an untradable trade must say why")
	}
}

// A tradable adverse move must be bounded by the RISK BUDGET, not by a cap on
// the reported return. This is the distinction the whole file exists for.
//
// MUTATION CHECK: delete the liquidation branch in Evaluate and this fails with
// a constrained loss of about -1.3% instead of -1%.
func TestEvaluate_RiskBudgetBoundsTheLossNotACap(t *testing.T) {
	c := DefaultConstraints()
	// A $10 name the short doubles against: -100% on unconstrained notional.
	got := Evaluate(Trade{Direction: -1, EntryPx: 10, ExitPx: 20, HighPx: 20, Short: ShortConfirmed}, c)

	if !got.Tradable {
		t.Fatalf("a $10 name must be tradable: %+v", got)
	}
	if math.Abs(got.RawTrade-(-1.0)) > 1e-9 {
		t.Fatalf("rawTrade = %.4f, want -1.0", got.RawTrade)
	}
	if !got.Liquidated {
		t.Fatal("a 100% adverse move must trip the risk budget")
	}
	if !got.GappedThrough {
		t.Fatal("the close is far worse than the stop, so this must be recorded as a gap-through, not a clean stop fill")
	}
	// Gap-through means the account eats the whole move at the position's weight,
	// which is worse than the budget — that is honest, and it is still bounded by
	// the weight rather than unbounded.
	if got.Constrained > -c.RiskBudget {
		t.Fatalf("constrained = %.5f; a gap-through must cost AT LEAST the risk budget (%.5f)", got.Constrained, -c.RiskBudget)
	}
	w := c.PositionWeight / c.ShortCollateral
	if got.Constrained < w*got.RawTrade-0.01 {
		t.Fatalf("constrained = %.5f is worse than weight*rawTrade (%.5f); the account cannot lose more than its position",
			got.Constrained, w*got.RawTrade)
	}
}

// A stop that is REACHED intraday but recovers by the close must still be taken.
// Without the intra-window extremes the model only sees the close and reports a
// position that was in fact liquidated as a winner.
//
// MUTATION CHECK: make adverseExcursion ignore LowPx/HighPx and this fails with
// Liquidated false.
func TestEvaluate_IntradayStopIsTakenEvenWhenTheCloseRecovers(t *testing.T) {
	c := DefaultConstraints()
	// Long at 100, traded down to 40 intraday (-60%), closed back at 101.
	got := Evaluate(Trade{Direction: 1, EntryPx: 100, ExitPx: 101, LowPx: 40, HighPx: 101}, c)
	if !got.Liquidated {
		t.Fatal("a position that traded 60% against itself was reported as never stopped out, because only the close was consulted")
	}
	if got.Constrained >= 0 {
		t.Fatalf("constrained = %.5f; a liquidated position cannot book the close's gain", got.Constrained)
	}
}

// Costs must make an ordinary winner smaller, and a short must additionally pay
// borrow. A book that reports gross moves as net performance is overstating.
func TestEvaluate_CostsAndBorrowReduceTheResult(t *testing.T) {
	c := DefaultConstraints()
	long := Evaluate(Trade{Direction: 1, EntryPx: 100, ExitPx: 110, LowPx: 99, HighPx: 110}, c)
	if !long.Tradable || long.Liquidated {
		t.Fatalf("a clean +10%% long: %+v", long)
	}
	if long.Constrained <= 0 {
		t.Fatalf("constrained = %.5f, want positive", long.Constrained)
	}
	if long.Constrained >= long.RawTrade {
		t.Fatalf("constrained %.5f is not smaller than raw %.5f — sizing and costs vanished", long.Constrained, long.RawTrade)
	}

	short := Evaluate(Trade{Direction: -1, EntryPx: 100, ExitPx: 99, HighPx: 100, Short: ShortConfirmed}, c)
	noBorrow := c
	noBorrow.BorrowRateAnnual = 0
	shortFree := Evaluate(Trade{Direction: -1, EntryPx: 100, ExitPx: 99, HighPx: 100, Short: ShortConfirmed}, noBorrow)
	if short.Constrained >= shortFree.Constrained {
		t.Fatalf("borrow cost had no effect: %.8f vs %.8f", short.Constrained, shortFree.Constrained)
	}
}

// An ineligible short is refused; an UNKNOWN one is not. Refusing unknowns would
// silently drop most of the short book; assuming them borrowable would silently
// assert a fact this system has no file for. The honest handling is to trade it
// and report the unknown count beside the result.
func TestEvaluate_ShortabilityUnknownIsTradedAndIneligibleIsNot(t *testing.T) {
	c := DefaultConstraints()
	unknown := Evaluate(Trade{Direction: -1, EntryPx: 20, ExitPx: 19, HighPx: 20, Short: ShortUnknown}, c)
	if !unknown.Tradable {
		t.Fatal("an unverified borrow must not be refused outright — that drops most of the short record")
	}
	ineligible := Evaluate(Trade{Direction: -1, EntryPx: 20, ExitPx: 19, HighPx: 20, Short: ShortIneligible}, c)
	if ineligible.Tradable {
		t.Fatal("a name evidenced as not borrowable must not be traded")
	}
	if ineligible.RawTrade == 0 {
		t.Fatal("the raw market fact must survive the refusal")
	}
}

// A day that flags hundreds of setups cannot be held at full weight. Both caps
// must bind, and BOTH must say that they bound.
//
// An all-long book is limited by the NET cap, which is tighter than the gross
// one: 200 longs scale to gross 1.0 and then again to 0.5, because a book that
// is 100% long is 100% net long. A balanced book is limited by the gross cap
// instead. Asserting only the gross cap would pass a model that had lost the
// net one entirely, so both shapes are checked.
//
// MUTATION CHECK: remove the gross-cap branch in Aggregate and the balanced case
// fails with gross = 4.0 (200 x 2%), i.e. four times leveraged. Remove the
// net-cap branch and the all-long case fails with net = 1.0.
func TestAggregate_ExposureCapsBindAndAreReported(t *testing.T) {
	c := DefaultConstraints()
	long := Evaluate(Trade{Direction: 1, EntryPx: 100, ExitPx: 101, LowPx: 100, HighPx: 101}, c)
	short := Evaluate(Trade{Direction: -1, EntryPx: 100, ExitPx: 99, HighPx: 100, Short: ShortConfirmed}, c)

	allLong := make([]Result, 200)
	for i := range allLong {
		allLong[i] = long
	}
	acct, gross, net, scaled := Aggregate(allLong, c)
	if !scaled {
		t.Fatal("200 full-weight positions were summed without scaling — that reports leverage the account never had")
	}
	if math.Abs(net-c.MaxNetExposure) > 1e-9 {
		t.Fatalf("all-long net = %.4f, want the net cap %.4f — a 100%% long book is 100%% net long", net, c.MaxNetExposure)
	}
	if gross > c.MaxGrossExposure+1e-9 {
		t.Fatalf("all-long gross = %.4f exceeds the gross cap %.4f", gross, c.MaxGrossExposure)
	}
	if acct <= 0 || acct > 1 {
		t.Fatalf("account return %.6f is not a plausible scaled contribution", acct)
	}

	// Balanced: the gross cap is what binds, and net stays inside its own.
	balanced := make([]Result, 0, 200)
	for i := 0; i < 100; i++ {
		balanced = append(balanced, long, short)
	}
	_, gross, net, scaled = Aggregate(balanced, c)
	if !scaled {
		t.Fatal("a balanced 200-position book was summed without scaling")
	}
	if math.Abs(gross-c.MaxGrossExposure) > 1e-9 {
		t.Fatalf("balanced gross = %.4f, want the gross cap %.4f", gross, c.MaxGrossExposure)
	}
	if math.Abs(net) > c.MaxNetExposure+1e-9 {
		t.Fatalf("balanced net = %.4f exceeds the net cap %.4f", net, c.MaxNetExposure)
	}
}

// Degenerate inputs must produce a refusal, never a number.
func TestEvaluate_RefusesDegenerateInput(t *testing.T) {
	for name, tr := range map[string]Trade{
		"zero entry":     {Direction: 1, EntryPx: 0, ExitPx: 10},
		"zero exit":      {Direction: 1, EntryPx: 10, ExitPx: 0},
		"no direction":   {Direction: 0, EntryPx: 10, ExitPx: 11},
		"negative entry": {Direction: -1, EntryPx: -1, ExitPx: 11},
	} {
		got := Evaluate(tr, DefaultConstraints())
		if got.Tradable || got.RawTrade != 0 || got.Constrained != 0 {
			t.Fatalf("%s: produced a number instead of a refusal: %+v", name, got)
		}
	}
	if acct, g, n, s := Aggregate(nil, DefaultConstraints()); acct != 0 || g != 0 || n != 0 || s {
		t.Fatal("an empty book must aggregate to zero, not to a scaled anything")
	}
}
