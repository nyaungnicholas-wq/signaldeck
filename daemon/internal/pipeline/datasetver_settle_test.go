package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func countRevised(t *testing.T, st *store.Store) int {
	ctx := context.Background()
	events, err := st.RecentDQ(ctx, 1000)
	if err != nil {
		t.Fatalf("failed to fetch DQ events: %v", err)
	}
	var c int
	for _, e := range events {
		if e.Kind == "dataset_revised" {
			c++
		}
	}
	return c
}

func TestDatasetVersionIgnoresTheFormingBar(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "dsv.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "DSV", md.Stocks, "DSV")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	id := sym.ID

	now := time.Date(2026, time.September, 23, 18, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		ts := now.AddDate(0, 0, -i).Truncate(24 * time.Hour).Unix()
		close := 100.0 + float64(i)
		bar := md.Bar{
			SymbolID: id,
			TF:       md.TF1d,
			Ts:       ts,
			Open:     close,
			High:     close,
			Low:      close,
			Close:    close,
			Volume:   1000,
		}
		if err := st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
			t.Fatalf("upsert bar i=%d: %v", i, err)
		}
	}

	runner := &DatasetVersionRunner{St: st, Now: func() time.Time { return now }}

	detail, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("baseline Run failed: %v (detail=%s)", err, detail)
	}
	if countRevised(t, st) != 0 {
		t.Fatalf("unexpected revised event after baseline Run (detail=%s)", detail)
	}

	for _, i := range []int{0, 1} {
		ts := now.AddDate(0, 0, -i).Truncate(24 * time.Hour).Unix()
		bar := md.Bar{
			SymbolID: id,
			TF:       md.TF1d,
			Ts:       ts,
			Open:     999.0,
			High:     999.0,
			Low:      999.0,
			Close:    999.0,
			Volume:   1000,
		}
		if err := st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
			t.Fatalf("upsert modified bar i=%d: %v", i, err)
		}
	}

	detail2, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("second Run failed: %v (detail=%s)", err, detail2)
	}
	if got := countRevised(t, st); got != 0 {
		t.Fatalf("expected 0 revised events after changing forming bars, got %d (detail=%s)", got, detail2)
	}
}

func TestDatasetVersionFlagsARewrittenSettledBar(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "dsv.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "DSV", md.Stocks, "DSV")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	id := sym.ID

	now := time.Date(2026, time.September, 23, 18, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		ts := now.AddDate(0, 0, -i).Truncate(24 * time.Hour).Unix()
		close := 100.0 + float64(i)
		bar := md.Bar{
			SymbolID: id,
			TF:       md.TF1d,
			Ts:       ts,
			Open:     close,
			High:     close,
			Low:      close,
			Close:    close,
			Volume:   1000,
		}
		if err := st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
			t.Fatalf("upsert bar i=%d: %v", i, err)
		}
	}

	runner := &DatasetVersionRunner{St: st, Now: func() time.Time { return now }}

	detail, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("baseline Run failed: %v (detail=%s)", err, detail)
	}
	if countRevised(t, st) != 0 {
		t.Fatalf("unexpected revised event after baseline Run (detail=%s)", detail)
	}

	i := 10
	ts := now.AddDate(0, 0, -i).Truncate(24 * time.Hour).Unix()
	bar := md.Bar{
		SymbolID: id,
		TF:       md.TF1d,
		Ts:       ts,
		Open:     999.0,
		High:     999.0,
		Low:      999.0,
		Close:    999.0,
		Volume:   1000,
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
		t.Fatalf("upsert modified bar i=%d: %v", i, err)
	}

	detail2, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("second Run failed: %v (detail=%s)", err, detail2)
	}
	if got := countRevised(t, st); got != 1 {
		t.Fatalf("expected 1 revised event after changing settled bar, got %d (detail=%s)", got, detail2)
	}
}

