// Package api serves SignalDeck's JSON API (and CSV exports) to the web app.
// Read paths are store queries only; the two POSTs mutate via injected
// callbacks so this package stays free of ingestion dependencies.
package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Deps wires the API to the rest of the daemon.
type Deps struct {
	St      *store.Store
	Cfg     config.Config
	Version string
	Started time.Time
	LLM     llm.Client // AI provider (may be disabled when no key is set)
	// Subscribe validates a new symbol, upserts it, and kicks off backfill
	// (async). Wired in cmd/signaldeckd.
	Subscribe func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error)
	// CurrentState returns the live expectancy state keys for a symbol.
	CurrentState func(ctx context.Context, symbolID int64) (map[md.Horizon]string, error)
}

// Serve runs the API server until ctx is canceled.
func Serve(ctx context.Context, d Deps) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", d.health)
	mux.HandleFunc("GET /api/watchlist", d.watchlist)
	mux.HandleFunc("GET /api/symbol", d.symbolDetail)
	mux.HandleFunc("GET /api/bars", d.bars)
	mux.HandleFunc("GET /api/scores/history", d.scoreHistory)
	mux.HandleFunc("GET /api/screener", d.watchlist) // same rows; UI filters
	mux.HandleFunc("GET /api/trends", d.trends)
	mux.HandleFunc("GET /api/honesty", d.honesty)
	mux.HandleFunc("GET /api/quality", d.quality)
	mux.HandleFunc("GET /api/agents", d.agents)
	mux.HandleFunc("GET /api/hud", d.hud)
	mux.HandleFunc("GET /api/insights", d.insights)
	mux.HandleFunc("GET /api/snaps", d.snaps)
	mux.HandleFunc("POST /api/subscribe", d.subscribe)
	mux.HandleFunc("POST /api/unsubscribe", d.unsubscribe)
	d.registerQuant(mux) // forecast, backtest, risk, correlation, portfolio
	d.registerAI(mux)    // analyst, chat, filingmind, status
	mux.HandleFunc("GET /api/export/bars.csv", d.exportBars)
	mux.HandleFunc("GET /api/export/scores.csv", d.exportScores)
	mux.HandleFunc("GET /api/export/outcomes.csv", d.exportOutcomes)

	srv := &http.Server{
		Addr:              d.Cfg.HTTPAddr,
		Handler:           d.secure(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	slog.Info("api listening", "url", "http://"+d.Cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("api: encode", "err", err)
	}
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// symbolFromQuery resolves ?symbol=&market= to a stored symbol.
func (d Deps) symbolFromQuery(r *http.Request) (md.Symbol, error) {
	sym := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	market := md.Market(r.URL.Query().Get("market"))
	if sym == "" || (market != md.Crypto && market != md.Stocks) {
		return md.Symbol{}, fmt.Errorf("need symbol= and market=crypto|stocks")
	}
	return d.St.GetSymbol(r.Context(), sym, market)
}

// ── basic ───────────────────────────────────────────────────────────────

func (d Deps) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"version":  d.Version,
		"uptimeS":  int(time.Since(d.Started).Seconds()),
		"alpaca":   d.Cfg.HasAlpaca(),
		"time":     time.Now().Unix(),
	})
}

// watchRow is one watchlist/screener entry.
type watchRow struct {
	md.Symbol
	LastClose    float64                 `json:"lastClose"`
	DayChangePct float64                 `json:"dayChangePct"`
	Spark        []float64               `json:"spark"`
	Scores       map[md.Horizon]md.Score `json:"scores"`
	LatestBarTs  int64                   `json:"latestBarTs"`
}

