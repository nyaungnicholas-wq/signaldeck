package api

import (
	"encoding/json"
	"math"
	"net/http"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/regimecond"
)

// news returns recent headlines: for one symbol (?symbol=&market=) or the
// whole feed.
func (d Deps) news(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("symbol") != "" {
		s, err := d.symbolFromQuery(r)
		if err != nil {
			httpErr(w, 404, err.Error())
			return
		}
		items, err := d.St.SymbolNews(r.Context(), s.ID, 30)
		if err != nil {
			httpInternal(w, err)
			return
		}
		writeJSON(w, items)
		return
	}
	items, err := d.St.RecentNews(r.Context(), 60)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, items)
}

// sectors returns the latest sector-strength aggregation (money rotating in).
func (d Deps) sectors(w http.ResponseWriter, r *http.Request) {
	raw, err := d.St.GetJSONRaw(r.Context(), "sector_agg")
	if err != nil {
		httpInternal(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if raw == "" {
		_, _ = w.Write([]byte("[]"))
		return
	}
	_, _ = w.Write([]byte(raw))
}

// regimeConditioned computes, on demand, how a symbol behaved historically in
// each regime — the "regime-conditioned forecast".
func (d Deps) regimeConditioned(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	daily, err := d.St.LastBars(r.Context(), s.ID, md.TF1d, 800)
	if err != nil {
		httpInternal(w, err)
		return
	}
	out := map[string]any{}
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		fb := regimecond.ForBars(h)
		if fb == 0 {
			continue
		}
		out[string(h)] = regimecond.Build(daily, fb)
	}
	cur, _ := regimecond.Current(daily)
	writeJSON(w, map[string]any{"current": cur, "byHorizon": out})
}

// macro surfaces a real macro/context read WITHOUT a paid data feed: market
// breadth from the tracked universe, a volatility regime from SPY, and the
// stock-trader PUSH-20 macro regime (already synced via hud).
func (d Deps) macro(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	syms, err := d.St.ListSymbols(ctx, false)
	if err != nil {
		httpInternal(w, err)
		return
	}
	// Breadth: fraction of tracked symbols with a positive 1d score.
	// readErrs is counted, not folded. `err != nil` and "no score yet" shared one
	// branch, so `scored` counted only what read CLEANLY: partial failure
	// computed breadth over a silently shrunk universe, and total failure gave
	// breadthPct 0, rendered as "BREADTH 0.0%, 0 of 0 scored positive", delta
	// -50, glow "down" -- and added to the risk-OFF list. A fabricated market
	// read, from a 5.9s endpoint sharing a 4-connection pool.
	pos, scored, readErrs := 0, 0, 0
	for _, s := range syms {
		sc, ok, err := d.St.LatestScore(ctx, s.ID, md.H1d)
		switch {
		case err != nil:
			readErrs++
		case ok:
			scored++
			if sc.Score > 0 {
				pos++
			}
		}
	}
	// breadthKnown gates the number rather than substituting one. A breadth
	// computed while a meaningful share of the universe failed to read is not a
	// measurement of the market, and 0.0 is the single most alarming value this
	// field can take.
	breadth := 0.0
	breadthKnown := scored > 0 && readErrs*4 <= scored
	if breadthKnown {
		breadth = float64(pos) / float64(scored)
	}
	// Volatility regime from SPY daily realized vol (proxy for "fear").
	volPct, volLabel := d.spyVolRegime(r)
	// PUSH-20 macro regime from the synced hud payload (real, from stock-trader).
	var hudMacro any
	var hudFetchedAt int64
	if payload, fetchedAt, ok, _ := d.St.GetHud(ctx); ok {
		hudFetchedAt = fetchedAt
		var hud map[string]any
		if json.Unmarshal([]byte(payload), &hud) == nil {
			if m, has := hud["macro"]; has {
				hudMacro = m
			} else if m, has := hud["regime"]; has {
				hudMacro = m
			}
		}
	}
	writeJSON(w, map[string]any{
		"breadthPct":        breadthValue(breadth, breadthKnown),
		"breadthKnown":      breadthKnown,
		"breadthReadErrors": readErrs,
		"positive":          pos,
		"scored":            scored,
		"volPct":            volPct,
		"volLabel":          volLabel,
		"push20Macro":       hudMacro,
		// F-2 shape: hud is a synced snapshot — publish ITS age, not asOf's.
		"push20MacroFetchedAt": hudFetchedAt,
		"note":                 "Breadth + volatility are computed from your stored bars. Full per-symbol fundamentals and an economic-event calendar require a paid data feed (not wired). PUSH-20 macro comes from your stock-trader monitor.",
		"asOf":                 time.Now().Unix(),
	})
}

// spyVolRegime returns SPY's recent realized vol percentile-ish label.
func (d Deps) spyVolRegime(r *http.Request) (float64, string) {
	s, err := d.St.GetSymbol(r.Context(), "SPY", md.Stocks)
	if err != nil {
		return 0, "unknown"
	}
	bars, err := d.St.LastBars(r.Context(), s.ID, md.TF1d, 60)
	if err != nil || len(bars) < 21 {
		return 0, "unknown"
	}
	// annualized realized vol over last 20 daily log returns
	var rets []float64
	for i := len(bars) - 20; i < len(bars); i++ {
		if bars[i-1].Close > 0 {
			rets = append(rets, math.Log(bars[i].Close/bars[i-1].Close))
		}
	}
	mean := 0.0
	for _, x := range rets {
		mean += x
	}
	mean /= float64(len(rets))
	varSum := 0.0
	for _, x := range rets {
		varSum += (x - mean) * (x - mean)
	}
	sd := math.Sqrt(varSum/float64(len(rets))) * math.Sqrt(252) * 100 // annualized %
	label := "normal"
	switch {
	case sd < 12:
		label = "calm"
	case sd > 25:
		label = "stressed"
	case sd > 18:
		label = "elevated"
	}
	return sd, label
}

func (d Deps) registerData(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/news", d.news)
	mux.HandleFunc("GET /api/sectors", d.sectors)
	mux.HandleFunc("GET /api/regime-conditioned", d.regimeConditioned)
	mux.HandleFunc("GET /api/macro", func(w http.ResponseWriter, r *http.Request) {
		// Perf wave 2026-07-24: measured 5.9s per request; SWR-cached.
		sharedMacroSWR.serve("macro", w, r, d.macro)
	})
}

// breadthValue returns nil rather than a number when too much of the universe
// failed to read. A percentage assembled from a partial scan is not a breadth
// reading, and 0.0 is the most alarming value the field can take -- so the
// honest answer to "we could not measure it" is no number at all.
func breadthValue(breadth float64, known bool) any {
	if !known {
		return nil
	}
	return breadth * 100
}
