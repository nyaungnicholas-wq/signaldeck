package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// H1-1H-GAP repair. On a fixture DB the dry run counts exactly the stock 1h rows
// graded across a >3h 1m-bar hole since the deploy, -apply voids only those (old
// values logged first), and a second -apply changes nothing.
func TestRevoidOnlyOvernight1hRowsAndIsIdempotent(t *testing.T) {
	gapBase := time.Date(2026, 9, 30, 21, 39, 0, 0, time.UTC).Unix() // Wed 17:39 EDT
	gapNext := time.Date(2026, 10, 1, 13, 19, 0, 0, time.UTC).Unix() // Thu 09:19 EDT: 14.7h past target
	okBase := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC).Unix()   // Thu 10:00 EDT

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sd.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	add := func(ticker string, h md.Horizon, bars []int64) int64 {
		t.Helper()
		sym, err := st.UpsertSymbol(ctx, ticker, md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		var mdbars []md.Bar
		for i, ts := range bars {
			mdbars = append(mdbars, md.Bar{SymbolID: sym.ID, TF: md.TF1m, Ts: ts, Open: 100 + float64(i), High: 100 + float64(i), Low: 100 + float64(i), Close: 100 + float64(i)})
		}
		if err := st.UpsertBars(ctx, mdbars); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: h, Ts: bars[0] + 30, Score: 0.5}); err != nil {
			t.Fatal(err)
		}
		return sym.ID
	}

	bad := add("BAD", md.H1h, []int64{gapBase, gapNext, gapNext + 60})
	if err := st.ResolveOutcome(ctx, bad, md.H1h, gapBase+30, 0.0083); err != nil {
		t.Fatal(err)
	}
	ok := add("OK", md.H1h, []int64{okBase, okBase + 3600, okBase + 3660})
	if err := st.ResolveOutcome(ctx, ok, md.H1h, okBase+30, 0.01); err != nil {
		t.Fatal(err)
	}
	voided := add("VOIDED", md.H1h, []int64{gapBase, gapNext, gapNext + 60})
	if err := st.ResolveOutcomeVoid(ctx, voided, md.H1h, gapBase+30); err != nil {
		t.Fatal(err)
	}
	daily := add("DAILY", md.H1d, []int64{gapBase, gapNext, gapNext + 60})
	if err := st.ResolveOutcome(ctx, daily, md.H1d, gapBase+30, 0.02); err != nil {
		t.Fatal(err)
	}
	old := add("OLD", md.H1h, []int64{gapBase, gapNext, gapNext + 60})
	if err := st.ResolveOutcome(ctx, old, md.H1h, gapBase+30, 0.03); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE score_outcomes SET resolved_at = ? WHERE symbol_id = ? AND horizon = '1h'`, deployTs-60, old); err != nil {
		db.Close() //nolint:errcheck
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot := func(t *testing.T, path string) []string {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close() //nolint:errcheck
		rows, err := db.QueryContext(ctx, `SELECT symbol_id, horizon, ts, fwd_return, resolved_at FROM score_outcomes ORDER BY symbol_id, horizon, ts`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close() //nolint:errcheck
		var out []string
		for rows.Next() {
			var sym int64
			var horizon string
			var ts int64
			var fwd sql.NullFloat64
			var res sql.NullInt64
			if err := rows.Scan(&sym, &horizon, &ts, &fwd, &res); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%d %s %d %v %v", sym, horizon, ts, fwd, res))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	before := snapshot(t, dbPath)
	if len(before) != 5 {
		t.Fatalf("before snapshot has %d rows, want 5", len(before))
	}

	var buf bytes.Buffer
	n, err := run(dbPath, false, "", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("dry run flagged %d, want 1\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "DRY RUN") {
		t.Fatalf("dry run output missing 'DRY RUN': %s", buf.String())
	}
	if got := snapshot(t, dbPath); !slices.Equal(before, got) {
		t.Fatalf("dry run wrote to the db\nbefore:%s\nafter:%s", strings.Join(before, "\n"), strings.Join(got, "\n"))
	}

	logA := filepath.Join(dir, "a.csv")
	n, err = run(dbPath, true, logA, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("apply flagged %d, want 1\n%s", n, buf.String())
	}
	after := snapshot(t, dbPath)
	changed := 0
	for i := range before {
		if after[i] != before[i] {
			changed++
			if !strings.HasPrefix(after[i], fmt.Sprintf("%d 1h ", bad)) {
				t.Fatalf("changed line %d does not start with bad symbol: %s", i, after[i])
			}
			if !strings.Contains(after[i], "{0 false}") {
				t.Fatalf("changed line %d missing null fwd_return: %s", i, after[i])
			}
			if !strings.HasPrefix(before[i], fmt.Sprintf("%d 1h ", bad)) {
				t.Fatalf("before line %d does not start with bad symbol: %s", i, before[i])
			}
			bFields := strings.Fields(before[i])
			aFields := strings.Fields(after[i])
			if len(bFields) < 2 || len(aFields) < 2 {
				t.Fatalf("not enough fields in lines")
			}
			if strings.Join(bFields[len(bFields)-2:], " ") != strings.Join(aFields[len(aFields)-2:], " ") {
				t.Fatalf("resolved_at changed: before %q, after %q", strings.Join(bFields[len(bFields)-2:], " "), strings.Join(aFields[len(aFields)-2:], " "))
			}
		}
	}
	if changed != 1 {
		t.Fatalf("expected exactly one changed line, got %d", changed)
	}
	logData, err := os.ReadFile(logA)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(logData), "\n")
	var nonEmpty []string
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if len(nonEmpty) != 2 {
		t.Fatalf("log file has %d non-empty lines, want 2 (header + data)", len(nonEmpty))
	}
	if !strings.HasPrefix(nonEmpty[1], fmt.Sprintf("%d,", bad)) {
		t.Fatalf("log data line does not start with bad symbol: %s", nonEmpty[1])
	}

	logB := filepath.Join(dir, "b.csv")
	n, err = run(dbPath, true, logB, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second apply flagged %d, want 0\n%s", n, buf.String())
	}
	if got := snapshot(t, dbPath); !slices.Equal(after, got) {
		t.Fatalf("second apply wrote to the db\nafter:%s\nagain:%s", strings.Join(after, "\n"), strings.Join(got, "\n"))
	}

	_, err = run(dbPath, true, logA, &buf)
	if err == nil {
		t.Fatalf("expected error when log file already exists, got nil")
	}
	if got := snapshot(t, dbPath); !slices.Equal(after, got) {
		t.Fatalf("third apply wrote to the db\nafter:%s\nagain:%s", strings.Join(after, "\n"), strings.Join(got, "\n"))
	}
}
