// Tests for the alert-sweep cursor helpers — the fan-out inputs (user list),
// the per-window dedup check, and the id-cursor reads over breakouts and
// regime_changes that make the sweep gap-free and idempotent.
package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestAlertSweep_UsersAndDedupWindow(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	u1, err := st.CreateUser(ctx, "alice", "h1", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	u2, err := st.CreateUser(ctx, "bob", "h2", true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ids, err := st.ListUserIDs(ctx)
	if err != nil || len(ids) != 2 || ids[0] != u1 || ids[1] != u2 {
		t.Fatalf("ListUserIDs = %v, %v; want [%d %d]", ids, err, u1, u2)
	}

	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	if err := st.InsertAlert(ctx, Alert{
		UserID: u1, SymbolID: &sym.ID, Horizon: "1d",
		Kind: "prediction_high", Detail: "p=0.71", Ts: 5000,
	}); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	got, err := st.HasAlertSince(ctx, u1, sym.ID, "1d", "prediction_high", 4000)
	if err != nil || !got {
		t.Fatalf("HasAlertSince in-window = %v, %v; want true", got, err)
	}
	// Outside the window, wrong horizon, and wrong user must all miss.
	if got, _ := st.HasAlertSince(ctx, u1, sym.ID, "1d", "prediction_high", 6000); got {
		t.Fatal("HasAlertSince matched an alert older than `since`")
	}
	if got, _ := st.HasAlertSince(ctx, u1, sym.ID, "1w", "prediction_high", 4000); got {
		t.Fatal("HasAlertSince matched across horizons")
	}
	if got, _ := st.HasAlertSince(ctx, u2, sym.ID, "1d", "prediction_high", 4000); got {
		t.Fatal("HasAlertSince matched across users")
	}
}

func TestAlertSweep_BreakoutCursor(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Empty table: cursor seed is 0, sweep returns nothing.
	if id, err := st.MaxBreakoutID(ctx); err != nil || id != 0 {
		t.Fatalf("MaxBreakoutID empty = %d, %v; want 0", id, err)
	}

	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "Nvidia")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	if err := st.InsertBreakout(ctx, &sym.ID, 1000, "volume_spike", "old", 2.0); err != nil {
		t.Fatalf("insert breakout: %v", err)
	}
	// Watchlist-wide event: NULL symbol_id must round-trip as nil.
	if err := st.InsertBreakout(ctx, nil, 2000, "corr_break", "new", 3.5); err != nil {
		t.Fatalf("insert breakout: %v", err)
	}

	maxID, err := st.MaxBreakoutID(ctx)
	if err != nil || maxID == 0 {
		t.Fatalf("MaxBreakoutID = %d, %v", maxID, err)
	}

	evs, err := st.BreakoutsAfterID(ctx, 0, 0, 10)
	if err != nil || len(evs) != 2 {
		t.Fatalf("BreakoutsAfterID(0,0) = %d rows, %v; want 2", len(evs), err)
	}
	if evs[0].SymbolID == nil || *evs[0].SymbolID != sym.ID || evs[0].Strength != 2.0 {
		t.Fatalf("first event mangled: %+v", evs[0])
	}
	if evs[1].SymbolID != nil {
		t.Fatalf("watchlist-wide event should have nil SymbolID: %+v", evs[1])
	}

	// sinceTs guard: an adopted database must not flood ancient events.
	evs, err = st.BreakoutsAfterID(ctx, 0, 1500, 10)
	if err != nil || len(evs) != 1 || evs[0].Kind != "corr_break" {
		t.Fatalf("BreakoutsAfterID since-filter = %+v, %v; want only corr_break", evs, err)
	}
	// Cursor advance: nothing after maxID.
	if evs, err := st.BreakoutsAfterID(ctx, maxID, 0, 10); err != nil || len(evs) != 0 {
		t.Fatalf("BreakoutsAfterID(max) = %d rows, %v; want 0", len(evs), err)
	}
}

func TestAlertSweep_RegimeChangeCursor(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if id, err := st.MaxRegimeChangeID(ctx); err != nil || id != 0 {
		t.Fatalf("MaxRegimeChangeID empty = %d, %v; want 0", id, err)
	}

	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "SPDR")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	// First upsert sets state; second (different label) logs a change row.
	if err := st.UpsertRegime(ctx, sym.ID, 1000, "range", 0.4, ""); err != nil {
		t.Fatalf("upsert regime: %v", err)
	}
	if err := st.UpsertRegime(ctx, sym.ID, 2000, "uptrend", 0.8, "breakout"); err != nil {
		t.Fatalf("upsert regime: %v", err)
	}

	maxID, err := st.MaxRegimeChangeID(ctx)
	if err != nil || maxID == 0 {
		t.Fatalf("MaxRegimeChangeID = %d, %v; want > 0", maxID, err)
	}

	evs, err := st.RegimeChangesAfterID(ctx, 0, 0, 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("RegimeChangesAfterID = %d rows, %v; want 1", len(evs), err)
	}
	e := evs[0]
	if e.SymbolID != sym.ID || e.From != "range" || e.To != "uptrend" || e.Ts != 2000 {
		t.Fatalf("regime change mangled: %+v", e)
	}
	// sinceTs filter excludes it; advanced cursor excludes it.
	if evs, _ := st.RegimeChangesAfterID(ctx, 0, 3000, 10); len(evs) != 0 {
		t.Fatal("sinceTs filter leaked an old regime change")
	}
	if evs, _ := st.RegimeChangesAfterID(ctx, maxID, 0, 10); len(evs) != 0 {
		t.Fatal("cursor advance leaked an already-swept regime change")
	}
}
