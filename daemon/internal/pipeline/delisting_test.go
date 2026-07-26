package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newDelistStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "delist.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// fleet builds n stock symbols whose newest daily bar is `staleDays` old, plus
// any explicitly-specified stragglers.
func seedFleet(t *testing.T, st *store.Store, now time.Time, healthy int, stale map[string]int) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	ids := map[string]int64{}
	add := func(sym string, daysOld int) {
		s, err := st.UpsertSymbol(ctx, sym, md.Stocks, sym+" Inc")
		if err != nil {
			t.Fatalf("UpsertSymbol %s: %v", sym, err)
		}
		ids[sym] = s.ID
		ts := now.AddDate(0, 0, -daysOld).Unix()
		ts -= ts % 86400
		if err := st.UpsertBars(ctx, []md.Bar{{
			SymbolID: s.ID, TF: md.TF1d, Ts: ts,
			Open: 10, High: 11, Low: 9, Close: 10, Volume: 1000,
		}}); err != nil {
			t.Fatalf("UpsertBars %s: %v", sym, err)
		}
	}
	for i := 0; i < healthy; i++ {
		add(fmt.Sprintf("LIVE%03d", i), 1)
	}
	for sym, days := range stale {
		add(sym, days)
	}
	return ids
}

// The headline guarantee: a symbol that stopped printing while the fleet kept
// printing is marked, and the recorded date is its LAST BAR — not today.
func TestDelistedIsDatedAtTheLastBarNotDetectionTime(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ids := seedFleet(t, st, now, 60, map[string]int{"DEADCO": 120})

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows, err := st.StockLastBars(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows {
		if r.SymbolID != ids["DEADCO"] {
			if r.DelistedAt != 0 {
				t.Fatalf("healthy symbol %s was marked delisted (detail: %s)", r.Symbol, detail)
			}
			continue
		}
		found = true
		if r.DelistedAt == 0 {
			t.Fatalf("DEADCO should be marked delisted (detail: %s)", detail)
		}
		if r.DelistedAt != r.LastTs {
			t.Fatalf("delisted_at = %d, want the last bar %d — dating it 'now' would hide a "+
				"tradable name from every point-in-time universe in between", r.DelistedAt, r.LastTs)
		}
	}
	if !found {
		t.Fatal("DEADCO missing from StockLastBars")
	}
}

// THE guard. Our own ingestion failing looks exactly like the whole market
// delisting at once. If most of the fleet is stale, the fault is ours and
// NOTHING may be marked — otherwise one outage permanently corrupts every
// point-in-time universe built afterwards.
func TestBrokenIngestionMarksNothing(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	// Whole fleet stale — as if the poller had been dead for months.
	stale := map[string]int{}
	for i := 0; i < 80; i++ {
		stale[fmt.Sprintf("OLD%03d", i)] = 200
	}
	seedFleet(t, st, now, 0, stale)

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows, _ := st.StockLastBars(ctx)
	for _, r := range rows {
		if r.DelistedAt != 0 {
			t.Fatalf("%s marked delisted during an ingestion outage — the guard failed (detail: %s)",
				r.Symbol, detail)
		}
	}
}

// Delisting must be REVERSIBLE. A halted name that resumes printing was never
// delisted, and a one-way marker turns every false positive into a permanent
// market "fact".
func TestResumedSymbolIsUnmarked(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ids := seedFleet(t, st, now, 60, map[string]int{"HALTED": 120})

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// It comes back: a fresh bar arrives.
	ts := now.AddDate(0, 0, -1).Unix()
	ts -= ts % 86400
	if err := st.UpsertBars(ctx, []md.Bar{{
		SymbolID: ids["HALTED"], TF: md.TF1d, Ts: ts,
		Open: 10, High: 11, Low: 9, Close: 10, Volume: 1000,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.StockLastBars(ctx)
	for _, r := range rows {
		if r.SymbolID == ids["HALTED"] && r.DelistedAt != 0 {
			t.Fatal("a symbol that resumed printing must have its delisting marker cleared")
		}
	}
}

// A symbol with NO bars at all is a backfill/subscription state, not evidence of
// delisting. Guessing here would mark every newly-added ticker dead.
func TestSymbolWithNoBarsIsNeverMarked(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	seedFleet(t, st, now, 60, nil)
	fresh, err := st.UpsertSymbol(ctx, "BRANDNEW", md.Stocks, "Brand New Inc")
	if err != nil {
		t.Fatal(err)
	}

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.StockLastBars(ctx)
	for _, r := range rows {
		if r.SymbolID == fresh.ID && r.DelistedAt != 0 {
			t.Fatal("a symbol with no bars yet must not be marked delisted")
		}
	}
}
