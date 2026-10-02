package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// keysOf lists the primary keys q returns, in order.
func keysOf(t *testing.T, st *Store, q string) string {
	rows, err := st.db.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	var out []string
	for rows.Next() {
		var symbolID int64
		var horizon string
		var ts int64
		if err := rows.Scan(&symbolID, &horizon, &ts); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, fmt.Sprintf("%d/%s/%d", symbolID, horizon, ts))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	return strings.Join(out, ",")
}

// seedPruneFixture writes 4 symbols x 2 horizons x 5 days x 30 rows; the day's
// last row (even symbols) and one mid-day row (symbol 3) keep their blob.
func seedPruneFixture(t *testing.T, st *Store, table, blobCol, stripped, unstripped string) {
	tx, err := st.w.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	var stmt string
	switch table {
	case "scores":
		stmt = "INSERT INTO scores (symbol_id, horizon, ts, score, components) VALUES (?, ?, ?, ?, ?)"
	case "composite_scores":
		stmt = "INSERT INTO composite_scores (symbol_id, ts, horizon, score, curve_pct, edge, payload) VALUES (?, ?, ?, ?, ?, ?, ?)"
	default:
		t.Fatalf("unknown table: %s", table)
	}
	ins, err := tx.PrepareContext(context.Background(), stmt)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer ins.Close() //nolint:errcheck
	for symbolID := 1; symbolID <= 4; symbolID++ {
		for _, horizon := range []string{"1h", "1d"} {
			for day := 0; day <= 4; day++ {
				for m := 0; m <= 29; m++ {
					ts := int64(day)*86400 + 3600 + int64(m)*600 + int64(symbolID)
					var blob string
					if (m == 29 && symbolID%2 == 0) || (m == 13 && symbolID == 3) {
						blob = unstripped
					} else {
						blob = stripped
					}
					var err error
					if table == "scores" {
						_, err = ins.ExecContext(context.Background(), symbolID, horizon, ts, 0.5, blob)
					} else {
						_, err = ins.ExecContext(context.Background(), symbolID, ts, horizon, 5, 50, 0.01, blob)
					}
					if err != nil {
						t.Fatalf("insert: %v", err)
					}
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// The batched prunes must leave exactly the table the old single statement
// left, including un-stripped rows (never deleted, but still a newer row of
// their day), two horizons, and a cutoff in the middle of a day.
func TestBatchedDailyPrunesMatchTheSingleStatement(t *testing.T) {
	tests := []struct {
		table      string
		blobCol    string
		stripped   string
		unstripped string
		prune      func(*Store, int64) (int64, error)
	}{
		{
			table:      "scores",
			blobCol:    "components",
			stripped:   "[]",
			unstripped: "[1]",
			prune: func(st *Store, c int64) (int64, error) {
				return st.PruneScoresKeepDailyLast(context.Background(), c)
			},
		},
		{
			table:      "composite_scores",
			blobCol:    "payload",
			stripped:   "{}",
			unstripped: "{\"x\":1}",
			prune: func(st *Store, c int64) (int64, error) {
				return st.PruneCompositeKeepDailyLast(context.Background(), c)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.table, func(t *testing.T) {
			ctx := context.Background()
			cutoff := int64(3*86400 + 3600 + 15*600)

			old := openTemp(t)
			for id := 1; id <= 4; id++ {
				_, err := old.w.ExecContext(ctx,
					"INSERT INTO symbols (id, symbol, market, added_at) VALUES (?, ?, 'stocks', 0)",
					id, fmt.Sprintf("EQ%d", id))
				if err != nil {
					t.Fatalf("insert symbol %d: %v", id, err)
				}
			}
			seedPruneFixture(t, old, tt.table, tt.blobCol, tt.stripped, tt.unstripped)

			delSQL := fmt.Sprintf(
				"DELETE FROM %s WHERE ts < ? AND %s = '%s' AND EXISTS (SELECT 1 FROM %s k WHERE k.symbol_id = %s.symbol_id AND k.horizon = %s.horizon AND k.ts/86400 = %s.ts/86400 AND k.ts > %s.ts)",
				tt.table, tt.blobCol, tt.stripped,
				tt.table, tt.table, tt.table, tt.table, tt.table)
			res, err := old.w.ExecContext(ctx, delSQL, cutoff)
			if err != nil {
				t.Fatalf("old delete: %v", err)
			}
			oldN, err := res.RowsAffected()
			if err != nil {
				t.Fatalf("rows affected: %v", err)
			}

			nw := openTemp(t)
			for id := 1; id <= 4; id++ {
				_, err := nw.w.ExecContext(ctx,
					"INSERT INTO symbols (id, symbol, market, added_at) VALUES (?, ?, 'stocks', 0)",
					id, fmt.Sprintf("EQ%d", id))
				if err != nil {
					t.Fatalf("insert symbol %d: %v", id, err)
				}
			}
			seedPruneFixture(t, nw, tt.table, tt.blobCol, tt.stripped, tt.unstripped)
			newN, err := tt.prune(nw, cutoff)
			if err != nil {
				t.Fatalf("prune: %v", err)
			}

			if newN != oldN {
				t.Fatalf("batched prune deleted %d rows, the single statement %d", newN, oldN)
			}
			if oldN == 0 {
				t.Fatal("the fixture pruned nothing; the comparison is vacuous")
			}

			q := fmt.Sprintf("SELECT symbol_id, horizon, ts FROM %s ORDER BY symbol_id, horizon, ts", tt.table)
			if a, b := keysOf(t, old, q), keysOf(t, nw, q); a != b {
				t.Fatalf("batched prune left a different table than the single statement\nold: %s\nnew: %s", a, b)
			}

			var n int
			err = nw.db.QueryRowContext(ctx,
				fmt.Sprintf("SELECT count(*) FROM %s WHERE %s != '%s'", tt.table, tt.blobCol, tt.stripped)).Scan(&n)
			if err != nil {
				t.Fatalf("count query: %v", err)
			}
			want := 2 * 5 * (2 + 1) // 2 horizons * 5 days * (2 even symbols + symbol 3)
			if n != want {
				t.Fatalf("unstripped rows count mismatch: got %d, want %d", n, want)
			}

			t.Logf("%s: %d rows pruned by both", tt.table, oldN)
		})
	}
}
