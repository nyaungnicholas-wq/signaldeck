package papertrade

import (
	"math"
	"testing"
)

func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// A single buy followed by a single full-size sell is one round trip whose P&L
// is the price difference minus BOTH sides' execution cost.
func TestMatchRoundTripsSingleCycleNetsBothCosts(t *testing.T) {
	got := MatchRoundTrips([]FillRecord{
		{SymbolID: 1, Side: "buy", Qty: 10, Px: 100, Cost: 5, Ts: 1},
		{SymbolID: 1, Side: "sell", Qty: 10, Px: 110, Cost: 6, Ts: 2},
	})
	if len(got.Closed) != 1 {
		t.Fatalf("want 1 round trip, got %d", len(got.Closed))
	}
	rt := got.Closed[0]
	if !nearly(rt.Cost, 11) {
		t.Errorf("cost: want 11 (5 entry + 6 exit), got %v", rt.Cost)
	}
	if !nearly(rt.PnL, 10*10-11) {
		t.Errorf("pnl: want %v, got %v", 10*10-11.0, rt.PnL)
	}
	if !rt.Won {
		t.Error("a +89 round trip should be a win")
	}
	if len(got.OpenQty) != 0 {
		t.Errorf("book should be flat, got open %v", got.OpenQty)
	}
}

// FIFO: a sell spanning two lots closes the OLDEST first, and each lot's own
// entry price and prorated entry cost follow it into its own round trip.
func TestMatchRoundTripsFIFOSpansLotsOldestFirst(t *testing.T) {
	got := MatchRoundTrips([]FillRecord{
		{SymbolID: 1, Side: "buy", Qty: 5, Px: 100, Cost: 5, Ts: 1},
		{SymbolID: 1, Side: "buy", Qty: 5, Px: 200, Cost: 10, Ts: 2},
		{SymbolID: 1, Side: "sell", Qty: 10, Px: 150, Cost: 20, Ts: 3},
	})
	if len(got.Closed) != 2 {
		t.Fatalf("want 2 round trips (one per lot), got %d", len(got.Closed))
	}
	if got.Closed[0].EntryPx != 100 {
		t.Errorf("first round trip must close the OLDEST lot (px 100), got %v", got.Closed[0].EntryPx)
	}
	if got.Closed[1].EntryPx != 200 {
		t.Errorf("second round trip should close the newer lot (px 200), got %v", got.Closed[1].EntryPx)
	}
	// Exit cost 20 over 10 units = 2/unit; each leg takes 5 units = 10 of it.
	// Leg 1 entry cost 5 over 5 units = 1/unit -> 5. Leg 2: 10 over 5 -> 10.
	if !nearly(got.Closed[0].Cost, 15) {
		t.Errorf("leg 1 cost: want 15, got %v", got.Closed[0].Cost)
	}
	if !nearly(got.Closed[1].Cost, 20) {
		t.Errorf("leg 2 cost: want 20, got %v", got.Closed[1].Cost)
	}
	// Leg 1 gained 50 gross, leg 2 lost 250 gross — the split matters, which is
	// exactly why FIFO order is asserted above.
	if !got.Closed[0].Won || got.Closed[1].Won {
		t.Errorf("want leg1 win / leg2 loss, got %v / %v", got.Closed[0].Won, got.Closed[1].Won)
	}
}

// A partial exit leaves the remainder of the lot open and unrealized.
func TestMatchRoundTripsPartialExitLeavesLotOpen(t *testing.T) {
	got := MatchRoundTrips([]FillRecord{
		{SymbolID: 7, Side: "buy", Qty: 10, Px: 50, Cost: 10, Ts: 1},
		{SymbolID: 7, Side: "sell", Qty: 4, Px: 60, Cost: 4, Ts: 2},
	})
	if len(got.Closed) != 1 || !nearly(got.Closed[0].Qty, 4) {
		t.Fatalf("want one 4-unit round trip, got %+v", got.Closed)
	}
	// Only 4/10 of the entry cost belongs to the closed portion.
	if !nearly(got.Closed[0].Cost, 4+4) {
		t.Errorf("cost: want 8 (4 prorated entry + 4 exit), got %v", got.Closed[0].Cost)
	}
	if !nearly(got.OpenQty[7], 6) {
		t.Errorf("want 6 units still open, got %v", got.OpenQty[7])
	}
}

