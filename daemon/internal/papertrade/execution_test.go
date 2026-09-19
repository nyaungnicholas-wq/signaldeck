package papertrade

import (
	"math"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// REPRODUCED FIRST, THEN FIXED. Against the live 2.1 GB database, read-only:
//
//	SELECT count(*) FROM paper_trades t JOIN bars b
//	  ON b.symbol_id=t.symbol_id AND b.tf='1d' AND b.ts=t.ts;      -> 44
//	... WHERE abs(t.px-b.open)/b.open <= 1e-6                      -> 23
//	max |px-open| in bps                                           -> 90.3
//	mean |px-open| in bps                                          -> 11.8
//
// Twenty-one of forty-four "fills computed from the daemon's own stored bar
// data" cannot be reproduced from that data, and the average discrepancy (11.8
// bps) is larger than the entire per-side cost the book charged itself (7.5
// bps). Two defects, fixed separately below:
//
//   - the fill price must BE the stored bar's open, exactly, so the trade log
//     stays auditable against the bars forever (fills 1-21 predate the bars they
//     are compared to: `bars` is written INSERT OR REPLACE, so a provider
//     revision silently rewrote the reference and nothing noticed);
//   - the cost charged must be MODELLED from the fill — half-spread plus
//     square-root-law market impact, capped by ADV participation with a partial
//     fill — instead of a flat constant the fills quietly beat.

func testBar(open, high, low, volume float64) md.Bar {
	return md.Bar{Ts: 1783396800, Open: open, High: high, Low: low, Close: open, Volume: volume}
}

// A deep, liquid name: $10k against $500M of daily dollar volume, the live
// book's actual scale.
func liquidInputs() ExecInputs {
	return ExecInputs{
		Bar:    testBar(100, 101, 99, 5_000_000),
		Market: md.Stocks,
		ADVUSD: 500_000_000,
	}
}

func TestExecutionRejectsNonFiniteAmounts(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if f, _, _, ok := EnterLong(v, liquidInputs()); ok {
			t.Fatalf("accepted non-finite budget: %+v", f)
		}
		if f, ok := ExitLong(v, liquidInputs()); ok {
			t.Fatalf("accepted non-finite position: %+v", f)
		}
	}
}

func TestPaperConfigRejectsNonFiniteValues(t *testing.T) {
	for _, v := range []string{"NaN", "+Inf", "-Inf"} {
		t.Setenv("SIGNALDECK_PAPER_CASH", v)
		if got := StartingCash(); got != defaultStartingCash {
			t.Fatalf("%s produced starting cash %v", v, got)
		}
	}
}

// TestEnterLong_FillPriceIsExactlyTheStoredBarOpen is the audit-trail half. A
// paper fill whose price is not reproducible from the bar it names is not a
// track record, it is an assertion.
func TestEnterLong_FillPriceIsExactlyTheStoredBarOpen(t *testing.T) {
	in := liquidInputs()
	f, _, avgPx, ok := EnterLong(10_000, in)
	if !ok {
		t.Fatalf("EnterLong refused a normal fill: %s", f.Reason)
	}
	if f.Px != in.Bar.Open {
		t.Errorf("fill px %.10f != stored bar open %.10f (%.2f bps)", f.Px, in.Bar.Open,
			(f.Px-in.Bar.Open)/in.Bar.Open*1e4)
	}
	if f.RefPx != in.Bar.Open {
		t.Errorf("RefPx %.10f != stored bar open %.10f", f.RefPx, in.Bar.Open)
	}
	if avgPx != in.Bar.Open {
		t.Errorf("position cost basis %.10f != stored bar open %.10f", avgPx, in.Bar.Open)
	}

	x, ok := ExitLong(50, in)
	if !ok {
		t.Fatalf("ExitLong refused a normal fill: %s", x.Reason)
	}
	if x.Px != in.Bar.Open {
		t.Errorf("exit px %.10f != stored bar open %.10f", x.Px, in.Bar.Open)
	}
}

