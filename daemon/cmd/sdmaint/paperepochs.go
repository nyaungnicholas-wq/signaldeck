package main

// sdmaint paper-epochs — apply and inspect the paper book's strategy-change
// boundaries.
//
// The daemon applies the same schedule on every paper pass, so this command is
// not required for correctness. It exists because the boundary is a fact about
// the RECORD, and a record's boundary should be inspectable and applicable
// without waiting for (or restarting) the trader.
//
// Safe to run against a live database: the writes are four idempotent upserts
// into a table nothing else reads, and no trading state is touched.

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func paperEpochs(args []string) error {
	fs := flag.NewFlagSet("paper-epochs", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	apply := fs.Bool("apply", false, "write the schedule (default: report only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	for _, strategy := range pipeline.PaperStrategyNames() {
		if *apply {
			for _, e := range pipeline.PaperEpochSchedule() {
				e.Strategy = strategy
				if err := st.UpsertPaperEpoch(ctx, e); err != nil {
					return fmt.Errorf("%s epoch %d: %w", strategy, e.Epoch, err)
				}
			}
		}
		epochs, err := st.PaperEpochs(ctx, strategy)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s — %d epoch(s)\n", strategy, len(epochs))
		if len(epochs) == 0 {
			fmt.Println("  (none recorded; re-run with -apply, or let the next paper pass write them)")
			continue
		}
		for i, e := range epochs {
			from, to := store.EpochBounds(epochs, i)
			fmt.Printf("  epoch %d  %-16s %s → %s\n", e.Epoch, e.Label,
				tsLabel(from, "inception"), tsLabel(to, "open"))

			// Fills and marks are what a reader actually wants to see beside a
			// boundary: an epoch with no evidence in it is a declared split, not
			// a measured one, and the difference should be visible at a glance.
			fills, marks, err := epochCounts(ctx, st, strategy, from, to)
			if err != nil {
				return err
			}
			fmt.Printf("            %d fill(s), %d equity mark(s)\n", fills, marks)
		}
	}
	if !*apply {
		fmt.Println("\n(report only — pass -apply to write the schedule)")
	}
	return nil
}

// tsLabel renders a boundary. 0 means unbounded, but it means different things
// on the two sides — "inception" below, "open" above — so the caller names it
// rather than the reader guessing which end they are looking at.
func tsLabel(ts int64, unbounded string) string {
	if ts == 0 {
		return unbounded
	}
	return time.Unix(ts, 0).UTC().Format("2006-01-02")
}

func epochCounts(ctx context.Context, st *store.Store, strategy string, from, to int64) (fills, marks int, err error) {
	trades, err := st.AllPaperTradesAsc(ctx, strategy)
	if err != nil {
		return 0, 0, err
	}
	for _, t := range trades {
		if store.InEpoch(t.Ts, from, to) {
			fills++
		}
	}
	curve, err := st.PaperEquityCurve(ctx, strategy, 100000)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range curve {
		if store.InEpoch(p.Ts, from, to) {
			marks++
		}
	}
	return fills, marks, nil
}
