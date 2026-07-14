// STRATEGY-LAB wave store tests: strategy_results upsert/read-back (honesty
// flags round-trip), and the fleet aggregates (median Sharpe, % profitable,
// deterministic ordering). Fixture data only.
package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"
)

func openStratStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "strat.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestStrategyResults_UpsertAndReadBack(t *testing.T) {
	st := openStratStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "NVDA", "stocks", "NVIDIA")

	r := StrategyResult{
		SymbolID: sym.ID, Strategy: "sma_cross_50_200", Ts: 1000,
		TotalReturn: 0.42, CAGR: 0.2, Sharpe: 1.1, MaxDD: 0.15,
		WinRate: 0.6, NTrades: 25, CAGRReported: true, WinRateOK: true, NBars: 504,
	}
	if err := st.UpsertStrategyResult(ctx, r); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Overwrite (idempotent PK) with the honesty flags DOWN — flags must
	// round-trip exactly, never sticky.
	r.Ts, r.NTrades, r.CAGRReported, r.WinRateOK = 2000, 3, false, false
	if err := st.UpsertStrategyResult(ctx, r); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	rows, err := st.StrategyResultsBySymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.Ts != 2000 || got.NTrades != 3 || got.CAGRReported || got.WinRateOK ||
		got.TotalReturn != 0.42 || got.Sharpe != 1.1 || got.NBars != 504 {
		t.Fatalf("round-trip wrong: %+v", got)
	}
}

func TestStrategyFleetAggs(t *testing.T) {
	st := openStratStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "NVDA", "stocks", "NVIDIA")
	b, _ := st.UpsertSymbol(ctx, "AAPL", "stocks", "Apple")
	c, _ := st.UpsertSymbol(ctx, "TSLA", "stocks", "Tesla")

	put := func(symID int64, strat string, sharpe, ret float64) {
		t.Helper()
		if err := st.UpsertStrategyResult(ctx, StrategyResult{
			SymbolID: symID, Strategy: strat, Ts: 1,
			TotalReturn: ret, Sharpe: sharpe,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	// donchian: sharpes {0.5, 1.5, 2.5} -> median 1.5; 2/3 profitable.
	put(a.ID, "donchian_20", 0.5, -0.1)
	put(b.ID, "donchian_20", 1.5, 0.2)
	put(c.ID, "donchian_20", 2.5, 0.3)
	// macd: sharpes {1.0, 2.0} -> median 1.5 (tie w/ donchian, name breaks it);
	// 1/2 profitable.
	put(a.ID, "macd_trend", 1.0, 0.1)
	put(b.ID, "macd_trend", 2.0, -0.2)
	// rsi2: median 3.0 -> ranks first.
	put(a.ID, "rsi2_meanrev", 3.0, 0.5)

	aggs, err := st.StrategyFleetAggs(ctx)
	if err != nil {
		t.Fatalf("aggs: %v", err)
	}
	if len(aggs) != 3 || aggs[0].Strategy != "rsi2_meanrev" {
		t.Fatalf("order wrong: %+v", aggs)
	}
	// The 1.5-median tie must break alphabetically: donchian_20 before macd_trend.
	if aggs[1].Strategy != "donchian_20" || aggs[2].Strategy != "macd_trend" {
		t.Fatalf("tie-break wrong: %+v", aggs)
	}
	d := aggs[1]
	if d.NSymbols != 3 || d.MedianSharpe != 1.5 ||
		math.Abs(d.PctProfitable-2.0/3.0) > 1e-12 || d.MedianTotalRet != 0.2 {
		t.Fatalf("donchian agg wrong: %+v", d)
	}
	m := aggs[2]
	if m.NSymbols != 2 || m.MedianSharpe != 1.5 || m.PctProfitable != 0.5 {
		t.Fatalf("macd agg wrong: %+v", m)
	}
}
