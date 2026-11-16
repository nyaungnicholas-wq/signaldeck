package pipeline

import (
	"context"
	"testing"
	"time"
)

// A symbol silent for 300 days that prints again keeps its marker (a reused
// ticker looks the same), and an INACTIVE stale symbol is never marked: we
// stopped fetching it, so its silence says nothing about the market.
func TestDelistingHoldsLongGapAndSkipsInactive(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	ids := seedFleet(t, st, now, 60, map[string]int{"REUSED": 1, "UNWATCHED": 120})
	if err := st.MarkDelisted(ctx, ids["REUSED"], now.AddDate(0, 0, -300).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolActive(ctx, ids["UNWATCHED"], false); err != nil {
		t.Fatal(err)
	}
	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.StockLastBars(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Symbol] = r.DelistedAt
	}
	if got["REUSED"] == 0 {
		t.Fatalf("a 300-day silence must not clear the marker (detail: %s)", detail)
	}
	if got["UNWATCHED"] != 0 {
		t.Fatalf("an inactive symbol was marked delisted (detail: %s)", detail)
	}
	// The held symbol is reported once, not on every daily pass.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	evs, err := st.RecentDQ(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Kind == "delisted_symbol_resumed" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d delisted_symbol_resumed events after two passes, want 1", n)
	}
}
