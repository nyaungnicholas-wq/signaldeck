// Stage 3 (visual kit) — GET /api/dashboard: the ONE-call payload behind the
// rebuilt home screen, so the browser stops issuing ~10 fetches per load.
// Everything here is assembled from data other workers already store; nothing
// is fetched live and nothing is fabricated.
//
// Sections: tape (ticker strip), heatmap (universe day-%chg grid with
// best-effort EDGAR mcap sizing), gauges (breadth / VIX regime / anomalies-24h
// / prediction confidence — every gauge carries its honesty caption), movers
// (top+bottom 5), feed (news + SEC filings + anomalies + daily briefing,
// merged and kind-tagged), and — ONLY when a session is present — the caller's
// watchlist sparklines (ONE batched window-function query, never per-symbol).
//
// CACHE: the user-independent sections are cached in-memory for 60s per
// server (the payload says so via cacheTtlS); the watchlist section is
// per-user and computed fresh on every request.
//
// HONESTY: gauges keep their gate captions (confidence is "not significant"
// below the resolved-N gate, mirroring /honesty's independent-N rule); heatmap
// cells without EDGAR shares carry mcap=null ("sized: mcap unavailable" in the
// UI, never a fabricated size); every price is a stored daily close on worker
// cadence, not a live quote; VIX is the FRED daily close (~1 trading day lag).
package api

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/macrofeat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
	"github.com/nyaungnicholas-wq/signaldeck/internal/universe"
)

// dashboardTTL is how long the user-independent dashboard sections are served
// from memory before being rebuilt.
const dashboardTTL = 60 * time.Second

// dashSparkPoints is how many daily closes each watchlist sparkline carries.
const dashSparkPoints = 60

// dashFeedLimit caps the merged feed.
const dashFeedLimit = 40

const (
	dashCacheNote = "Shared sections are cached up to 60s; the watchlist section is per-user and always fresh."
	heatSizeNote  = "Cells sized by sqrt(mcap) when SEC EDGAR covers the symbol; mcap=null cells render uniform and are labeled 'sized: mcap unavailable' — never a guessed size."
	feedNote      = "Merged news + SEC filings + anomalies + daily briefings, newest first. Each source keeps its own honest lag: filings lag by law, anomaly rows are descriptive z-scores (not predictions), news sentiment is model-rated."
	breadthNote   = "Advancers vs decliners over stored daily universe closes (worker cadence, not live); ETF baskets excluded."
	anomNote      = "Distinct anomalies recorded in the last 24h (hour-deduped) — descriptive z-scores vs each symbol's own baseline, NOT predictions."
	confNote      = "Mean |calibrated p − 0.5| × 2 over each symbol's latest 1d prediction — calibration is backtested, not a live track record."
)

// vixRegimeLabels maps macrofeat's ordinal VIX bands to display labels.
var vixRegimeLabels = [4]string{"calm", "normal", "elevated", "stressed"}

// ── payload rows ─────────────────────────────────────────────────────────

// heatCell is one heatmap tile. Mcap nil = EDGAR hasn't covered the symbol
// (the UI sizes it uniformly and says so — honesty rule).
type heatCell struct {
	Symbol    string   `json:"symbol"`
	Name      string   `json:"name"`
	ChangePct float64  `json:"changePct"`
	Mcap      *float64 `json:"mcap"`
	Ts        int64    `json:"ts"`
}

// feedItem is one kind-tagged row of the merged home feed.
type feedItem struct {
	Kind      string  `json:"kind"` // news | filing | anomaly | briefing
	Ts        int64   `json:"ts"`
	Symbol    string  `json:"symbol,omitempty"`
	Market    string  `json:"market,omitempty"`
	Title     string  `json:"title"`
	Detail    string  `json:"detail,omitempty"`
	URL       string  `json:"url,omitempty"`
	Sentiment string  `json:"sentiment,omitempty"` // news only
	Sub       string  `json:"sub,omitempty"`       // anomaly kind / filing form
	Z         float64 `json:"z,omitempty"`         // anomaly z-score
}

