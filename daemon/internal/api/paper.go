package api

import (
	"net/http"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/moneymetrics"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// paperMoneyCaption ships VERBATIM on the paper/track-record money scoreboard —
// the one honesty line that reframes the whole page: profit, not win rate.
const paperMoneyCaption = "Win rate alone does not equal profit — a high win rate with large losers still loses money. Expectancy (avg profit per trade after costs) is what matters."

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

	// MONEY SCOREBOARD: score the closed round-trips by EXPECTED PROFIT
	// (expectancy / profit factor / payoff), the numbers that actually decide
	// whether the signal makes money. Returns are already NET of both-side costs.
	money := moneymetrics.FromReturns(roundTripReturns(all))

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
		// Money scoreboard leads the display; the caption reframes win rate.
		"money":        money,
		"moneyCaption": paperMoneyCaption,
	})
}

// roundTripReturns walks the ordered trade log and returns the NET fractional
// return of each completed buy→sell round-trip PER SYMBOL: (sell proceeds − buy
// outlay) / buy outlay, where both sides are already net of their per-side cost.
// A high win rate over these can still lose money if the losers are large — which
// is exactly what the money scoreboard exposes. An unmatched trailing buy is an
// open position (no round-trip yet) and is skipped.
func roundTripReturns(all []store.PaperTrade) []float64 {
	outlayBySym := map[int64]float64{} // buy net outlay = px*qty + cost
	var out []float64
	for _, t := range all {
		notional := t.Px * t.Qty
		if notional < 0 {
			notional = -notional
		}
		switch t.Side {
		case "buy":
			outlayBySym[t.SymbolID] = notional + t.Cost
		case "sell":
			outlay, ok := outlayBySym[t.SymbolID]
			if !ok || outlay <= 0 {
				continue // sell with no recorded open — skip (shouldn't happen)
			}
			delete(outlayBySym, t.SymbolID)
			proceeds := notional - t.Cost
			out = append(out, proceeds/outlay-1)
		}
	}
	return out
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