// TestEnterLong_ChargesSpreadPlusImpact is the cost half: the charge is derived
// from THIS fill's size against THIS bar's liquidity, not read off a constant.
func TestEnterLong_ChargesSpreadPlusImpact(t *testing.T) {
	in := liquidInputs()
	f, _, _, ok := EnterLong(10_000, in)
	if !ok {
		t.Fatalf("EnterLong refused: %s", f.Reason)
	}
	spread := CostBpsFor(md.Stocks)
	if math.Abs(f.SpreadBps-spread) > 1e-9 {
		t.Errorf("SpreadBps=%.4f want %.4f", f.SpreadBps, spread)
	}
	if f.ImpactBps <= 0 {
		t.Error("ImpactBps must be positive: taking liquidity is never free")
	}
	notional := f.Qty * f.Px
	wantCost := notional * (f.SpreadBps + f.ImpactBps) / 1e4
	if math.Abs(f.Cost-wantCost) > 1e-6 {
		t.Errorf("Cost=%.6f, want notional*(spread+impact)=%.6f", f.Cost, wantCost)
	}
	// The whole point: the charge now EXCEEDS the flat assumption it replaced.
	if f.Cost <= notional*spread/1e4 {
		t.Errorf("cost %.6f no larger than the old flat %.1f bps charge %.6f", f.Cost, spread, notional*spread/1e4)
	}
	if f.Participation <= 0 {
		t.Error("Participation must be reported so the impact figure can be checked")
	}
}

// TestEnterLong_ImpactGrowsWithSize pins the shape of the model: impact follows
// the square-root law, so a 100x larger order pays ~10x the impact per share.
// A model whose impact is flat in size is a constant with extra steps.
func TestEnterLong_ImpactGrowsWithSize(t *testing.T) {
	in := liquidInputs()
	small, _, _, ok1 := EnterLong(10_000, in)
	big, _, _, ok2 := EnterLong(1_000_000, in)
	if !ok1 || !ok2 {
		t.Fatalf("fills refused: %s / %s", small.Reason, big.Reason)
	}
	if big.ImpactBps <= small.ImpactBps {
		t.Fatalf("impact did not grow with size: %.4f bps at $10k vs %.4f bps at $1M",
			small.ImpactBps, big.ImpactBps)
	}
	ratio := big.ImpactBps / small.ImpactBps
	if ratio < 8 || ratio > 12 {
		t.Errorf("impact ratio %.2f for a 100x size step; the square-root law wants ~10", ratio)
	}
}

// TestEnterLong_ADVCapProducesAPartialFill: an order that would take more than
// the participation cap of a day's dollar volume gets FILLED SHORT, and says
// how much did not fill. Pretending the whole order executed at the open is how
// a paper book manufactures capacity it does not have.
func TestEnterLong_ADVCapProducesAPartialFill(t *testing.T) {
	in := ExecInputs{
		Bar:    testBar(100, 101, 99, 20_000),
		Market: md.Stocks,
		ADVUSD: 2_000_000, // thin name: $2M/day
	}
	budget := 1_000_000.0 // half the name's daily volume
	f, qty, _, ok := EnterLong(budget, in)
	if !ok {
		t.Fatalf("EnterLong refused: %s", f.Reason)
	}
	if !f.Capped {
		t.Fatal("Capped must be true when the ADV participation limit bound the size")
	}
	maxNotional := MaxParticipation() * in.ADVUSD
	if got := qty * f.Px; got > maxNotional+1e-6 {
		t.Errorf("filled $%.2f of a $%.2f cap", got, maxNotional)
	}
	if f.UnfilledNotional <= 0 {
		t.Error("UnfilledNotional must report the shortfall of a partial fill")
	}
	if -f.CashDelta > budget+1e-6 {
		t.Errorf("outlay $%.2f exceeds budget $%.2f", -f.CashDelta, budget)
	}
}

// TestExitLong_OverCapIsAMultiBarLiquidation: a position too large to exit
// inside one bar's participation limit is still exitable, but the model has to
// say it takes more than one bar. Silently dumping it at the open is the
// fiction this replaces.
func TestExitLong_OverCapIsAMultiBarLiquidation(t *testing.T) {
	in := ExecInputs{
		Bar:    testBar(100, 101, 99, 20_000),
		Market: md.Stocks,
		ADVUSD: 1_000_000,
	}
	f, ok := ExitLong(50_000, in) // $5M position against a $1M/day name
	if !ok {
		t.Fatalf("ExitLong refused: %s", f.Reason)
	}
	if !f.Capped {
		t.Fatal("Capped must be true when the position exceeds one bar's participation limit")
	}
	if f.LiquidationBars < 2 {
		t.Errorf("LiquidationBars=%d, want >=2 for a position 5x the daily volume", f.LiquidationBars)
	}
	if f.Participation > MaxParticipation()+1e-12 {
		t.Errorf("Participation %.6f exceeds the cap %.6f", f.Participation, MaxParticipation())
	}
}