// watchSpark is one watchlist row with its sparkline closes (oldest→newest).
// Score1d (Stage 4, dashboard rebuild) is the latest 1d ensemble score for
// the sidebar score chip; nil = not scored yet (the UI shows "—", honestly).
//
// Stage 2 (verdict cards): CalProb1d/NUsed1d/Tier1d/NSamples1d/TierThreshold
// feed the sidebar verdict card — the newest REAL calibrated P(up) plus the
// honest evidence tier behind it, from ONE batched VerdictStats read.
// CalProb1d nil = no prediction stored yet → the card says "NO READ YET",
// never a fabricated lean; Tier1d "" = no symbol-agent row yet (static).
type watchSpark struct {
	Symbol        string    `json:"symbol"`
	Market        md.Market `json:"market"`
	Name          string    `json:"name"`
	Closes        []float64 `json:"closes"`
	LastClose     float64   `json:"lastClose"`
	DayChangePct  float64   `json:"dayChangePct"`
	Score1d       *float64  `json:"score1d"`
	CalProb1d     *float64  `json:"calProb1d"`
	NUsed1d       int       `json:"nUsed1d"`
	Tier1d        string    `json:"tier1d"`
	NSamples1d    int       `json:"nSamples1d"`
	TierThreshold int       `json:"tierThreshold"`
}

// ── cache ────────────────────────────────────────────────────────────────

// dashCache holds the user-independent sections for one server instance.
type dashCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	builtAt time.Time
	global  map[string]any
}

func newDashCache(ttl time.Duration) *dashCache { return &dashCache{ttl: ttl} }

// get returns the cached global sections, rebuilding when stale. Built under
// the lock so concurrent requests never stampede the store.
func (c *dashCache) get(ctx context.Context, d Deps) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.global != nil && time.Since(c.builtAt) < c.ttl {
		return c.global, nil
	}
	g, err := d.buildDashGlobal(ctx)
	if err != nil {
		return nil, err
	}
	c.global = g
	c.builtAt = time.Now()
	return g, nil
}

// registerDashboard wires GET /api/dashboard with the production 60s cache.
func (d Deps) registerDashboard(mux *http.ServeMux) {
	d.registerDashboardCache(mux, newDashCache(dashboardTTL))
}

// registerDashboardCache wires the route against an injected cache (tests
// pass their own instance to drive TTL expiry deterministically).
func (d Deps) registerDashboardCache(mux *http.ServeMux, c *dashCache) {
	mux.HandleFunc("GET /api/dashboard", d.dashboardHandler(c))
}

// dashboardHandler serves the merged payload: cached global sections + the
// per-user watchlist section when (and only when) a session is present.
func (d Deps) dashboardHandler(c *dashCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		global, err := c.get(r.Context(), d)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		// Shallow-copy the cached map; nested sections are read-only once built.
		out := make(map[string]any, len(global)+1)
		for k, v := range global {
			out[k] = v
		}
		out["watchlist"] = nil // anonymous: no per-user section (the UI says "log in")
		if uid := userID(r); uid != 0 {
			wl, werr := d.buildDashWatchlist(r.Context(), uid)
			if werr != nil {
				httpErr(w, 500, werr.Error())
				return
			}
			out["watchlist"] = wl
		}
		writeJSON(w, out)
	}
}

// ── builders ─────────────────────────────────────────────────────────────

