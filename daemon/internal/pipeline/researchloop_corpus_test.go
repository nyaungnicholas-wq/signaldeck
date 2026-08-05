package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The Bonferroni divisor is grid x (1 + searches), and the loop used to charge a
// search every night unconditionally. researchx.Discover is deterministic, so a
// night whose corpus did not change reproduces the previous night's verdicts
// exactly — the same test re-read, not a second chance to be fooled. Charging it
// inflated the bar without buying error control: measured on the live ledger,
// 2026-08-04 added ZERO observations and still took the divisor 384 -> 432, and
// 08-02 added 15 rows out of 296,712 for the same 48.
//
// These pin the fix in both directions: an unchanged corpus costs no look, and a
// materially grown one still costs a full one.

// seedSearchedRun writes a prior run that DID take a look (grid_size > 0), which
// is what LastSearchedRun keys off.
func seedSearchedRun(t *testing.T, st *store.Store, day string, obsCount int, tsTo int64) {
	t.Helper()
	if err := st.UpsertLoopRun(context.Background(), store.LoopRun{
		Day: day, RanAt: 1, GridSize: 48, Divisor: 384, CorrectedAlpha: 0.05 / 384,
		ObsCount: obsCount, ObsTsFrom: 604800, ObsTsTo: tsTo, Judged: 48,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLoopChargesNoLookOnUnchangedCorpus(t *testing.T) {
	ctx := context.Background()
	st := newLoopStore(t)
	seedLoopCorpus(t, st, 4000)

	obs, err := st.ResearchObservations(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Same observation count, and a window end far ahead of anything the corpus
	// holds, so neither materiality trigger fires.
	seedSearchedRun(t, st, "2020-01-01", len(obs), 1<<40)

	before, _ := st.GetMeta(ctx, loopMetaSearches)

	w := &ResearchLoop{St: st}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "skip") || !strings.Contains(msg, "corpus unchanged") {
		t.Fatalf("unchanged corpus must skip with a reason, got %q", msg)
	}

	// The look must not be charged: the searches counter cannot move...
	if after, _ := st.GetMeta(ctx, loopMetaSearches); after != before {
		t.Errorf("searches counter moved on an unchanged corpus: %q -> %q", before, after)
	}
	// ...and the refusal row must carry grid_size 0, which is exactly what
	// DurableLoopSearches filters on, so the durable count cannot rise either.
	today := time.Now().UTC().Format("2006-01-02")
	row, ok, err := st.LoopRunForDay(ctx, today)
	if err != nil || !ok {
		t.Fatalf("no run row for %s (ok=%v err=%v)", today, ok, err)
	}
	if row.GridSize != 0 {
		t.Errorf("skip row grid_size = %d, want 0 or DurableLoopSearches counts it", row.GridSize)
	}
	if row.RefusalReason == "" {
		t.Error("skip row must record why the look was declined")
	}
	dur, err := st.DurableLoopSearches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dur != 1 {
		t.Errorf("DurableLoopSearches = %d, want 1 (only the seeded searched run)", dur)
	}
}

func TestLoopStillChargesOnGrownCorpus(t *testing.T) {
	ctx := context.Background()
	st := newLoopStore(t)
	seedLoopCorpus(t, st, 4000)

	// Last search saw half the corpus: 100% growth, far above loopCorpusGrowthMin.
	obs, err := st.ResearchObservations(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	seedSearchedRun(t, st, "2020-01-01", len(obs)/2, 1<<40)

	w := &ResearchLoop{St: st}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "corpus unchanged") {
		t.Fatalf("a corpus that doubled must still cost a look, got %q", msg)
	}

	// A real search: the run row records the grid, and the durable count rises.
	today := time.Now().UTC().Format("2006-01-02")
	row, ok, err := st.LoopRunForDay(ctx, today)
	if err != nil || !ok {
		t.Fatalf("no run row for %s (ok=%v err=%v)", today, ok, err)
	}
	if row.GridSize == 0 {
		t.Errorf("grown corpus produced grid_size 0; the search did not run: %q", msg)
	}
	dur, err := st.DurableLoopSearches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dur != 2 {
		t.Errorf("DurableLoopSearches = %d, want 2 (seeded run + tonight's)", dur)
	}
}