// TestFills_WithheldWhenLiquidityIsUnknown: with no dollar-volume estimate the
// impact and the capacity are both unknowable. The honest output is no fill and
// a stated reason — NOT a zero-impact fill, which is the most optimistic
// possible answer dressed as the default.
func TestFills_WithheldWhenLiquidityIsUnknown(t *testing.T) {
	in := ExecInputs{Bar: testBar(100, 101, 99, 0), Market: md.Stocks, ADVUSD: 0}
	f, _, _, ok := EnterLong(10_000, in)
	if ok {
		t.Fatalf("filled with no liquidity estimate: cost %.6f, impact %.4f bps", f.Cost, f.ImpactBps)
	}
	if !strings.Contains(strings.ToLower(f.Reason), "volume") {
		t.Errorf("refusal reason %q must name the missing dollar volume", f.Reason)
	}
	x, ok := ExitLong(10, in)
	if ok {
		t.Fatalf("exited with no liquidity estimate: cost %.6f", x.Cost)
	}
	if x.Reason == "" {
		t.Error("a withheld exit must carry a reason")
	}
}

// TestFills_WithheldOnAnUnusableBar: an inverted or non-positive bar is not a
// price. Refuse it rather than pricing off garbage.
func TestFills_WithheldOnAnUnusableBar(t *testing.T) {
	base := liquidInputs()
	bad := []struct {
		name string
		bar  md.Bar
	}{
		{"zero open", testBar(0, 101, 99, 5_000_000)},
		{"negative open", testBar(-5, 101, 99, 5_000_000)},
		{"inverted range", testBar(100, 99, 101, 5_000_000)},
		{"zero low", testBar(100, 101, 0, 5_000_000)},
	}
	for _, c := range bad {
		in := base
		in.Bar = c.bar
		if f, _, _, ok := EnterLong(10_000, in); ok {
			t.Errorf("%s: EnterLong filled at %.4f", c.name, f.Px)
		}
		if f, ok := ExitLong(10, in); ok {
			t.Errorf("%s: ExitLong filled at %.4f", c.name, f.Px)
		}
	}
}

// TestEnterLong_StillFullyFundedAndNeverNegative keeps the original invariant:
// the outlay (notional + all modelled costs) never exceeds the budget, so the
// simulated book cannot go cash-negative.
func TestEnterLong_StillFullyFundedAndNeverNegative(t *testing.T) {
	in := liquidInputs()
	const cash = 10_000.0
	f, qty, _, ok := EnterLong(cash, in)
	if !ok {
		t.Fatalf("EnterLong refused: %s", f.Reason)
	}
	outlay := qty*f.Px + f.Cost
	if outlay > cash+1e-9 {
		t.Errorf("outlay %.10f exceeds budget %.10f", outlay, cash)
	}
	if math.Abs(outlay-cash) > 1e-6 {
		t.Errorf("outlay %.10f should consume the whole budget %.10f when uncapped", outlay, cash)
	}
	if math.Abs(-f.CashDelta-outlay) > 1e-9 {
		t.Errorf("CashDelta %.10f inconsistent with outlay %.10f", f.CashDelta, outlay)
	}
}

// ── fill fidelity: the check that would have caught this in the first place ──