// buildDashGlobal assembles every user-independent section from batched store
// reads (one LastTwoDailyCloses scan feeds heatmap + movers + breadth).
func (d Deps) buildDashGlobal(ctx context.Context) (map[string]any, error) {
	now := time.Now().Unix()

	// Tape — same assembly as GET /api/tape (shared builder).
	tapeItems := d.buildTape(ctx)

	// Universe rows: ONE closes scan + ONE fundamentals scan.
	syms, err := d.St.ActiveStockSymbols(ctx, nil)
	if err != nil {
		return nil, err
	}
	closes, err := d.St.LastTwoDailyCloses(ctx)
	if err != nil {
		return nil, err
	}
	shares, err := d.St.LatestMetricAll(ctx, "SharesOutstanding")
	if err != nil {
		return nil, err
	}
	staleCutoff := now - 5*86400
	cells := make([]heatCell, 0, len(syms))
	mcapCovered := 0
	advancers, decliners := 0, 0
	for _, s := range syms {
		if universe.IsTapeETF(s.Symbol) {
			continue // baskets, not single-name moves
		}
		dc, ok := closes[s.ID]
		if !ok || dc.Prev == 0 || dc.Ts < staleCutoff {
			continue // no fresh bars → honestly absent, not grey-faked
		}
		cell := heatCell{
			Symbol: s.Symbol, Name: s.Name,
			ChangePct: pctChange(dc.Last, dc.Prev), Ts: dc.Ts,
		}
		if f, has := shares[s.ID]; has && f.Value > 0 {
			mc := f.Value * dc.Last
			cell.Mcap = &mc
			mcapCovered++
		}
		if cell.ChangePct > 0 {
			advancers++
		} else if cell.ChangePct < 0 {
			decliners++
		}
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i].Symbol < cells[j].Symbol })

	// Movers: top/bottom 5 of the same rows (no second scan).
	byChg := make([]heatCell, len(cells))
	copy(byChg, cells)
	sort.Slice(byChg, func(i, j int) bool { return byChg[i].ChangePct > byChg[j].ChangePct })
	nTop := 5
	if len(byChg) < nTop {
		nTop = len(byChg)
	}
	gainers := append([]heatCell{}, byChg[:nTop]...)
	losers := make([]heatCell, 0, nTop)
	for i := len(byChg) - 1; i >= 0 && len(losers) < nTop; i-- {
		losers = append(losers, byChg[i])
	}

	gauges, err := d.buildDashGauges(ctx, len(cells), advancers, decliners, now)
	if err != nil {
		return nil, err
	}
	feed, err := d.buildDashFeed(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"asOf":      now,
		"cacheTtlS": int(dashboardTTL / time.Second),
		"note":      dashCacheNote,
		"tape":      map[string]any{"items": tapeItems, "note": tapeNote},
		"heatmap": map[string]any{
			"items":       cells,
			"n":           len(cells),
			"mcapCovered": mcapCovered,
			"note":        moversNote,
			"mcapNote":    mcapNote,
			"sizeNote":    heatSizeNote,
		},
		"movers": map[string]any{
			"gainers": gainers, "losers": losers,
			"note": moversNote, "mcapNote": mcapNote,
		},
		"gauges": gauges,
		"feed":   map[string]any{"items": feed, "count": len(feed), "note": feedNote},
	}, nil
}

// buildDashGauges assembles the four dial inputs. EVERY gauge carries its
// honesty caption — the UI renders the caption line unconditionally.
func (d Deps) buildDashGauges(ctx context.Context, universeN, advancers, decliners int, now int64) (map[string]any, error) {
	// Breadth: % advancers over symbols with fresh bars.
	breadthPct := 0.0
	if universeN > 0 {
		breadthPct = float64(advancers) / float64(universeN) * 100
	}
	breadth := map[string]any{
		"pct": breadthPct, "advancers": advancers, "decliners": decliners,
		"n": universeN, "hasData": universeN > 0,
		"caption": fmtBreadthCaption(universeN),
	}

	// VIX: FRED daily close + the conventional regime bands (macrofeat owns
	// the thresholds; ordinal 0..3 = calm..stressed).
	vix := map[string]any{"hasData": false, "caption": vixNote}
	if pts, err := d.St.MacroSeries(ctx, "VIXCLS", 2); err != nil {
		return nil, err
	} else if len(pts) > 0 {
		last := pts[len(pts)-1]
		ord := int(math.Round(macrofeat.FromVIX(last.Value).Regime * 3))
		if ord < 0 {
			ord = 0
		} else if ord > 3 {
			ord = 3
		}
		v := map[string]any{
			"level": last.Value, "regime": vixRegimeLabels[ord], "regimeOrdinal": ord,
			"ts": last.Ts, "hasData": true, "caption": vixNote,
		}
		if len(pts) > 1 {
			v["dayChangePct"] = pctChange(last.Value, pts[0].Value)
		}
		vix = v
	}

	// Anomalies in the last 24h (hour-deduped, descriptive).
	anomN, err := d.St.AnomalyCountSince(ctx, now-86400)
	if err != nil {
		return nil, err
	}
	anoms := map[string]any{"count": anomN, "windowH": 24, "caption": anomNote}

	// Confidence: mean calibrated-prob distance from coin-flip over the latest
	// 1d prediction per symbol, GATED on resolved outcomes (same 30-obs floor
	// as /honesty). Below the gate the UI shows the caption, not a verdict.
	avgConf, predN, err := d.St.LatestPredictionStats(ctx, md.H1d)
	if err != nil {
		return nil, err
	}
	// INDEPENDENT (symbol, UTC-day) resolutions — see stage5.go. The gauge's
	// "resolved outcomes" claim must count bets, not rows.
	resolvedN, distinctDays, err := d.St.ResolvedPredictionIndependentCount(ctx, md.H1d)
	if err != nil {
		return nil, err
	}
	gated := resolvedN < minIndependentN
	confCaption := confNote
	if gated {
		confCaption = fmtGateCaption(resolvedN)
	}
	conf := map[string]any{
		"avg": avgConf, "n": predN, "resolvedN": resolvedN,
		"distinctDays": distinctDays,
		"minResolvedN": minIndependentN, "gated": gated,
		"hasData": predN > 0, "caption": confCaption,
	}

	return map[string]any{
		"breadth":    breadth,
		"vix":        vix,
		"anomalies":  anoms,
		"confidence": conf,
	}, nil
}

