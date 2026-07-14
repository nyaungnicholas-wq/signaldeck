// STRATEGY-LAB wave — worker: strategy-lab (24h tick, gated to once per UTC
// day via meta strategy_lab_day).
//
// Replays the 8 classic PUBLISHED strategies in internal/stratlib (golden
// cross, Donchian 20, Connors RSI-2, Jegadeesh-Titman 12-1 momentum, MACD
// trend, Bollinger mean-reversion, 52w-high breakout, absolute dual momentum)
// through the existing bias-free next-bar-fill backtest engine
// (backtest.BacktestPositions — identical fill/cost/annualization accounting
// to Backtest) on ~2y of OUR OWN daily bars with real per-side costs
// (papertrade.CostBpsFor per market). Scope is BOUNDED to the streamed hot
// set + crypto — never the full broad universe. Results land in
// strategy_results, one row per (symbol, strategy), honoring the engine's
// CAGRReported / WinRateMeaningful honesty flags verbatim. Symbols with too
// few daily bars are skipped and counted honestly in the detail string.
//
// Once per ISO week (meta strategy_lab_insight_week) it writes ONE
// market-scope insight (kind strategy_lab) naming the top-3 strategies
// fleet-wide by median Sharpe.
//
// HONESTY (carried verbatim by the insight and the API): classic published
// strategies backtested walk-forward on our own bars with costs — in-sample
// history, not live performance and not advice; a strategy is only as good
// as its next trade.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/backtest"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/stratlib"
)

const (
	// stratLabNote ships verbatim with the insight and every API payload.
	stratLabNote = "classic published strategies backtested walk-forward on our own bars with costs — in-sample history, not live performance and not advice; a strategy is only as good as its next trade"
	// stratLabDayKey gates the daily pass (meta, YYYY-MM-DD UTC).
	stratLabDayKey = "strategy_lab_day"
	// stratLabWeekKey gates the weekly fleet insight (meta, ISO year-week).
	stratLabWeekKey = "strategy_lab_insight_week"
	// stratLabBars is the daily-bar window (~2 trading years).
	stratLabBars = 504
	// stratLabMinBars is the floor below which a symbol is skipped — a
	// backtest over a stub series would annualize noise.
	stratLabMinBars = 120
)

// StrategyLab is the strategy-lab worker.
type StrategyLab struct {
	St *store.Store
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *StrategyLab) Name() string            { return "strategy-lab" }
func (w *StrategyLab) Interval() time.Duration { return 24 * time.Hour }

func (w *StrategyLab) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *StrategyLab) Run(ctx context.Context) (string, error) {
	now := w.now()
	today := now.UTC().Format("2006-01-02")
	if last, _ := w.St.GetMeta(ctx, stratLabDayKey); last == today {
		return fmt.Sprintf("up to date (%s)", today), nil
	}

	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	strategies := stratlib.All()
	ran, skippedThin, resultRows := 0, 0, 0
	for _, s := range syms {
		// Bound the compute: streamed hot set + crypto only, never all 516.
		if !s.Stream && s.Market != md.Crypto {
			continue
		}
		bars, err := w.St.LastBars(ctx, s.ID, md.TF1d, stratLabBars)
		if err != nil {
			return "", err
		}
		if len(bars) < stratLabMinBars {
			skippedThin++
			continue
		}
		cost := papertrade.CostBpsFor(s.Market)
		for _, strat := range strategies {
			pos := strat.Positions(bars)
			res, err := backtest.BacktestPositions(bars, pos, cost)
			if err != nil {
				// Engine refusal (e.g. degenerate series) — skip this pair
				// honestly; the fleet never fails over one symbol.
				continue
			}
			if err := w.St.UpsertStrategyResult(ctx, store.StrategyResult{
				SymbolID: s.ID, Strategy: strat.Name, Ts: now.Unix(),
				TotalReturn: res.TotalReturn, CAGR: res.CAGR, Sharpe: res.Sharpe,
				MaxDD: res.MaxDrawdown, WinRate: res.WinRate, NTrades: res.NumTrades,
				CAGRReported: res.CAGRReported, WinRateOK: res.WinRateMeaningful,
				NBars: len(bars),
			}); err != nil {
				return "", err
			}
			resultRows++
		}
		ran++
	}

	// Weekly fleet insight: top-3 strategies by median Sharpe.
	insight := "insight not due"
	year, week := now.UTC().ISOWeek()
	weekKey := fmt.Sprintf("%d-W%02d", year, week)
	if last, _ := w.St.GetMeta(ctx, stratLabWeekKey); last != weekKey {
		aggs, err := w.St.StrategyFleetAggs(ctx)
		if err != nil {
			return "", err
		}
		if len(aggs) == 0 {
			insight = "insight skipped: no strategy results stored yet"
		} else {
			if err := w.St.InsertInsight(ctx, stratLabInsight(now, aggs)); err != nil {
				return "", err
			}
			if err := w.St.SetMeta(ctx, stratLabWeekKey, weekKey); err != nil {
				return "", err
			}
			insight = "weekly insight written"
		}
	}

	if err := w.St.SetMeta(ctx, stratLabDayKey, today); err != nil {
		return "", err
	}
	return fmt.Sprintf("backtested %d strategies over %d symbol(s) (%d result rows; %d skipped: <%d daily bars); %s",
		len(strategies), ran, resultRows, skippedThin, stratLabMinBars, insight), nil
}

// stratLabInsight composes the weekly market-scope insight (kind
// strategy_lab) naming the top-3 strategies fleet-wide by median Sharpe.
func stratLabInsight(now time.Time, aggs []store.StrategyFleetAgg) md.Insight {
	top := aggs
	if len(top) > 3 {
		top = top[:3]
	}
	parts := make([]string, 0, len(top))
	for _, a := range top {
		parts = append(parts, fmt.Sprintf("%s (median Sharpe %.2f, %.0f%% of %d symbols profitable)",
			a.Strategy, a.MedianSharpe, a.PctProfitable*100, a.NSymbols))
	}
	data, _ := json.Marshal(map[string]any{
		"kind": "strategy_lab", "top": top, "note": stratLabNote,
	})
	return md.Insight{
		Scope:    "market",
		Ts:       now.Unix(),
		Headline: fmt.Sprintf("Strategy lab: %s leads this week's fleet backtests", top[0].Strategy),
		Body:     "Top strategies fleet-wide by median Sharpe: " + strings.Join(parts, "; ") + ". " + stratLabNote + ".",
		Data:     string(data),
	}
}
