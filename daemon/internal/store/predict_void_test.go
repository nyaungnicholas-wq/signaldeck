package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestResolvedPredictionPairsSkipsVoidedRows(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "void.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "VOID", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := st.SeedBenchmarkOutcome(ctx, sym.ID, md.H1d, 1_700_000_000, 0.6); err != nil {
		t.Fatal(err)
	}
	if err := st.SeedBenchmarkOutcome(ctx, sym.ID, md.H1d, 1_700_000_060, 0.6); err != nil {
		t.Fatal(err)
	}

	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, 1_700_000_000, 0.01); err != nil {
		t.Fatal(err)
	}

	if _, err := st.DB().ExecContext(ctx, `UPDATE prediction_outcomes SET resolved_at = ? WHERE symbol_id = ? AND ts = ?`, 1_700_100_000, sym.ID, 1_700_000_060); err != nil {
		t.Fatal(err)
	}

	probs, ups, err := st.ResolvedPredictionPairs(ctx, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(probs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(probs))
	}
	if probs[0] != 0.6 {
		t.Fatalf("expected prob 0.6, got %v", probs[0])
	}
	if ups[0] != 1 {
		t.Fatalf("expected up 1, got %v", ups[0])
	}

	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM prediction_outcomes WHERE horizon='1d' AND resolved_at IS NOT NULL AND up IS NOT NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected dashboard count 1, got %d", n)
	}

	var raw int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM prediction_outcomes WHERE horizon='1d' AND resolved_at IS NOT NULL`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != 2 {
		t.Fatalf("expected raw resolved count 2, got %d", raw)
	}
}
