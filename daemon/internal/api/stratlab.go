// STRATEGY-LAB wave — API (read-only, public read):
//
//	GET /api/strategy-lab?symbol=&market= — one symbol's per-strategy
//	backtest rows (engine honesty flags carried per row) + the fleet
//	aggregates per strategy.
//	GET /api/strategy-lab                — the fleet table alone.
//
// HONESTY (shipped verbatim in every payload): classic published strategies
// backtested walk-forward on our own bars with costs — in-sample history,
// not live performance and not advice; a strategy is only as good as its
// next trade.
package api

import (
	"net/http"
)

// stratLabAPINote ships verbatim with every /api/strategy-lab payload (the
// same caveat the strategy-lab worker writes into its weekly insight).
const stratLabAPINote = "classic published strategies backtested walk-forward on our own bars with costs — in-sample history, not live performance and not advice; a strategy is only as good as its next trade"

// strategyLab serves the strategy-lab results.
// GET /api/strategy-lab?symbol=&market=  (no symbol => fleet table only)
func (d Deps) strategyLab(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fleet, err := d.St.StrategyFleetAggs(ctx)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"fleet": fleet, // per strategy: median Sharpe, % profitable, median return
		"note":  stratLabAPINote,
	}
	if r.URL.Query().Get("symbol") != "" {
		s, err := d.symbolFromQuery(r)
		if err != nil {
			httpErr(w, 404, err.Error())
			return
		}
		rows, err := d.St.StrategyResultsBySymbol(ctx, s.ID)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		out["symbol"] = s.Symbol
		out["strategies"] = rows // cagr/winRate must be gated on their flags
		if len(rows) == 0 {
			out["emptyNote"] = "no strategy results stored for this symbol yet — the strategy-lab worker covers the streamed hot set + crypto once per UTC day"
		}
	}
	writeJSON(w, out)
}

func (d Deps) registerStrategyLab(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/strategy-lab", d.strategyLab)
}
