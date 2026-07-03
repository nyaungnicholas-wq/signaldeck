package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func discoveryTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "discovery.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestUpsertCandidateBumpsAndRefreshes(t *testing.T) {
	st := discoveryTestStore(t)
	ctx := context.Background()

	// First sighting.
	if err := st.UpsertCandidate(ctx, Candidate{
		Symbol: "COIN", Market: md.Stocks, LastSeenTs: 100, DollarVol: 1.2e9, PctChange: 3.4,
	}); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	cands, err := st.Candidates(ctx, "new")
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1", len(cands))
	}
	c := cands[0]
	if c.SeenCount != 1 || c.FirstSeenTs != 100 || c.LastSeenTs != 100 || c.Status != "new" {
		t.Fatalf("first sighting row: %+v", c)
	}

	// Second sighting: seen_count bumps, last_seen moves, metrics refresh,
	// first_seen stays.
	if err := st.UpsertCandidate(ctx, Candidate{
		Symbol: "COIN", Market: md.Stocks, LastSeenTs: 200, DollarVol: 2.5e9, PctChange: -1.1,
	}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	cands, _ = st.Candidates(ctx, "new")
	c = cands[0]
	if c.SeenCount != 2 || c.FirstSeenTs != 100 || c.LastSeenTs != 200 {
		t.Fatalf("second sighting row: %+v", c)
	}
	if c.DollarVol != 2.5e9 || c.PctChange != -1.1 {
		t.Fatalf("metrics not refreshed: %+v", c)
	}

	// Zero metrics on a later sweep (partial screener data) must NOT wipe the
	// known values.
	if err := st.UpsertCandidate(ctx, Candidate{
		Symbol: "COIN", Market: md.Stocks, LastSeenTs: 300,
	}); err != nil {
		t.Fatalf("upsert 3: %v", err)
	}
	cands, _ = st.Candidates(ctx, "new")
	c = cands[0]
	if c.SeenCount != 3 || c.DollarVol != 2.5e9 || c.PctChange != -1.1 {
		t.Fatalf("zero metrics overwrote known values: %+v", c)
	}
}

func TestCandidatesFilterAndRanking(t *testing.T) {
	st := discoveryTestStore(t)
	ctx := context.Background()

	for _, c := range []Candidate{
		{Symbol: "AAA", Market: md.Stocks, LastSeenTs: 10, DollarVol: 1e6},
		{Symbol: "BBB", Market: md.Stocks, LastSeenTs: 10, DollarVol: 9e9},
		{Symbol: "CCC", Market: md.Stocks, LastSeenTs: 10, DollarVol: 5e8},
	} {
		if err := st.UpsertCandidate(ctx, c); err != nil {
			t.Fatalf("upsert %s: %v", c.Symbol, err)
		}
	}
	if err := st.SetCandidateStatus(ctx, "CCC", md.Stocks, "dismissed"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	fresh, err := st.Candidates(ctx, "new")
	if err != nil {
		t.Fatalf("candidates(new): %v", err)
	}
	if len(fresh) != 2 || fresh[0].Symbol != "BBB" || fresh[1].Symbol != "AAA" {
		t.Fatalf("new candidates ranked wrong: %+v", fresh)
	}

	dismissed, _ := st.Candidates(ctx, "dismissed")
	if len(dismissed) != 1 || dismissed[0].Symbol != "CCC" {
		t.Fatalf("dismissed filter: %+v", dismissed)
	}

	all, _ := st.Candidates(ctx, "")
	if len(all) != 3 {
		t.Fatalf("all candidates: got %d want 3", len(all))
	}
}

func TestCandidateStatusTransitionsSurviveResweep(t *testing.T) {
	st := discoveryTestStore(t)
	ctx := context.Background()

	if err := st.UpsertCandidate(ctx, Candidate{Symbol: "XYZ", Market: md.Stocks, LastSeenTs: 1, DollarVol: 1e7}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.SetCandidateStatus(ctx, "XYZ", md.Stocks, "dismissed"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	// A later sweep sees it again: sighting bookkeeping updates but the
	// dismissal sticks.
	if err := st.UpsertCandidate(ctx, Candidate{Symbol: "XYZ", Market: md.Stocks, LastSeenTs: 2, DollarVol: 2e7}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	all, _ := st.Candidates(ctx, "dismissed")
	if len(all) != 1 || all[0].SeenCount != 2 || all[0].Status != "dismissed" {
		t.Fatalf("dismissal did not survive resweep: %+v", all)
	}

	// Invalid status is rejected by the CHECK constraint.
	if err := st.SetCandidateStatus(ctx, "XYZ", md.Stocks, "bogus"); err == nil {
		t.Fatalf("bogus status accepted")
	}

	// added transition works.
	if err := st.SetCandidateStatus(ctx, "XYZ", md.Stocks, "added"); err != nil {
		t.Fatalf("added transition: %v", err)
	}
	added, _ := st.Candidates(ctx, "added")
	if len(added) != 1 {
		t.Fatalf("added filter: %+v", added)
	}
}

func TestActiveSymbolCount(t *testing.T) {
	st := discoveryTestStore(t)
	ctx := context.Background()

	n, err := st.ActiveSymbolCount(ctx)
	if err != nil || n != 0 {
		t.Fatalf("empty count: %d, %v", n, err)
	}
	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	_, _ = st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if n, _ = st.ActiveSymbolCount(ctx); n != 2 {
		t.Fatalf("count after 2 upserts: %d want 2", n)
	}
	// Deactivation frees budget.
	if err := st.SetSymbolActive(ctx, a.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if n, _ = st.ActiveSymbolCount(ctx); n != 1 {
		t.Fatalf("count after deactivate: %d want 1", n)
	}
}
