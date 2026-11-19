package store

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const noon = 1793448000

func seedOutcomes(t *testing.T, st *Store, symbols int, days []int, perDay int) {
	t.Helper()
	tx, err := st.w.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO score_outcomes (symbol_id, horizon, ts, score, fwd_return, resolved_at, settle_ts) VALUES (?,?,?,?,?,?,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close() //nolint:errcheck

	for sym := 1; sym <= symbols; sym++ {
		for _, d := range days {
			base := noon - int64(d)*86400
			for k := 0; k < perDay; k++ {
				ts := base + int64(k)*60
				_, err := stmt.Exec(sym, string(md.H1d), ts, float64(k)/10, 0.01, ts+86400)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertSameAsFullScan(t *testing.T, st *Store, limit int) ([]md.ScoreOutcome, int) {
	t.Helper()
	ctx := context.Background()
	out, rawRows, err := st.ResolvedOutcomesIndependent(ctx, md.H1d, limit)
	if err != nil {
		t.Fatal(err)
	}
	fullOut, fullRaw, err := st.resolvedOutcomesIndependentSince(ctx, md.H1d, limit, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, fullOut) || rawRows != fullRaw {
		t.Fatalf("bounded != fullScan: out len %d vs %d, raw %d vs %d", len(out), len(fullOut), rawRows, fullRaw)
	}
	return out, rawRows
}

func TestResolvedOutcomesIndependent_BoundedPathMatchesFullScan(t *testing.T) {
	st := openTestStore(t)
	days := make([]int, 60)
	for i := range days {
		days[i] = i
	}
	seedOutcomes(t, st, 20, days, 3)

	ctx := context.Background()
	var maxTs sql.NullInt64
	if err := st.db.QueryRowContext(ctx, `SELECT MAX(ts) FROM score_outcomes`).Scan(&maxTs); err != nil {
		t.Fatal(err)
	}
	since := maxTs.Int64 - independentWindowSecs
	out, _, err := st.resolvedOutcomesIndependentSince(ctx, md.H1d, 100, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 100 {
		t.Fatalf("precondition: expected 100 rows, got %d", len(out))
	}

	out, rawRows := assertSameAsFullScan(t, st, 100)
	if len(out) != 100 || rawRows != 300 {
		t.Fatalf("expected 100 rows and 300 raw, got %d and %d", len(out), rawRows)
	}
}

func TestResolvedOutcomesIndependent_FallsBackWhenWindowIsShort(t *testing.T) {
	st := openTestStore(t)
	days := make([]int, 60)
	for i := range days {
		days[i] = i
	}
	seedOutcomes(t, st, 2, days, 2)

	out, rawRows := assertSameAsFullScan(t, st, 100)
	if len(out) != 100 || rawRows != 200 {
		t.Fatalf("expected 100 rows and 200 raw, got %d and %d", len(out), rawRows)
	}
}

func TestResolvedOutcomesIndependent_FallsBackNearCutoff(t *testing.T) {
	st := openTestStore(t)
	days := make([]int, 60)
	for i := range days {
		days[i] = i
	}
	seedOutcomes(t, st, 4, days, 2)

	out, rawRows := assertSameAsFullScan(t, st, 120)
	if len(out) != 120 || rawRows != 240 {
		t.Fatalf("expected 120 rows and 240 raw, got %d and %d", len(out), rawRows)
	}
}

func TestResolvedOutcomesIndependent_OnBackup(t *testing.T) {
	path := os.Getenv("SD_BACKUP_DB")
	if path == "" {
		t.Skip("SD_BACKUP_DB not set")
	}
	path = strings.ReplaceAll(path, "\\", "/")
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck
	st := &Store{db: db}
	ctx := context.Background()

	for _, h := range []md.Horizon{md.H1h, md.H1d, md.H1w} {
		start := time.Now()
		fullOut, fullRaw, err := st.resolvedOutcomesIndependentSince(ctx, h, 5000, 0)
		if err != nil {
			t.Fatal(err)
		}
		fullDur := time.Since(start)

		start = time.Now()
		out, rawRows, err := st.ResolvedOutcomesIndependent(ctx, h, 5000)
		if err != nil {
			t.Fatal(err)
		}
		boundedDur := time.Since(start)

		if !reflect.DeepEqual(out, fullOut) || rawRows != fullRaw {
			t.Fatalf("horizon %s: mismatch", h)
		}

		var oldestAgeDays float64
		if len(out) > 0 {
			var maxTs sql.NullInt64
			if err := st.db.QueryRowContext(ctx, `SELECT MAX(ts) FROM score_outcomes WHERE resolved_at IS NOT NULL AND horizon=?`, string(h)).Scan(&maxTs); err == nil && maxTs.Valid {
				oldestAgeDays = float64(maxTs.Int64-out[len(out)-1].Ts) / 86400.0
			}
		}
		t.Logf("%s: rows=%d rawRows=%d fullScan=%v bounded=%v oldestAge=%.1fd", h, len(out), rawRows, fullDur, boundedDur, oldestAgeDays)
	}
}

// A group that straddles the cut-off is the case the slack check exists for:
// one symbol's rows on day 30 and day 37 share a settle day, so the bounded scan
// (cut-off at day 35) sees one of the two. Without the slack check the row comes
// back with grp_n 1 instead of 2.
func TestResolvedOutcomesIndependent_StraddlingGroupKeepsItsCount(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	noon := int64(1793448000)
	ins := func(sym, ts int64, settle any) {
		t.Helper()
		if _, err := st.w.ExecContext(ctx, `INSERT INTO score_outcomes (symbol_id, horizon, ts, score, fwd_return, resolved_at, settle_ts) VALUES (?,?,?,?,?,?,?)`,
			sym, string(md.H1d), ts, 0.5, 0.01, ts+86400, settle); err != nil {
			t.Fatal(err)
		}
	}
	for d := int64(0); d < 30; d++ { // 30 plain one-row groups, newest first
		ins(1, noon-d*86400, nil)
	}
	settle := noon - 29*86400
	ins(2, noon-30*86400, settle) // above the cut-off
	ins(2, noon-37*86400, settle) // below it, same settle day
	out, raw := assertSameAsFullScan(t, st, 31)
	if len(out) != 31 || raw != 32 || out[30].SymbolID != 2 {
		t.Fatalf("rows=%d rawRows=%d, want 31 rows standing for 32 raw rows, oldest = symbol 2", len(out), raw)
	}
}

// A quiet stretch must not shorten the answer: with nothing between day 9 and
// day 40 the bounded scan returns 20 groups, all far above the cut-off, and only
// the row-count check sends the read on to the older groups.
func TestResolvedOutcomesIndependent_GapInHistoryStillFillsTheLimit(t *testing.T) {
	st := openTestStore(t)
	days := []int{}
	for d := 0; d < 10; d++ {
		days = append(days, d, d+40)
	}
	seedOutcomes(t, st, 2, days, 1)
	if out, raw := assertSameAsFullScan(t, st, 100); len(out) != 40 || raw != 40 {
		t.Fatalf("rows=%d rawRows=%d, want all 40 groups", len(out), raw)
	}
}