func TestDatasetVersionRebaselinesAPreLagSnapshot(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "dsv.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "DSV", md.Stocks, "DSV")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	id := sym.ID

	now := time.Date(2026, time.September, 23, 18, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		ts := now.AddDate(0, 0, -i).Truncate(24 * time.Hour).Unix()
		close := 100.0 + float64(i)
		bar := md.Bar{
			SymbolID: id,
			TF:       md.TF1d,
			Ts:       ts,
			Open:     close,
			High:     close,
			Low:      close,
			Close:    close,
			Volume:   1000,
		}
		if err := st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
			t.Fatalf("upsert bar i=%d: %v", i, err)
		}
	}

	oldestTS := now.AddDate(0, 0, -29).Truncate(24 * time.Hour).Unix()
	newestTS := now.Truncate(24 * time.Hour).Unix()

	dv := store.DatasetVersion{
		SymbolID:  id,
		Timeframe: "1d",
		FirstTs:   oldestTS,
		LastTs:    newestTS,
		N:         30,
		Hash:      "stale-hash-from-before-the-lag",
		CheckedAt: now.Add(-time.Hour).Unix(),
		Revisions: 0,
	}
	if err := st.UpsertDatasetVersion(ctx, dv); err != nil {
		t.Fatalf("upsert dataset version: %v", err)
	}

	runner := &DatasetVersionRunner{St: st, Now: func() time.Time { return now }}

	detail, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("baseline Run failed: %v (detail=%s)", err, detail)
	}
	if countRevised(t, st) != 0 {
		t.Fatalf("unexpected revised event after baseline Run with pre-lag version (detail=%s)", detail)
	}

	i := 10
	ts := now.AddDate(0, 0, -i).Truncate(24 * time.Hour).Unix()
	bar := md.Bar{
		SymbolID: id,
		TF:       md.TF1d,
		Ts:       ts,
		Open:     999.0,
		High:     999.0,
		Low:      999.0,
		Close:    999.0,
		Volume:   1000,
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
		t.Fatalf("upsert modified bar i=%d: %v", i, err)
	}

	detail2, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("second Run failed: %v (detail=%s)", err, detail2)
	}
	if got := countRevised(t, st); got != 1 {
		t.Fatalf("expected 1 revised event after changing settled bar with pre-lag version, got %d (detail=%s)", got, detail2)
	}
}

// A snapshot hashed BEFORE the settle lag existed, and not re-checked until days
// later, is still a pre-lag snapshot: its last bar was forming when IT was hashed.
// Judging that by today's clock compared it and raised a false revision.
// Its revision count survives the re-baseline.
func TestDatasetVersionRebaselinesAnOldPreLagSnapshotCheckedLate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "dsv.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "DSV", md.Stocks, "DSV")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 23, 18, 0, 0, 0, time.UTC)
	day := func(i int) int64 { return now.AddDate(0, 0, -i).Truncate(24 * time.Hour).Unix() }
	for i := 0; i < 30; i++ {
		c := 100.0 + float64(i)
		if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1d, Ts: day(i), Open: c, High: c, Low: c, Close: c, Volume: 1000}}); err != nil {
			t.Fatal(err)
		}
	}
	// Hashed 6 days ago, ending on the bar that was forming then.
	if err := st.UpsertDatasetVersion(ctx, store.DatasetVersion{
		SymbolID: sym.ID, Timeframe: "1d", FirstTs: day(29), LastTs: day(6), N: 24,
		Hash: "pre-lag-hash", CheckedAt: day(6) + 3600, Revisions: 2,
	}); err != nil {
		t.Fatal(err)
	}
	runner := &DatasetVersionRunner{St: st, Now: func() time.Time { return now }}
	if detail, err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v (%s)", err, detail)
	}
	if got := countRevised(t, st); got != 0 {
		t.Fatalf("an old pre-lag snapshot raised %d false revision(s)", got)
	}
	v, ok, err := st.DatasetVersion(ctx, sym.ID, "1d")
	if err != nil || !ok {
		t.Fatalf("DatasetVersion: ok=%v err=%v", ok, err)
	}
	if v.Revisions != 2 {
		t.Fatalf("re-baseline reset the revision count to %d; want 2", v.Revisions)
	}
}
