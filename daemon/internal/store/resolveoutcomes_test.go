package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The outcome resolver pages its queue on a (ts, symbol_id) keyset and writes
// its verdicts outcomeWriteChunk rows per transaction. More rows than two
// chunks, on two symbols sharing timestamps: paging returns every row once, in
// order, and every write lands, graded or void.
func TestResolveOutcomesChunksAndKeysetPages(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	const n = 230
	for i := int64(0); i < n; i++ {
		ts := 1000 + i
		if err := st.InsertScore(ctx, md.Score{SymbolID: a.ID, Horizon: md.H1d, Ts: ts, Score: 0.5}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertScore(ctx, md.Score{SymbolID: b.ID, Horizon: md.H1d, Ts: ts, Score: 0.5}); err != nil {
			t.Fatal(err)
		}
	}

	afterTs, afterSym := int64(-1), int64(0)
	var seen []md.ScoreOutcome
	for {
		page, err := st.UnresolvedOutcomesByHorizon(ctx, md.H1d, 1000+n, afterTs, afterSym, 37)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, page...)
		if len(page) < 37 {
			break
		}
		last := page[len(page)-1]
		afterTs, afterSym = last.Ts, last.SymbolID
	}
	if len(seen) != 2*n {
		t.Fatalf("expected %d rows, got %d", 2*n, len(seen))
	}
	for i := 1; i < len(seen); i++ {
		p, c := seen[i-1], seen[i]
		if c.Ts < p.Ts || (c.Ts == p.Ts && c.SymbolID <= p.SymbolID) {
			t.Fatalf("page order broken at %d: %+v then %+v", i, p, c)
		}
	}

	page, err := st.UnresolvedOutcomesByHorizon(ctx, md.H1d, 1009, -1, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 20 {
		t.Fatalf("expected 20 rows for cutoff 1009, got %d", len(page))
	}

	ws := make([]OutcomeWrite, 0, len(seen))
	for i, o := range seen {
		ws = append(ws, OutcomeWrite{
			SymbolID:  o.SymbolID,
			Ts:        o.Ts,
			FwdReturn: float64(i) / 1000,
			Void:      i%3 == 0,
		})
	}
	if err := st.ResolveOutcomes(ctx, md.H1d, ws); err != nil {
		t.Fatal(err)
	}

	rest, err := st.UnresolvedOutcomesByHorizon(ctx, md.H1d, 1000+n, -1, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Fatalf("%d rows still pending after ResolveOutcomes", len(rest))
	}

	expectedVoids := 0
	for i := 0; i < 2*n; i++ {
		if i%3 == 0 {
			expectedVoids++
		}
	}
	for _, id := range []int64{a.ID, b.ID} {
		got, err := st.ResolvedOutcomes(ctx, id, md.H1d, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n {
			t.Fatalf("symbol %d: expected %d resolved outcomes, got %d", id, n, len(got))
		}
		voids := 0
		for _, o := range got {
			if o.FwdReturn == nil {
				voids++
			}
			if o.ResolvedAt == nil {
				t.Fatalf("symbol %d: resolved outcome has nil ResolvedAt", id)
			}
		}
		expectedVoids -= voids
	}
	if expectedVoids != 0 {
		t.Fatalf("total voids mismatch, expected 0, got %d", -expectedVoids)
	}

	expected := make(map[[2]int64]float64)
	for i, o := range seen {
		if i%3 != 0 {
			key := [2]int64{o.SymbolID, o.Ts}
			expected[key] = float64(i) / 1000
		}
	}
	for _, id := range []int64{a.ID, b.ID} {
		got, err := st.ResolvedOutcomes(ctx, id, md.H1d, 10000)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range got {
			if o.FwdReturn != nil {
				key := [2]int64{o.SymbolID, o.Ts}
				if exp, ok := expected[key]; !ok {
					t.Fatalf("unexpected resolved outcome: %+v", o)
				} else if *o.FwdReturn != exp {
					t.Fatalf("mismatch for %+v: expected %v, got %v", o, exp, *o.FwdReturn)
				}
				delete(expected, key)
			}
		}
	}
	if len(expected) != 0 {
		t.Fatalf("some expected resolved outcomes not found: %v", expected)
	}

	if err := st.ResolveOutcomes(ctx, md.H1d, nil); err != nil {
		t.Fatal(err)
	}
}
