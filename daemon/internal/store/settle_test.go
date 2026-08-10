package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The settled move is the independence unit; the calendar day is not.
//
// base = BarAtOrBefore(ts), so a Saturday prediction takes Friday's base bar and
// Monday's forward — identical to Friday's own prediction. Measured on the live
// record 2026-08-08, 32.9% of consecutive stock symbol-day pairs carried an
// IDENTICAL label (Sun 84.7%, Sat 64.9%) while crypto carried 0.0%, and every
// day-clustered statistic counted them as separate observations.
//
// Folding weekends into trading_day() would be wrong in the other direction:
// for a 24/7 market Saturday IS an independent session. Keying on the base bar
// is correct for both, which is what this asserts.
func TestSettleTsIsTheIndependenceUnit(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "TEST", md.Stocks, "Test Co")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	// Two sessions three days apart — a Friday and the following Monday, with
	// no bars on the weekend between them. That gap is the whole point.
	const fri = int64(1786000000)
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: fri, Open: 100, High: 101, Low: 99, Close: 100, Volume: 1},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: fri + 3*86400, Open: 100, High: 102, Low: 99, Close: 101, Volume: 1},
	}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	// Three predictions: Friday, Saturday, Sunday. All settle on the SAME
	// Friday bar, so all three are one observation.
	for _, off := range []int64{0, 86400, 2 * 86400} {
		if err := st.UpsertPrediction(ctx, Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: fri + off,
			RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: "{}",
		}); err != nil {
			t.Fatalf("UpsertPrediction: %v", err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, fri+off, 0.01); err != nil {
			t.Fatalf("ResolvePrediction: %v", err)
		}
	}

	if _, err := st.BackfillSettleTs(ctx, 100); err != nil {
		t.Fatalf("BackfillSettleTs: %v", err)
	}

	var byDay, bySettle int
	if err := st.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT date(ts,'unixepoch')), COUNT(DISTINCT settle_ts)
		FROM prediction_outcomes WHERE symbol_id=? AND horizon='1d'`, sym.ID).
		Scan(&byDay, &bySettle); err != nil {
		t.Fatalf("count: %v", err)
	}
	if byDay != 3 {
		t.Fatalf("fixture did not produce 3 calendar days, got %d", byDay)
	}
	if bySettle != 1 {
		t.Errorf("three predictions sharing one Friday base bar produced %d settle keys, want 1 — "+
			"they are ONE observation and every day-clustered statistic must see them as one", bySettle)
	}
}
