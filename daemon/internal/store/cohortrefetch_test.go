package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// THE COHORT RE-FETCH MUST BE RESUMABLE WITHOUT EATING ITS OWN REPAIR.
//
// The migration quarantines a symbol's old bars and then re-fetches it. That
// order is safe once. Run twice without a progress marker and the second pass
// quarantines the FRESH bars — the repair is filed away as if it were the
// defect, and the symbol is left with whatever the second fetch returns on top
// of an empty table. A partial network failure across 650 symbols makes a
// second pass certain, so the marker is not an optimisation, it is the thing
// that stops a resume from destroying the work.
//
// MUTATION CHECKS, both verified:
//   - make CohortForRefetch always report Refetched=false →
//     TestCohortRefetch_SecondPassSkipsWhatIsAlreadyDone fails, showing the
//     fresh bars captured into quarantine;
//   - drop the DELETE from QuarantineSymbolBars →
//     TestCohortRefetch_QuarantineEmptiesTheSymbol fails with the old bars still
//     in `bars`, which is what would leave two price conventions in one column.

func seedCohortSymbol(t *testing.T, st *Store, sym string, day0 int64, closes []float64) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.UpsertSymbol(ctx, sym, md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	bars := make([]md.Bar, 0, len(closes))
	for i, c := range closes {
		bars = append(bars, md.Bar{
			SymbolID: s.ID, TF: md.TF1d, Ts: (day0 + int64(i)) * 86400,
			Open: c, High: c * 1.01, Low: c * 0.99, Close: c, Volume: 100_000,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
	return s.ID
}

func TestCohortRefetch_QuarantineEmptiesTheSymbol(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	id := seedCohortSymbol(t, st, "CADE", 20000, []float64{10, 11, 12, 13})

	n, err := st.QuarantineSymbolBars(ctx, id, string(md.TF1d), "run-1", "test", 1)
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if n != 4 {
		t.Fatalf("moved %d bar(s), want 4", n)
	}

	var left int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d'`, id).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Fatalf("%d old bar(s) remain in `bars`. A re-fetch upserts by timestamp, so any survivor "+
			"stays on the OLD price basis and the symbol ends up holding two conventions.", left)
	}

	// Reversible: the whole point of quarantine over delete.
	back, err := st.RestoreQuarantinedBars(ctx, "run-1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if back != 4 {
		t.Fatalf("restored %d bar(s), want 4", back)
	}
}

func TestCohortRefetch_SecondPassSkipsWhatIsAlreadyDone(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	id := seedCohortSymbol(t, st, "CADE", 20000, []float64{10, 11, 12, 13})

	const run = "run-resume"
	before, err := st.CohortForRefetch(ctx, string(md.TF1d), []string{"CADE"}, run)
	if err != nil {
		t.Fatalf("cohort: %v", err)
	}
	if len(before) != 1 || before[0].Refetched {
		t.Fatalf("a symbol never touched by this run must not read as done: %+v", before)
	}
	if before[0].Bars != 4 || before[0].FirstDay == "" {
		t.Fatalf("cohort row does not describe the stored series: %+v", before[0])
	}

	// Pass one: quarantine the old bars, then "re-fetch" different prices.
	if _, err := st.QuarantineSymbolBars(ctx, id, string(md.TF1d), run, "test", 1); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	seedCohortSymbol(t, st, "CADE", 20000, []float64{7, 7.7, 8.4, 9.1}) // the repaired series

	// Pass two must NOT touch it again.
	after, err := st.CohortForRefetch(ctx, string(md.TF1d), []string{"CADE"}, run)
	if err != nil {
		t.Fatalf("cohort: %v", err)
	}
	if !after[0].Refetched {
		t.Fatal("a symbol already re-fetched under this run reads as still to do. A resume would " +
			"quarantine the FRESH bars — filing the repair away as if it were the defect.")
	}

	// And the fresh series is intact.
	var closes int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d' AND close < 10`, id).Scan(&closes); err != nil {
		t.Fatalf("count: %v", err)
	}
	if closes != 4 {
		t.Fatalf("%d repaired bar(s) survive, want 4", closes)
	}
}