func (d Deps) watchlist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	syms, err := d.St.ListSymbols(ctx, false)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	rows := make([]watchRow, 0, len(syms))
	for _, s := range syms {
		row := watchRow{Symbol: s, Scores: map[md.Horizon]md.Score{}}
		daily, err := d.St.LastBars(ctx, s.ID, md.TF1d, 30)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		for _, b := range daily {
			row.Spark = append(row.Spark, b.Close)
		}
		if n := len(daily); n > 0 {
			row.LastClose = daily[n-1].Close
			row.LatestBarTs = daily[n-1].Ts
			if n > 1 && daily[n-2].Close != 0 {
				row.DayChangePct = (daily[n-1].Close/daily[n-2].Close - 1) * 100
			}
		}
		for _, h := range md.Horizons {
			if sc, ok, err := d.St.LatestScore(ctx, s.ID, h); err == nil && ok {
				sc.Symbol = s.Symbol
				row.Scores[h] = sc
			}
		}
		rows = append(rows, row)
	}
	writeJSON(w, rows)
}

func (d Deps) symbolDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	out := map[string]any{"symbol": s}

	coverage := map[string]any{}
	for _, tf := range []md.Timeframe{md.TF1m, md.TF1h, md.TF1d} {
		n, mn, mx, err := d.St.BarCount(ctx, s.ID, tf)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		coverage[string(tf)] = map[string]int64{"bars": n, "from": mn, "to": mx}
	}
	out["coverage"] = coverage

	scores := map[md.Horizon]md.Score{}
	for _, h := range md.Horizons {
		if sc, ok, err := d.St.LatestScore(ctx, s.ID, h); err == nil && ok {
			scores[h] = sc
		}
	}
	out["scores"] = scores

	states := map[md.Horizon]string{}
	if d.CurrentState != nil {
		if st, err := d.CurrentState(ctx, s.ID); err == nil {
			states = st
		}
	}
	out["stateKeys"] = states

	expect := map[md.Horizon][]md.Expectancy{}
	for _, h := range md.Horizons {
		rows, err := d.St.Expectancy(ctx, s.ID, h)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		expect[h] = rows
	}
	out["expectancy"] = expect

	if ins, err := d.St.RecentInsights(ctx, s.ID, 5); err == nil {
		out["insights"] = ins
	}
	if s.Market == md.Crypto {
		now := time.Now().Unix()
		if snaps, err := d.St.Snaps(ctx, s.ID, now-120, now+1, 120); err == nil && len(snaps) > 0 {
			out["latestSnap"] = snaps[len(snaps)-1]
		}
	}
	writeJSON(w, out)
}

func (d Deps) bars(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	tf := md.Timeframe(r.URL.Query().Get("tf"))
	if tf != md.TF1m && tf != md.TF1h && tf != md.TF1d {
		tf = md.TF1d
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	bars, err := d.St.LastBars(r.Context(), s.ID, tf, limit)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, bars)
}

func (d Deps) scoreHistory(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	now := time.Now().Unix()
	scores, err := d.St.ScoreHistory(r.Context(), s.ID, h, now-int64(days)*86400, now+1)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, scores)
}

func (d Deps) snaps(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	secs, _ := strconv.Atoi(r.URL.Query().Get("seconds"))
	if secs <= 0 || secs > 3600 {
		secs = 300
	}
	now := time.Now().Unix()
	snaps, err := d.St.Snaps(r.Context(), s.ID, now-int64(secs), now+1, secs+1)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, snaps)
}

// ── aggregates ──────────────────────────────────────────────────────────

func (d Deps) trends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	syms, err := d.St.ListSymbols(ctx, false)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	type mover struct {
		Symbol string    `json:"symbol"`
		Market md.Market `json:"market"`
		Score  float64   `json:"score"`
		Change float64   `json:"dayChangePct"`
	}
	var movers []mover
	positive := 0
	scored := 0
	for _, s := range syms {
		sc, ok, err := d.St.LatestScore(ctx, s.ID, md.H1d)
		if err != nil || !ok {
			continue
		}
		scored++
		if sc.Score > 0 {
			positive++
		}
		mv := mover{Symbol: s.Symbol, Market: s.Market, Score: sc.Score}
		if daily, err := d.St.LastBars(ctx, s.ID, md.TF1d, 2); err == nil && len(daily) == 2 && daily[0].Close != 0 {
			mv.Change = (daily[1].Close/daily[0].Close - 1) * 100
		}
		movers = append(movers, mv)
	}
	var marketInsight any
	if ins, err := d.St.RecentInsights(ctx, 0, 20); err == nil {
		for _, in := range ins {
			if in.Scope == "market" {
				marketInsight = in
				break
			}
		}
	}
	writeJSON(w, map[string]any{
		"tracked":       len(syms),
		"scored":        scored,
		"positive1d":    positive,
		"movers":        movers,
		"marketInsight": marketInsight,
	})
}

