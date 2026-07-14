// STRATEGY-LAB wave — store layer for strategy_results: per-(symbol,strategy)
// walk-forward backtest results of the 8 classic published strategies on our
// own daily bars with costs, honoring the backtest engine's CAGRReported /
// WinRateMeaningful honesty flags. HONESTY: in-sample history on our bars,
// not live performance and not advice.
package store

import (
	"context"
	"sort"
)

// StrategyResult is one (symbol, strategy) backtest outcome.
type StrategyResult struct {
	SymbolID     int64   `json:"-"`
	Symbol       string  `json:"symbol,omitempty"`
	Strategy     string  `json:"strategy"`
	Ts           int64   `json:"ts"`
	TotalReturn  float64 `json:"totalReturn"`
	CAGR         float64 `json:"cagr"` // only show when CAGRReported
	Sharpe       float64 `json:"sharpe"`
	MaxDD        float64 `json:"maxDrawdown"`
	WinRate      float64 `json:"winRate"` // only show when WinRateOK
	NTrades      int     `json:"nTrades"`
	CAGRReported bool    `json:"cagrReported"`
	WinRateOK    bool    `json:"winRateMeaningful"`
	NBars        int     `json:"nBars"`
}

// UpsertStrategyResult writes one (symbol, strategy) result row.
func (s *Store) UpsertStrategyResult(ctx context.Context, r StrategyResult) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO strategy_results
		  (symbol_id, strategy, ts, total_return, cagr, sharpe, max_dd,
		   win_rate, n_trades, cagr_reported, win_rate_ok, n_bars)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id, strategy) DO UPDATE SET
		  ts=excluded.ts, total_return=excluded.total_return,
		  cagr=excluded.cagr, sharpe=excluded.sharpe, max_dd=excluded.max_dd,
		  win_rate=excluded.win_rate, n_trades=excluded.n_trades,
		  cagr_reported=excluded.cagr_reported, win_rate_ok=excluded.win_rate_ok,
		  n_bars=excluded.n_bars`,
		r.SymbolID, r.Strategy, r.Ts, r.TotalReturn, r.CAGR, r.Sharpe, r.MaxDD,
		r.WinRate, r.NTrades, r.CAGRReported, r.WinRateOK, r.NBars)
	return err
}

// StrategyResultsBySymbol returns a symbol's stored strategy results, sorted
// by strategy name (stable for the API).
func (s *Store) StrategyResultsBySymbol(ctx context.Context, symbolID int64) ([]StrategyResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT strategy, ts, total_return, cagr, sharpe, max_dd, win_rate,
		       n_trades, cagr_reported, win_rate_ok, n_bars
		FROM strategy_results WHERE symbol_id=? ORDER BY strategy ASC`, symbolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []StrategyResult
	for rows.Next() {
		r := StrategyResult{SymbolID: symbolID}
		if err := rows.Scan(&r.Strategy, &r.Ts, &r.TotalReturn, &r.CAGR, &r.Sharpe,
			&r.MaxDD, &r.WinRate, &r.NTrades, &r.CAGRReported, &r.WinRateOK, &r.NBars); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StrategyFleetAgg is one strategy's fleet-wide aggregate across all symbols
// it was backtested on.
type StrategyFleetAgg struct {
	Strategy       string  `json:"strategy"`
	NSymbols       int     `json:"nSymbols"`
	MedianSharpe   float64 `json:"medianSharpe"`
	PctProfitable  float64 `json:"pctProfitable"`  // fraction with total_return > 0
	MedianTotalRet float64 `json:"medianTotalRet"` // median total return
}

// StrategyFleetAggs computes per-strategy fleet aggregates (median Sharpe,
// fraction of symbols profitable, median total return) over all stored rows,
// sorted by median Sharpe descending then strategy name (deterministic).
func (s *Store) StrategyFleetAggs(ctx context.Context) ([]StrategyFleetAgg, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT strategy, sharpe, total_return FROM strategy_results`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	type acc struct {
		sharpes, rets []float64
		profitable    int
	}
	byStrat := map[string]*acc{}
	for rows.Next() {
		var strat string
		var sharpe, ret float64
		if err := rows.Scan(&strat, &sharpe, &ret); err != nil {
			return nil, err
		}
		a := byStrat[strat]
		if a == nil {
			a = &acc{}
			byStrat[strat] = a
		}
		a.sharpes = append(a.sharpes, sharpe)
		a.rets = append(a.rets, ret)
		if ret > 0 {
			a.profitable++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]StrategyFleetAgg, 0, len(byStrat))
	for strat, a := range byStrat {
		out = append(out, StrategyFleetAgg{
			Strategy:       strat,
			NSymbols:       len(a.sharpes),
			MedianSharpe:   median(a.sharpes),
			PctProfitable:  float64(a.profitable) / float64(len(a.sharpes)),
			MedianTotalRet: median(a.rets),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MedianSharpe != out[j].MedianSharpe {
			return out[i].MedianSharpe > out[j].MedianSharpe
		}
		return out[i].Strategy < out[j].Strategy
	})
	return out, nil
}

// median returns the median of vs (vs is copied; empty -> 0).
func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	c := append([]float64(nil), vs...)
	sort.Float64s(c)
	if len(c)%2 == 1 {
		return c[len(c)/2]
	}
	return (c[len(c)/2-1] + c[len(c)/2]) / 2
}