func fmtBreadthCaption(n int) string {
	if n == 0 {
		return "no fresh daily universe bars yet — the universe poller fills them on its own cadence"
	}
	return "breadth over " + strconv.Itoa(n) + " symbols — " + breadthNote
}

func fmtGateCaption(resolvedN int) string {
	return "n=" + strconv.Itoa(resolvedN) + "/" + strconv.Itoa(minIndependentN) +
		" resolved — not significant yet; " + confNote
}

// buildDashFeed merges the four stored event sources into ONE kind-tagged,
// newest-first feed (four bounded reads, no per-symbol queries).
func (d Deps) buildDashFeed(ctx context.Context) ([]feedItem, error) {
	items := make([]feedItem, 0, dashFeedLimit*2)

	news, err := d.St.RecentNews(ctx, dashFeedLimit)
	if err != nil {
		return nil, err
	}
	for _, n := range news {
		items = append(items, feedItem{
			Kind: "news", Ts: n.Ts, Symbol: n.Symbol,
			Title: n.Headline, URL: n.URL, Sentiment: n.Sentiment,
		})
	}

	filings, err := d.St.Filings(ctx, 0, "", dashFeedLimit)
	if err != nil {
		return nil, err
	}
	for _, f := range filings {
		title := f.Label
		if title == "" {
			title = f.Form + " — " + f.Title
		}
		items = append(items, feedItem{
			Kind: "filing", Ts: f.FiledTs, Symbol: f.Symbol, Market: string(md.Stocks),
			Title: title, URL: f.URL, Sub: f.Form,
		})
	}

	anoms, err := d.St.Anomalies(ctx, 0, "", dashFeedLimit)
	if err != nil {
		return nil, err
	}
	for _, a := range anoms {
		items = append(items, feedItem{
			Kind: "anomaly", Ts: a.Ts, Symbol: a.Symbol, Market: a.Market,
			Title: a.Detail, Sub: a.Kind, Z: a.Z,
		})
	}

	briefs, err := d.St.InsightsByKind(ctx, "daily_briefing", 3)
	if err != nil {
		return nil, err
	}
	for _, b := range briefs {
		items = append(items, feedItem{
			Kind: "briefing", Ts: b.Ts, Symbol: b.Symbol,
			Title: b.Headline, Detail: b.Body,
		})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Ts > items[j].Ts })
	if len(items) > dashFeedLimit {
		items = items[:dashFeedLimit]
	}
	return items, nil
}