func TestCohortRefetch_DeltasMeasureWhatChanged(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	id := seedCohortSymbol(t, st, "CIVI", 20000, []float64{10, 11, 12, 13})

	const run = "run-delta"
	if _, err := st.QuarantineSymbolBars(ctx, id, string(md.TF1d), run, "test", 1); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	// Repaired series: same days, two closes changed, one day dropped, one added.
	seedCohortSymbol(t, st, "CIVI", 20000, []float64{10, 5.5, 6, 13, 14})

	ds, err := st.CohortRefetchDeltas(ctx, string(md.TF1d), run)
	if err != nil {
		t.Fatalf("deltas: %v", err)
	}
	if len(ds) != 1 {
		t.Fatalf("got %d delta row(s), want 1", len(ds))
	}
	d := ds[0]
	if d.OldBars != 4 || d.NewBars != 5 {
		t.Fatalf("old=%d new=%d, want 4 and 5", d.OldBars, d.NewBars)
	}
	if d.Common != 4 {
		t.Fatalf("common=%d, want the 4 shared days", d.Common)
	}
	if d.Differing != 2 {
		t.Fatalf("differing=%d, want 2 (the two changed closes). A migration that cannot say what "+
			"it changed is asserting success rather than reporting it.", d.Differing)
	}
	if d.WorstPct < 49 || d.WorstPct > 51 {
		t.Fatalf("worst=%.2f%%, want ~50%% (11 -> 5.5)", d.WorstPct)
	}
}

func TestCohortRefetch_RefusesAnUnidentifiableRun(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.CohortForRefetch(ctx, string(md.TF1d), []string{"CADE"}, ""); err == nil {
		t.Fatal("an empty run id must be refused: without one the migration cannot be resumed or undone")
	}
	if _, err := st.QuarantineSymbolBars(ctx, 1, string(md.TF1d), "", "test", 1); err == nil {
		t.Fatal("an empty run id must be refused by the mover too")
	}
}

// A ROLLBACK THAT MERGES IS NOT A ROLLBACK.
//
// RestoreQuarantinedBars is additive: it re-inserts the quarantined rows and
// leaves everything else alone. That is exact for a flat-pad run, where nothing
// replaced the removed bars. A re-fetch DOES replace them, and the fresh series
// routinely carries days the old one lacked — ACACU went in with 248 bars and
// came back with 318. Restoring additively then leaves the UNION: measured on
// the canary, ACACU ended at 396 bars holding BOTH price conventions, which is
// a worse state than either series alone and the exact thing a rollback exists
// to prevent.
//
// MUTATION CHECK, verified: point the command at RestoreQuarantinedBars instead
// and this test fails with 5 bars restored instead of 3.
func TestCohortRefetch_RollbackReplacesRatherThanMerges(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	id := seedCohortSymbol(t, st, "ACACU", 20000, []float64{10, 11, 12})

	const run = "run-rollback"
	if _, err := st.QuarantineSymbolBars(ctx, id, string(md.TF1d), run, "test", 1); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	// The "re-fetch": same three days on a different basis, plus two days the
	// old series never had.
	seedCohortSymbol(t, st, "ACACU", 20000, []float64{7, 7.7, 8.4, 9.1, 9.8})

	n, err := st.RestoreCohortRefetch(ctx, string(md.TF1d), run)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n != 3 {
		t.Fatalf("restored %d row(s), want the 3 that were quarantined", n)
	}

	var count int
	var maxClose float64
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(close),0) FROM bars WHERE symbol_id=? AND tf='1d'`,
		id).Scan(&count, &maxClose); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("symbol holds %d bar(s) after rollback, want exactly the original 3. The two "+
			"fresh-only days survived, leaving a union of two price conventions in one symbol.", count)
	}
	if maxClose != 12 {
		t.Fatalf("max close %.2f after rollback, want the original 12 — the fresh basis is still present", maxClose)
	}

	// Idempotent: the quarantine rows are consumed, so a second call is a no-op.
	again, err := st.RestoreCohortRefetch(ctx, string(md.TF1d), run)
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if again != 0 {
		t.Fatalf("a second rollback moved %d row(s); it must be a no-op", again)
	}
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d'`, id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("a second rollback emptied the symbol to %d bar(s); it must change nothing", count)
	}
}
