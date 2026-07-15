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

	"github.com/nyaungnicholas-wq/signaldeck/internal/backup"
	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
)

// Deps wires the API to the rest of the daemon.
type Deps struct {
	St      *store.Store
	Cfg     config.Config
	Version string
	Started time.Time
	LLM     llm.Client // AI provider (may be disabled when no key is set)
	// Subscribe validates a new symbol, upserts it into the STREAMED hot set
	// (stream=1), and kicks off backfill (async). Wired in cmd/signaldeckd.
	Subscribe func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error)
	// Monitor validates + registers a symbol into the broad POLLED universe
	// (stream=0 for stocks — no live-ws slot) and kicks off backfill. Used when
	// the free-ws stream cap is full so a symbol can still be fully monitored
	// (scored/predicted/charted) via polling. nil is safe (falls back to a 503
	// on the monitor path). Wired in cmd/signaldeckd.
	Monitor func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error)
	// CurrentState returns the live expectancy state keys for a symbol.
	CurrentState func(ctx context.Context, symbolID int64) (map[md.Horizon]string, error)
	// Notifier is the Stage-3 remote-delivery notifier (Discord/Telegram/
	// webhook), surfaced read-only via GET /api/notify-status. nil is safe
	// (tests / minimal wiring): every remote transport reads unconfigured.
	Notifier *notify.Notifier
}