// TestCheckFillFidelity_ReproducesTheLiveMismatch feeds the checker the exact
// shape of the live log: some fills reproduce from the stored bar, some do not.
// The verdict must be "not verified", and the counts must be the ones a reader
// can act on.
func TestCheckFillFidelity_ReproducesTheLiveMismatch(t *testing.T) {
	pairs := []FillVsBar{
		{Px: 100, BarOpen: 100, HasBar: true},       // exact
		{Px: 100, BarOpen: 100, HasBar: true},       // exact
		{Px: 103.92, BarOpen: 102.99, HasBar: true}, // the live ACGL case: +90.3 bps
		{Px: 148.84, BarOpen: 149.45, HasBar: true}, // the live ABNB case: -40.8 bps
		{Px: 50, HasBar: false},                     // bar no longer stored
	}
	f := CheckFillFidelity(pairs)
	if f.Fills != 5 || f.Matched != 2 || f.Mismatched != 2 || f.NoBar != 1 {
		t.Errorf("counts = fills %d matched %d mismatched %d nobar %d; want 5/2/2/1",
			f.Fills, f.Matched, f.Mismatched, f.NoBar)
	}
	if f.Verified {
		t.Error("Verified must be false when a fill does not reproduce from its bar")
	}
	if math.Abs(f.MaxAbsBps-90.3) > 0.1 {
		t.Errorf("MaxAbsBps=%.2f, want ~90.3 (the figure measured on the live DB)", f.MaxAbsBps)
	}
	if f.Reason == "" {
		t.Error("an unverified book must state why")
	}
}

func TestCheckFillFidelity_CleanLogVerifies(t *testing.T) {
	f := CheckFillFidelity([]FillVsBar{
		{Px: 100, BarOpen: 100, HasBar: true},
		{Px: 321.79, BarOpen: 321.79, HasBar: true},
	})
	if !f.Verified {
		t.Errorf("a log whose fills all reproduce must verify; reason=%q", f.Reason)
	}
	if f.MaxAbsBps != 0 {
		t.Errorf("MaxAbsBps=%.6f, want 0", f.MaxAbsBps)
	}
}

// An empty log is not a verified log. "No fills yet" is a different statement
// from "every fill checks out", and conflating them is how a fresh book claims
// a clean audit.
func TestCheckFillFidelity_EmptyIsNotVerified(t *testing.T) {
	f := CheckFillFidelity(nil)
	if f.Verified {
		t.Error("an empty trade log must not report itself as verified")
	}
}

// A failed bar LOOKUP is not a missing bar. api/paper.go used to fold the store
// error into the has-bar condition (`err == nil && ok && ...`), so an outage was
// indistinguishable from a fill whose bar is genuinely absent — and the verdict
// then blamed "no stored bar at all", sending the reader hunting a data gap that
// did not exist. Same principle as the empty-log case above: "we could not look"
// is not "we looked and found nothing".
func TestCheckFillFidelity_LookupFailureIsNotAMissingBar(t *testing.T) {
	f := CheckFillFidelity([]FillVsBar{
		{Px: 100, Unchecked: true},
		{Px: 200, Unchecked: true},
	})
	if f.Verified {
		t.Error("fills that could not be checked must not verify")
	}
	if f.Unchecked != 2 {
		t.Errorf("Unchecked=%d, want 2", f.Unchecked)
	}
	if f.NoBar != 0 {
		t.Errorf("NoBar=%d, want 0 — a failed lookup is not a missing bar", f.NoBar)
	}
	if strings.Contains(f.Reason, "no stored bar at all") {
		t.Errorf("an outage must not be reported as missing bars; reason=%q", f.Reason)
	}
	if !strings.Contains(f.Reason, "could not be checked") {
		t.Errorf("the reason must say the check could not run; reason=%q", f.Reason)
	}
}

// Real findings AND an outage together: the counts must stay separable, or the
// reader cannot tell which half of the verdict is evidence about the book.
func TestCheckFillFidelity_UncheckedIsReportedApartFromFindings(t *testing.T) {
	f := CheckFillFidelity([]FillVsBar{
		{Px: 100, BarOpen: 100, HasBar: true}, // matched
		{Px: 110, BarOpen: 100, HasBar: true}, // mismatched
		{Px: 120},                             // genuinely no stored bar
		{Px: 130, Unchecked: true},            // lookup errored
	})
	if f.Verified {
		t.Error("must not verify")
	}
	if f.Matched != 1 || f.Mismatched != 1 || f.NoBar != 1 || f.Unchecked != 1 {
		t.Errorf("matched=%d mismatched=%d noBar=%d unchecked=%d; want 1/1/1/1",
			f.Matched, f.Mismatched, f.NoBar, f.Unchecked)
	}
	if f.Fills != 4 {
		t.Errorf("Fills=%d, want 4", f.Fills)
	}
	// The mean/max must describe only what was actually compared.
	if f.MeanAbsBps <= 0 {
		t.Errorf("MeanAbsBps=%.4f, want >0 from the one compared mismatch", f.MeanAbsBps)
	}
}
