// NEWS-TRENDS wave — API (read-only, public read like the other descriptive
// context endpoints):
//
//	GET /api/news-trends?symbol=&market= — one symbol's 30d headline-volume
//	series, the live news-volume z (null with a stated gate reason when the
//	symbol's own baseline can't support one), and the fleet's top-10 trending
//	headline tokens over the last 24h.
//
// HONESTY (shipped verbatim in every payload): headline-frequency trend —
// descriptive attention, not a forecast.
package api

import (
	"net/http"
	"time"
)

// newsTrendsAPINote ships verbatim with every /api/news-trends payload (the
// same caveat the news-trends worker writes into its insights).
const newsTrendsAPINote = "headline-frequency trend — descriptive attention, not a forecast"

// newsTrends serves one symbol's news-volume trend + the fleet token board.
// GET /api/news-trends?symbol=&market=
func (d Deps) newsTrends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	series, err := d.St.NewsVolumeByDay(ctx, s.ID, 30)
	if err != nil {
		httpInternal(w, err)
		return
	}
	day, n, z, ok, reason, err := d.St.NewsVolumeZ(ctx, s.ID)
	if err != nil {
		httpInternal(w, err)
		return
	}
	toks, err := d.St.FleetTrendingTokens(ctx, time.Now().Add(-24*time.Hour).Unix(), 10)
	if err != nil {
		httpInternal(w, err)
		return
	}
	out := map[string]any{
		"symbol":       s.Symbol,
		"volumeSeries": series, // oldest first; zero-news days absent
		"day":          day,
		"todayCount":   n,
		"fleetTokens":  toks,
		"note":         newsTrendsAPINote,
	}
	if ok {
		out["latestZ"] = z
	} else {
		out["latestZ"] = nil
		out["zGateReason"] = reason // honest absence, never a fabricated 0
	}
	writeJSON(w, out)
}

func (d Deps) registerNewsTrends(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/news-trends", d.newsTrends)
}