// Serve runs the API server until ctx is canceled.
func Serve(ctx context.Context, d Deps) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", d.health)
	d.registerAuth(mux) // register, login, logout, me
	mux.HandleFunc("GET /api/watchlist", d.watchlist)
	mux.HandleFunc("GET /api/symbol", d.symbolDetail)
	mux.HandleFunc("GET /api/bars", d.bars)
	mux.HandleFunc("GET /api/scores/history", d.scoreHistory)
	mux.HandleFunc("GET /api/screener", d.screener) // all symbols; UI filters
	mux.HandleFunc("GET /api/trends", d.trends)
	mux.HandleFunc("GET /api/honesty", d.honesty)
	mux.HandleFunc("GET /api/quality", d.quality)
	mux.HandleFunc("GET /api/agents", d.agents)
	mux.HandleFunc("GET /api/hud", d.hud)
	mux.HandleFunc("GET /api/insights", d.insights)
	mux.HandleFunc("GET /api/snaps", d.snaps)
	mux.HandleFunc("POST /api/subscribe", d.subscribe)
	mux.HandleFunc("POST /api/unsubscribe", d.unsubscribe)
	d.registerQuant(mux)   // forecast, backtest, risk, correlation, portfolio
	d.registerAI(mux)      // analyst, chat, filingmind, debate, status
	d.registerCapstones(mux) // scenario simulation, portfolio optimizer
	d.registerPredict(mux) // predictions, calibration, regime, ranking, breakouts
	d.registerData(mux)    // news, sectors, regime-conditioned, macro
	mux.HandleFunc("GET /api/export/bars.csv", d.exportBars)
	mux.HandleFunc("GET /api/export/scores.csv", d.exportScores)
	mux.HandleFunc("GET /api/export/outcomes.csv", d.exportOutcomes)
	// ── storage-permanence wave (appended — keep new routes at the END of
	// this block so parallel route edits by other agents never collide) ──
	mux.HandleFunc("GET /api/datastats", d.datastats)      // dataset accounting (read, gated like other reads)
	d.registerAlerts(mux)                                  // alerts wave: per-user alerts list + mark-seen
	d.registerDiscovery(mux)                               // discovery wave: candidates list/add/dismiss
	mux.HandleFunc("GET /api/adaptive", d.adaptiveWeights) // learning-flywheel wave: learned per-regime ensemble weights
	mux.HandleFunc("GET /api/universe", d.universe)        // broad-universe wave: streamed-count vs daily-universe-count + caps
	mux.HandleFunc("GET /api/symbol-agent", d.symbolAgent) // per-symbol agents wave: one symbol's own model (tier + personality + skill + active weights)
	d.registerFreeData(mux)                                // free-data wave (Stage 2): FRED macro series + SEC EDGAR fundamentals
	d.registerLedger(mux)                                  // Stage 3: append-only hash-chained prediction ledger (verify + per-symbol list)
	d.registerPaper(mux)                                   // Stage 4: INTERNAL simulated paper-trading book (equity curve + positions + trades + costed summary)
	d.registerSignalBT(mux)                                // Stage 5: OWN-signal backtester (replay the feature store through the ensemble blend; IC/quintiles/turnover/costed equity vs SPY, gated on independent-N)
	d.registerTrackRecord(mux)                             // Stage 7: LIVE OOS track record over resolved calibrated predictions (winrate/Brier/reliability/IC w/ CIs, independent-N gated, links ledger + paper)
	d.registerChartOverlays(mux)                           // Stage 7: per-symbol chart-overlay markers (score extremes, regime changes, breakouts) for the candlestick chart
	d.registerSignal8(mux)                                 // Signal8 wave Stage 1: SEC filings feed + Form 4 insiders + 13F institutions + dilution flags (all reads, honest lag notes)
	d.registerCongress(mux)                                // Signal8 wave Stage 2: congressional trades (STOCK Act disclosures via free mirrors; explicit 30-45d legal-lag note + honest mirror-health status)
	d.registerAnomalies(mux)                               // Signal8 wave Stage 3: anomaly layer — trade imbalance + unusual vol/volume as DESCRIPTIVE z-scores vs each symbol's own baseline (stock imbalance = volume-side proxy, labeled)
	d.registerSignal8Home(mux)                             // Signal8 wave Stage 4: home surfaces — ticker tape (index/sector ETFs + BTC + FRED VIX), movers w/ best-effort EDGAR mcap, honest FRED/EDGAR calendar (earnings = labeled ESTIMATE; IPO omitted — no free source)
	d.registerDashboard(mux)                               // Visual-kit Stage 3: ONE-call GET /api/dashboard (tape + heatmap + gauges w/ honesty captions + movers + merged feed; 60s cache; per-user watchlist sparks only with a session)
	d.registerStage5(mux)                                  // Visual-hub Stage 5: GET /api/predictions/latest — SIGNALS hub predictions table in one batched read (latest calibrated prediction per active symbol; independent-N gate + backtested-not-live label carried in the payload)
	d.registerCompanies(mux)                               // Signal8 wave Stage 5: COMPANIES DIRECTORY (free EDGAR company map joined to our tracked bars/fundamentals; untracked rows honest "—") + GET /api/earnings-est (filing-cadence estimate, labeled — never a confirmed date)
	d.registerNotify(mux)                                  // Stage 3 alert delivery: GET /api/notify-status — which remote transports (Discord/Telegram/webhook) are configured + last delivery/redacted error; macOS listed with an honest "untracked" note; email honestly absent (needs SMTP/provider — future)
	d.registerTVWebhook(mux)                               // TradingView wave: POST /api/tv-webhook (shared-secret inbound Pine alerts) + GET /api/tv-signals (received signals, newest first)
	d.registerTVStatus(mux)                                // TradingView wave: GET /api/tv-status — webhook ops (secret set? public tunnel host + best-effort reachability, received-signal totals, per-streamed-symbol fired counts); never echoes the secret
	d.registerShorts(mux)                                  // Stage 5 FINRA Reg SHO: GET /api/shorts — daily short sale VOLUME ratio (per-symbol series + fleet-wide latest extremes w/ stated min-volume floor); caveat verbatim in every payload: NOT short interest, includes market makers, high ratio NOT directly bearish
	// ── SIGNALS-hub overhaul (appended — keep new routes at the END of this
	// block so parallel route edits by other agents never collide) ──────────
	d.registerStream(mux)         // live-feed wave: SSE push of the newest 1s microstructure snap (GET /api/stream/snaps) so the UI keeps up at 1 Hz+ without REST polling
	d.registerComposite(mux)      // composite SignalScore: GET /api/composite (one symbol's forced-curve 1-10 + factor tiles + additive ledger) + GET /api/composite/top (ranked leaderboard w/ rank-change vs previous day); whole-pass gated below 30 usable predictions — a rank, never a probability
	d.registerTVRating(mux)       // TradingView scanner ratings: GET /api/tv-rating — the LATEST TradingView OWN technical-analysis rating for a tracked symbol (reco_all/ma/other + rsi + close + label), ingested by the tv-rating worker from TradingView's public scanner; EXTERNAL/descriptive/delayed, NOT our model and not advice (caveat verbatim)
	d.registerSelfAudit(mux)      // self-audit / drift watchdog: GET /api/self-audit — latest finding per metric (calibration drift, factor-IC sign flips, prediction bias) measured deterministically from resolved history; every check gated at n>=30 (status "insufficient" below), never a false alarm
	d.registerModelEvolution(mux) // model-evolution: GET /api/model-evolution — trailing-N-day adaptive-weight snapshots (per regime cell + leg) and per-leg factor-IC trend from self_audit, as compact chartable series; honest gaps where no data, no interpolation
	// ── DATA-EXPANSION wave (appended — keep new routes at the END of this
	// block so parallel route edits by other agents never collide) ──────────
	d.registerDataExpansion(mux) // six free external context reads, every payload carrying its caveat verbatim: /api/short-interest (FINRA bi-monthly SI, ~2wks lagged), /api/crypto-perp (Hyperliquid funding/OI — one DEX venue), /api/cot (CFTC weekly positioning, not prediction), /api/stocktwits (retail page-snapshot sentiment), /api/wiki-attention (page views — attention proxy, not a signal), /api/cboe-pc (market-wide put/call — hedging gauge); DESCRIPTIVE context only, nothing here is a scored factor
	// ── NEWS-TRENDS + STRATEGY-LAB wave (appended — keep new routes at the
	// END of this block so parallel route edits by other agents never collide) ─
	d.registerNewsTrends(mux)  // news trends: GET /api/news-trends — per-symbol 30d headline-volume series + own-baseline z (null w/ stated gate reason below 10 active prior days) + fleet top-10 trending headline tokens; caveat verbatim: headline-frequency trend — descriptive attention, not a forecast
	d.registerStrategyLab(mux) // strategy lab: GET /api/strategy-lab[?symbol&market] — 8 classic published strategies replayed through the bias-free next-bar-fill backtester on our own ~2y daily bars with costs (per-row CAGRReported/WinRateMeaningful honesty flags) + fleet aggregates; caveat verbatim: in-sample history, not live performance and not advice
	// ── CROSS-SECTIONAL ALPHA wave (appended — keep new routes at the END of
	// this block so parallel route edits by other agents never collide) ─────
	d.registerAlphaX(mux) // pooled cross-sectional alpha model: GET /api/alphax — per-horizon purged-walk-forward OOS grade + gate state + (only while measured OOS lift > 0) top-20 current symbol scores framed as RELATIVE to the same-day universe median; caveat verbatim: gated off (never blended, never displayed as signal) until measured OOS lift > 0; backtested, not a live track record
	// ── DATA-SOURCE FRESHNESS wave (appended — keep new routes at the END of
	// this block so parallel route edits by other agents never collide) ──────
	mux.HandleFunc("GET /api/source-health", d.sourceHealth) // the one "is anything quietly dead?" dashboard: per EXTERNAL source lastTs/ageSecs/staleBudgetSecs/stale/marketGated/note + overall{staleCount}; market-gated stock sources report "market closed" (not stale) when the exchange is closed; crypto/24-7 sources always checked
	// ── CANDLESTICK-PATTERNS wave (appended — keep new routes at the END of
	// this block so parallel route edits by other agents never collide) ──────
	d.registerCandlePatterns(mux) // GET /api/candle-patterns — candlestick patterns firing over the recent ~200 daily bars (no-pattern bars omitted), each annotated with its MEASURED edge on this symbol's own history where n>=15 (else null); caveat verbatim: patterns are WEAK, context-only signals; measured hit-rate is descriptive, not advice
	d.registerTrendRead(mux)      // GET /api/trend — geometric trend read (uptrend|downtrend|range) + regression slope + fitted support/resistance trendlines + channel flag over the recent daily window; thin history returns a gate reason; caveat verbatim: descriptive read from recent swings, trendlines are fitted, not predictive
	// ── AI RESEARCH DESK wave (appended — keep new routes at the END of this
	// block so parallel route edits by other agents never collide) ───────────
	d.registerDesk(mux) // AI Research Desk: GET /api/world-model (+/shocks +/propagate) = live macro causal graph + shock propagation; GET /api/recommendation (explain-every-rec structured card assembled from real composite+conviction+fundamentals+expectancy, 9 deterministic agent views, reproducible hash-chained audit) + GET /api/recommendation/top (high-conviction opportunities); relative-rank read, heuristic fair value labeled, not advice

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
		"version": d.Version,
		"uptimeS": int(time.Since(d.Started).Seconds()),
		"alpaca":  d.Cfg.HasAlpaca(),
		"time":    time.Now().Unix(),
	})
}

