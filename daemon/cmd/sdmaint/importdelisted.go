package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// importDelisted merges the delisted-company staging DB built by
// tools/alpha/fetch_delisted.py + tag_delisted.py into signaldeck.db.
//
// WHY THIS IS A GO COMMAND AND NOT A PYTHON SCRIPT
// The single-writer-connection doctrine lives in internal/store: exactly one
// connection may write signaldeck.db. A Python script writing 169k bars
// directly would bypass that and can corrupt the file if the daemon is up. So
// the Python side stays read-only and produces a staging artifact; this applies
// it through store's own writer, like apply-delistings already does.
//
// SAFETY
//   - -dry-run reports precisely what would change and writes nothing.
//   - Symbols already ACTIVE in the live DB are refused, never overwritten:
//     exchanges recycle tickers, and stamping a dead company onto a live row
//     would delete a real company from every point-in-time universe afterwards.
//   - Cohorts may be excluded (-skip-cohorts) so the 2021-22 SPAC shells can be
//     held out rather than silently diluting the delisting signal.
//   - Bars are upserted by (symbol_id, tf, ts), so re-running is idempotent.
func importDelisted(args []string) error {
	fs := flag.NewFlagSet("import-delisted", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	stagePath := fs.String("staging", "", "staging DB from tools/alpha/fetch_delisted.py")
	dryRun := fs.Bool("dry-run", false, "report what would change, write nothing")
	minBars := fs.Int("min-bars", 60, "skip symbols with fewer daily bars than this")
	skipCohorts := fs.String("skip-cohorts", "too_short",
		"comma-separated cohorts to exclude (e.g. too_short,spac_shell)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stagePath == "" {
		return fmt.Errorf("-staging is required")
	}

	skip := map[string]bool{}
	for _, c := range splitCSV(*skipCohorts) {
		skip[c] = true
	}

	stage, err := sql.Open("sqlite", "file:"+*stagePath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open staging: %w", err)
	}
	defer stage.Close() //nolint:errcheck

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	rows, err := stage.QueryContext(ctx,
		`SELECT symbol, COALESCE(name,''), COALESCE(cohort,'unknown'), n_bars, last_bar, first_bar
		   FROM delisted_symbol WHERE reused=0 ORDER BY symbol`)
	if err != nil {
		return fmt.Errorf("read staging symbols: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	type cand struct {
		symbol, name, cohort, lastBar, firstBar string
		nBars                                   int
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.symbol, &c.name, &c.cohort, &c.nBars, &c.lastBar, &c.firstBar); err != nil {
			return err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var (
		imported, skippedCohort, skippedShort, refusedActive, barsWritten int
		refusals                                                          []string
		byCohort                                                          = map[string]int{}
	)

	for _, c := range cands {
		if skip[c.cohort] {
			skippedCohort++
			continue
		}
		if c.nBars < *minBars {
			skippedShort++
			continue
		}
		delistedAt, err := time.Parse("2006-01-02", c.lastBar)
		if err != nil {
			refusals = append(refusals, c.symbol+": unparseable last_bar "+c.lastBar)
			continue
		}
		// added_at is the FIRST bar, not now: TradableAt filters `added_at <= ts`,
		// so an import-dated added_at hides the company from every past universe.
		addedAt, err := time.Parse("2006-01-02", c.firstBar)
		if err != nil {
			refusals = append(refusals, c.symbol+": unparseable first_bar "+c.firstBar)
			continue
		}

		if *dryRun {
			// Mirror the live guard without writing: is this ticker active today?
			var active int
			err := st.DB().QueryRowContext(ctx,
				`SELECT active FROM symbols WHERE symbol=? AND market=?`,
				c.symbol, string(md.Stocks)).Scan(&active)
			if err == nil && active == 1 {
				refusedActive++
				refusals = append(refusals, c.symbol+": ACTIVE in live DB")
				continue
			}
			imported++
			byCohort[c.cohort]++
			barsWritten += c.nBars
			continue
		}

		sym, err := st.UpsertHistoricalSymbol(ctx, c.symbol, md.Stocks, c.name, addedAt.Unix(), delistedAt.Unix())
		if err != nil {
			refusedActive++
			refusals = append(refusals, c.symbol+": "+err.Error())
			continue
		}

		bars, err := stageBars(ctx, stage, c.symbol, sym.ID)
		if err != nil {
			return fmt.Errorf("%s: read staging bars: %w", c.symbol, err)
		}
		if err := st.UpsertBars(ctx, bars); err != nil {
			return fmt.Errorf("%s: write bars: %w", c.symbol, err)
		}
		imported++
		byCohort[c.cohort]++
		barsWritten += len(bars)
	}

	report := map[string]any{
		"dry_run":            *dryRun,
		"staging_candidates": len(cands),
		"imported":           imported,
		"bars_written":       barsWritten,
		"skipped_cohort":     skippedCohort,
		"skipped_too_short":  skippedShort,
		"refused_active":     refusedActive,
		"by_cohort":          byCohort,
	}
	if len(refusals) > 0 {
		if len(refusals) > 20 {
			refusals = append(refusals[:20], fmt.Sprintf("... and %d more", len(refusals)-20))
		}
		report["refusals"] = refusals
	}
	out, _ := json.MarshalIndent(report, "", "  ")
	fmt.Fprintln(os.Stdout, string(out))
	return nil
}

// repairAddedAt rewrites every symbol's added_at to its first daily bar.
//
// added_at was the date the daemon first SAW a symbol, not the date it started
// trading, and TradableAt filters `added_at <= ts`. Measured 2026-08-02, that
// made the point-in-time universe return ZERO symbols for 2021, 2023 and 2025
// alike — the survivorship accessor was returning nothing at all, silently, for
// every historical date.
func repairAddedAt(args []string) error {
	fs := flag.NewFlagSet("repair-added-at", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	probe := fs.String("probe", "2021-06-01,2023-06-01,2025-06-01",
		"comma-separated dates to report TradableAt counts for, before and after")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	before := map[string]int{}
	for _, d := range splitCSV(*probe) {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			return fmt.Errorf("bad -probe date %q: %w", d, err)
		}
		syms, err := st.TradableAt(ctx, t.Unix())
		if err != nil {
			return err
		}
		before[d] = len(syms)
	}

	repaired, skipped, err := st.RepairAddedAtFromBars(ctx)
	if err != nil {
		return fmt.Errorf("repair: %w", err)
	}

	after := map[string]int{}
	for _, d := range splitCSV(*probe) {
		t, _ := time.Parse("2006-01-02", d)
		syms, err := st.TradableAt(ctx, t.Unix())
		if err != nil {
			return err
		}
		after[d] = len(syms)
	}

	out, _ := json.MarshalIndent(map[string]any{
		"repaired":              repaired,
		"skipped_no_daily_bars": skipped,
		"tradable_at_before":    before,
		"tradable_at_after":     after,
	}, "", "  ")
	fmt.Fprintln(os.Stdout, string(out))
	return nil
}

func stageBars(ctx context.Context, stage *sql.DB, symbol string, symbolID int64) ([]md.Bar, error) {
	rows, err := stage.QueryContext(ctx,
		`SELECT ts, open, high, low, close, volume FROM delisted_bar
		  WHERE symbol=? ORDER BY ts`, symbol)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Bar
	for rows.Next() {
		b := md.Bar{SymbolID: symbolID, TF: md.TF1d}
		if err := rows.Scan(&b.Ts, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		if r != ' ' {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
