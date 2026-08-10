package pipeline

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/sectors"
)

// These tests exercise the pretrade risk gate THROUGH the worker, not in
// isolation. riskgate's own tests prove the rules; what has to be proven here is
// that the book actually consults them — a gate the trade path never calls is the
// exact failure mode it was written to fix.

// A sector cap must bound what the book can accumulate in one sector WITHIN a
// single step. Three financials all screaming long, against a cap of 5% of a
// $100k book, cannot all be bought.
func TestRiskGateEnforcesSectorCapAcrossOneStep(t *testing.T) {
	t.Setenv("SIGNALDECK_RISK_MAX_SECTOR_WEIGHT", "0.05") // $5,000 on a $100k book

	st := openStore(t)
	ctx := context.Background()

	// JPM/BAC/GS all map to "Financials" in the static sector table — the whole
	// point of the test, so assert it rather than trusting it.
	for _, s := range []string{"JPM", "BAC", "GS"} {
		if got := sectors.SectorOf(s); got != "Financials" {
			t.Fatalf("fixture assumption broken: SectorOf(%s)=%q, want Financials", s, got)
		}
	}

	for _, name := range []string{"JPM", "BAC", "GS"} {
		sym, err := st.UpsertSymbol(ctx, name, md.Stocks, "")
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		seedDailyPx(t, st, sym.ID, [][3]float64{
			{1, 100, 100},
			{2, 100, 100},
			{3, 100, 100},
		})
		seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)
		seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)
	}

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	trades, err := st.PaperTrades(ctx, "flagship-1d", 50)
	if err != nil {
		t.Fatalf("trades: %v", err)
	}
	var bought float64
	for _, tr := range trades {
		if tr.Side == "buy" {
			bought += tr.Qty * tr.Px
		}
	}
	// Without the gate every name gets the $10,000 equal slice: $30,000 of
	// financials on a $100k book.
	const sectorCap = 0.05 * 100_000
	if bought > sectorCap*1.05 {
		t.Errorf("bought $%.0f of financials against a $%.0f sector cap — the gate did not bind", bought, sectorCap)
	}
	if bought <= 0 {
		t.Fatal("nothing was bought at all; the first name should fill against an empty sector")
	}
	// And the names that arrive after the sector is full must be REFUSED, not
	// filled with the remnant. One fill consumes the whole cap here.
	if len(trades) != 1 {
		t.Errorf("want 1 fill and 2 refusals, got %d fills: %+v", len(trades), trades)
	}
	for _, tr := range trades {
		if n := tr.Qty * tr.Px; n < 0.005*100_000 {
			t.Errorf("fill of $%.4f is below the minimum ticket — dust was filled", n)
		}
	}
}

// The sizing rationale must reach the trade log. A reader auditing a fill should
// be able to see WHY that size, and on a young book the honest answer is "equal
// slice, not measured odds".
func TestRiskGateRecordsSizingRationaleOnTheFill(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	seedDailyPx(t, st, sym.ID, [][3]float64{{1, 100, 100}, {2, 100, 100}, {3, 100, 100}})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	if len(trades) != 1 {
		t.Fatalf("want one entry, got %d", len(trades))
	}
	// A book with no closed round trips cannot have a measured edge, so the fill
	// must be labeled equal-slice rather than implying Kelly.
	if want := "equal-slice"; !contains(trades[0].Reason, want) {
		t.Errorf("fill reason %q should disclose %q sizing", trades[0].Reason, want)
	}
}

// THE safety property: with the gate refusing every new entry, an EXIT must
// still execute. A breaker that traps the book in the position that tripped it is
// worse than no breaker.
func TestRiskGateNeverBlocksAnExit(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "JPM", md.Stocks, "")

	// Days 1-3 only: the worker's as-of clock is the newest daily bar, and its
	// cursor then blocks any later run at the same clock. Seeding day 4/5 up front
	// would make the SECOND run an idempotent no-op and the test would "pass" for
	// the wrong reason.
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); !ok {
		t.Fatal("setup failed: no position to exit")
	}

	// Now clamp the gate shut for entries: a 1%-of-book sector cap on a name
	// whose sector is already fully occupied by the position we just opened.
	t.Setenv("SIGNALDECK_RISK_MAX_SECTOR_WEIGHT", "0.01")
	t.Setenv("SIGNALDECK_RISK_MAX_DRAWDOWN", "0.0001") // and trip the breaker too

	// Advance the clock with two fresh bars, then flip the signal flat stamped at
	// day 4 so it fills at day 5's open.
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{4, 100, 100},
		{5, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 4*86400, 0.10)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("exit run: %v", err)
	}

	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Error("the position survived a flat signal — an exit was gated, which must never happen")
	}
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	sells := 0
	for _, tr := range trades {
		if tr.Side == "sell" {
			sells++
		}
	}
	if sells != 1 {
		t.Errorf("want exactly one sell despite every entry limit being breached, got %d", sells)
	}
}

// The edge fed to the gate comes from the book's own realized round trips, and
// must read as unmeasurable until enough of them have closed.
func TestTradedEdgeIsWithheldOnAYoungBook(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	w := &PaperTrader{St: st}

	edge, err := w.tradedEdge(ctx, "flagship-1d", 9*86400)
	if err != nil {
		t.Fatalf("tradedEdge: %v", err)
	}
	if edge.Valid {
		t.Error("a book with no fills cannot have a measured edge")
	}
	if edge.Trips != 0 {
		t.Errorf("want 0 round trips, got %d", edge.Trips)
	}
	// And the floor the gate uses is the same one papertrade applies to payoffs,
	// so the two cannot drift apart.
	if papertrade.MinPayoffTrips <= 0 {
		t.Error("MinPayoffTrips must be a positive floor")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
