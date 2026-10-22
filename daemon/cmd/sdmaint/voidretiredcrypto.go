// voidretiredcrypto voids open crypto forecast outcomes the prediction resolver can never settle
// from stored daily bars, stamps delisted_at on crypto symbols at the UTC day of their last
// stored daily bar, and records one dq_events row of kind predictions_voided_source_retired.
// Dry run by default, opened read-only (SQLite URI mode=ro) so it is safe against the live DB.
// -apply requires -log, writes old values to a CSV opened with O_EXCL BEFORE the write
// transaction, then one transaction with guarded UPDATEs (resolved_at IS NULL plus primary
// key), RowsAffected must be 1 or the whole transaction rolls back. A void sets resolved_at
// with up, fwd_return, settle_ts left NULL, the same shape as store.VoidDeadPredictions.
// Never DELETE; never touches prediction_ledger or predictions, so VerifyLedger and /proof
// are unchanged. The reason is recorded as one dq_events row (pattern of maintain's
// dead_predictions_voided). Raw database/sql, NOT store.Open, so no schema migration runs
// against a DB the deployed daemon owns (cmd/score-1h-revoid pattern). -apply refuses while
// any crypto symbol is still active=1 because then the paused build is not live and crypto
// bars are still arriving. Idempotent: a second -apply finds nothing, writes no CSV and no
// dq_events row.
//
// Why the stamp: pauseCrypto leaves the paused symbols active=0 with delisted_at NULL, and
// tools/accuracy_registry.py measure_universe_completeness counts exactly that shape as an
// UNRESOLVABLE exit, which would flip survivorship_clean false on every published row. The
// stamp is the UTC day of the symbol's last stored daily bar: a dated exit, with no stored
// bar after it for the post-delisting, ticker-reuse or point-in-time-universe readers.

package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const (
	vrcSettledBaseSinceTs = int64(1788807600) // pipeline.settledBaseSinceTs
	vrcLabelBaseSinceTs   = int64(1791072000) // pipeline.labelBaseSinceTs
	vrcDSTSlackSecs       = int64(6 * 3600)   // pipeline.dstStampSlackSecs
	vrcDQKind             = "predictions_voided_source_retired"
	vrcPausedOn           = "2026-10-21"
)

type vrcBar struct {
	ts    int64
	close float64
}

type vrcRow struct {
	sym      int64
	horizon  string
	ts       int64
	prob     float64
	up       sql.NullInt64
	fwd      sql.NullFloat64
	resolved sql.NullInt64
	settle   sql.NullInt64
}

type vrcStamp struct {
	sym  int64
	symb string
	old  sql.NullInt64
	ts   int64
}

type vrcPlan struct {
	void         []vrcRow
	byHorizon    map[string]int
	settleable   int
	regimeOpen   int
	activeCrypto int
	stamps       []vrcStamp
	undated      int
}

func vrcHorizonSecs(h string) (int64, bool) {
	h = strings.TrimSuffix(h, "#pm")
	switch h {
	case string(md.H1d):
		return 86400, true
	case string(md.H1w):
		return 604800, true
	default:
		return 0, false // PredictionResolver only walks 1d, 1w and their #pm twins
	}
}

func vrcAtOrBefore(bars []vrcBar, t int64) int {
	i := sort.Search(len(bars), func(i int) bool { return bars[i].ts > t }) - 1
	if i < 0 {
		return -1
	}
	return i
}

func vrcAtOrAfter(bars []vrcBar, t int64) int {
	i := sort.Search(len(bars), func(i int) bool { return bars[i].ts >= t })
	if i == len(bars) {
		return -1
	}
	return i
}

