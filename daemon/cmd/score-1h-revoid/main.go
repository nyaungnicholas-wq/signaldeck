// Command score-1h-revoid re-voids stock 1h score outcomes that the outcome
// resolver graded across a data hole (ledger H1-1H-GAP).
//
// Commit 36625a2 (SD-55, first deployed as cf9cdad at 2026-10-01 09:17:38Z)
// exempted stock rows from the 3-horizon gap rule whenever no whole NYSE session
// closed between target and the forward bar. That is right for daily bars and
// wrong for 1h, which resolves on 1m bars: no session closes overnight, so a
// 17:39 ET -> 09:19 ET move was graded as a one-hour return. The fixed resolver
// voids such rows again; this tool re-applies that rule to stock 1h rows
// resolved since the deploy. A VOID is resolved_at set with fwd_return NULL, so
// the fix is fwd_return=NULL; resolved_at and settle_ts are left as they are
// (settle_ts is derived identically for graded and void rows).
//
// Dry run by default. -apply writes the old values to -log FIRST, then updates
// every flagged row in one transaction, each guarded on its old (fwd_return,
// resolved_at). Re-runnable: a voided row has fwd_return NULL and is never
// selected again.
//
// It opens the database with database/sql, NOT store.Open, so it never applies
// a schema migration from this build to a database a deployed daemon owns.
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	_ "modernc.org/sqlite"
)

const (
	// deployTs is cf9cdad's first worker_runs.started_at: the first binary carrying 36625a2.
	deployTs int64 = 1790846258
	// hSecs mirrors maintain.horizonSeconds(md.H1h); the resolver voids fwd.Ts-target > 3*hSecs.
	hSecs int64 = 3600
)

type row struct {
	sym, ts       int64
	oldFwd        float64
	oldResolved   int64
	baseTs, fwdTs int64
}

func main() {
	dbPath := flag.String("db", "", "path to signaldeck.db")
	apply := flag.Bool("apply", false, "write the corrections (default: dry run)")
	logPath := flag.String("log", "", "CSV of old values, written before any update (required with -apply)")
	flag.Parse()
	if *dbPath == "" || (*apply && *logPath == "") {
		fmt.Fprintln(os.Stderr, "usage: score-1h-revoid -db PATH [-apply -log CSV]")
		os.Exit(2)
	}
	if _, err := run(*dbPath, *apply, *logPath, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "score-1h-revoid:", err)
		os.Exit(1)
	}
}

func run(dbPath string, apply bool, logPath string, out io.Writer) (int, error) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(30000)")
	if err != nil {
		return 0, err
	}
	defer db.Close() //nolint:errcheck
	db.SetMaxOpenConns(1)

	q, err := db.QueryContext(ctx,
		`SELECT c.symbol_id, c.ts, c.fwd_return, c.resolved_at, c.base_ts,
			(SELECT MIN(b.ts) FROM bars b WHERE b.symbol_id = c.symbol_id AND b.tf = ? AND b.ts >= c.base_ts + 3600) AS fwd_ts
		FROM (
			SELECT o.symbol_id, o.ts, o.fwd_return, o.resolved_at,
				(SELECT MAX(b.ts) FROM bars b WHERE b.symbol_id = o.symbol_id AND b.tf = ? AND b.ts <= o.ts) AS base_ts
			FROM score_outcomes o JOIN symbols s ON s.id = o.symbol_id
			WHERE o.horizon = ? AND s.market = ? AND o.fwd_return IS NOT NULL AND o.resolved_at >= ?
		) c
		ORDER BY c.symbol_id, c.ts`,
		string(md.TF1m), string(md.TF1m), string(md.H1h), string(md.Stocks), deployTs)
	if err != nil {
		return 0, err
	}
	var scanned int
	var flagged []row
	var unlocatable int
	var maxGapSec float64
	for q.Next() {
		var r row
		var baseTsNull, fwdTsNull sql.NullInt64
		if err := q.Scan(&r.sym, &r.ts, &r.oldFwd, &r.oldResolved, &baseTsNull, &fwdTsNull); err != nil {
			q.Close() //nolint:errcheck
			return 0, err
		}
		scanned++
		if !baseTsNull.Valid || !fwdTsNull.Valid {
			unlocatable++
			continue
		}
		r.baseTs = baseTsNull.Int64
		r.fwdTs = fwdTsNull.Int64
		gap := float64(r.fwdTs - r.baseTs - hSecs)
		if gap > 3*float64(hSecs) {
			flagged = append(flagged, r)
			if gap > maxGapSec {
				maxGapSec = gap
			}
		}
	}
	q.Close() //nolint:errcheck
	if err := q.Err(); err != nil {
		return 0, err
	}
	maxGapH := maxGapSec / 3600.0
	_, _ = fmt.Fprintf(out, "%d stock 1h outcomes graded since %d scanned; %d graded across a 1m-bar hole > 3h (max %.1fh past target) -> void; %d without a base/forward bar today left untouched\n",
		scanned, deployTs, len(flagged), maxGapH, unlocatable)
	if !apply {
		_, _ = fmt.Fprintln(out, "DRY RUN: nothing written")
		return len(flagged), nil
	}

	if err := writeLog(logPath, flagged); err != nil {
		return 0, fmt.Errorf("log not written, nothing updated: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	wrote := 0
	for _, r := range flagged {
		res, err := tx.ExecContext(ctx,
			`UPDATE score_outcomes SET fwd_return = NULL WHERE symbol_id = ? AND horizon = ? AND ts = ? AND fwd_return = ? AND resolved_at = ?`,
			r.sym, string(md.H1h), r.ts, r.oldFwd, r.oldResolved)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return 0, fmt.Errorf("row (%d,%d,1h) changed underneath; rolled back, nothing written", r.sym, r.ts)
		}
		wrote++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	_, _ = fmt.Fprintf(out, "APPLIED: %d rows voided in one transaction; old values in %s\n", wrote, logPath)
	return wrote, nil
}

func writeLog(path string, rs []row) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"symbol_id", "ts", "horizon", "old_fwd_return", "old_resolved_at", "base_ts", "fwd_ts", "gap_hours"})
	for _, r := range rs {
		gapHours := float64(r.fwdTs-r.baseTs-hSecs) / 3600.0
		_ = w.Write([]string{
			strconv.FormatInt(r.sym, 10),
			strconv.FormatInt(r.ts, 10),
			"1h",
			strconv.FormatFloat(r.oldFwd, 'g', 17, 64),
			strconv.FormatInt(r.oldResolved, 10),
			strconv.FormatInt(r.baseTs, 10),
			strconv.FormatInt(r.fwdTs, 10),
			strconv.FormatFloat(gapHours, 'f', 2, 64),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	return f.Close()
}