// watchRow is one watchlist/screener entry.
//
// Stage 2 (verdict cards): CalProb1d/NUsed1d/Tier1d/NSamples1d/TierThreshold
// carry the newest REAL calibrated 1d P(up) plus the honest symbol-agent
// evidence tier behind it, filled from ONE batched VerdictStats read per
// response (never per-row). CalProb1d nil = no prediction stored yet — the
// UI renders "NO READ YET", never a fabricated lean; Tier1d "" = no
// symbol-agent row yet (honest static/still-learning).
type watchRow struct {
	md.Symbol
	LastClose     float64                 `json:"lastClose"`
	DayChangePct  float64                 `json:"dayChangePct"`
	Spark         []float64               `json:"spark"`
	Scores        map[md.Horizon]md.Score `json:"scores"`
	LatestBarTs   int64                   `json:"latestBarTs"`
	CalProb1d     *float64                `json:"calProb1d"`
	NUsed1d       int                     `json:"nUsed1d"`
	Tier1d        string                  `json:"tier1d"`
	NSamples1d    int                     `json:"nSamples1d"`
	TierThreshold int                     `json:"tierThreshold"`
}

// watchlist returns the session user's watchlist rows (auth enforced by the
// middleware, so userID is always non-zero here).
func (d Deps) watchlist(w http.ResponseWriter, r *http.Request) {
	syms, err := d.St.ListUserSymbols(r.Context(), userID(r))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	d.writeWatchRows(w, r, syms)
}

