package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The retention delete now runs in reader-found batches. It must still delete
// EXACTLY the rows below the cutoff — across several 500-row writes and more
// than one 20,000-row reader pass — and nothing at or above it.
func TestDeleteScoreOutcomesBeforeIsExactAcrossBatches(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "so.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	const cutoff = 1_000_000
	// 21,000 rows below the cutoff (two reader passes) and 700 at or above it,
	// spread over 30 symbols and two horizons.
	if _, err := st.w.Exec(`
		WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < 21699)
		INSERT INTO score_outcomes (symbol_id, horizon, ts, score)
		SELECT i % 30, CASE WHEN i % 2 = 0 THEN '1d' ELSE '1w' END,
		       CASE WHEN i < 21000 THEN i ELSE ? + (i - 21000) END, 0.5
		FROM n`, cutoff); err != nil {
		t.Fatal(err)
	}
	deleted, err := st.DeleteScoreOutcomesBefore(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 21000 {
		t.Fatalf("deleted %d rows, want exactly the 21000 below the cutoff", deleted)
	}
	var below, kept int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM score_outcomes WHERE ts < ?`, cutoff).Scan(&below); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM score_outcomes WHERE ts >= ?`, cutoff).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if below != 0 || kept != 700 {
		t.Fatalf("after the delete: %d rows below the cutoff (want 0), %d at or above (want 700)", below, kept)
	}
	// A second run finds nothing and deletes nothing.
	if again, err := st.DeleteScoreOutcomesBefore(ctx, cutoff); err != nil || again != 0 {
		t.Fatalf("second run: deleted %d, err %v; want 0, nil", again, err)
	}
}

// UpsertBars now commits in chunks; every bar of a call larger than one chunk
// must land, and a repeat must stay idempotent.
func TestUpsertBarsAcrossChunks(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "bars.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "CHUNK", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	n := 2*upsertBarsChunk + 37
	bars := make([]md.Bar, 0, n)
	for i := 0; i < n; i++ {
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1m, Ts: int64(i) * 60,
			Open: 1, High: 1, Low: 1, Close: float64(i), Volume: 1})
	}
	for pass := 0; pass < 2; pass++ {
		if err := st.UpsertBars(ctx, bars); err != nil {
			t.Fatal(err)
		}
		var got int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM bars WHERE symbol_id=?`, sym.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != n {
			t.Fatalf("pass %d: %d bars stored, want %d", pass, got, n)
		}
	}
}
