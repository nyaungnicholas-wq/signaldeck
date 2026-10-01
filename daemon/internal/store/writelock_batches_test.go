package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Sign-in's account writes (Store.aw) give up after 12s, and the priority gate
// can only let them in at the main writer's statement and transaction
// boundaries. A bulk write done as ONE statement or ONE transaction therefore
// locks sign-in out for its whole length (logged live 2026-09-30: a
// score_outcomes prune 5m19s, an alpaca bar page 1m47s).
//
// midBulkWrite starts work, waits until the read pool sees countSQL move off
// before (work has committed something), then makes one priority account
// write that stores countSQL as seen from INSIDE that write: what was
// committed at the moment the account write held the SQLite lock. Between
// batches that is a partial count; behind a single statement or transaction
// it can only be all of the work.
func midBulkWrite(t *testing.T, st *Store, before int64, countSQL string, work func() error) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := st.w.ExecContext(ctx, `CREATE TABLE lock_probe (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- work() }()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Millisecond) {
		var n int64
		if err := st.db.QueryRowContext(ctx, countSQL).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bulk write committed nothing in 30s")
		}
	}
	release := st.Priority()
	_, err := st.authW().ExecContext(ctx, `INSERT INTO lock_probe (n) `+countSQL)
	release()
	if err != nil {
		t.Fatalf("account write during the bulk write: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var seen int64
	if err := st.db.QueryRowContext(ctx, `SELECT n FROM lock_probe`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	return seen
}

// A large retention prune must let an account write in between its batches,
// and still prune exactly the rows below the cutoff.
func TestRetentionPruneLetsAnAccountWriteIn(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	const old, recent, cutoff = 20000, 5000, 1000000
	if _, err := st.w.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO score_outcomes (symbol_id, horizon, ts, score, fwd_return, resolved_at)
		SELECT i % 400 + 1, '1d', CASE WHEN i <= ? THEN i ELSE 2000000 + i END, 0.1, 0.01, 1
		FROM n`, old+recent, old); err != nil {
		t.Fatal(err)
	}
	countOld := fmt.Sprintf(`SELECT count(*) FROM score_outcomes WHERE ts < %d`, cutoff)
	seen := midBulkWrite(t, st, old, countOld, func() error {
		n, err := st.DeleteScoreOutcomesBefore(ctx, cutoff)
		if err == nil && n != old {
			err = fmt.Errorf("pruned %d rows, want %d", n, old)
		}
		return err
	})
	if seen == 0 {
		t.Fatalf("the account write found all %d old rows already pruned: it waited out the whole prune, one statement holding the write lock", old)
	}
	var left, total int64
	if err := st.db.QueryRowContext(ctx, countOld).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM score_outcomes`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if left != 0 || total != recent {
		t.Fatalf("after the prune: %d old rows left (want 0), %d rows total (want the %d recent ones)", left, total, recent)
	}
	t.Logf("account write landed with %d of %d old rows still unpruned", seen, old)
}

// A large UpsertBars call must let an account write in between its chunks.
func TestUpsertBarsLetsAnAccountWriteIn(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	bars := make([]md.Bar, 20000)
	for i := range bars {
		bars[i] = md.Bar{SymbolID: int64(i%400 + 1), TF: md.TF1m, Ts: int64(i), Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}
	}
	seen := midBulkWrite(t, st, 0, `SELECT count(*) FROM bars`, func() error { return st.UpsertBars(ctx, bars) })
	if seen == int64(len(bars)) {
		t.Fatalf("the account write saw all %d bars: it waited out the whole UpsertBars call, one transaction holding the write lock", len(bars))
	}
	var total int64
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM bars`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != int64(len(bars)) {
		t.Fatalf("%d bars stored, want %d", total, len(bars))
	}
	t.Logf("account write landed with %d of %d bars committed", seen, len(bars))
}

// Retention picks score_outcomes rows by ts alone, and ts is the primary key's
// third column: without its own index every prune batch scans the whole table
// while holding the write lock, and every archive read sorts all of it.
func TestScoreOutcomesRetentionSeeksOnTs(t *testing.T) {
	st := openTemp(t)
	for _, q := range []string{
		// DeleteScoreOutcomesBefore's key read (the reader pass that picks each
		// batch; the DELETE itself then seeks on the full primary key).
		`SELECT symbol_id, horizon, ts FROM score_outcomes WHERE ts < 1 LIMIT 20000`,
		// ScoreOutcomesBefore, the archive read that precedes it.
		`SELECT symbol_id, horizon, ts FROM score_outcomes WHERE ts < 1 ORDER BY ts LIMIT 1000`,
	} {
		rows, err := st.w.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		if p := strings.Join(plan, "; "); strings.Contains(p, "SCAN score_outcomes") {
			t.Fatalf("score_outcomes retention query full-scans: %s\nquery: %s", p, q)
		}
	}
}

// The filings prune (the 18.6 s holder named in SD-51) must yield between its
// batches too, and still prune exactly the rows below the cutoff.
func TestFilingsPruneLetsAnAccountWriteIn(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "FIL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	const old, recent, cutoff = 20000, 500, 1000000
	if _, err := st.w.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO filings (id, symbol_id, form, filed_ts)
		SELECT 'acc-' || i, ?, '8-K', CASE WHEN i <= ? THEN i ELSE 2000000 + i END
		FROM n`, old+recent, sym.ID, old); err != nil {
		t.Fatal(err)
	}
	countOld := fmt.Sprintf(`SELECT count(*) FROM filings WHERE filed_ts < %d`, cutoff)
	seen := midBulkWrite(t, st, old, countOld, func() error {
		n, err := st.DeleteFilingsBefore(ctx, cutoff)
		if err == nil && n != old {
			err = fmt.Errorf("pruned %d filings, want %d", n, old)
		}
		return err
	})
	if seen == 0 {
		t.Fatalf("the account write found all %d old filings already pruned: it waited out the whole prune", old)
	}
	var total int64
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM filings`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != recent {
		t.Fatalf("%d filings left, want the %d recent ones", total, recent)
	}
}
