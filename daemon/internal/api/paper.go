package api

import (
	"net/http"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── STAGE 4: INTERNAL SIMULATED paper-trading read route ─────────────────────
//
// GET /api/paper?strategy=flagship-1d returns the simulated book's equity curve,
// open positions, recent trades, and a costed summary (return, Sharpe when
// valid, maxDD, win-rate when enough closed trades, turnover). It is a READ:
// public under SIGNALDECK_PUBLIC_READS like the other market-data reads. The
// book is a SIMULATION driven by the platform's own predictions — no broker, no
// real money — and the payload carries live:false + a label so the UI cannot
// misrepresent it.

// defaultPaperStrategy is what the UI lands on when no ?strategy= is given.
const defaultPaperStrategy = "flagship-1d"

// paperStrategies mirrors the worker's simulated portfolios (kept here so the
// API can advertise the choices without importing the pipeline package).
var paperStrategies = []string{"flagship-1d", "flagship-1w"}

func (d Deps) paper(w http.ResponseWriter, r *http.Request) {
	strategy := r.URL.Query().Get("strategy")
	if strategy == "" {
		strategy = defaultPaperStrategy
	}
	tradeLimit := 100
	if q := r.URL.Query().Get("trades"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 1000 {
			tradeLimit = n
		}
	}

	rawCurve, err := d.St.PaperEquityCurve(r.Context(), strategy, 5000)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	curve := make([]papertrade.EquityPoint, len(rawCurve))
	for i, p := range rawCurve {
		curve[i] = papertrade.EquityPoint{Ts: p.Ts, Cash: p.Cash, PositionsValue: p.PositionsValue, Equity: p.Equity}
	}

	positions, err := d.St.PaperPositions(r.Context(), strategy)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	recent, err := d.St.PaperTrades(r.Context(), strategy, tradeLimit)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	// Reconstruct closed round-trips + turnover from the FULL ordered trade log
	// (per symbol: a buy opens, the matching sell closes; won = sell net proceeds
	// exceed the buy net outlay). This is how win-rate + turnover stay honest.
	all, err := d.St.AllPaperTradesAsc(r.Context(), strategy)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	closed, numFills, tradedNotional := reconstructRoundTrips(all)
	summary := papertrade.Summarize(curve, closed, numFills, tradedNotional)

	writeJSON(w, map[string]any{
		"strategy":   strategy,
		"strategies": paperStrategies,
		// Honesty framing: this is a self-contained simulation, not a live account.
		"live":       false,
		"label":      "simulated paper trading — not live money, not advice",
		"startCash":  papertrade.StartingCash(),
		"longThresh": papertrade.LongThreshold(),
		"flatThresh": papertrade.FlatThreshold(),
		"equity":     curve,
		"positions":  positions,
		"trades":     recent,
		"summary":    summary,
	})
}

// reconstructRoundTrips walks the ordered trade log and pairs each open (buy)
// with its closing sell PER SYMBOL, producing the closed round-trips the summary
// grades. A round-trip WON when the sell's net proceeds (px*qty - cost) exceeded
// the buy's net outlay (px*qty + cost). It also returns the total fill count and
// the total traded notional (abs px*qty over every fill) for turnover.
//
// The worker is long/flat and closes the entire position on a sell, so per
// symbol the log alternates buy, sell, buy, sell…; this pairing is exact for
// that pattern and degrades safely (an unmatched trailing buy is an open
// position, not a round-trip).
func reconstructRoundTrips(all []store.PaperTrade) ([]papertrade.Trade, int, float64) {
	type open struct {
		outlay float64 // buy net outlay = px*qty + cost
	}
	openBySym := map[int64]open{}
	var closed []papertrade.Trade
	var tradedNotional float64
	numFills := len(all)
	for _, t := range all {
		notional := t.Px * t.Qty
		if notional < 0 {
			notional = -notional
		}
		tradedNotional += notional
		switch t.Side {
		case "buy":
			openBySym[t.SymbolID] = open{outlay: notional + t.Cost}
		case "sell":
			o, ok := openBySym[t.SymbolID]
			if !ok {
				continue // sell with no recorded open — skip (shouldn't happen)
			}
			delete(openBySym, t.SymbolID)
			proceeds := notional - t.Cost
			closed = append(closed, papertrade.Trade{Won: proceeds > o.outlay, Notional: notional})
		}
	}
	return closed, numFills, tradedNotional
}

// registerPaper wires the Stage-4 simulated paper-trading read route.
func (d Deps) registerPaper(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/paper", d.paper)
}
