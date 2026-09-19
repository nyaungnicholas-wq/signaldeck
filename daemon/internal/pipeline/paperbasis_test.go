package pipeline

import (
	"context"
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// A STOP MUST BE PLACED FROM A PRICE ON THE SAME BASIS AS THE BARS IT WATCHES.
//
// paper_positions.avg_px is stored when a position opens and then compared
// against live bars to place the stop and the target. `bars` is written INSERT
// OR REPLACE and a detected split re-backfills the symbol's entire series, so
// those two prices can end up on different bases — and the rescale factor lands
// whole in the barrier levels.
//
// This is the confluence defect in a second place. There, a frozen entry_px
// divided into a live exit close published DFNS at +8541% on a price no bar of
// that symbol has ever carried. Here it does not produce a silly number, it
// produces a silly STOP: after a 100:1 reverse split a stored pre-split entry
// makes every level unreachable, and the position is never stopped out.
//
// Measured on the live database 2026-08-21: 24 successful rescales exist, all
// between 2026-07-25 and 2026-08-11, and 0 open positions predate one. The path
// was clean by luck. These tests make it clean by construction.
//
// MUTATION CHECKS, both verified:
//   - restore `FindBarrierExit(pos.AvgPx, ...)` in findBarrier →
//     TestPaperBarrier_UsesTheEntryBarNotTheStoredPrice fails, holding a position
//     through a stop it should have hit;
//   - make entryPxOnCurrentBasis return basisStoredClean instead of
//     basisUnresolvable → TestPaperBarrier_RefusesPriceLevelsOnADeadBasis fails,
//     placing a stop from a price the current series never carried.

// seedRescaledSeries writes a post-reverse-split series and records the repair
// that rescaled it, so the position below is genuinely on a dead basis.
func seedRescaledSeries(t *testing.T, st *store.Store, symbol string, openedTs int64) md.Symbol {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, symbol, md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	const day = int64(86400)
	// 40 bars of history so the ATR is measurable, then the holding window.
	var bars []md.Bar
	for i := int64(-40); i <= 6; i++ {
		px := 100.0 + float64(i%3)
		if i >= 1 {
			px = 100 - float64(i)*4 // walks down into the stop
		}
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: openedTs + i*day,
			Open: px, High: px * 1.02, Low: px * 0.98, Close: px, Volume: 100_000,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
	return sym
}

// TestPaperBarrier_UsesTheEntryBarNotTheStoredPrice: the stored price is on the
// pre-split basis (a hundredth of the current one). Placing levels from it puts
// the stop far below anything the series can reach.
func TestPaperBarrier_UsesTheEntryBarNotTheStoredPrice(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	const openedTs = int64(20000) * 86400
	sym := seedRescaledSeries(t, st, "DFNS", openedTs)

	w := &PaperTrader{St: st}
	pos := store.PaperPosition{
		Strategy: "flagship-1d", SymbolID: sym.ID, Qty: 10,
		AvgPx:    1.00, // pre-rescale: the current series opens this bar at ~100
		OpenedTs: openedTs,
	}
	held, err := st.Bars(ctx, sym.ID, md.TF1d, openedTs, openedTs+7*86400, 0)
	if err != nil {
		t.Fatalf("bars: %v", err)
	}
	px, basis, err := w.entryPxOnCurrentBasis(ctx, sym.ID, pos, held)
	if err != nil {
		t.Fatalf("basis: %v", err)
	}
	if basis != basisEntryBar {
		t.Fatalf("basis = %q, want %q — the entry bar is present and must be preferred", basis, basisEntryBar)
	}
	if math.Abs(px-held[0].Open) > 1e-9 {
		t.Fatalf("entry px = %.4f, want the entry bar open %.4f. Using the stored %.4f places every "+
			"barrier a rescale factor away from the series it is meant to watch.", px, held[0].Open, pos.AvgPx)
	}

	// And the consequence: from the live basis the walk-down DOES reach the stop.
	// The 1w horizon is used so the position survives long enough for a PRICE
	// barrier to be the thing that fires — at the 1d horizon expiry always wins on
	// the entry bar and the test would prove nothing about levels.
	exit, found, err := w.findBarrier(ctx, sym, md.H1w, pos, openedTs+6*86400)
	if err != nil {
		t.Fatalf("findBarrier: %v", err)
	}
	if !found {
		t.Fatal("no barrier found on a series that walks 24% down from entry — the levels were placed off a dead basis")
	}
	if exit.Kind == papertrade.BarrierExpiry {
		t.Fatalf("exit was the horizon, not the stop: the price barriers were unreachable. exit = %+v", exit)
	}
}

// TestPaperBarrier_RefusesPriceLevelsOnADeadBasis: the entry bar is GONE (purged
// or quarantined) and the series has been rescaled since the position opened.
// There is no comparable price, so price levels must not be placed at all.
func TestPaperBarrier_RefusesPriceLevelsOnADeadBasis(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	const openedTs = int64(20000) * 86400
	sym := seedRescaledSeries(t, st, "DFNS", openedTs)

	// Record a SUCCESSFUL rescale after the position opened.
	if err := st.RecordSplitRepair(ctx, sym.ID, "2026-08-01", "1:100 (reverse)", 1, true, "", openedTs+86400); err != nil {
		t.Fatalf("record repair: %v", err)
	}

	w := &PaperTrader{St: st}
	pos := store.PaperPosition{
		Strategy: "flagship-1d", SymbolID: sym.ID, Qty: 10,
		AvgPx: 1.00, OpenedTs: openedTs,
	}
	// Holding window that does NOT include the entry bar — the bar is gone.
	held, err := st.Bars(ctx, sym.ID, md.TF1d, openedTs+86400, openedTs+7*86400, 0)
	if err != nil {
		t.Fatalf("bars: %v", err)
	}
	_, basis, err := w.entryPxOnCurrentBasis(ctx, sym.ID, pos, held)
	if err != nil {
		t.Fatalf("basis: %v", err)
	}
	if basis != basisUnresolvable {
		t.Fatalf("basis = %q, want %q. A stored price from before a rescale, with no entry bar left to "+
			"re-read, has no comparable value on the current series — placing a stop from it is inventing a level.", basis, basisUnresolvable)
	}

	// With no rescale on record the same stored price is usable, because nothing
	// rewrote the series under it. Fail-closed must not mean fail-always.
	clean := seedRescaledSeries(t, st, "CLEAN", openedTs)
	heldClean, err := st.Bars(ctx, clean.ID, md.TF1d, openedTs+86400, openedTs+7*86400, 0)
	if err != nil {
		t.Fatalf("bars: %v", err)
	}
	pos.SymbolID = clean.ID
	if _, basis, err = w.entryPxOnCurrentBasis(ctx, clean.ID, pos, heldClean); err != nil {
		t.Fatalf("basis: %v", err)
	}
	if basis != basisStoredClean {
		t.Fatalf("basis = %q, want %q — a stored price with no rescale behind it is still usable", basis, basisStoredClean)
	}
}

// TestStaleBasisPositions_IsAnAuditNotAnAssumption pins the reporting half: the
// audit must FIND a stale position when one exists, or a report of "0" means
// nothing.
func TestStaleBasisPositions_IsAnAuditNotAnAssumption(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	const openedTs = int64(20000) * 86400
	sym := seedRescaledSeries(t, st, "DFNS", openedTs)

	openPosition(t, st, "flagship-1d", sym.ID, 1.00, openedTs)

	clean, err := st.StaleBasisPositions(ctx)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(clean) != 0 {
		t.Fatalf("no rescale is on record yet, so nothing can be stale: %+v", clean)
	}

	if err := st.RecordSplitRepair(ctx, sym.ID, "2026-08-01", "1:100 (reverse)", 1, true, "", openedTs+86400); err != nil {
		t.Fatalf("record repair: %v", err)
	}
	got, err := st.StaleBasisPositions(ctx)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(got) != 1 || got[0].Symbol != "DFNS" {
		t.Fatalf("the audit missed a position opened before a rescale of its own symbol: %+v", got)
	}

	// A FAILED repair rewrote nothing, so it must not fail-close anything.
	other := seedRescaledSeries(t, st, "REAL", openedTs)
	openPosition(t, st, "flagship-1w", other.ID, 100, openedTs)
	if err := st.RecordSplitRepair(ctx, other.ID, "2026-08-01", "2:1", 1, false,
		"provider returned the same move on a clean refetch", openedTs+86400); err != nil {
		t.Fatalf("record repair: %v", err)
	}
	got, err = st.StaleBasisPositions(ctx)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	for _, p := range got {
		if p.Symbol == "REAL" {
			t.Fatal("a repair that DECLINED to rescale (ok=0) was treated as a basis change; " +
				"1,181 such rows exist live and fail-closing on them would be a false alarm on every one")
		}
	}
}

// openPosition seeds one open paper position through the store's only write
// path, so the fixture cannot drift from how the worker actually writes one.
func openPosition(t *testing.T, st *store.Store, strategy string, symbolID int64, avgPx float64, openedTs int64) {
	t.Helper()
	if _, err := st.InitPaperBook(context.Background(), strategy, 100000, openedTs-86400); err != nil {
		t.Fatalf("init book: %v", err)
	}
	applied, err := st.ApplyPaperStep(context.Background(), store.PaperApply{
		Strategy: strategy, BarTs: openedTs, NewCash: 90000,
		Opens: []store.PaperPosition{{
			Strategy: strategy, SymbolID: symbolID, Qty: 10, AvgPx: avgPx, OpenedTs: openedTs,
		}},
		EquityTs: openedTs, EquityCash: 90000, EquityPositionsValue: 10000, EquityValue: 100000,
	})
	if err != nil || !applied {
		t.Fatalf("seed position: applied=%v err=%v", applied, err)
	}
}