// A sell with no open lot is a LOG DEFECT, not a short. It must be reported, and
// it must not manufacture a round trip.
func TestMatchRoundTripsUnmatchedSellIsReportedNotShorted(t *testing.T) {
	got := MatchRoundTrips([]FillRecord{
		{SymbolID: 3, Side: "sell", Qty: 8, Px: 90, Cost: 1, Ts: 1},
	})
	if len(got.Closed) != 0 {
		t.Errorf("a sell with no open lot must not create a round trip, got %+v", got.Closed)
	}
	if !nearly(got.UnmatchedSellQty, 8) {
		t.Errorf("want 8 unmatched sell qty reported, got %v", got.UnmatchedSellQty)
	}
}

// Symbols are matched independently — one name's sell can never close another's
// lot.
func TestMatchRoundTripsNeverCrossesSymbols(t *testing.T) {
	got := MatchRoundTrips([]FillRecord{
		{SymbolID: 1, Side: "buy", Qty: 1, Px: 10, Ts: 1},
		{SymbolID: 2, Side: "sell", Qty: 1, Px: 999, Ts: 2},
	})
	if len(got.Closed) != 0 {
		t.Errorf("symbol 2's sell must not close symbol 1's lot, got %+v", got.Closed)
	}
	if !nearly(got.OpenQty[1], 1) {
		t.Errorf("symbol 1 should still be open, got %v", got.OpenQty[1])
	}
}

// The matcher must not mutate or depend on the caller's ordering.
func TestMatchRoundTripsSortsWithoutMutatingInput(t *testing.T) {
	in := []FillRecord{
		{SymbolID: 1, Side: "sell", Qty: 10, Px: 110, Cost: 0, Ts: 20},
		{SymbolID: 1, Side: "buy", Qty: 10, Px: 100, Cost: 0, Ts: 10},
	}
	got := MatchRoundTrips(in)
	if len(got.Closed) != 1 {
		t.Fatalf("out-of-order input should still match by ts, got %d round trips", len(got.Closed))
	}
	if in[0].Ts != 20 || in[1].Ts != 10 {
		t.Error("input slice was reordered — the matcher must copy before sorting")
	}
}

// Unusable fills are skipped and counted, never priced.
func TestMatchRoundTripsRejectsUnusableFills(t *testing.T) {
	got := MatchRoundTrips([]FillRecord{
		{SymbolID: 1, Side: "buy", Qty: 0, Px: 100, Ts: 1},
		{SymbolID: 1, Side: "buy", Qty: 5, Px: 0, Ts: 2},
		{SymbolID: 1, Side: "buy", Qty: 5, Px: math.NaN(), Ts: 3},
		{SymbolID: 1, Side: "short", Qty: 5, Px: 10, Ts: 4},
	})
	if got.Unusable != 4 {
		t.Errorf("want 4 unusable fills counted, got %d", got.Unusable)
	}
	if len(got.Closed) != 0 || len(got.OpenQty) != 0 {
		t.Error("no unusable fill should reach the book")
	}
}

// Below MinPayoffTrips the payoff shape is withheld, with a reason.
func TestPayoffsWithheldOnThinSample(t *testing.T) {
	rts := make([]RoundTrip, 0, MinPayoffTrips-1)
	for i := 0; i < MinPayoffTrips-1; i++ {
		rts = append(rts, RoundTrip{PnL: 1, Won: true})
	}
	p := Payoffs(rts)
	if p.Valid {
		t.Errorf("want withheld below %d trips, got Valid=true", MinPayoffTrips)
	}
	if p.Note == "" {
		t.Error("a withheld payoff must carry a reason")
	}
}

// An unbroken winning streak has an unbounded payoff ratio. Sizing on it would
// bet the book, so it must be withheld even on a large sample.
func TestPayoffsWithheldWhenNoLossesYet(t *testing.T) {
	rts := make([]RoundTrip, 0, MinPayoffTrips+5)
	for i := 0; i < MinPayoffTrips+5; i++ {
		rts = append(rts, RoundTrip{PnL: 3, Won: true})
	}
	p := Payoffs(rts)
	if p.Valid {
		t.Error("an all-wins sample must not produce a valid payoff ratio")
	}
	if p.PayoffRatio != 0 {
		t.Errorf("payoff ratio should stay 0, not infinite, got %v", p.PayoffRatio)
	}
}

