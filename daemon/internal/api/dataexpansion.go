// DATA-EXPANSION wave API handlers (read-only, gated like every other read):
// six free external context datasets, each served WITH ITS CAVEAT VERBATIM —
// none of this is a scored factor, a prediction, or advice.
//
//   - GET /api/short-interest  — FINRA bi-monthly short interest (per symbol)
//   - GET /api/crypto-perp     — Hyperliquid perp funding/OI (per crypto symbol)
//   - GET /api/cot             — CFTC COT weekly positioning (curated contracts)
//   - GET /api/stocktwits      — StockTwits page-snapshot sentiment (per symbol)
//   - GET /api/wiki-attention  — Wikipedia daily page views + descriptive z
//   - GET /api/cboe-pc         — CBOE market-wide daily put/call ratios
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// Caveats ship VERBATIM with every payload and every UI surface.
	shortInterestNote = "bi-monthly FINRA short interest (settlement-dated, published ~2wks lagged) — descriptive positioning, not advice"
	cryptoPerpNote    = "perp funding/OI from Hyperliquid (a DEX) — venue-specific positioning proxy, descriptive"
	cotNote           = "CFTC Commitments of Traders, legacy futures-only report — Tuesday positions published Friday (weekly lag); positioning, not prediction"
	stocktwitsNote    = "retail message sentiment (StockTwits public stream) — self-selected crowd, descriptive only"
	wikiNote          = "public attention proxy — not a trading signal"
	wikiZNote         = "z-score of the latest day's views vs this symbol's own trailing days in the window (needs 10+ prior days, stated stddev floor) — descriptive only; article resolution is a name heuristic"
	stSnapshotNote    = "each point is a PAGE SNAPSHOT: sentiment-tag counts over the ~30 newest messages at fetch time, not a census"
	cboePCNote        = "CBOE market-wide daily put/call ratios — a positioning/hedging gauge; index puts are largely hedges, so a high index P/C is NOT directly bearish; descriptive only"
	tvQuoteNote       = "price from TradingView's public scanner — rtc is a real-time Cboe One composite when present, otherwise a 15-minute-delayed close (update_mode-flagged); full-market cumulative day volume; descriptive supplement to the bar record, not a replacement"
)

// shortInterest serves the bi-monthly FINRA short interest for one stock.
// GET /api/short-interest?symbol=&market=stocks
func (d Deps) shortInterest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	rows, err := d.St.ShortInterestRecent(ctx, s.ID, 8)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"symbol": s.Symbol,
		"note":   shortInterestNote,
		"recent": rows, // newest first, ≤8 settlement periods
	}
	if len(rows) > 0 {
		rows[0].Symbol = s.Symbol
		out["latest"] = rows[0]
	} else {
		out["latest"] = nil
		out["emptyNote"] = "no short interest stored yet — the finra-shortint worker probes the newest bi-monthly file each run (publication lags settlement ~9 business days)"
	}
	if len(rows) > 1 {
		out["previous"] = rows[1]
	} else {
		out["previous"] = nil
	}
	writeJSON(w, out)
}

// cryptoPerp serves Hyperliquid perp funding/OI snapshots for one crypto pair.
// GET /api/crypto-perp?symbol=BTC/USD&market=crypto&hours=
func (d Deps) cryptoPerp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	hours := 72
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24*30 {
			hours = n
		}
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	series, err := d.St.CryptoPerpSeries(ctx, s.ID, since)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"symbol": s.Symbol,
		"note":   cryptoPerpNote,
		"hours":  hours,
		"series": series, // ASC
	}
	if len(series) > 0 {
		out["latest"] = series[len(series)-1]
	} else {
		out["latest"] = nil
		out["emptyNote"] = "no perp snapshots stored yet — the crypto-perp worker polls Hyperliquid every 15m"
	}
	writeJSON(w, out)
}

// cot serves the stored COT positioning: latest per contract + ≤1y series.
// GET /api/cot
func (d Deps) cot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since := time.Now().AddDate(-1, 0, 0).Format("2006-01-02")
	rows, err := d.St.COTSince(ctx, since)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	type contractOut struct {
		Contract string         `json:"contract"`
		Latest   store.COTRow   `json:"latest"`
		Series   []store.COTRow `json:"series"` // ASC, ≤1y
	}
	var contracts []contractOut
	for _, row := range rows { // rows are ordered contract ASC, date ASC
		if n := len(contracts); n == 0 || contracts[n-1].Contract != row.Contract {
			contracts = append(contracts, contractOut{Contract: row.Contract})
		}
		c := &contracts[len(contracts)-1]
		c.Series = append(c.Series, row)
		c.Latest = row // date-ASC ⇒ last assignment wins
	}
	out := map[string]any{
		"note":      cotNote,
		"contracts": contracts,
	}
	if len(contracts) == 0 {
		out["emptyNote"] = "no COT reports stored yet — the cot-poller backfills ~1y of the curated contracts on its first run"
	}
	writeJSON(w, out)
}

