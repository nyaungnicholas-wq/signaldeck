package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// refetchCohort re-pulls the delisted-import cohort onto the CURRENT price
// basis, one symbol at a time, reversibly.
//
// THE DEFECT. ~650 symbols were imported through `sdmaint import-delisted` from
// a staging DB built by tools/alpha/fetch_delisted.py, which then requested
// adjustment=all — split PLUS dividends. Every other ingest path requests
// adjustment=split. The bars table holds ONE series per symbol, so those 650
// histories carry a different price convention from the rest of the column, and
// any cross-sectional computation that mixes them with live-backfilled names is
// comparing two conventions.
//
// HOW BIG. Measured before this was written, on ten sample symbols fetched fresh
// at adjustment=split and compared close-by-close against what is stored:
//
//	CADE 97.7% of bars differ (worst 26.9%)   CIVI 97.1% (49.4%)
//	ELON 99.3% (47.3%)                        PRMW 99.6% (10.5%)
//	DNB  90.7% ( 5.2%)                        PPEM 39.0% (66.2%)
//
// Not a tail effect on a few dividend payers — most of the cohort's history.
//
// WHY THIS USES THE GO CLIENT rather than re-running the Python fetcher. The Go
// client bakes in barAdjustment and refuses vendor pads inside backfill(), so a
// re-fetch is on the right basis and pad-free BY CONSTRUCTION. Re-running the
// Python path would reproduce the pads: PPEM stores 123 bars today because 676
// pads were quarantined, and a raw fetch returns all 799 of them.
//
// SAFETY. The old bars are moved to bars_quarantine under a run id, never
// deleted, so `-restore` puts the cohort back exactly. Progress is per symbol
// and resumable: a symbol already present under this run id is skipped, because
// re-quarantining it would capture its FRESH bars and discard the repair.
//
// DRY RUN BY DEFAULT.
func refetchCohort(args []string) error {
	fs := flag.NewFlagSet("refetch-cohort", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	stagePath := fs.String("staging", "", "staging DB naming the cohort (delisted_symbol table)")
	symbolsCSV := fs.String("symbols", "", "comma-separated symbols instead of -staging (canary use)")
	apply := fs.Bool("apply", false, "perform the re-fetch (default: report only)")
	runID := fs.String("run-id", "", "run identifier (default: refetch-<unix>)")
	restore := fs.String("restore", "", "restore a run by id and exit")
	limit := fs.Int("limit", 0, "process at most this many symbols (0 = all)")
	out := fs.String("out", "", "write the per-symbol delta report to this JSON file")
	minBars := fs.Int("min-bars", 60, "cohort filter: skip staging symbols with fewer bars")
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
		// EXACT rollback: replace, not merge. The generic RestoreQuarantinedBars
		// is additive, which is right for a run that removed bars and put nothing
		// in their place — but a re-fetch DID put something there, and the fresh
		// series carries days the old one lacked. Merging the two leaves a union
		// holding both price conventions in one symbol (measured on the canary:
		// ACACU restored to 396 bars against an original 248).
		n, err := st.RestoreCohortRefetch(ctx, string(md.TF1d), *restore, strings.TrimSpace(strings.ToUpper(*symbolsCSV)))
		if err != nil {
			return fmt.Errorf("restore %s: %w", *restore, err)
		}
		fmt.Printf("restored %d bar(s) from run %s; the touched symbols now hold exactly\n", n, *restore)
		fmt.Println("what they held before the run, and the run's quarantine rows are cleared.")
		return nil
	}

	id := *runID
	if id == "" {
		id = fmt.Sprintf("refetch-%d", time.Now().Unix())
	}

	names, err := cohortNames(*stagePath, *symbolsCSV, *minBars)
	if err != nil {
		return err
	}
	cohort, err := st.CohortForRefetch(ctx, string(md.TF1d), names, id)
	if err != nil {
		return err
	}

	todo, done, bars := 0, 0, 0
	for _, c := range cohort {
		if c.Refetched {
			done++
			continue
		}
		todo++
		bars += c.Bars
	}
	fmt.Printf("database : %s\n", *dbPath)
	fmt.Printf("run id   : %s\n", id)
	fmt.Printf("cohort   : %d named, %d resolved to live symbols\n", len(names), len(cohort))
	fmt.Printf("progress : %d already re-fetched under this run, %d to do (%d stored bar(s))\n",
		done, todo, bars)
	if len(names) != len(cohort) {
		fmt.Printf("NOTE     : %d named symbol(s) are not live rows and are skipped\n", len(names)-len(cohort))
	}

	if !*apply {
		fmt.Println("\nDRY RUN — nothing was changed. Re-run with -apply.")
		fmt.Printf("Old bars move to bars_quarantine under %s; undo with -restore %s.\n", id, id)
		return nil
	}

	cfg := config.Load()
	if cfg.AlpacaKey == "" || cfg.AlpacaSecret == "" {
		return fmt.Errorf("ALPACA_KEY/ALPACA_SECRET are not set: the re-fetch cannot run without them, " +
			"and running without them would quarantine the old bars and replace them with nothing")
	}
	client := alpaca.New(cfg.AlpacaKey, cfg.AlpacaSecret)

	reason := fmt.Sprintf("delisted-cohort re-fetch onto adjustment=%s (was adjustment=all); run %s",
		alpaca.BarAdjustment(), id)
	moved, written, failed := 0, 0, 0
	var failures []string

	for i, c := range cohort {
		if *limit > 0 && i >= *limit {
			fmt.Printf("\nstopping at -limit %d; re-run to continue (progress is per symbol)\n", *limit)
			break
		}
		if c.Refetched {
			continue
		}
		if c.Bars == 0 || c.FirstDay == "" {
			continue
		}
		start, err := time.Parse("2006-01-02", c.FirstDay)
		if err != nil {
			failed++
			failures = append(failures, c.Symbol+": unparseable first day "+c.FirstDay)
			continue
		}

		// ORDER MATTERS. Quarantine first, then fetch: fetching first and
		// quarantining after would capture the fresh bars.
		n, err := st.QuarantineSymbolBars(ctx, c.SymbolID, string(md.TF1d), id, reason, time.Now().Unix())
		if err != nil {
			return fmt.Errorf("%s: quarantine: %w", c.Symbol, err)
		}
		moved += int(n)

		got, err := client.BackfillDailyFrom(ctx, st, c.SymbolID, c.Symbol, start)
		if err != nil {
			// The old bars are already in quarantine and recoverable, so this is
			// reported and the pass continues rather than aborting the cohort
			// halfway with no record of where it stopped.
			failed++
			failures = append(failures, fmt.Sprintf("%s: fetch: %v", c.Symbol, err))
			continue
		}
		written += got
		if got == 0 {
			failures = append(failures, fmt.Sprintf(
				"%s: re-fetch returned 0 bars — its %d old bar(s) are in quarantine run %s", c.Symbol, n, id))
		}
	}

	deltas, err := st.CohortRefetchDeltas(ctx, string(md.TF1d), id)
	if err != nil {
		return err
	}
	changed := 0
	for _, d := range deltas {
		if d.Differing > 0 {
			changed++
		}
	}

	fmt.Printf("\nquarantined : %d old bar(s)\n", moved)
	fmt.Printf("re-fetched  : %d bar(s) on adjustment=%s\n", written, alpaca.BarAdjustment())
	fmt.Printf("changed     : %d of %d symbol(s) have at least one differing close\n", changed, len(deltas))
	if failed > 0 {
		fmt.Printf("failed      : %d symbol(s)\n", failed)
	}
	for _, f := range failures {
		if len(failures) > 15 {
			break
		}
		fmt.Println("  -", f)
	}
	if len(failures) > 15 {
		fmt.Printf("  ... and %d more (see -out)\n", len(failures)-15)
	}
	fmt.Printf("undo with   : sdmaint refetch-cohort -restore %s\n", id)
	fmt.Println("rebuild the point-in-time universe next: sdmaint build-universe")

	if *out != "" {
		blob, err := json.MarshalIndent(map[string]any{
			"generated_utc": time.Now().UTC().Format(time.RFC3339),
			"run_id":        id,
			"adjustment":    alpaca.BarAdjustment(),
			"quarantined":   moved,
			"refetched":     written,
			"symbols":       len(deltas),
			"changed":       changed,
			"failures":      failures,
			"deltas":        deltas,
		}, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*out, blob, 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("delta report written to %s\n", *out)
	}
	return nil
}

// cohortNames resolves the symbol list from an explicit CSV or the staging DB.
//
// The staging filter mirrors importDelisted's exactly (reused=0, n_bars>=minBars,
// cohort<>"too_short"), because the set to REPAIR must be the set that was
// imported — a wider one would quarantine bars this defect never touched.
func cohortNames(stagePath, csv string, minBars int) ([]string, error) {
	if csv != "" {
		var out []string
		for _, s := range strings.Split(csv, ",") {
			if s = strings.TrimSpace(strings.ToUpper(s)); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	}
	if stagePath == "" {
		return nil, fmt.Errorf("one of -staging or -symbols is required")
	}
	db, err := sql.Open("sqlite", "file:"+stagePath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open staging: %w", err)
	}
	defer db.Close() //nolint:errcheck
	rows, err := db.Query(
		`SELECT symbol FROM delisted_symbol
		  WHERE reused = 0 AND n_bars >= ? AND cohort <> 'too_short' ORDER BY symbol`, minBars)
	if err != nil {
		return nil, fmt.Errorf("read staging: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