func (d Deps) honesty(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	outcomes, err := d.St.ResolvedOutcomes(ctx, 0, h, 5000)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// Quintile buckets by score + Pearson correlation (information
	// coefficient) — computed over outcomes with realized returns only.
	var pts []honestyPt
	for _, o := range outcomes {
		if o.FwdReturn != nil {
			pts = append(pts, honestyPt{o.Score, *o.FwdReturn, o.Ts})
		}
	}
	buckets := make([]map[string]any, 0, 5)
	edges := []float64{-1, -0.6, -0.2, 0.2, 0.6, 1.01}
	labels := []string{"strong sell", "sell", "neutral", "buy", "strong buy"}
	for i := 0; i < 5; i++ {
		var sum float64
		var n, hits int
		for _, p := range pts {
			if p.Score >= edges[i] && p.Score < edges[i+1] {
				sum += p.Fwd
				n++
				if p.Fwd > 0 {
					hits++
				}
			}
		}
		b := map[string]any{"label": labels[i], "n": n, "meanFwd": 0.0, "hitRate": 0.0}
		if n > 0 {
			b["meanFwd"] = sum / float64(n)
			b["hitRate"] = float64(hits) / float64(n)
		}
		buckets = append(buckets, b)
	}
	writeJSON(w, map[string]any{
		"horizon": h,
		"n":       len(pts),
		"ic":      pearson(pts),
		"buckets": buckets,
		// pts is newest-first (ResolvedOutcomes orders ts DESC); take the
		// NEWEST 2000 for the scatter, not the oldest, so it tracks fresh data.
		"points": head(pts, 2000),
	})
}

// honestyPt is one (score, realized forward return) pair.
type honestyPt struct {
	Score float64 `json:"score"`
	Fwd   float64 `json:"fwd"`
	Ts    int64   `json:"ts"`
}

func pearson(pts []honestyPt) float64 {
	n := float64(len(pts))
	if n < 3 {
		return 0
	}
	var sx, sy, sxx, syy, sxy float64
	for _, p := range pts {
		sx += p.Score
		sy += p.Fwd
		sxx += p.Score * p.Score
		syy += p.Fwd * p.Fwd
		sxy += p.Score * p.Fwd
	}
	den := (n*sxx - sx*sx) * (n*syy - sy*sy)
	if den <= 0 {
		return 0
	}
	return (n*sxy - sx*sy) / math.Sqrt(den)
}

func head[T any](s []T, n int) []T {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (d Deps) quality(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	syms, err := d.St.ListSymbols(ctx, false)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	type cov struct {
		Symbol   string         `json:"symbol"`
		Market   md.Market      `json:"market"`
		Active   bool           `json:"active"`
		Coverage map[string]any `json:"coverage"`
	}
	out := make([]cov, 0, len(syms))
	for _, s := range syms {
		c := cov{Symbol: s.Symbol, Market: s.Market, Active: s.Active, Coverage: map[string]any{}}
		for _, tf := range []md.Timeframe{md.TF1m, md.TF1h, md.TF1d} {
			n, mn, mx, err := d.St.BarCount(ctx, s.ID, tf)
			if err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			c.Coverage[string(tf)] = map[string]int64{"bars": n, "from": mn, "to": mx}
		}
		out = append(out, c)
	}
	events, err := d.St.RecentDQ(ctx, 100)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"symbols": out, "events": events})
}

func (d Deps) agents(w http.ResponseWriter, r *http.Request) {
	runs, err := d.St.RecentWorkerRuns(r.Context(), 200)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, runs)
}

