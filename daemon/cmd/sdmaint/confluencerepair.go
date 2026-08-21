package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// confluenceRepair brings a live confluence_outcomes table onto the semantics
// the scorer and resolver now write, in two deterministic steps.
//
// STEP 1 — RETIRE THE UNGRADABLE. A row whose entry leg cannot come from its own
// bucket day was graded against a bar from another day. Measured on the live
// table: 1,430 of 5,428 resolved rows, of which 1,429 sit on a Saturday or a
// Sunday. The scorer used to open a bucket every calendar day while running
// every 30 minutes, and the resolver's entry read reaches BACKWARD while its
// forward read reaches FORWARD — so Friday, Saturday and Sunday all resolved to
// one identical pair of bars. RNWWW published the same +93.33% three times.
//
// STEP 2 — ASSIGN EPISODES. Consecutive same-direction days on one symbol are
// ONE bet held, not one new bet per day, and the published scoreboard reads the
// episode. This must run AFTER step 1: an episode compounds its days, and
// compounding a duplicated day squares a move that was only made once (RNWWW's
// three copies compound to +622%).
//
// Both steps are pure re-derivations of the stored table, so the command is
// idempotent and safe to re-run. Nothing is deleted: a retired row keeps its
// entry_px audit value and its place in the table, and only the return computed
// from the wrong bars is cleared.
//
// DRY RUN BY DEFAULT.
func confluenceRepair(args []string) error {
	fs := flag.NewFlagSet("confluence-repair", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	apply := fs.Bool("apply", false, "write the repair (default: report only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	before, err := st.ConfluencePopulation(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("database   : %s\n", *dbPath)
	fmt.Printf("before     : %d row(s), %d resolved, %d ungradable, %d episode(s)\n",
		before.Rows, before.Resolved, before.Ungradable, before.Episodes)
	fmt.Printf("stale entry: %d resolved row(s) were graded from a bar outside their own bucket day\n",
		before.StaleEntry)
	bw, br, bt := 0, 0, 0
	if bw, br, bt, err = st.ConfluencePriceLevelCoverage(ctx); err != nil {
		return err
	}
	fmt.Printf("basis      : %d/%d gradable row(s) reproduce their own stored fwd_return from the current bars "+
		"(%d carry price levels)\n", br, bt, bw)

	if !*apply {
		fmt.Println("\nDRY RUN — nothing was changed. Re-run with -apply.")
		fmt.Println("A retired row keeps entry_px and its place in the table; only the return")
		fmt.Println("computed from the wrong pair of bars is cleared.")
		return nil
	}

	reason := fmt.Sprintf(
		"entry bar outside the bucket day — graded from another day's price; retired %s",
		time.Now().UTC().Format("2006-01-02"))
	retired, err := st.MarkUngradableConfluenceOutcomes(ctx, reason)
	if err != nil {
		return fmt.Errorf("retire ungradable: %w", err)
	}
	assigned, err := st.BackfillConfluenceEpisodes(ctx)
	if err != nil {
		return fmt.Errorf("assign episodes: %w", err)
	}
	// STEP 3 — STAMP THE PRICE LEVELS. The constrained basis needs levels, not a
	// ratio: it cannot test a tradable minimum or place a stop on a return alone.
	// The derivation is the resolver's own window, so a backfilled row and a
	// freshly graded one describe the same trade.
	priced, err := st.BackfillConfluenceGradePrices(ctx)
	if err != nil {
		return fmt.Errorf("stamp grade prices: %w", err)
	}
	// STEP 4 — RE-GRADE ONTO ONE BASIS. 72007b3 stopped the resolver dividing a
	// live exit close by a FROZEN entry_px, but only for rows graded after it.
	// Measured on the live table, 3,408 of 3,998 gradable rows still carried the
	// superseded number. This applies the shipped derivation to them.
	regraded, err := st.RegradeConfluenceFromCurrentBars(ctx)
	if err != nil {
		return fmt.Errorf("regrade: %w", err)
	}

	after, err := st.ConfluencePopulation(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("\nretired    : %d row(s)\n", retired)
	fmt.Printf("episodes   : %d row(s) assigned an episode\n", assigned)
	fmt.Printf("priced     : %d row(s) stamped with entry/exit levels\n", priced)
	fmt.Printf("regraded   : %d row(s) recomputed onto one basis (both legs from the current series)\n", regraded)

	withLevels, reproducing, total, err := st.ConfluencePriceLevelCoverage(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("coverage   : %d/%d gradable row(s) carry price levels; %d of those reproduce their own stored fwd_return\n",
		withLevels, total, reproducing)
	if withLevels > 0 && reproducing < withLevels {
		fmt.Printf("             %d row(s) do NOT reproduce — their bars moved after grading; the constrained\n"+
			"             basis counts them, so inspect before quoting an account number.\n", withLevels-reproducing)
	}
	fmt.Printf("after      : %d row(s), %d resolved, %d ungradable, %d episode(s)\n",
		after.Rows, after.Resolved, after.Ungradable, after.Episodes)
	if after.StaleEntry != 0 {
		return fmt.Errorf("%d stale-entry row(s) survived the repair — inspect before trusting the scoreboard",
			after.StaleEntry)
	}
	fmt.Println("\nno stale-entry rows remain. The scoreboard population is now episodes.")
	return nil
}
