package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// limit < 0 reads the whole graded window; a positive limit still caps it. The
// published track record passes -1: a fixed 120,000 cap had become binding and
// silently trimmed the oldest graded days. 20,001 rows clears the old 20,000
// default that a non-positive limit used to fall back to.
func TestResolvedPredictionOutcomesWholeWindow(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "WIN", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	const n = 20001
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO prediction_outcomes (symbol_id, horizon, ts, prob, up, fwd_return, resolved_at)
		WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < ?)
		SELECT ?, '1w', ? + i*60, 0.6, 1, 0.01, ? + i*60 FROM c`,
		n, sym.ID, GradingEpochTS, GradingEpochTS+604800); err != nil {
		t.Fatal(err)
	}
	all, err := st.ResolvedPredictionOutcomes(ctx, md.H1w, -1)
	if err != nil || len(all) != n {
		t.Fatalf("whole window = %d rows, %v; want %d", len(all), err, n)
	}
	two, err := st.ResolvedPredictionOutcomes(ctx, md.H1w, 2)
	if err != nil || len(two) != 2 || two[0].Ts < two[1].Ts {
		t.Fatalf("capped = %d rows, %v; want the 2 newest", len(two), err)
	}
}