func (d Deps) hud(w http.ResponseWriter, r *http.Request) {
	payload, fetchedAt, ok, err := d.St.GetHud(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"available": false})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `{"available":true,"fetchedAt":%d,"summary":%s}`, fetchedAt, payload)
}

func (d Deps) insights(w http.ResponseWriter, r *http.Request) {
	var symbolID int64
	if r.URL.Query().Get("symbol") != "" {
		s, err := d.symbolFromQuery(r)
		if err != nil {
			httpErr(w, 404, err.Error())
			return
		}
		symbolID = s.ID
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	ins, err := d.St.RecentInsights(r.Context(), symbolID, limit)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, ins)
}

// ── mutations ───────────────────────────────────────────────────────────

func (d Deps) subscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol string    `json:"symbol"`
		Market md.Market `json:"market"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	body.Symbol = strings.ToUpper(strings.TrimSpace(body.Symbol))
	if body.Symbol == "" || (body.Market != md.Crypto && body.Market != md.Stocks) {
		httpErr(w, 400, "need symbol and market=crypto|stocks")
		return
	}
	if d.Subscribe == nil {
		httpErr(w, 503, "subscribe not wired")
		return
	}
	sym, err := d.Subscribe(r.Context(), body.Symbol, body.Market)
	if err != nil {
		httpErr(w, 422, err.Error())
		return
	}
	writeJSON(w, sym)
}

func (d Deps) unsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol string    `json:"symbol"`
		Market md.Market `json:"market"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	s, err := d.St.GetSymbol(r.Context(), strings.ToUpper(strings.TrimSpace(body.Symbol)), body.Market)
	if err != nil {
		httpErr(w, 404, "unknown symbol")
		return
	}
	if err := d.St.SetSymbolActive(r.Context(), s.ID, false); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.Active = false
	writeJSON(w, s) // history is kept by design; only the live feed stops
}

// ── CSV exports ─────────────────────────────────────────────────────────

func (d Deps) exportBars(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	tf := md.Timeframe(r.URL.Query().Get("tf"))
	if tf != md.TF1m && tf != md.TF1h && tf != md.TF1d {
		tf = md.TF1d
	}
	bars, err := d.St.LastBars(r.Context(), s.ID, tf, 100000)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	csvStart(w, fmt.Sprintf("%s_%s_bars.csv", sanitize(s.Symbol), tf))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"ts", "open", "high", "low", "close", "volume"})
	for _, b := range bars {
		_ = cw.Write([]string{
			strconv.FormatInt(b.Ts, 10), f(b.Open), f(b.High), f(b.Low), f(b.Close), f(b.Volume),
		})
	}
	cw.Flush()
}

func (d Deps) exportScores(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	now := time.Now().Unix()
	scores, err := d.St.ScoreHistory(r.Context(), s.ID, h, now-365*86400, now+1)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	csvStart(w, fmt.Sprintf("%s_%s_scores.csv", sanitize(s.Symbol), h))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"ts", "horizon", "score"})
	for _, sc := range scores {
		_ = cw.Write([]string{strconv.FormatInt(sc.Ts, 10), string(sc.Horizon), f(sc.Score)})
	}
	cw.Flush()
}

func (d Deps) exportOutcomes(w http.ResponseWriter, r *http.Request) {
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	outcomes, err := d.St.ResolvedOutcomes(r.Context(), 0, h, 100000)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	csvStart(w, fmt.Sprintf("outcomes_%s.csv", h))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"symbol_id", "ts", "horizon", "score", "fwd_return"})
	for _, o := range outcomes {
		fwd := ""
		if o.FwdReturn != nil {
			fwd = f(*o.FwdReturn)
		}
		_ = cw.Write([]string{
			strconv.FormatInt(o.SymbolID, 10), strconv.FormatInt(o.Ts, 10),
			string(o.Horizon), f(o.Score), fwd,
		})
	}
	cw.Flush()
}

func csvStart(w http.ResponseWriter, filename string) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
}

func f(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, s)
}
