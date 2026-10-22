package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestCandidatesNewHidesRowsSeenBeforeFreshSince(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "cand.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	candidates := []Candidate{
		{Symbol: "OLD", Market: marketdata.Stocks, LastSeenTs: 1000, DollarVol: 1},
		{Symbol: "FRESH", Market: marketdata.Stocks, LastSeenTs: 3000, DollarVol: 1},
	}

	for _, c := range candidates {
		if err := st.UpsertCandidate(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	if got, err := st.Candidates(ctx, "new"); err != nil || len(got) != 2 {
		t.Fatalf("Expected 2 rows, got %v, err %v", got, err)
	}

	if err := st.SetMeta(ctx, CandidatesFreshSinceKey, "2000"); err != nil {
		t.Fatal(err)
	}

	got, err := st.Candidates(ctx, "new")
	if err != nil || len(got) != 1 || got[0].Symbol != "FRESH" {
		t.Fatalf("Expected 1 row with Symbol 'FRESH', got %v, err %v", got, err)
	}

	if got, err := st.Candidates(ctx, ""); err != nil || len(got) != 2 {
		t.Fatalf("Expected 2 rows, got %v, err %v", got, err)
	}

	candidates[0].LastSeenTs = 4000
	if err := st.UpsertCandidate(ctx, candidates[0]); err != nil {
		t.Fatal(err)
	}

	if got, err := st.Candidates(ctx, "new"); err != nil || len(got) != 2 {
		t.Fatalf("Expected 2 rows, got %v, err %v", got, err)
	}
}