package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestDirectionalRecordFoldsOnTheSettledMove(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	// ONE daily bar. All three predictions below grade against this single
	// settled move, which is exactly why they are one observation and not three.
	const fridayBar = int64(1784001600)
	if err := st.UpsertBars(ctx, []md.Bar{{
		SymbolID: sym.ID, TF: md.TF1d, Ts: fridayBar,
		Open: 100, High: 101, Low: 99, Close: 100, Volume: 1000,
	}}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	friPred := int64(1784042580)
	for i := 0; i < 3; i++ {
		ts := friPred + int64(i)*86400
		if err := st.UpsertPrediction(ctx, Prediction{SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, RawProb: 0.6, CalProb: 0.6, NUsed: 10, Components: "{}"}); err != nil {
			t.Fatalf("UpsertPrediction(%d): %v", i, err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 0.01); err != nil {
			t.Fatalf("ResolvePrediction(%d): %v", i, err)
		}
	}

	rec, err := st.DirectionalRecord(ctx, md.H1d, 0)
	if err != nil {
		t.Fatalf("DirectionalRecord: %v", err)
	}

	if rec.N != 1 {
		t.Fatalf("N = %d, want 1: Fri/Sat/Sun resolve against ONE Friday bar; %d means the calendar-day fold is back and effective N is inflated", rec.N, rec.N)
	}
}

func TestSettleDaySQLFunctionIsRegistered(t *testing.T) {
	st := openTemp(t)

	var got int64
	if err := st.db.QueryRowContext(context.Background(), `SELECT settle_day(?, ?)`, int64(1784001600), int64(1784042580)).Scan(&got); err != nil {
		t.Fatalf("SELECT settle_day: %v", err)
	}

	want := md.SettleDay(1784001600, 1784042580)
	if got != want {
		t.Fatalf("settle_day(1784001600, 1784042580) = %d, want %d", got, want)
	}
}