// With both tails present the ratios are the plain definitions.
func TestPayoffsRatiosOnMixedSample(t *testing.T) {
	var rts []RoundTrip
	for i := 0; i < 15; i++ { // 15 winners of +20
		rts = append(rts, RoundTrip{PnL: 20, Won: true})
	}
	for i := 0; i < 10; i++ { // 10 losers of -10
		rts = append(rts, RoundTrip{PnL: -10})
	}
	p := Payoffs(rts)
	if !p.Valid {
		t.Fatalf("25 trips with both tails should be valid: %s", p.Note)
	}
	if !nearly(p.WinRate, 0.6) {
		t.Errorf("win rate: want 0.6, got %v", p.WinRate)
	}
	if !nearly(p.PayoffRatio, 2) {
		t.Errorf("payoff ratio: want 2 (20/10), got %v", p.PayoffRatio)
	}
	if !nearly(p.ProfitFactor, 3) {
		t.Errorf("profit factor: want 3 (300/100), got %v", p.ProfitFactor)
	}
	if !nearly(p.NetPnL, 200) {
		t.Errorf("net: want 200, got %v", p.NetPnL)
	}
}

// A scratch is neither tail: it counts in N but inflates no hit rate.
func TestPayoffsScratchIsNotAWin(t *testing.T) {
	p := Payoffs([]RoundTrip{{PnL: 0}, {PnL: 5}, {PnL: -5}})
	if p.Wins != 1 || p.Losses != 1 || p.N != 3 {
		t.Errorf("want 1 win / 1 loss / N=3, got %d/%d/%d", p.Wins, p.Losses, p.N)
	}
	if !nearly(p.WinRate, 1.0/3.0) {
		t.Errorf("win rate must divide by all 3 trips, got %v", p.WinRate)
	}
}

// Sortino is withheld when nothing ever went down — the denominator is zero.
func TestSortinoWithheldWithoutDownside(t *testing.T) {
	curve := []EquityPoint{
		{Ts: 0, Equity: 100}, {Ts: 86400, Equity: 101}, {Ts: 2 * 86400, Equity: 102},
		{Ts: 3 * 86400, Equity: 103}, {Ts: 4 * 86400, Equity: 104}, {Ts: 5 * 86400, Equity: 105},
	}
	if v, ok := Sortino(curve); ok {
		t.Errorf("want withheld on an all-up curve, got %v", v)
	}
}

// A curve with real downside produces a finite Sortino, and a curve that mostly
// falls produces a negative one.
func TestSortinoSignFollowsMeanReturn(t *testing.T) {
	up := []EquityPoint{
		{Ts: 0, Equity: 100}, {Ts: 86400, Equity: 103}, {Ts: 2 * 86400, Equity: 102},
		{Ts: 3 * 86400, Equity: 106}, {Ts: 4 * 86400, Equity: 105}, {Ts: 5 * 86400, Equity: 110},
	}
	v, ok := Sortino(up)
	if !ok {
		t.Fatal("a curve with down marks should produce a Sortino")
	}
	if v <= 0 {
		t.Errorf("a net-rising curve should have positive Sortino, got %v", v)
	}
	down := []EquityPoint{
		{Ts: 0, Equity: 110}, {Ts: 86400, Equity: 105}, {Ts: 2 * 86400, Equity: 106},
		{Ts: 3 * 86400, Equity: 100}, {Ts: 4 * 86400, Equity: 101}, {Ts: 5 * 86400, Equity: 95},
	}
	if v, ok := Sortino(down); !ok || v >= 0 {
		t.Errorf("a net-falling curve should have negative Sortino, got %v (ok=%v)", v, ok)
	}
}

// CurrentDrawdown measures the wound the book still carries, not the worst it
// ever carried — the distinction a circuit breaker depends on.
func TestCurrentDrawdownIsNotMaxDrawdown(t *testing.T) {
	// Fell from 100 to 70 (30% max), then fully recovered to 100.
	curve := []EquityPoint{
		{Ts: 0, Equity: 100}, {Ts: 1, Equity: 70}, {Ts: 2, Equity: 100},
	}
	cur, ok := CurrentDrawdown(curve)
	if !ok {
		t.Fatal("want a current drawdown")
	}
	if !nearly(cur, 0) {
		t.Errorf("a fully recovered book has 0 current drawdown, got %v", cur)
	}
	if max := maxDrawdown(curve); !nearly(max, 0.3) {
		t.Errorf("max drawdown should still be 0.30, got %v", max)
	}
	// And while still under water, it reports the live shortfall.
	cur, _ = CurrentDrawdown([]EquityPoint{{Equity: 100}, {Equity: 80}})
	if !nearly(cur, 0.2) {
		t.Errorf("want 0.20 current drawdown, got %v", cur)
	}
}
