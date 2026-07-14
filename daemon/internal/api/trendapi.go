// CANDLESTICK-PATTERNS wave — trend API (read-only, public read):
//
//	GET /api/trend?symbol=&market=&tf=1d — the geometric trend read over the
//	recent daily window: classification (uptrend|downtrend|range), the
//	regression slope, the fitted support/resistance trendlines, and whether the
//	two form a channel. Thin history returns a gate reason instead of a
//	fabricated classification.
//
// HONESTY (shipped verbatim in every payload): a geometric read from recent
// swings — descriptive; trendlines are fitted, not predictive.
package api

import (
	"fmt"
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/trend"
)

// trendAPINote ships verbatim with every /api/trend payload.
const trendAPINote = "geometric trend read from recent swings — descriptive; trendlines are fitted, not predictive"

// trendVisibleBars is the recent daily window the trend geometry is read over.
const trendVisibleBars = 200

// trendRead serves the geometric trend classification + fitted trendlines.
// GET /api/trend?symbol=&market=&tf=1d
func (d Deps) trendRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	tf := r.URL.Query().Get("tf")
	if tf == "" {
		tf = "1d"
	}
	if tf != "1d" {
		httpErr(w, 400, "only tf=1d is supported")
		return
	}

	bars, err := d.St.LastBars(ctx, s.ID, md.TF1d, trendVisibleBars)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	out := map[string]any{
		"symbol": s.Symbol,
		"market": s.Market,
		"tf":     tf,
		"note":   trendAPINote,
	}
	res, ok := trend.Analyze(bars)
	if !ok {
		// Thin history: an honest gate reason, never a fabricated classification.
		out["classification"] = nil
		out["slopePctPerBar"] = 0.0
		out["trendlines"] = []trend.Line{}
		out["channel"] = false
		out["gateReason"] = fmt.Sprintf("need >= %d daily bars for a geometric trend read (have %d)", trend.MinBars, len(bars))
		writeJSON(w, out)
		return
	}
	lines := res.Trendlines
	if lines == nil {
		lines = []trend.Line{}
	}
	out["classification"] = res.Class
	out["slopePctPerBar"] = res.SlopePctPerBar
	out["trendlines"] = lines
	out["channel"] = res.Channel
	writeJSON(w, out)
}

func (d Deps) registerTrendRead(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/trend", d.trendRead)
}
