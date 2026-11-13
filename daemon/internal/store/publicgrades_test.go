package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Open, graded, distinct days, and the call and the baseline scored on the same
// graded rows. A row with no baseline is left out of the baseline tally only.
func TestRegimeKindTallies(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	ins := func(day int, resolved bool, regime, actual, naive string, correct int) {
		var res, act, cor, nv any
		if resolved {
			res, act, cor = 1000+day, actual, correct
		}
		if naive != "" {
			nv = naive
		}
		if _, err := st.w.ExecContext(ctx, `INSERT INTO regime_outcomes
			(symbol_id, kind, ts, day, horizon_days, regime, conviction, historical_accuracy, rank,
			 resolved_at, actual, correct, naive_label)
			VALUES (?, 'vol21', ?, ?, 21, ?, 0.5, 0, 0, ?, ?, ?, ?)`,
			sym.ID, day*86400, day, regime, res, act, cor, nv); err != nil {
			t.Fatal(err)
		}
	}
	ins(1, true, "elevated", "elevated", "calm", 1) // call right, baseline wrong
	ins(2, true, "elevated", "calm", "calm", 0)     // call wrong, baseline right
	ins(3, true, "calm", "calm", "", 1)             // call right, no baseline frozen
	ins(4, false, "calm", "", "calm", 0)            // still open
	got, err := st.RegimeKindTallies(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("tallies = %+v err=%v", got, err)
	}
	want := RegimeKindTally{Kind: "vol21", Open: 1, Graded: 3, Days: 3, Correct: 2, BaselineN: 2, BaselineCorrect: 1, CorrectOnBaseline: 1, BaselineDays: 2}
	if got[0] != want {
		t.Fatalf("tally = %+v, want %+v", got[0], want)
	}
}
