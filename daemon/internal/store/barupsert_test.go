package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// totalChanges is the main writer's running count of rows written.
func totalChanges(t *testing.T, st *Store) int64 {
	t.Helper()
	var n int64
	if err := st.w.QueryRowContext(context.Background(), `SELECT total_changes()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Re-sending bars that are already stored must write nothing: universe-poller
// re-upserts ~826k bars every boot and the Downsampler re-rolls 72h of hours
// every 5 minutes, and as INSERT OR REPLACE each one rewrote its page into the
// WAL (1.6 GB in the first 6 minutes after the 2026-10-02 03:30 boot). Changed
// and new bars must still land.
func TestBarUpsertsSkipUnchangedRows(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	bars := make([]md.Bar, 0, 2*180)
	for s := int64(1); s <= 2; s++ {
		for m := int64(0); m < 180; m++ { // three hours of minutes
			bars = append(bars, md.Bar{SymbolID: s, TF: md.TF1m, Ts: 3600 + m*60,
				Open: float64(m), High: float64(m) + 1, Low: float64(m) - 1, Close: float64(m) + 0.5, Volume: 10})
		}
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	c0 := totalChanges(t, st)
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	if d := totalChanges(t, st) - c0; d != 0 {
		t.Fatalf("re-sending %d stored bars wrote %d rows, want 0", len(bars), d)
	}

	// Ten changed bars and five new ones: exactly fifteen writes.
	again := append([]md.Bar(nil), bars[:10]...)
	for i := range again {
		again[i].Close += 100
	}
	for m := int64(0); m < 5; m++ {
		again = append(again, md.Bar{SymbolID: 3, TF: md.TF1m, Ts: 3600 + m*60, Open: 1, High: 1, Low: 1, Close: 1})
	}
	c1 := totalChanges(t, st)
	if err := st.UpsertBars(ctx, again); err != nil {
		t.Fatal(err)
	}
	if d := totalChanges(t, st) - c1; d != 15 {
		t.Fatalf("10 changed + 5 new bars wrote %d rows, want 15", d)
	}
	got, err := st.Bars(ctx, 1, md.TF1m, 3600, 3601, 0)
	if err != nil || len(got) != 1 || got[0].Close != bars[0].Close+100 {
		t.Fatalf("changed bar not stored: %+v %v", got, err)
	}

	// The same holds for the hourly rollup: a second pass over unchanged minutes
	// writes nothing; one changed minute rewrites one bucket.
	if err := st.Rollup(ctx, 1, md.TF1m, md.TF1h, 3600, 0, 4*3600); err != nil {
		t.Fatal(err)
	}
	c2 := totalChanges(t, st)
	if err := st.Rollup(ctx, 1, md.TF1m, md.TF1h, 3600, 0, 4*3600); err != nil {
		t.Fatal(err)
	}
	if d := totalChanges(t, st) - c2; d != 0 {
		t.Fatalf("re-rolling unchanged minutes wrote %d hourly bars, want 0", d)
	}
	last := bars[179] // symbol 1, last minute of hour 3 (ts 3600+179*60 = 14340)
	last.Close = 999
	if err := st.UpsertBars(ctx, []md.Bar{last}); err != nil {
		t.Fatal(err)
	}
	c3 := totalChanges(t, st)
	if err := st.Rollup(ctx, 1, md.TF1m, md.TF1h, 3600, 0, 4*3600); err != nil {
		t.Fatal(err)
	}
	if d := totalChanges(t, st) - c3; d != 1 {
		t.Fatalf("one changed minute rewrote %d hourly bars, want 1", d)
	}
	hours, err := st.Bars(ctx, 1, md.TF1h, 10800, 10801, 0)
	if err != nil || len(hours) != 1 || hours[0].Close != 999 {
		t.Fatalf("hour 3 close not updated: %+v %v", hours, err)
	}
}
