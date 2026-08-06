package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The gate reads store.PriorDayCrossSection, so the wiring that matters is:
// it must take the most recent COMPLETE day (never today), dedup to one row per
// symbol the way the accuracy registry does, prefer cal_prob, and hand back
// something MeasureCrossSection classifies the way the live day behaved.
func TestPriorDayCrossSectionDrivesTheGate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	h := md.Horizon("1d")

	// Days relative to now, so the `< date('now')` clause behaves as in prod.
	day := func(back int, hour int) int64 {
		return time.Now().UTC().AddDate(0, 0, -back).
			Truncate(24 * time.Hour).Add(time.Duration(hour) * time.Hour).Unix()
	}
	write := func(ts int64, probs []float64) {
		t.Helper()
		for i, p := range probs {
			if err := st.UpsertPrediction(ctx, store.Prediction{
				SymbolID: int64(i + 1), Horizon: h, Ts: ts,
				RawProb: 0.5, CalProb: p, NUsed: 3,
				Components: "{}", Weights: "{}", Basis: "test",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Two days back — the 2026-08-03 shape: 5 distinct values across 300 symbols.
	collapsed := make([]float64, 300)
	for i := range collapsed {
		collapsed[i] = 0.515 + 0.0165*float64(i%5)
	}
	write(day(2, 14), collapsed)

	got, d, err := st.PriorDayCrossSection(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 300 || d == "" {
		t.Fatalf("PriorDayCrossSection = %d rows on %q, want 300 on the prior day", len(got), d)
	}
	if cs := ensemble.MeasureCrossSection(got); func() bool { ok, _ := cs.Usable(); return ok }() {
		t.Fatalf("collapsed cross-section must gate (distinct=%d spread=%.4f)", cs.Distinct, cs.Spread)
	}

	// ONE day back, and written TWICE — an earlier degenerate pass then a later
	// healthy one. The registry keeps the last row per symbol per day, so this
	// must read the repaired sweep, not a pool of both.
	write(day(1, 10), collapsed)
	repaired := make([]float64, 300)
	for i := range repaired {
		repaired[i] = 0.36 + 0.296*float64(i)/299.0
	}
	write(day(1, 20), repaired)

	got, d, err = st.PriorDayCrossSection(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 300 {
		t.Fatalf("got %d rows, want 300 — must dedup to one row per symbol, not pool passes", len(got))
	}
	cs := ensemble.MeasureCrossSection(got)
	if cs.Distinct != 300 {
		t.Fatalf("distinct=%d, want 300 — the LAST pass of the day must win the dedup", cs.Distinct)
	}
	if ok, reason := cs.Usable(); !ok {
		t.Fatalf("repaired cross-section must publish, refused: %s", reason)
	}

	// Today's rows must never be measured: the gate would then read the pass it
	// is in the middle of writing.
	write(day(0, 1), collapsed)
	got, _, err = st.PriorDayCrossSection(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if cs := ensemble.MeasureCrossSection(got); cs.Distinct != 300 {
		t.Fatalf("distinct=%d after writing today — today must be excluded", cs.Distinct)
	}

	// A horizon never written must not read as collapsed. A cold start and a
	// degenerate fleet have to be distinguishable, or the gate wedges itself shut
	// on an empty database and nothing is ever published again.
	empty, d, err := st.PriorDayCrossSection(ctx, md.Horizon("1w"))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 || d != "" {
		t.Fatalf("unwritten horizon returned %d rows on %q, want 0 and empty", len(empty), d)
	}
}
