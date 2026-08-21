package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A ROW GRADED FROM ANOTHER DAY'S PRICE MUST BE RETIRED, NOT COMPOUNDED.
//
// The historical table holds 1,430 resolved rows whose entry leg came from a bar
// outside their own bucket day — 1,429 of them Saturday or Sunday buckets, where
// the resolver's backward entry read and forward exit read both landed on the
// same pair of bars as the adjacent Friday. RNWWW carried the identical +93.33%
// on 2026-07-17, 07-18 and 07-19.
//
// Retiring them is not optional once episodes exist: an episode COMPOUNDS its
// days, so three copies of +93.33% compound to +622% — a move that was made
// once, published as if it were made three times over. Leaving the duplicates
// graded would make the episode collapse actively worse than the defect it fixes.
//
// MUTATION CHECKS, all four run:
//   - run BackfillConfluenceEpisodes over ALL rows instead of gradable ones →
//     TestUngradable_EpisodesSkipRetiredRows fails, compounding the duplicate;
//   - remove BOTH the resolved_at clearing in MarkUngradableConfluenceOutcomes
//     and the `ungradable IS NULL` filter in ResolvedConfluenceOutcomes →
//     TestUngradable_RetiredRowsLeaveThePublishedPopulation fails with the
//     scoreboard reading 3 RNWWW rows instead of 1.
//
// Removing EITHER of those two alone does NOT fail, and that is stated rather
// than hidden: they are redundant on purpose. Clearing the return removes the
// wrong number from the table; the filter removes the row from the published
// population. A reader checking one of them should know the other is there.

// seedWeekendDuplication reproduces the live RNWWW shape: a Friday bar, a Monday
// bar, and outcome rows on Friday, Saturday and Sunday all carrying the same
// Friday-to-Monday move.
func seedWeekendDuplication(t *testing.T, st *Store) int64 {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "RNWWW", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	const friBar, monBar = int64(1784260800), int64(1784520000)
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: friBar, Open: 0.0014, High: 0.0025, Low: 0.0010, Close: 0.0015, Volume: 54019},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: monBar, Open: 0.0018, High: 0.0030, Low: 0.0011, Close: 0.0029, Volume: 19558},
	}); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
	for _, bucket := range []int64{1784246400, 1784332800, 1784419200} { // Fri, Sat, Sun
		if err := st.InsertConfluenceOutcome(ctx, ConfluenceOutcome{
			SymbolID: sym.ID, Ts: bucket, Horizon: "1d",
			Direction: -1, Agree: 3, EntryPx: 0.0015,
		}); err != nil {
			t.Fatalf("insert %d: %v", bucket, err)
		}
		// Graded the way the old resolver did: the same move on all three.
		if err := st.ResolveConfluenceOutcome(ctx, sym.ID, bucket, "1d", 0.9333, false, friBar,
			ConfluenceGradePrices{EntryClose: 0.0015, ExitLow: 0.0011, ExitHigh: 0.0030}); err != nil {
			t.Fatalf("resolve %d: %v", bucket, err)
		}
	}
	return sym.ID
}

func TestUngradable_RetiredRowsLeaveThePublishedPopulation(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	id := seedWeekendDuplication(t, st)

	before, err := st.ConfluencePopulation(ctx)
	if err != nil {
		t.Fatalf("population: %v", err)
	}
	if before.Resolved != 3 || before.StaleEntry != 2 {
		t.Fatalf("fixture wrong: resolved=%d staleEntry=%d, want 3 and 2 (the Sat/Sun buckets)",
			before.Resolved, before.StaleEntry)
	}

	n, err := st.MarkUngradableConfluenceOutcomes(ctx, "test retirement")
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if n != 2 {
		t.Fatalf("retired %d row(s), want the 2 weekend buckets", n)
	}

	after, err := st.ConfluencePopulation(ctx)
	if err != nil {
		t.Fatalf("population: %v", err)
	}
	if after.StaleEntry != 0 {
		t.Fatalf("%d stale-entry row(s) survived; the published mean still carries a duplicated move", after.StaleEntry)
	}
	if after.Rows != 3 {
		t.Fatalf("rows = %d, want 3 — a bet that was PLACED is a fact and must not be deleted", after.Rows)
	}
	if after.Ungradable != 2 || after.Resolved != 1 {
		t.Fatalf("ungradable=%d resolved=%d, want 2 and 1", after.Ungradable, after.Resolved)
	}

	// The published read must show exactly one.
	pub, err := st.ResolvedConfluenceOutcomes(ctx, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := 0
	for _, o := range pub {
		if o.SymbolID == id {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("the scoreboard reads %d RNWWW rows, want 1. One Friday-to-Monday move published "+
			"%d times is the pseudo-replication this retirement exists to remove.", got, got)
	}

	// The audit view keeps all three, with the reason on the retired ones.
	all, err := st.ConfluenceOutcomesForSymbol(ctx, id, "1d")
	if err != nil {
		t.Fatalf("audit read: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("audit view holds %d row(s), want all 3", len(all))
	}

	// Idempotent.
	again, err := st.MarkUngradableConfluenceOutcomes(ctx, "test retirement")
	if err != nil {
		t.Fatalf("second retire: %v", err)
	}
	if again != 0 {
		t.Fatalf("a second pass retired %d more row(s); the repair must be idempotent", again)
	}
}

func TestUngradable_EpisodesSkipRetiredRows(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	id := seedWeekendDuplication(t, st)

	if _, err := st.MarkUngradableConfluenceOutcomes(ctx, "test retirement"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := st.BackfillConfluenceEpisodes(ctx); err != nil {
		t.Fatalf("episodes: %v", err)
	}

	rows, err := st.ConfluenceOutcomesForSymbol(ctx, id, "1d")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	episodes := map[int64]bool{}
	for _, r := range rows {
		if r.EpisodeTs != 0 {
			episodes[r.EpisodeTs] = true
		}
		// A retired row must carry NO episode: an episode compounds its days, and
		// a retired day would be compounded into a move that was made once.
		if r.EpisodeTs != 0 && r.FwdReturn == 0 && r.Win == 0 && r.Ts != 1784246400 {
			t.Fatalf("a retired row at ts=%d carries episode_ts=%d; compounding it would square "+
				"a duplicated move (3 copies of +93.33%% compound to +622%%)", r.Ts, r.EpisodeTs)
		}
	}
	if len(episodes) != 1 {
		t.Fatalf("%d episode(s) over one real bet, want 1", len(episodes))
	}
}