// vrcSettleable reports whether pipeline.PredictionResolver could EVER grade a
// crypto row (ts, horizon) from bars, the symbol's stored 1d bars ascending. It
// mirrors the resolver step by step and leaves out only the clock gates (ts <=
// now-H, now >= target), which delay a row but never refuse it. The resolver's
// pieces are unexported and need a *store.Store, so this is a mirror;
// TestVoidRetiredCryptoMirrorsResolver pins it by running the real resolver on
// the same fixture.
func vrcSettleable(bars []vrcBar, ts int64, horizon string) bool {
	hs, ok := vrcHorizonSecs(horizon)
	if !ok {
		return false
	}
	// queue (store.UnresolvedPredictions): a 1d bar at or after ts+hs must exist
	if len(bars) == 0 || bars[len(bars)-1].ts < ts+hs {
		return false
	}
	// settledBase
	i := vrcAtOrBefore(bars, ts)
	if i < 0 {
		return false
	}
	base := bars[i]
	if ts >= vrcLabelBaseSinceTs {
		// labelBaseMissing for crypto: the bar at-or-before is a day or more before issue
		if ts-base.ts >= 86400 {
			return false
		}
	} else if ts >= vrcSettledBaseSinceTs && !md.DailyBarSettled(md.Crypto, base.ts, ts) {
		i = vrcAtOrBefore(bars, base.ts-1)
		if i < 0 {
			return false
		}
		base = bars[i]
	}
	target := base.ts + hs - vrcDSTSlackSecs
	j := vrcAtOrAfter(bars, target)
	if j < 0 || base.close <= 0 {
		return false // no forward bar / bad base
	}
	if bars[j].ts-target > 3*hs {
		return false // GAP GUARD (crypto keeps the 3-horizon rule)
	}
	return j+1 < len(bars) // SETTLEMENT GUARD: a later bar must exist
}

