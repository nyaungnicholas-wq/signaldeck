// TradingView SCANNER RATINGS API: GET /api/tv-rating (read-only, public like
// /api/shorts). Serves the LATEST stored TradingView technical-analysis rating
// for one tracked symbol, ingested by the tv-rating worker from TradingView's
// public scanner endpoint.
//
// HONESTY, verbatim in every payload: this is TradingView's OWN technical-
// analysis rating from its public scanner — DESCRIPTIVE, DELAYED, an EXTERNAL
// signal, NOT SignalDeck's model and not advice.
package api

import (
	"net/http"
)

// tvRatingNote ships VERBATIM with every payload and UI surface.
const tvRatingNote = "TradingView's own technical-analysis rating from its public scanner — descriptive, delayed, NOT our model and not advice"

// tvRating serves the latest stored TradingView rating for a symbol.
// GET /api/tv-rating?symbol=&market=
func (d Deps) tvRating(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sym, err := d.symbolFromQuery(r)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "reason": err.Error(), "note": tvRatingNote})
		return
	}
	row, ok := d.St.LatestTVRating(ctx, sym.ID)
	if !ok {
		writeJSON(w, map[string]any{
			"available": false,
			"symbol":    sym.Symbol,
			"market":    string(sym.Market),
			"reason":    "no TradingView rating stored yet for this symbol — the tv-rating worker resolves its exchange and scans on a 15m cadence",
			"note":      tvRatingNote,
		})
		return
	}
	writeJSON(w, map[string]any{
		"available": true,
		"symbol":    sym.Symbol,
		"market":    string(sym.Market),
		"ts":        row.Ts,
		"recoAll":   row.RecoAll,
		"recoMA":    row.RecoMA,
		"recoOther": row.RecoOther,
		"rsi":       row.RSI,
		"close":     row.Close,
		"label":     row.Label,
		"note":      tvRatingNote,
	})
}

// registerTVRating wires the read route.
func (d Deps) registerTVRating(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tv-rating", d.tvRating)
}
