package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The public attribution and own-signal surfaces pass GradingEpochTS as
// issuedSince: a call issued before the window must not be graded, one issued
// inside it must, and 0 keeps the full ledger.
func TestIssuedSinceFloorsTheOldLabel(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "floor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sym, err := st.UpsertSymbol(ctx, "FLR", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range []int64{GradingEpochTS - 5*86400, GradingEpochTS + 3600} {
		if err := st.UpsertPrediction(ctx, Prediction{SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
			RawProb: 0.7, CalProb: 0.7, NUsed: 3, Components: "{}"}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 0.01); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		since int64
		want  int
	}{{GradingEpochTS, 1}, {0, 2}} {
		acc, err := st.DirectionalAccuracyBySymbol(ctx, md.H1d, c.since)
		if err != nil || acc[sym.ID].N != c.want {
			t.Fatalf("attribution since %d: N=%d want %d err %v", c.since, acc[sym.ID].N, c.want, err)
		}
		obs, err := st.SignalBacktestObs(ctx, md.H1d, nil, 100, c.since)
		if err != nil || len(obs) != c.want {
			t.Fatalf("signal backtest since %d: %d obs want %d err %v", c.since, len(obs), c.want, err)
		}
	}
	// FirstResolutionAt: the pre-window call resolved first, so only the floor
	// can move the answer to the in-window call's resolution.
	if _, err := st.w.ExecContext(ctx, `UPDATE prediction_outcomes SET resolved_at = CASE WHEN ts < ? THEN 100 ELSE 200 END WHERE symbol_id = ?`,
		GradingEpochTS, sym.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ since, want int64 }{{0, 100}, {GradingEpochTS, 200}} {
		if got, ok, err := st.FirstResolutionAt(ctx, md.H1d, c.since); err != nil || !ok || got != c.want {
			t.Fatalf("FirstResolutionAt since %d = %d (ok %v err %v), want %d", c.since, got, ok, err, c.want)
		}
	}
}
