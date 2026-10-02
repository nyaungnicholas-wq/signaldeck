package store

import (
	"context"
	"fmt"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The Downsampler hands PruneSnaps a whole 50k-row archive batch; as one DELETE
// that held the write lock for all of it.
func TestPruneSnapsLetsAnAccountWriteIn(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	const old, recent, cutoff = 20000, 500, 1000000
	if _, err := st.w.ExecContext(ctx,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?) `+
			`INSERT INTO snapshots_1s (symbol_id, ts, bid, ask, mid) `+
			`SELECT i % 50 + 1, CASE WHEN i <= ? THEN i ELSE 2000000 + i END, 1, 1, 1 FROM n`,
		old+recent, old); err != nil {
		t.Fatalf("insert snapshots: %v", err)
	}
	countOld := fmt.Sprintf("SELECT count(*) FROM snapshots_1s WHERE ts < %d", cutoff)
	seen := midBulkWrite(t, st, old, countOld, func() error {
		n, err := st.PruneSnaps(ctx, cutoff)
		if err == nil && n != old {
			err = fmt.Errorf("pruned %d snapshots, want %d", n, old)
		}
		return err
	})
	if seen == 0 {
		t.Fatalf("the account write found all %d old snapshots already pruned: it waited out the whole prune, one statement holding the write lock", old)
	}
	var left int64
	if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM snapshots_1s WHERE ts < ?", cutoff).Scan(&left); err != nil {
		t.Fatalf("left query: %v", err)
	}
	var total int64
	if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM snapshots_1s").Scan(&total); err != nil {
		t.Fatalf("total query: %v", err)
	}
	if left != 0 || total != recent {
		t.Fatalf("after prune: left=%d, total=%d; want left=0, total=%d", left, total, recent)
	}
	t.Logf("account write landed with %d of %d old snapshots still unpruned", seen, old)
}

// The composite_scores downsample held the write lock 35.4 s as one DELETE on
// 2026-10-01; it must yield between UTC days and still keep exactly the last
// row per (symbol, horizon, day).
func TestCompositeDailyPruneLetsAnAccountWriteIn(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	const syms, days, perDay = 50, 20, 20
	for k := 1; k <= syms; k++ {
		s, err := st.UpsertSymbol(ctx, fmt.Sprintf("CMP%02d", k), md.Stocks, "")
		if err != nil {
			t.Fatalf("UpsertSymbol: %v", err)
		}
		if s.ID != int64(k) {
			t.Fatalf("symbol %d got id %d; the fixture maps i %% syms + 1 to ids", k, s.ID)
		}
	}
	if _, err := st.w.ExecContext(ctx,
		`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < ?) `+
			`INSERT INTO composite_scores (symbol_id, ts, horizon, score, curve_pct, edge, payload) `+
			`SELECT i % ? + 1, (i / ?) % ? * 86400 + (i / (? * ?)) * 60 + 3600, '1d', 5, 50, 0.01, '{}' FROM n`,
		syms*days*perDay-1, syms, syms, days, syms, days); err != nil {
		t.Fatalf("insert composite_scores: %v", err)
	}
	const total = syms * days * perDay
	seen := midBulkWrite(t, st, total, "SELECT count(*) FROM composite_scores", func() error {
		n, err := st.PruneCompositeKeepDailyLast(ctx, days*86400)
		if err == nil && n != total-syms*days {
			err = fmt.Errorf("pruned %d rows, want %d", n, total-syms*days)
		}
		return err
	})
	if seen == syms*days {
		t.Fatalf("the account write saw the prune finished: it waited out the whole prune, one statement holding the write lock")
	}
	var left, groups int64
	if err := st.db.QueryRowContext(ctx, "SELECT count(*), count(DISTINCT symbol_id || ':' || (ts/86400)) FROM composite_scores").Scan(&left, &groups); err != nil {
		t.Fatalf("query left/groups: %v", err)
	}
	const want = syms * days
	if left != want || groups != want {
		t.Fatalf("after the prune: %d rows in %d (symbol, day) groups, want %d in %d", left, groups, want, want)
	}
	var notLast int64
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM composite_scores c WHERE EXISTS (SELECT 1 FROM composite_scores d WHERE d.symbol_id=c.symbol_id AND d.horizon=c.horizon AND d.ts/86400=c.ts/86400 AND d.ts>c.ts)`).Scan(&notLast); err != nil {
		t.Fatalf("notLast query: %v", err)
	}
	if notLast != 0 {
		t.Fatalf("after prune, %d rows are not the last of their day", notLast)
	}
}

// One UTC day per statement was still 1m36.8s on 2026-10-01 22:45 (a busy day
// of scores). Within a day the prune must yield between short batches too.
func TestScoresDailyPruneBatchesWithinADay(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	const syms, perDay = 50, 400 // one day, 19,950 deletable rows: ~20 batches
	if _, err := st.w.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO scores (symbol_id, horizon, ts, score, components)
		SELECT i % ? + 1, '1d', (i / ?) * 60 + 3600, 0.5, '[]' FROM n`,
		syms*perDay-1, syms, syms); err != nil {
		t.Fatal(err)
	}
	seen := midBulkWrite(t, st, syms*perDay, `SELECT count(*) FROM scores`, func() error {
		n, err := st.PruneScoresKeepDailyLast(ctx, 86400)
		if err == nil && n != syms*(perDay-1) {
			err = fmt.Errorf("pruned %d rows, want %d", n, syms*(perDay-1))
		}
		return err
	})
	if seen == syms {
		t.Fatal("the account write saw the day's prune finished: one statement held the write lock for the whole day")
	}
	var left, last int64
	if err := st.db.QueryRowContext(ctx, `SELECT count(*), sum(ts = (SELECT max(ts) FROM scores s2 WHERE s2.symbol_id = scores.symbol_id)) FROM scores`).Scan(&left, &last); err != nil {
		t.Fatal(err)
	}
	if left != syms || last != syms {
		t.Fatalf("after the prune: %d rows, %d of them their symbol's last of the day; want %d and %d", left, last, syms, syms)
	}
	t.Logf("account write landed with %d rows still in the day", seen)
}
