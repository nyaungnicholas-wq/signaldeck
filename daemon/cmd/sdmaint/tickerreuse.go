package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// splitReusedTickers applies the plan emitted by
// tools/plan_ticker_reuse_split.py, the same Python-plans/Go-applies split
// apply-delistings uses: the planner opens the database read-only and decides
// nothing that is not auditable from its JSON, and every write here rides
// store's single writer connection.
//
// Two operations, because the evidence has two shapes. A row whose bars resume
// years after its delisting is TICKER REUSE and gets split in two. A row whose
// last bar is a settlement day or two after the stamp is an EARLY STAMP and
// gets the stamp nudged forward. Conflating them would invent a second company
// out of a rounding difference — measured 2026-08-04, that would have been
// three of the four candidates.
type reusePlan struct {
	Generated    string `json:"generated"`
	ReuseSplits  []struct {
		SymbolID         int64  `json:"symbol_id"`
		Symbol           string `json:"symbol"`
		HistoricalTicker string `json:"historical_ticker"`
		HistoricalName   string `json:"historical_name"`
		DelistedAt       int64  `json:"delisted_at"`
		LiveAddedAt      int64  `json:"live_added_at"`
		GapDays          int64  `json:"gap_days"`
		LaterDays        int64  `json:"later_days"`
	} `json:"reuse_splits"`
	StampNudges []struct {
		SymbolID  int64  `json:"symbol_id"`
		Symbol    string `json:"symbol"`
		From      int64  `json:"from"`
		To        int64  `json:"to"`
		GapDays   int64  `json:"gap_days"`
		LaterDays int64  `json:"later_days"`
	} `json:"stamp_nudges"`
}

func splitReusedTickers(args []string) error {
	fs := flag.NewFlagSet("split-reused-tickers", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	planPath := fs.String("plan", "", "plan JSON from tools/plan_ticker_reuse_split.py")
	dryRun := fs.Bool("dry-run", false, "report what would change, write nothing")
	maxNudge := fs.Int64("max-nudge-days", 5,
		"widest settlement lag treated as an early stamp; anything wider must be a split")
	// The spliced-latest category was identified after the first split had
	// already been applied, so there has to be a way to finish that job without
	// re-running a split the database will (correctly) refuse.
	purgeOnly := fs.Bool("purge-spliced-only", false,
		"skip the splits and nudges; only delete post-boundary rows computed across a splice")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *planPath == "" {
		return fmt.Errorf("-plan is required")
	}
	raw, err := os.ReadFile(*planPath)
	if err != nil {
		return err
	}
	var plan reusePlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}
	if len(plan.ReuseSplits) == 0 && len(plan.StampNudges) == 0 {
		return fmt.Errorf("plan has nothing to apply")
	}

	fmt.Printf("plan generated %s: %d ticker-reuse split(s), %d stamp nudge(s)\n",
		plan.Generated, len(plan.ReuseSplits), len(plan.StampNudges))
	for _, s := range plan.ReuseSplits {
		fmt.Printf("  SPLIT  %-10s id=%-6d gap=%dd later_days=%d  dead company -> %q\n",
			s.Symbol, s.SymbolID, s.GapDays, s.LaterDays, s.HistoricalTicker)
	}
	for _, n := range plan.StampNudges {
		fmt.Printf("  NUDGE  %-10s id=%-6d delisted_at %d -> %d (+%dd)\n",
			n.Symbol, n.SymbolID, n.From, n.To, n.GapDays)
	}
	if *dryRun {
		fmt.Println("dry-run: nothing written")
		return nil
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	if *purgeOnly {
		for _, s := range plan.ReuseSplits {
			boundary := (s.DelistedAt/86400)*86400 + 86399
			got, err := st.PurgeSplicedLatest(ctx, s.SymbolID, boundary)
			if err != nil {
				return fmt.Errorf("purge %s: %w", s.Symbol, err)
			}
			if len(got) == 0 {
				fmt.Printf("  %s: nothing spliced left to purge\n", s.Symbol)
				continue
			}
			for _, k := range store.SortedKeys(got) {
				fmt.Printf("      purged  %-20s %d  (computed across the splice; cannot self-heal)\n", k, got[k])
			}
		}
		return nil
	}

	for _, s := range plan.ReuseSplits {
		res, err := st.SplitReusedTicker(ctx, store.ReusedTickerSplit{
			SymbolID:         s.SymbolID,
			Symbol:           s.Symbol,
			HistoricalTicker: s.HistoricalTicker,
			HistoricalName:   s.HistoricalName,
			DelistedAt:       s.DelistedAt,
			LiveAddedAt:      s.LiveAddedAt,
		})
		if err != nil {
			// One refusal must not half-apply the rest: each split is its own
			// transaction, so stopping here leaves a consistent database.
			return fmt.Errorf("split %s: %w", s.Symbol, err)
		}
		fmt.Printf("  split %s -> new symbol id %d\n", s.Symbol, res.NewSymbolID)
		for _, k := range store.SortedKeys(res.Moved) {
			fmt.Printf("      moved   %-20s %d\n", k, res.Moved[k])
		}
		for _, k := range store.SortedKeys(res.Deleted) {
			fmt.Printf("      deleted %-20s %d  (fit over the spliced series; regenerates)\n", k, res.Deleted[k])
		}
	}

	for _, n := range plan.StampNudges {
		moved, err := st.NudgeDelisting(ctx, n.SymbolID, n.To, *maxNudge)
		if err != nil {
			return fmt.Errorf("nudge %s: %w", n.Symbol, err)
		}
		fmt.Printf("  nudge %-10s applied=%v\n", n.Symbol, moved)
	}

	// The membership is derived from what just changed, so leaving it stale
	// would mean the audit surface disagrees with the database that fed it.
	res, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		return fmt.Errorf("rebuild universe after split: %w", err)
	}
	fmt.Printf("universe_membership rebuilt: %d rows, %d reused-ticker symbol-days remaining\n",
		res.Rows, res.ReusedTickerDays)
	if res.ReusedTickerDays != 0 {
		fmt.Println("NOTE: reused-ticker days remain — re-run the planner; some rows were not in this plan")
	}
	return nil
}
