// The re-admission gate must grade the SAME rows the accuracy registry grades.
//
// Re-admission is the one coded door back to emitting for a retired model, and
// it decides by comparing an ABSOLUTE record against an absolute null. That is
// exactly the comparison survivorship contamination distorts: rows written
// before the survivorship epoch were graded against a universe seeded with
// survivors, which is why tools/accuracy_registry.py excludes them outright and
// internal/pipeline's live majority benchmark refuses to learn from them.
//
// Measured on the live DB (2026-07-27): the 1d shadow record ran 13,065 rows
// over 24 distinct days, of which 21 rows over 3 days were post-epoch. The
// gate's 20-distinct-day floor was therefore satisfied ENTIRELY by rows the
// platform's own grader had already declared unusable.
package api

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedShadowDay writes one resolved observation for a symbol on a given UTC day.
func seedShadowDay(t *testing.T, st *store.Store, sym int64, day int64, prob float64, up bool) {
	t.Helper()
	ctx := context.Background()
	ts := day*86400 + 3600
	if err := st.InsertFeatures(ctx, sym, md.H1d, ts, 1, map[string]float64{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym, Horizon: md.H1d, Ts: ts, RawProb: prob, CalProb: prob,
		NUsed: 3, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	fwd := -0.01
	if up {
		fwd = 0.01
	}
	if err := st.ResolvePrediction(ctx, sym, md.H1d, ts, fwd); err != nil {
		t.Fatal(err)
	}
}

// A shadow record built mostly from pre-epoch rows must be graded on the
// post-epoch remainder alone. Anything else lets survivor-seeded days satisfy a
// day floor whose whole purpose is to demand real, clean evidence.
func TestDirectionalShadowExcludesPreSurvivorshipEpochRows(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "shadow_epoch.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "A")
	if err != nil {
		t.Fatal(err)
	}

	epochDay := store.SurvivorshipEpoch / 86400
	// 30 pre-epoch days, every call correct — the most flattering contaminated
	// record possible, and far past the 20-day re-admission floor on its own.
	const preDays = 30
	for i := int64(1); i <= preDays; i++ {
		seedShadowDay(t, st, sym.ID, epochDay-i, 0.9, true)
	}
	// 2 post-epoch days: the clean evidence, well below every floor.
	const postDays = 2
	for i := int64(0); i < postDays; i++ {
		seedShadowDay(t, st, sym.ID, epochDay+i, 0.9, true)
	}

	rec, ok := (Deps{St: st}).directionalShadow(ctx, md.H1d)
	if !ok {
		t.Fatal("no shadow record built")
	}
	if rec.Days != postDays {
		t.Fatalf("shadow record covers %d distinct days, want %d — pre-epoch "+
			"survivor-seeded days are counting toward the re-admission floor", rec.Days, postDays)
	}
	if rec.N != postDays {
		t.Fatalf("shadow record holds %d observations, want %d post-epoch rows", rec.N, postDays)
	}
	for _, tl := range rec.DayTallies {
		if tl.Day < epochDay {
			t.Fatalf("a pre-epoch day (%d < %d) survived into the graded tallies", tl.Day, epochDay)
		}
	}
}

// The epoch the gate uses must be the one the registry and the live benchmark
// use. Three copies of a boundary is three chances for it to drift.
func TestSurvivorshipEpochMatchesRegistryBoundary(t *testing.T) {
	// 2026-07-24T00:00:00Z — SURVIVORSHIP_EPOCH in tools/accuracy_registry.py.
	const want = int64(1784851200)
	if store.SurvivorshipEpoch != want {
		t.Fatalf("store.SurvivorshipEpoch = %d, want %d (tools/accuracy_registry.py SURVIVORSHIP_EPOCH)",
			store.SurvivorshipEpoch, want)
	}
}
