// STRATEGY-LAB wave tests — BacktestPositions must be the SAME engine as
// Backtest: identical fill timing, costs, accounting and honesty flags.
package backtest

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestBacktestPositions_MatchesRuleEngine replays an sma_cross(3,8) strategy
// two ways — the Rule engine and an externally computed position series with
// the same decide() semantics — and requires bit-identical results.
func TestBacktestPositions_MatchesRuleEngine(t *testing.T) {
	bars := make([]marketdata.Bar, 60)
	px := 100.0
	for i := range bars {
		// Deterministic zig-zag with a trend: forces several crosses.
		px *= 1 + 0.01*math.Sin(float64(i)/3) + 0.002
		bars[i] = marketdata.Bar{Ts: int64(i+1) * 86400, Open: px, High: px, Low: px, Close: px, Volume: 1}
	}
	strat := Strategy{
		Name:    "sma_cross_3_8",
		Entry:   Rule{Indicator: "sma_cross", Fast: 3, Slow: 8},
		Exit:    Rule{Indicator: "sma_cross", Fast: 3, Slow: 8},
		CostBps: 10,
	}
	want, err := Backtest(bars, strat)
	if err != nil {
		t.Fatalf("rule backtest: %v", err)
	}
	// The same decisions as decide(): pos[i] from bars[0..i], holding the
	// prior state through the warm-up window.
	pos := make([]int, len(bars))
	long := false
	for i := range bars {
		long = decide(bars[:i+1], strat, long)
		if long {
			pos[i] = 1
		}
	}
	got, err := BacktestPositions(bars, pos, strat.CostBps)
	if err != nil {
		t.Fatalf("positions backtest: %v", err)
	}
	if got.TotalReturn != want.TotalReturn || got.NumTrades != want.NumTrades ||
		got.Sharpe != want.Sharpe || got.MaxDrawdown != want.MaxDrawdown ||
		got.WinRate != want.WinRate || got.CAGR != want.CAGR ||
		got.CAGRReported != want.CAGRReported || got.WinRateMeaningful != want.WinRateMeaningful ||
		got.ExposurePct != want.ExposurePct || got.VsBuyHold != want.VsBuyHold {
		t.Fatalf("engines diverged:\n rule: %+v\n pos:  %+v", want, got)
	}
}

// TestBacktestPositions_HandComputedFills: a 4-bar series with one round trip
// checked against hand-computed equity (next-bar fill + per-side cost).
func TestBacktestPositions_HandComputedFills(t *testing.T) {
	bars := []marketdata.Bar{
		{Ts: 1 * 86400, Close: 100},
		{Ts: 2 * 86400, Close: 110}, // long DURING this bar (decided at bar 0)
		{Ts: 3 * 86400, Close: 121},
		{Ts: 4 * 86400, Close: 121}, // flat during this bar
	}
	pos := []int{1, 1, 0, 0}
	res, err := BacktestPositions(bars, pos, 100) // 1% per side
	if err != nil {
		t.Fatalf("backtest: %v", err)
	}
	// Enter at bar1 open: eq=0.99; bar1 return +10% -> 1.089; bar2 +10% ->
	// 1.1979; exit at bar3 open: *0.99 -> 1.185921; bar3 flat.
	want := 0.99 * 1.1 * 1.1 * 0.99
	if math.Abs(res.Equity[3]-want) > 1e-12 {
		t.Fatalf("final equity %v want %v", res.Equity[3], want)
	}
	if res.NumTrades != 1 || res.ClosedTrades != 1 {
		t.Fatalf("trades %d/%d want 1/1", res.NumTrades, res.ClosedTrades)
	}
	// HONESTY FLAGS: 3 days span, 1 trade — CAGR must NOT be reported and a
	// single-trade win rate is not meaningful.
	if res.CAGRReported || res.WinRateMeaningful {
		t.Fatalf("honesty flags wrong on a 4-bar 1-trade window: %+v", res)
	}
}

// TestBacktestPositions_Validation: length mismatch and stub series refuse.
func TestBacktestPositions_Validation(t *testing.T) {
	bars := []marketdata.Bar{{Ts: 86400, Close: 1}, {Ts: 2 * 86400, Close: 1}}
	if _, err := BacktestPositions(bars, []int{1}, 0); err == nil {
		t.Fatal("expected len-mismatch error")
	}
	if _, err := BacktestPositions(bars[:1], []int{1}, 0); err == nil {
		t.Fatal("expected too-few-bars error")
	}
	// -1 is honored as FLAT (long/flat engine), never a short.
	res, err := BacktestPositions(bars, []int{-1, -1}, 0)
	if err != nil || res.NumTrades != 0 {
		t.Fatalf("-1 must read as flat: %+v err=%v", res, err)
	}
}