// stocktwitsSentiment serves the page-snapshot sentiment series for a symbol.
// GET /api/stocktwits?symbol=&market=stocks&hours=
func (d Deps) stocktwitsSentiment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	hours := 72
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24*30 {
			hours = n
		}
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	series, err := d.St.StocktwitsSeries(ctx, s.ID, since)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"symbol":       s.Symbol,
		"note":         stocktwitsNote,
		"snapshotNote": stSnapshotNote,
		"hours":        hours,
		"series":       series, // ASC
	}
	if len(series) > 0 {
		out["latest"] = series[len(series)-1]
	} else {
		out["latest"] = nil
		out["emptyNote"] = "no snapshots stored yet — the stocktwits-fetcher covers watchlist + hot-set stocks every 15m"
	}
	writeJSON(w, out)
}

// wikiAttention serves the Wikipedia page-view attention series for a symbol.
// GET /api/wiki-attention?symbol=&market=stocks&days=
func (d Deps) wikiAttention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	days := 90
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	series, err := d.St.WikiViewsSeries(ctx, s.ID, days)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"symbol": s.Symbol,
		"note":   wikiNote,
		"days":   days,
		"series": series, // ASC
	}
	if wa, found, _ := d.St.GetWikiArticle(ctx, s.ID); found {
		out["article"] = wa.Article
		out["resolved"] = wa.OK
		if !wa.OK {
			out["resolutionNote"] = "article resolution failed for this symbol's company name (cached — not re-attempted); no attention series is possible"
		}
	}
	// Descriptive z of the latest day vs the symbol's own trailing baseline —
	// same gate discipline as /api/shorts (10+ prior days, stddev floor).
	views := make([]float64, 0, len(series))
	for _, p := range series {
		views = append(views, float64(p.Views))
	}
	if z, ok := shortsZ(views); ok {
		out["latestZ"] = z
	} else {
		out["latestZ"] = nil
	}
	out["zNote"] = wikiZNote
	if len(series) == 0 {
		out["emptyNote"] = "no page views stored yet — the wiki-attention worker covers watchlist + hot-set stocks daily (needs the companies directory for name resolution)"
	}
	writeJSON(w, out)
}

// cboePC serves the market-wide daily put/call ratio series.
// GET /api/cboe-pc?days=
func (d Deps) cboePC(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	days := 90
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	series, err := d.St.CboePCSeries(ctx, days)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"note":   cboePCNote,
		"days":   days,
		"series": series, // ASC
	}
	if len(series) > 0 {
		out["latest"] = series[len(series)-1]
	} else {
		out["latest"] = nil
		out["emptyNote"] = "no put/call data stored yet — the cboe-pc worker ingests each trade date's statistics after ~6:30pm ET and backfills ~30 trading days on first run"
	}
	writeJSON(w, out)
}

// tvQuote serves the newest scanner quote for a tracked symbol.
// GET /api/tv-quote?symbol=&market=
func (d Deps) tvQuote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	out := map[string]any{
		"symbol": s.Symbol,
		"note":   tvQuoteNote,
	}
	q, ok, err := d.St.LatestTVQuote(ctx, s.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !ok {
		out["available"] = false
		out["emptyNote"] = "no quote stored yet — the tv-quotes worker tapes stocks each minute while the market is open (crypto 24/7) and keeps only a trailing ~2h window"
		writeJSON(w, out)
		return
	}
	out["available"] = true
	out["quote"] = q
	writeJSON(w, out)
}

// registerDataExpansion wires the data-expansion read routes.
func (d Deps) registerDataExpansion(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/short-interest", d.shortInterest)
	mux.HandleFunc("GET /api/crypto-perp", d.cryptoPerp)
	mux.HandleFunc("GET /api/cot", d.cot)
	mux.HandleFunc("GET /api/stocktwits", d.stocktwitsSentiment)
	mux.HandleFunc("GET /api/wiki-attention", d.wikiAttention)
	mux.HandleFunc("GET /api/cboe-pc", d.cboePC)
	mux.HandleFunc("GET /api/tv-quote", d.tvQuote)
}
