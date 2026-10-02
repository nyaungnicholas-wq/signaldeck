package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestLatestRVForecastsIsNotQuadratic: 300 symbols x 60 days x 2 horizons
// (36,000 rows, the live table's order of magnitude) must answer well inside
// 2s. The correlated MAX(ts) form took minutes at this size.
func TestLatestRVForecastsIsNotQuadratic(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "rv.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	tx, err := st.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	day := int64(1_790_000_000) / 86400 * 86400
	for s := 0; s < 300; s++ {
		res, err := tx.ExecContext(ctx, `INSERT INTO symbols (symbol, market, name, added_at) VALUES (?, 'stocks', '', 0)`,
			fmt.Sprintf("S%03d", s))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		for d := int64(0); d < 60; d++ {
			for _, h := range []int{1, 5} {
				if _, err := tx.ExecContext(ctx, `INSERT INTO rv_forecasts (symbol_id, ts, horizon, rv_hat, null_rw, null_ewma,
					beta0, beta_d, beta_w, beta_m, resid_var, n_train, revision, created_ts)
					VALUES (?,?,?,0.0004,0.0004,0.0004,0,0,0,0,0,600,'t',0)`, id, day+d*86400, h); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := st.LatestRVForecasts(ctx)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 600 || got[0].Ts != day+59*86400 {
		t.Fatalf("%d rows, first %+v: want every symbol's newest bar on both horizons", len(got), got[0])
	}
	if took > 2*time.Second {
		t.Fatalf("LatestRVForecasts took %v on 36k rows: the query is not linear", took)
	}
	t.Logf("36k rows in %v", took)
}