// buildDashWatchlist assembles the per-user sidebar: sparkline closes for
// every watched symbol from ONE batched window-function query (no per-symbol
// walk), plus the unseen-alert badge count.
func (d Deps) buildDashWatchlist(ctx context.Context, uid int64) (map[string]any, error) {
	syms, err := d.St.ListUserSymbols(ctx, uid)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(syms))
	for i, s := range syms {
		ids[i] = s.ID
	}
	sparkMap, err := d.St.LastNDailyCloses(ctx, ids, dashSparkPoints)
	if err != nil {
		return nil, err
	}
	// Stage 2 (verdict cards): the whole watchlist's newest calibrated 1d
	// prediction + symbol-agent tier in one batched read (never per-symbol).
	verdicts, err := d.St.VerdictStats(ctx, ids, md.H1d)
	if err != nil {
		return nil, err
	}
	sparks := make([]watchSpark, 0, len(syms))
	for _, s := range syms {
		ws := watchSpark{Symbol: s.Symbol, Market: s.Market, Name: s.Name, Closes: sparkMap[s.ID]}
		if vs, ok := verdicts[s.ID]; ok {
			ws.CalProb1d, ws.NUsed1d = vs.CalProb, vs.NUsed
			ws.Tier1d, ws.NSamples1d = vs.Tier, vs.NSamples
		}
		ws.TierThreshold = symbolagent.MinPersonal
		if n := len(ws.Closes); n > 0 {
			ws.LastClose = ws.Closes[n-1]
			if n > 1 {
				ws.DayChangePct = pctChange(ws.Closes[n-1], ws.Closes[n-2])
			}
		}
		// Stage 4: 1d score chip for the sidebar — same per-symbol lookup the
		// /api/watchlist handler already does; watchlists are small (user-sized).
		if sc, ok, serr := d.St.LatestScore(ctx, s.ID, md.H1d); serr != nil {
			return nil, serr
		} else if ok {
			v := sc.Score
			ws.Score1d = &v
		}
		sparks = append(sparks, ws)
	}
	unseen, err := d.St.UnseenAlertCount(ctx, uid)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"sparks":       sparks,
		"unseenAlerts": unseen,
		"sparkPoints":  dashSparkPoints,
		"note":         "Sparklines are the last " + strconv.Itoa(dashSparkPoints) + " stored daily closes per watched symbol (worker cadence, not live).",
	}, nil
}

// buildTape assembles the ticker-tape items from stored data — the shared
// builder behind GET /api/tape and the dashboard's tape section.
func (d Deps) buildTape(ctx context.Context) []tapeItem {
	items := make([]tapeItem, 0, len(universe.TapeETFs)+2)

	// Index + sector ETFs from stored daily bars.
	for _, e := range universe.TapeETFs {
		it := tapeItem{Symbol: e.Symbol, Label: e.Name, Kind: e.Kind, Market: string(md.Stocks)}
		if s, err := d.St.GetSymbol(ctx, e.Symbol, md.Stocks); err == nil {
			if last, prev, ts, ok := d.lastTwoClosesCtx(ctx, s.ID); ok {
				it.Price, it.DayChangePct, it.Ts, it.HasData = last, pctChange(last, prev), ts, true
			}
		}
		items = append(items, it)
	}

	// BTC (the consolidated crypto symbol) from its daily bars.
	btc := tapeItem{Symbol: d.Cfg.CryptoSymbol, Label: "Bitcoin", Kind: "crypto", Market: string(md.Crypto)}
	if s, err := d.St.GetSymbol(ctx, d.Cfg.CryptoSymbol, md.Crypto); err == nil {
		if last, prev, ts, ok := d.lastTwoClosesCtx(ctx, s.ID); ok {
			btc.Price, btc.DayChangePct, btc.Ts, btc.HasData = last, pctChange(last, prev), ts, true
		}
	}
	items = append(items, btc)

	// VIX from the stored FRED VIXCLS series (daily close, ~1 day lag).
	vix := tapeItem{Symbol: "VIX", Label: "CBOE Volatility Index", Kind: "vix", Note: vixNote}
	if pts, err := d.St.MacroSeries(ctx, "VIXCLS", 2); err == nil && len(pts) > 0 {
		n := len(pts)
		vix.Price, vix.Ts, vix.HasData = pts[n-1].Value, pts[n-1].Ts, true
		if n > 1 {
			vix.DayChangePct = pctChange(pts[n-1].Value, pts[n-2].Value)
		}
	}
	items = append(items, vix)
	return items
}

// lastTwoClosesCtx is lastTwoCloses without the *http.Request dependency (the
// dashboard builder runs off a context, not a request).
func (d Deps) lastTwoClosesCtx(ctx context.Context, symbolID int64) (last, prev float64, ts int64, ok bool) {
	bars, err := d.St.LastBars(ctx, symbolID, md.TF1d, 2)
	if err != nil || len(bars) == 0 {
		return 0, 0, 0, false
	}
	n := len(bars)
	last, ts = bars[n-1].Close, bars[n-1].Ts
	if n > 1 {
		prev = bars[n-2].Close
	}
	return last, prev, ts, true
}
