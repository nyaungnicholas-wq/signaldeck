// Command rv-reresolve re-resolves HAR volatility outcomes that RVOutcomeWorker
// froze from a FORMING bar, before it checked that the outcome window's last
// session had settled (fixed 3fdddf7; ledger
// audits/2026-10-01-rv-grader-ambiguities.md).
//
// A row is affected when its resolved_ts falls before md.DailyBarSettled's bound
// for the window's last bar -- exactly the rule the resolver now enforces. Each
// one is recomputed with the resolver's own estimator (harrv.RVSeries +
// harrv.TargetAt) from today's settled bars. Forecasts and nulls are never
// touched: only the measured outcome changes. A window that is no longer
// estimable on settled bars is closed as ungradable with a stated reason, which
// is what the fixed resolver would have done.
//
// Dry run by default. -apply writes the old values to -log FIRST, then updates
// every row in one transaction, each guarded on its old (actual, resolved_ts)
// so a concurrent change is never overwritten. Re-runnable: corrected rows carry
// a settled resolved_ts and are not selected again.
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
	"math"
	"os"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/harrv"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	_ "modernc.org/sqlite"
)

const ungradableReason = "re-resolved 2026-10-01: first resolved from a forming bar, and the settled window is not estimable"

type row struct {
	sym, ts     int64
	h           int
	oldActual   float64
	oldResolved int64
	newActual   float64
	estimable   bool
}

func main() {
	dbPath := flag.String("db", "", "path to signaldeck.db")
	apply := flag.Bool("apply", false, "write the corrections (default: dry run)")
	logPath := flag.String("log", "", "CSV of old values, written before any update (required with -apply)")
	flag.Parse()
	if *dbPath == "" || (*apply && *logPath == "") {
		fmt.Fprintln(os.Stderr, "usage: rv-reresolve -db PATH [-apply -log CSV]")
		os.Exit(2)
	}
	if err := run(*dbPath, *apply, *logPath); err != nil {
		fmt.Fprintln(os.Stderr, "rv-reresolve:", err)
		os.Exit(1)
	}
}

func run(dbPath string, apply bool, logPath string) error {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(30000)")
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck
	db.SetMaxOpenConns(1)

	q, err := db.QueryContext(ctx, `SELECT symbol_id, ts, horizon, actual, resolved_ts
		FROM rv_forecasts WHERE actual IS NOT NULL ORDER BY symbol_id, ts, horizon`)
	if err != nil {
		return err
	}
	var all []row
	for q.Next() {
		var r row
		if err := q.Scan(&r.sym, &r.ts, &r.h, &r.oldActual, &r.oldResolved); err != nil {
			q.Close() //nolint:errcheck
			return err
		}
		all = append(all, r)
	}
	q.Close() //nolint:errcheck
	if err := q.Err(); err != nil {
		return err
	}

	var flagged []row
	noWindow, okRows, okSame := 0, 0, 0
	type series struct {
		rv []float64
		ts []int64
	}
	cache := map[int64]series{}
	for _, r := range all {
		s, ok := cache[r.sym]
		if !ok {
			bars, err := loadBars(ctx, db, r.sym)
			if err != nil {
				return err
			}
			s.rv, s.ts, _ = harrv.RVSeries(bars)
			cache[r.sym] = s
		}
		idx := -1
		for i, t := range s.ts {
			if t == r.ts {
				idx = i
				break
			}
		}
		last := idx + r.h
		if idx < 0 || last >= len(s.ts) {
			noWindow++ // cannot locate the window today; left as it is
			continue
		}
		if md.DailyBarSettled(md.Stocks, s.ts[last], r.oldResolved) {
			// Resolved from a settled window: correct already. Recomputing it
			// is the self-check that this tool computes what the resolver did.
			okRows++
			if v, ok := harrv.TargetAt(s.rv, idx, harrv.Horizon(r.h)); ok && v == r.oldActual {
				okSame++
			}
			continue
		}
		r.newActual, r.estimable = harrv.TargetAt(s.rv, idx, harrv.Horizon(r.h))
		flagged = append(flagged, r)
	}

	for _, h := range []int{1, 5} {
		var n, changed, same, unest int
		maxRel := 0.0
		for _, r := range flagged {
			if r.h != h {
				continue
			}
			n++
			switch {
			case !r.estimable:
				unest++
			case r.newActual == r.oldActual:
				same++
			default:
				changed++
				maxRel = math.Max(maxRel, math.Abs(r.newActual-r.oldActual)/r.oldActual)
			}
		}
		fmt.Printf("h=%d: %d resolved before their window settled; %d change, %d identical, %d now unestimable (-> ungradable); max relative change %.3g\n",
			h, n, changed, same, unest, maxRel)
	}
	fmt.Printf("self-check: %d of %d rows resolved from a settled window recompute bit-identically\n", okSame, okRows)
	fmt.Printf("%d resolved rows scanned; %d whose window cannot be located today were left untouched\n", len(all), noWindow)
	if !apply {
		fmt.Println("DRY RUN: nothing written")
		return nil
	}

	if err := writeLog(logPath, flagged); err != nil {
		return fmt.Errorf("log not written, nothing updated: %w", err)
	}
	now := time.Now().Unix()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	wrote := 0
	for _, r := range flagged {
		var res sql.Result
		if r.estimable {
			res, err = tx.ExecContext(ctx, `UPDATE rv_forecasts SET actual=?, resolved_ts=?
				WHERE symbol_id=? AND ts=? AND horizon=? AND actual=? AND resolved_ts=?`,
				r.newActual, now, r.sym, r.ts, r.h, r.oldActual, r.oldResolved)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE rv_forecasts SET actual=NULL, ungradable=?, resolved_ts=?
				WHERE symbol_id=? AND ts=? AND horizon=? AND actual=? AND resolved_ts=?`,
				ungradableReason, now, r.sym, r.ts, r.h, r.oldActual, r.oldResolved)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("row (%d,%d,h%d) changed underneath; rolled back, nothing written", r.sym, r.ts, r.h)
		}
		wrote++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Printf("APPLIED: %d rows corrected in one transaction; old values in %s\n", wrote, logPath)
	return nil
}

// loadBars reads one symbol's daily bars, the same rows store.Bars returns.
func loadBars(ctx context.Context, db *sql.DB, sym int64) ([]md.Bar, error) {
	rows, err := db.QueryContext(ctx, `SELECT ts, open, high, low, close, volume FROM bars
		WHERE symbol_id=? AND tf=? ORDER BY ts`, sym, string(md.TF1d))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Bar
	for rows.Next() {
		b := md.Bar{SymbolID: sym, TF: md.TF1d}
		if err := rows.Scan(&b.Ts, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func writeLog(path string, rs []row) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"symbol_id", "ts", "horizon", "old_actual", "old_resolved_ts", "new_actual", "new_state"})
	for _, r := range rs {
		state, na := "resolved", strconv.FormatFloat(r.newActual, 'g', 17, 64)
		if !r.estimable {
			state, na = "ungradable", ""
		}
		_ = w.Write([]string{strconv.FormatInt(r.sym, 10), strconv.FormatInt(r.ts, 10), strconv.Itoa(r.h),
			strconv.FormatFloat(r.oldActual, 'g', 17, 64), strconv.FormatInt(r.oldResolved, 10), na, state})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	return f.Close()
}
