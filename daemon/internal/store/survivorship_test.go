package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Survivorship bias is not caused by deleting data here — it is caused by
// research only ever LOOKING at active=1. These tests pin that the dead stay
// visible to research while staying out of live trading paths.

func newSurvStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestResearchUniverseIncludesTheDead(t *testing.T) {
	ctx := context.Background()
	st := newSurvStore(t)
	liveSym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	live := liveSym.ID
	deadSym, _ := st.UpsertSymbol(ctx, "GONE", md.Stocks, "Delisted Corp")
	dead := deadSym.ID
	if err := st.SetSymbolActive(ctx, dead, false); err != nil {
		t.Fatal(err)
	}

	active, err := st.ActiveStockSymbols(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != live {
		t.Fatalf("live trading must see only active symbols, got %+v", active)
	}

	all, err := st.ResearchUniverse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("research must see the dead too, got %d: %+v", len(all), all)
	}
	var sawDead bool
	for _, s := range all {
		if s.ID == dead {
			sawDead = true
			if s.Active {
				t.Fatal("delisted symbol should report Active=false")
			}
		}
	}
	if !sawDead {
		t.Fatal("delisted symbol missing from the research universe")
	}
}

func TestUniverseCoverageDeclaresTheSplit(t *testing.T) {
	ctx := context.Background()
	st := newSurvStore(t)
	_, _ = st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	_, _ = st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	deadSym, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")
	dead := deadSym.ID
	_ = st.SetSymbolActive(ctx, dead, false)

	total, active, inactive, err := st.UniverseCoverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || active != 2 || inactive != 1 {
		t.Fatalf("coverage = %d/%d/%d, want 3/2/1", total, active, inactive)
	}
}

func TestTradableAtReconstructsPointInTime(t *testing.T) {
	ctx := context.Background()
	st := newSurvStore(t)
	oldSym, _ := st.UpsertSymbol(ctx, "OLD", md.Stocks, "")
	old := oldSym.ID
	// added_at is stamped with real wall-clock time, so the point-in-time
	// probes must straddle it rather than use synthetic epochs.
	delistTs := oldSym.AddedAt + 86400
	// A symbol that delisted partway through the window must be IN the universe
	// before its delisting and OUT after — the whole point of the marker.
	if err := st.MarkDelisted(ctx, old, delistTs); err != nil {
		t.Fatal(err)
	}

	before, err := st.TradableAt(ctx, oldSym.AddedAt+3600)
	if err != nil {
		t.Fatal(err)
	}
	var inBefore bool
	for _, s := range before {
		if s.ID == old {
			inBefore = true
		}
	}
	if !inBefore {
		t.Fatal("a symbol must be tradable BEFORE it delists")
	}

	after, err := st.TradableAt(ctx, delistTs+3600)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range after {
		if s.ID == old {
			t.Fatal("a delisted symbol must not be tradable after its delisting")
		}
	}
}

func TestMarkDelistedIsIdempotentAndKeepsFirstDate(t *testing.T) {
	ctx := context.Background()
	st := newSurvStore(t)
	idSym, _ := st.UpsertSymbol(ctx, "DEAD", md.Stocks, "")
	id := idSym.ID
	first := idSym.AddedAt + 86400
	if err := st.MarkDelisted(ctx, id, first); err != nil {
		t.Fatal(err)
	}
	// A later call must NOT move the date — the first observation is the fact.
	if err := st.MarkDelisted(ctx, id, first+100_000); err != nil {
		t.Fatal(err)
	}
	rows, err := st.TradableAt(ctx, first+50_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rows {
		if s.ID == id {
			t.Fatal("delisting date was overwritten by a later call")
		}
	}
}

func TestDailyBarsAreNeverPruned(t *testing.T) {
	// The permanence guarantee that makes all of the above possible: if daily
	// history could be pruned, a survivorship-clean universe would be empty
	// for the dead names.
	st := newSurvStore(t)
	if _, err := st.PruneBars(context.Background(), md.TF1d, 1<<40); err == nil {
		t.Fatal("PruneBars must refuse daily bars in code, not by convention")
	}
}
