// STRATEGY-LAB wave (appended file) — BacktestPositions: the SAME bias-free
// next-bar-fill engine as Backtest, but driven by a precomputed position
// series instead of a Rule pair, so callers (internal/stratlib) can replay
// arbitrary published strategies through identical fill/cost/annualization
// accounting. No mechanics differ from Backtest: pos[i] is the position
// DECIDED at bar i (from bars[0..i] only — the caller's no-lookahead
// contract), it fills at bar i+1's open, per-side costs apply on every
// transition, and all honesty flags (CAGRReported, WinRateMeaningful) carry
// the same thresholds.
package backtest

import (
	"fmt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// BacktestPositions runs a precomputed long/flat position series over bars
// (ascending by Ts) with strict next-bar execution and per-side costs.
// pos must have len(bars) entries; pos[i] > 0 means "want long for the NEXT
// bar", anything else means flat. The engine is long/flat only (as Backtest):
// a -1 is honored as flat, never as a short. costBps is charged per side in
// basis points of equity.
func BacktestPositions(bars []marketdata.Bar, pos []int, costBps float64) (Result, error) {
	if len(bars) < 2 {
		return Result{}, fmt.Errorf("backtest: need at least 2 bars, got %d", len(bars))
	}
	if len(pos) != len(bars) {
		return Result{}, fmt.Errorf("backtest: len(pos)=%d != len(bars)=%d", len(pos), len(bars))
	}
	cost := costBps / 10000.0

	n := len(bars)
	equity := make([]float64, n)
	equity[0] = 1.0

	long := false
	var entryEquity float64
	longBars := 0
	numTrades := 0
	wins := 0
	closedTrades := 0
	perBarRet := make([]float64, 0, n-1)

	for i := 1; i < n; i++ {
		// Position for bar i was decided at bar i-1 (fills at bar i's open) —
		// identical to Backtest's decide(bars[:i], ...) timing.
		wantLong := pos[i-1] > 0

		eq := equity[i-1]
		if wantLong && !long {
			eq *= (1 - cost)
			long = true
			numTrades++
			entryEquity = eq
		} else if !wantLong && long {
			eq *= (1 - cost)
			long = false
			closedTrades++
			if eq > entryEquity {
				wins++
			}
		}

		ret := 0.0
		if long {
			prev := bars[i-1].Close
			if prev != 0 {
				ret = bars[i].Close/prev - 1
			}
			eq *= (1 + ret)
			longBars++
		}
		perBarRet = append(perBarRet, ret)
		equity[i] = eq
	}

	if long {
		final := equity[n-1] * (1 - cost)
		closedTrades++
		if final > entryEquity {
			wins++
		}
		equity[n-1] = final
	}

	res := Result{Equity: equity}
	res.TotalReturn = equity[n-1] - 1
	res.NumTrades = numTrades
	res.ClosedTrades = closedTrades
	if closedTrades > 0 {
		res.WinRate = float64(wins) / float64(closedTrades)
	}
	res.WinRateMeaningful = closedTrades >= 2
	res.ExposurePct = float64(longBars) / float64(n-1)
	res.MaxDrawdown = maxDrawdown(equity)

	bpy := barsPerYear(bars)
	res.BarsPerYear = bpy
	res.Sharpe = sharpe(perBarRet, bpy)

	res.SpanYears = spanYears(bars)
	res.CAGR = cagr(equity[n-1], res.SpanYears)
	res.CAGRReported = res.SpanYears >= minCAGRYears && numTrades >= minCAGRTrades

	res.VsBuyHold = res.TotalReturn - buyHoldReturn(bars)
	return res, nil
}