func vrcBuildPlan(ctx context.Context, db *sql.DB) (vrcPlan, error) {
	plan := vrcPlan{byHorizon: make(map[string]int)}

	rows, err := db.QueryContext(ctx, `SELECT id, symbol, active, delisted_at FROM symbols WHERE market = ? ORDER BY id`, string(md.Crypto))
	if err != nil {
		return plan, err
	}
	type symInfo struct {
		id       int64
		symbol   string
		active   int
		delisted sql.NullInt64
	}
	var syms []symInfo
	for rows.Next() {
		var s symInfo
		if err := rows.Scan(&s.id, &s.symbol, &s.active, &s.delisted); err != nil {
			rows.Close() //nolint:errcheck
			return plan, err
		}
		syms = append(syms, s)
	}
	if err := rows.Close(); err != nil {
		return plan, err
	}
	if err := rows.Err(); err != nil {
		return plan, err
	}

	barsMap := make(map[int64][]vrcBar)
	for _, s := range syms {
		barRows, err := db.QueryContext(ctx, `SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts`, s.id)
		if err != nil {
			return plan, err
		}
		var bars []vrcBar
		for barRows.Next() {
			var b vrcBar
			if err := barRows.Scan(&b.ts, &b.close); err != nil {
				barRows.Close() //nolint:errcheck
				return plan, err
			}
			bars = append(bars, b)
		}
		if err := barRows.Close(); err != nil {
			return plan, err
		}
		if err := barRows.Err(); err != nil {
			return plan, err
		}
		barsMap[s.id] = bars

		if s.active == 1 {
			plan.activeCrypto++
		}
		if !s.delisted.Valid || s.delisted.Int64 == 0 {
			if len(bars) > 0 {
				lastTs := bars[len(bars)-1].ts
				plan.stamps = append(plan.stamps, vrcStamp{sym: s.id, symb: s.symbol, old: s.delisted, ts: (lastTs / 86400) * 86400}) // UTC day of last stored daily bar
			} else {
				plan.undated++
			}
		}
	}

	outRows, err := db.QueryContext(ctx, `SELECT o.symbol_id, o.horizon, o.ts, o.prob, o.up, o.fwd_return, o.resolved_at, o.settle_ts FROM prediction_outcomes o JOIN symbols s ON s.id = o.symbol_id WHERE s.market = ? AND o.resolved_at IS NULL ORDER BY o.symbol_id, o.horizon, o.ts`, string(md.Crypto))
	if err != nil {
		return plan, err
	}
	for outRows.Next() {
		var r vrcRow
		if err := outRows.Scan(&r.sym, &r.horizon, &r.ts, &r.prob, &r.up, &r.fwd, &r.resolved, &r.settle); err != nil {
			outRows.Close() //nolint:errcheck
			return plan, err
		}
		if vrcSettleable(barsMap[r.sym], r.ts, r.horizon) {
			plan.settleable++
		} else {
			plan.void = append(plan.void, r)
			plan.byHorizon[r.horizon]++
		}
	}
	if err := outRows.Close(); err != nil {
		return plan, err
	}
	if err := outRows.Err(); err != nil {
		return plan, err
	}

	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM regime_outcomes r JOIN symbols s ON s.id = r.symbol_id WHERE s.market = ? AND r.resolved_at IS NULL`, string(md.Crypto)).Scan(&plan.regimeOpen)
	return plan, err
}

func vrcByHorizon(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, m[k])
	}
	return strings.Join(parts, ",")
}

func vrcOpen(path string, write bool) (*sql.DB, error) {
	mode := "ro"
	if write {
		mode = "rw"
	}
	dsn := "file:" + path + "?mode=" + mode + "&_pragma=busy_timeout(30000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close() //nolint:errcheck
		return nil, err
	}
	return db, nil
}

func vrcNullInt(n sql.NullInt64) string {
	if n.Valid {
		return strconv.FormatInt(n.Int64, 10)
	}
	return ""
}

func vrcNullFloat(f sql.NullFloat64) string {
	if f.Valid {
		return strconv.FormatFloat(f.Float64, 'g', 17, 64)
	}
	return ""
}

func vrcWriteLog(path string, p vrcPlan) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{"table", "symbol_id", "horizon", "ts", "prob", "up", "fwd_return", "resolved_at", "settle_ts", "delisted_at_old", "delisted_at_new"}); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	for _, r := range p.void {
		rec := []string{
			"prediction_outcomes",
			strconv.FormatInt(r.sym, 10),
			r.horizon,
			strconv.FormatInt(r.ts, 10),
			strconv.FormatFloat(r.prob, 'g', 17, 64),
			vrcNullInt(r.up),
			vrcNullFloat(r.fwd),
			vrcNullInt(r.resolved),
			vrcNullInt(r.settle),
			"",
			"",
		}
		if err := w.Write(rec); err != nil {
			f.Close() //nolint:errcheck
			return err
		}
	}
	for _, s := range p.stamps {
		rec := []string{
			"symbols",
			strconv.FormatInt(s.sym, 10),
			"",
			"",
			"",
			"",
			"",
			"",
			"",
			vrcNullInt(s.old),
			strconv.FormatInt(s.ts, 10),
		}
		if err := w.Write(rec); err != nil {
			f.Close() //nolint:errcheck
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	return f.Close()
}

func vrcRun(ctx context.Context, dbPath string, apply bool, logPath string, now int64, out io.Writer) (vrcPlan, int, error) {
	if apply && logPath == "" {
		return vrcPlan{}, 0, fmt.Errorf("-apply requires -log <csv>: the old values are written there before any update")
	}
	db, err := vrcOpen(dbPath, apply)
	if err != nil {
		return vrcPlan{}, 0, err
	}
	defer db.Close() //nolint:errcheck

	plan, err := vrcBuildPlan(ctx, db)
	if err != nil {
		return plan, 0, err
	}

	stampedCount := len(plan.stamps)
	voidCount := len(plan.void)
	summary := func(dry bool, voided, stamped int) {
		_, _ = fmt.Fprintf(out, "VOID-RETIRED-CRYPTO dry_run=%t voided=%d by_horizon=%s settleable_unresolved=%d regime_open=%d delisted_stamped=%d active_crypto=%d\n",
			dry, voided, vrcByHorizon(plan.byHorizon), plan.settleable, plan.regimeOpen, stamped, plan.activeCrypto)
	}

	if plan.undated > 0 {
		_, _ = fmt.Fprintf(out, "NOTE: %d crypto symbol(s) hold no daily bar, so no exit date can be stamped; left as they are\n", plan.undated)
	}

	if !apply {
		if plan.activeCrypto > 0 {
			_, _ = fmt.Fprintf(out, "NOTE: %d crypto symbol(s) are still active=1: the paused build is not live and crypto bars are still arriving, so these counts are informative only; -apply refuses until pauseCrypto has run\n", plan.activeCrypto)
		}
		summary(true, voidCount, stampedCount)
		_, _ = fmt.Fprintf(out, "DRY RUN: nothing written\n")
		return plan, 0, nil
	}

	if plan.activeCrypto > 0 {
		return plan, 0, fmt.Errorf("refusing: %d crypto symbol(s) still active=1; deploy the paused build (pauseCrypto runs at boot) and re-run — nothing written", plan.activeCrypto)
	}

	if voidCount == 0 && stampedCount == 0 {
		_, _ = fmt.Fprintf(out, "nothing to void or stamp; nothing written (no log created)\n")
		summary(false, 0, 0)
		return plan, 0, nil
	}

	if err := vrcWriteLog(logPath, plan); err != nil {
		return plan, 0, fmt.Errorf("log not written, nothing updated: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return plan, 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	for _, r := range plan.void {
		res, err := tx.ExecContext(ctx, `UPDATE prediction_outcomes SET resolved_at = ? WHERE symbol_id = ? AND horizon = ? AND ts = ? AND resolved_at IS NULL`, now, r.sym, r.horizon, r.ts)
		if err != nil {
			return plan, 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return plan, 0, err
		}
		if n != 1 {
			return plan, 0, fmt.Errorf("prediction_outcomes (%d,%s,%d) changed underneath (did the daemon resolve it?); rolled back, nothing written", r.sym, r.horizon, r.ts)
		}
	}

	for _, s := range plan.stamps {
		res, err := tx.ExecContext(ctx, `UPDATE symbols SET delisted_at = ? WHERE id = ? AND market = ? AND active = 0 AND (delisted_at IS NULL OR delisted_at = 0)`, s.ts, s.sym, string(md.Crypto))
		if err != nil {
			return plan, 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return plan, 0, err
		}
		if n != 1 {
			return plan, 0, fmt.Errorf("symbol %d (%s) changed underneath; rolled back, nothing written", s.sym, s.symb)
		}
	}

	detail := fmt.Sprintf("source_retired: crypto paused %s (no licensed crypto data source). Voided %d open crypto forecast outcome(s) the prediction resolver can never settle from stored daily bars (%s); %d settleable left open for the resolver; %d open crypto regime_outcomes untouched; delisted_at stamped on %d crypto symbol(s) at the UTC day of their last stored daily bar. Old values: %s", vrcPausedOn, voidCount, vrcByHorizon(plan.byHorizon), plan.settleable, plan.regimeOpen, stampedCount, logPath)
	if _, err := tx.ExecContext(ctx, `INSERT INTO dq_events (ts, kind, detail) VALUES (?, ?, ?)`, now, vrcDQKind, detail); err != nil {
		return plan, 0, err
	}

	if err := tx.Commit(); err != nil {
		return plan, 0, err
	}

	_, _ = fmt.Fprintf(out, "APPLIED: %d forecast outcome(s) voided and %d crypto symbol(s) stamped in one transaction; old values in %s\n", voidCount, stampedCount, logPath)
	summary(false, voidCount, stampedCount)
	return plan, voidCount, nil
}

func voidRetiredCrypto(args []string) error {
	fs := flag.NewFlagSet("void-retired-crypto", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	apply := fs.Bool("apply", false, "void and stamp (default: dry run, opened read-only)")
	logPath := fs.String("log", "", "CSV of old values, created with O_EXCL before any write (required with -apply)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, _, err := vrcRun(context.Background(), *dbPath, *apply, *logPath, time.Now().Unix(), os.Stdout)
	return err
}
