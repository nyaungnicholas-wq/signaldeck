// Signal8 wave — Stage 4: LatestMetricAll tests (t.TempDir store only).
package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestLatestMetricAll(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "home.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")

	seed := []FundamentalRow{
		// AAA: two SharesOutstanding vintages → latest as_of must win.
		{SymbolID: a.ID, Metric: "SharesOutstanding", Value: 100, AsOf: 10, FetchedAt: 1},
		{SymbolID: a.ID, Metric: "SharesOutstanding", Value: 200, AsOf: 20, FetchedAt: 2},
		// AAA: a different metric must NOT leak into the result.
		{SymbolID: a.ID, Metric: "Revenues", Value: 999, AsOf: 30, FetchedAt: 3},
		// BBB: single row.
		{SymbolID: b.ID, Metric: "SharesOutstanding", Value: 50, AsOf: 15, FetchedAt: 4},
	}
	for _, r := range seed {
		if err := st.UpsertFundamental(ctx, r); err != nil {
			t.Fatalf("seed %+v: %v", r, err)
		}
	}

	got, err := st.LatestMetricAll(ctx, "SharesOutstanding")
	if err != nil {
		t.Fatalf("LatestMetricAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (one row per symbol): %+v", len(got), got)
	}
	if got[a.ID].Value != 200 || got[a.ID].AsOf != 20 {
		t.Errorf("AAA latest = %+v, want value 200 @ as_of 20", got[a.ID])
	}
	if got[b.ID].Value != 50 {
		t.Errorf("BBB latest = %+v, want value 50", got[b.ID])
	}

	// A metric with no rows anywhere → empty map, not an error.
	none, err := st.LatestMetricAll(ctx, "NoSuchMetric")
	if err != nil || len(none) != 0 {
		t.Fatalf("empty metric: len=%d err=%v", len(none), err)
	}
}

// TestLastTwoDailyCloses locks the /api/movers batching contract: ONE query
// returns every symbol's (last, prev) daily close — replacing the old
// ~2-queries-per-active-stock-per-request walk.
func TestLastTwoDailyCloses(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "closes.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	c, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")

	day := int64(86400)
	base := int64(1_750_000_000)
	err = st.UpsertBars(ctx, []md.Bar{
		// AAA: three daily bars — only the newest two count.
		{SymbolID: a.ID, TF: md.TF1d, Ts: base - 2*day, Open: 90, High: 90, Low: 90, Close: 90},
		{SymbolID: a.ID, TF: md.TF1d, Ts: base - day, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: a.ID, TF: md.TF1d, Ts: base, Open: 110, High: 110, Low: 110, Close: 110},
		// BBB: a single daily bar — prev must be 0 (movers skips it, honestly).
		{SymbolID: b.ID, TF: md.TF1d, Ts: base, Open: 50, High: 50, Low: 50, Close: 50},
		// CCC: only 1m bars — must NOT appear at all (daily closes only).
		{SymbolID: c.ID, TF: md.TF1m, Ts: base, Open: 7, High: 7, Low: 7, Close: 7},
		{SymbolID: c.ID, TF: md.TF1m, Ts: base + 60, Open: 8, High: 8, Low: 8, Close: 8},
	})
	if err != nil {
		t.Fatalf("seed bars: %v", err)
	}

	got, err := st.LastTwoDailyCloses(ctx)
	if err != nil {
		t.Fatalf("LastTwoDailyCloses: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (AAA + BBB): %+v", len(got), got)
	}
	if dc := got[a.ID]; dc.Last != 110 || dc.Prev != 100 || dc.Ts != base {
		t.Errorf("AAA = %+v, want last 110 prev 100 ts %d", dc, base)
	}
	if dc := got[b.ID]; dc.Last != 50 || dc.Prev != 0 || dc.Ts != base {
		t.Errorf("BBB = %+v, want last 50 prev 0 (single bar)", dc)
	}
	if _, has := got[c.ID]; has {
		t.Errorf("CCC has no daily bars but appeared: %+v", got[c.ID])
	}
}
