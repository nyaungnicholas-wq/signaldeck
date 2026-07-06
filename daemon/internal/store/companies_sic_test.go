// Stage 4 tests — SIC bulk-sync store support: the one-query coverage counter
// and the one-transaction batch SIC update (by-CIK fan-out across share
// classes, blank-SIC skip, unknown-CIK zero-rows). t.TempDir stores only.
package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openSICStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "sic.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestCompanySICCoverage(t *testing.T) {
	st := openSICStore(t)
	ctx := context.Background()

	// Empty table: honest zeros (companies-sync hasn't run).
	total, with, err := st.CompanySICCoverage(ctx)
	if err != nil || total != 0 || with != 0 {
		t.Fatalf("empty coverage = %d/%d err=%v", with, total, err)
	}

	if err := st.UpsertCompanies(ctx, []CompanyRow{
		{CIK: 1, Ticker: "AAA", Name: "A", SIC: "3571", SICDesc: "Electronic Computers", UpdatedTs: 1},
		{CIK: 2, Ticker: "BBB", Name: "B", UpdatedTs: 1},
		{CIK: 3, Ticker: "CCC", Name: "C", UpdatedTs: 1},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	total, with, err = st.CompanySICCoverage(ctx)
	if err != nil || total != 3 || with != 1 {
		t.Fatalf("coverage = %d/%d err=%v, want 1/3", with, total, err)
	}
}

func TestBatchUpdateCompanySICByCIK(t *testing.T) {
	st := openSICStore(t)
	ctx := context.Background()
	if err := st.UpsertCompanies(ctx, []CompanyRow{
		// Two share classes on ONE CIK — both must be updated by that CIK.
		{CIK: 1652044, Ticker: "GOOGL", Name: "Alphabet Inc.", UpdatedTs: 1},
		{CIK: 1652044, Ticker: "GOOG", Name: "Alphabet Inc.", UpdatedTs: 1},
		{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc.", UpdatedTs: 1},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := st.BatchUpdateCompanySICByCIK(ctx, []SICUpdate{
		{CIK: 1652044, SIC: "7370", SICDesc: "Services-Computer Programming"},
		{CIK: 320193, SIC: "", SICDesc: "must be skipped"}, // blank SIC — never applied
		{CIK: 424242, SIC: "1234", SICDesc: "not in directory"},
	}, 99)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	// GOOGL + GOOG rows from the one CIK; the blank and the unknown add none.
	if n != 2 {
		t.Fatalf("rowsUpdated = %d, want 2", n)
	}
	rows, _ := st.ListCompanies(ctx, "", "", "")
	for _, r := range rows {
		switch r.Ticker {
		case "GOOGL", "GOOG":
			if r.SIC != "7370" || r.UpdatedTs != 99 {
				t.Fatalf("%s = %+v", r.Ticker, r)
			}
		case "AAPL":
			if r.SIC != "" {
				t.Fatalf("AAPL SIC = %q — a blank incoming SIC must never overwrite/land", r.SIC)
			}
		}
	}

	// Empty batch is a no-op, not an error.
	if n, err := st.BatchUpdateCompanySICByCIK(ctx, nil, 100); err != nil || n != 0 {
		t.Fatalf("empty batch: n=%d err=%v", n, err)
	}
}
