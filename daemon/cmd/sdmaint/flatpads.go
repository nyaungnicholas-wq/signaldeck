package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// quarantineFlatPads reports, and optionally quarantines, runs of vendor flat-pad
// bars — synthetic sessions the market-data vendor emits when the requested feed
// saw no trade (open=high=low=close, volume 0).
//
// DRY RUN BY DEFAULT. Nothing moves without -apply, and nothing is ever DELETED:
// qualifying bars move to bars_quarantine and -restore puts a run back. The point
// of quarantine over deletion is that a predicate this subtle deserves an undo.
//
// The report prints how much of each run currently sits in universe_membership,
// because that is the number that matters: a padded day inside the point-in-time
// universe is a name ranked in the cross-section while printing exactly 0.0%
// return. SBNY (Signature Bank) contributes 509 such days after the bank was
// seized.
func quarantineFlatPadsCmd(args []string) error {
	fs := flag.NewFlagSet("quarantine-flat-pads", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	tf := fs.String("tf", "1d", "timeframe to scan")
	minRun := fs.Int("min-run", 20,
		"minimum consecutive flat zero-volume sessions at ONE price to count as a pad")
	apply := fs.Bool("apply", false, "move the qualifying bars into bars_quarantine")
	runID := fs.String("run-id", "", "identifier for this quarantine run (default: flatpad-<unix>)")
	restore := fs.String("restore", "", "restore a previous run by id and exit")
	top := fs.Int("top", 25, "how many runs to list")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	if *restore != "" {
		n, err := st.RestoreQuarantinedBars(ctx, *restore)
		if err != nil {
			return fmt.Errorf("restore %s: %w", *restore, err)
		}
		fmt.Printf("restored %d bar(s) from run %s back into bars\n", n, *restore)
		return nil
	}

	runs, err := st.FlatPadRuns(ctx, *tf, *minRun)
	if err != nil {
		return err
	}
	var bars, inUniverse int
	syms := map[int64]bool{}
	for _, r := range runs {
		bars += r.Bars
		inUniverse += r.InUniverse
		syms[r.SymbolID] = true
	}

	fmt.Printf("database   : %s\n", *dbPath)
	fmt.Printf("predicate  : >= %d consecutive %s sessions, volume=0, open=high=low=close, ONE distinct close\n",
		*minRun, *tf)
	fmt.Printf("found      : %d run(s), %d bar(s), %d symbol(s)\n", len(runs), bars, len(syms))
	fmt.Printf("in universe: %d of those days are materialized in universe_membership\n\n", inUniverse)

	if len(runs) == 0 {
		fmt.Println("nothing qualifies.")
		return nil
	}
	fmt.Printf("%-10s %7s %12s %12s %12s %10s\n", "SYMBOL", "BARS", "PRICE", "FROM", "TO", "IN-UNIV")
	for i, r := range runs {
		if i >= *top {
			fmt.Printf("... and %d more run(s) not shown (raise -top to see them)\n", len(runs)-*top)
			break
		}
		fmt.Printf("%-10s %7d %12.4f %12s %12s %10d\n", r.Symbol, r.Bars, r.Close,
			time.Unix(r.FromTs, 0).UTC().Format("2006-01-02"),
			time.Unix(r.ToTs, 0).UTC().Format("2006-01-02"), r.InUniverse)
	}

	if !*apply {
		fmt.Println("\nDRY RUN — nothing was moved. Re-run with -apply to quarantine these bars.")
		fmt.Println("They move to bars_quarantine, not to /dev/null: -restore <run-id> puts them back.")
		fmt.Println("Rebuild universe_membership afterwards (`sdmaint build-universe`) so the")
		fmt.Println("cross-sectional denominator stops carrying the padded days.")
		return nil
	}

	id := *runID
	if id == "" {
		id = fmt.Sprintf("flatpad-%d", time.Now().Unix())
	}
	moved, err := st.QuarantineFlatPads(ctx, *tf, *minRun, id,
		fmt.Sprintf("vendor flat pad: >=%d consecutive %s sessions, volume=0, o=h=l=c, one close", *minRun, *tf),
		time.Now().Unix())
	if err != nil {
		return err
	}
	if int(moved) != bars {
		fmt.Fprintf(os.Stderr,
			"WARNING: moved %d bar(s) but the scan above reported %d. The database changed between\n"+
				"the two, or the predicates have drifted. Inspect bars_quarantine for run %s before\n"+
				"trusting either number.\n", moved, bars, id)
	}
	fmt.Printf("\nquarantined %d bar(s) under run id %s\n", moved, id)
	fmt.Printf("undo with: sdmaint quarantine-flat-pads -restore %s\n", id)
	fmt.Println("now rebuild the point-in-time universe: sdmaint build-universe")
	return nil
}