// screener returns rows for every tracked symbol (global market data).
func (d Deps) screener(w http.ResponseWriter, r *http.Request) {
	syms, err := d.St.ListSymbols(r.Context(), false)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	d.writeWatchRows(w, r, syms)
}

func (d Deps) writeWatchRows(w http.ResponseWriter, r *http.Request, syms []md.Symbol) {
	ctx := r.Context()
	// Stage 2 (verdict cards): every row's newest calibrated 1d prediction +
	// symbol-agent tier from ONE batched read up front (never per-row).
	ids := make([]int64, len(syms))
	for i, s := range syms {
		ids[i] = s.ID
	}
	verdicts, err := d.St.VerdictStats(ctx, ids, md.H1d)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	rows := make([]watchRow, 0, len(syms))
	for _, s := range syms {
		row := watchRow{Symbol: s, Scores: map[md.Horizon]md.Score{}, TierThreshold: symbolagent.MinPersonal}
		if vs, ok := verdicts[s.ID]; ok {
			row.CalProb1d, row.NUsed1d = vs.CalProb, vs.NUsed
			row.Tier1d, row.NSamples1d = vs.Tier, vs.NSamples
		}
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
	// Raw resolved (score, realized fwd) pairs, newest-first.
	var raw []honestyPt
	for _, o := range outcomes {
		if o.FwdReturn != nil {
			raw = append(raw, honestyPt{o.Score, *o.FwdReturn, o.Ts, o.SymbolID})
		}
	}
	// IC PSEUDO-REPLICATION FIX (honesty doctrine): the minute-cadence scoring
	// pipeline writes MANY score_outcomes per symbol per day that all resolve
	// against the SAME ~1 daily forward move. Pooling them inflates the row
	// count without adding independent information, so any IC/quintile/Brier
	// computed over the raw rows is a pseudo-replicated statistic that
	// overstates confidence. Collapse to ONE observation per (symbol, UTC-day)
	// — keeping the LATEST score that day — before computing any skill number.
	// (Handler is already per-horizon, so the forward-period key is the day.)
	pts := dedupeIndependent(raw)
	rawN := len(raw)
	indepN := len(pts)

	// Quintile buckets by score — computed over the INDEPENDENT set only.
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

	// GATE the IC/skill number behind a minimum independent-N. Below the gate,
	// a correlation off a handful of independent symbol-days is noise, so we
	// return no number and a plain "insufficient independent resolutions" note
	// instead of a figure that would misrepresent skill.
	gated := indepN < minIndependentN
	resp := map[string]any{
		"horizon": h,
		// n stays = the independent count so downstream "resolved" copy is honest.
		"n":               indepN,
		"rawN":            rawN,
		"independentN":    indepN,
		"minIndependentN": minIndependentN,
		"icGated":         gated,
		"buckets":         buckets,
		// pts is newest-first (dedupe preserves order); NEWEST 2000 for scatter.
		"points": head(pts, 2000),
		// Phase 0 labeling: every figure here is backtested / in-sample until
		// live resolutions clear the gate. The frontend badges off these.
		"live":       false,
		"trackLabel": "backtested / in-sample — not a live track record",
	}
	if gated {
		resp["ic"] = nil
		resp["icNote"] = fmt.Sprintf("insufficient independent resolutions (%d/%d)", indepN, minIndependentN)
	} else {
		resp["ic"] = pearson(pts)
	}
	writeJSON(w, resp)
}

// honestyPt is one (score, realized forward return) pair. SymbolID is carried
// (not serialized) so we can collapse to one independent observation per
// (symbol, UTC-day) before computing any skill statistic.
type honestyPt struct {
	Score    float64 `json:"score"`
	Fwd      float64 `json:"fwd"`
	Ts       int64   `json:"ts"`
	SymbolID int64   `json:"-"`
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
	writeJSON(w, map[string]any{"symbols": out, "events": events, "ops": d.backupOps(ctx)})
}

// backupOps surfaces the off-machine backup state (from the meta keys the
// backup worker records) so a silent backup failure — the local file dying with
// no offsite copy — is VISIBLE, never discovered only after a total loss.
// lastBackupTs/lastOffsiteTs are 0 when that step has never succeeded.
func (d Deps) backupOps(ctx context.Context) map[string]any {
	atoi := func(k string) int64 {
		v, _ := d.St.GetMeta(ctx, k)
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	offsiteDir, _ := d.St.GetMeta(ctx, backup.MetaOffsiteDir)
	lastFile, _ := d.St.GetMeta(ctx, backup.MetaLastBackupFile)
	return map[string]any{
		"lastBackupTs":      atoi(backup.MetaLastBackupTs),
		"lastBackupFile":    lastFile,
		"lastOffsiteTs":     atoi(backup.MetaLastOffsiteTs),
		"offsiteConfigured": offsiteDir != "",
		"offsiteDir":        offsiteDir,
	}
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
	// ?kind= filters on the evidence blob's data.kind (e.g. daily_briefing).
	if kind := r.URL.Query().Get("kind"); kind != "" {
		ins, err := d.St.InsightsByKind(r.Context(), kind, limit)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		writeJSON(w, ins)
		return
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
	// Put it on the caller's watchlist (ingestion itself is global).
	if err := d.St.AddUserSymbol(r.Context(), userID(r), sym.ID); err != nil {
		httpErr(w, 500, err.Error())
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
	// Remove from the caller's watchlist; ingestion stays on while ANY other
	// user still watches the symbol.
	if err := d.St.RemoveUserSymbol(r.Context(), userID(r), s.ID); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	watchers, err := d.St.SymbolWatcherCount(r.Context(), s.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if watchers == 0 {
		if err := d.St.SetSymbolActive(r.Context(), s.ID, false); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		s.Active = false
	}
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

// ─────────────────────────────────────────────────────────────────────────────
// PHASE 0 — HONESTY: independent-N de-duplication for the /honesty IC
// (appended; see honesty handler above). The scoring pipeline emits many
// score_outcomes per symbol per day that all resolve against the same forward
// move; pooling them pseudo-replicates the sample. Collapsing to one obs per
// (symbol, UTC-day) yields the effective INDEPENDENT set the IC must be
// computed over, and minIndependentN gates the number below significance.
// ─────────────────────────────────────────────────────────────────────────────

// minIndependentN is the floor of distinct symbol-days below which the honesty
// IC/skill number is withheld (shown as "insufficient independent resolutions").
const minIndependentN = 30

const secondsPerDay = 86400

// dedupeIndependent collapses per-minute resolved pairs to ONE observation per
// (symbol, UTC-day): the LATEST score for that symbol on that day. Input is
// expected newest-first (ResolvedOutcomes orders ts DESC); the output preserves
// that order and keeps the first (newest) row seen for each key. This is the
// effective independent sample for IC/quintile/Brier — computing skill stats on
// the raw minute rows would pseudo-replicate the same daily forward move.
func dedupeIndependent(pts []honestyPt) []honestyPt {
	if len(pts) == 0 {
		return nil
	}
	type key struct {
		sym int64
		day int64
	}
	seen := make(map[key]struct{}, len(pts))
	out := make([]honestyPt, 0, len(pts))
	for _, p := range pts {
		k := key{sym: p.SymbolID, day: p.Ts / secondsPerDay}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, p)
	}
	return out
}
