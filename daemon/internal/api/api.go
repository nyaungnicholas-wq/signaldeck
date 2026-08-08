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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/backup"
	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
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
	// RegistryPath overrides where data/accuracy_registry.json is read from
	// (empty = resolve relative to the working directory, as the daemon does in
	// production). Mirrors ModelHealthWorker.RegistryPath, and exists for the
	// same reason: without it a test reads the LIVE registry.
	RegistryPath string
	LLM          llm.Client // AI provider (may be disabled when no key is set)
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
	mux.HandleFunc("GET /api/ready", d.ready)     // can it serve CORRECT answers, not just answers
	mux.HandleFunc("GET /api/version", d.version) // which code is producing these numbers
	d.registerAuth(mux)                           // register, login, logout, me
	mux.HandleFunc("GET /api/watchlist", d.watchlist)
	mux.HandleFunc("GET /api/symbol", d.symbolDetail)
	mux.HandleFunc("GET /api/bars", d.bars)
	mux.HandleFunc("GET /api/scores/history", d.scoreHistory)
	mux.HandleFunc("GET /api/screener", d.screener) // all symbols; UI filters
	mux.HandleFunc("GET /api/trends", d.trends)
	d.registerHonestyCached(mux) // /api/honesty behind the 60s response cache
	mux.HandleFunc("GET /api/quality", d.quality)
	mux.HandleFunc("GET /api/agents", d.agents)
	mux.HandleFunc("GET /api/hud", d.hud)
	mux.HandleFunc("GET /api/insights", d.insights)
	mux.HandleFunc("GET /api/snaps", d.snaps)
	mux.HandleFunc("POST /api/subscribe", d.subscribe)
	mux.HandleFunc("POST /api/unsubscribe", d.unsubscribe)
	d.registerQuant(mux)     // forecast, backtest, risk, correlation, portfolio
	d.registerAI(mux)        // analyst, chat, filingmind, debate, status
	d.registerCapstones(mux) // scenario simulation, portfolio optimizer
	d.registerPredict(mux)   // predictions, calibration, regime, ranking, breakouts
	d.registerData(mux)      // news, sectors, regime-conditioned, macro
	mux.HandleFunc("GET /api/export/bars.csv", d.exportBars)
	mux.HandleFunc("GET /api/export/scores.csv", d.exportScores)
	mux.HandleFunc("GET /api/export/outcomes.csv", d.exportOutcomes)
	// ── storage-permanence wave (appended — keep new routes at the END of
	// this block so parallel route edits by other agents never collide) ──
	mux.HandleFunc("GET /api/datastats", func(w http.ResponseWriter, r *http.Request) {
		// Perf wave 2026-07-24: measured >30s (timed out); SWR-cached.
		sharedDatastatsSWR.serve("datastats", w, r, d.datastats)
	}) // dataset accounting (read, gated like other reads)
	d.registerAlerts(mux)                                         // alerts wave: per-user alerts list + mark-seen
	d.registerDiscovery(mux)                                      // discovery wave: candidates list/add/dismiss
	mux.HandleFunc("GET /api/adaptive", d.adaptiveWeights)        // learning-flywheel wave: learned per-regime ensemble weights
	mux.HandleFunc("GET /api/postmortems", d.postmortems)         // Research Lab: clustered failure attribution over resolved WRONG predictions
	mux.HandleFunc("GET /api/research", d.research)               // Research Lab: hypothesis registry (shadow/promoted/rejected) + advisory feedback
	mux.HandleFunc("GET /api/research-ledger", d.researchLedger)  // Bayesian Research Ledger: program-level hypotheses w/ prior→posterior evidence chains + meta-analysis
	mux.HandleFunc("GET /api/research-loop", d.researchLoop)      // autonomous research loop: every pass (incl. refusals), judged rules, append-only per-(day,rule) judgments, rejection tally by gate
	mux.HandleFunc("GET /api/vol-regime", d.volRegime)            // the validated-edge forecast: per-stock volatility regime (elevated/calm) + MEASURED walk-forward accuracy tiers
	mux.HandleFunc("GET /api/regimes", d.structuralRegimesCached) // 2026-07-17 alpha-loop winners: trend21/liquidity21/vol21 regimes, measured per-band tiers + caveats in-payload
	mux.HandleFunc("GET /api/signal-report", d.signalReport)      // per-signal detail report: why it fired (raw inputs), walk-forward history on THIS symbol, full signal stack, trade context
	mux.HandleFunc("GET /api/universe", d.universe)               // broad-universe wave: streamed-count vs daily-universe-count + caps
	mux.HandleFunc("GET /api/symbol-agent", d.symbolAgent)        // per-symbol agents wave: one symbol's own model (tier + personality + skill + active weights)
	d.registerFreeData(mux)                                       // free-data wave (Stage 2): FRED macro series + SEC EDGAR fundamentals
	d.registerLedger(mux)                                         // Stage 3: append-only hash-chained prediction ledger (verify + per-symbol list)
	d.registerPaper(mux)                                          // Stage 4: INTERNAL simulated paper-trading book (equity curve + positions + trades + costed summary)
	d.registerSignalBT(mux)                                       // Stage 5: OWN-signal backtester (replay the feature store through the ensemble blend; IC/quintiles/turnover/costed equity vs SPY, gated on independent-N)
	d.registerTrackRecord(mux)                                    // Stage 7: LIVE OOS track record over resolved calibrated predictions (winrate/Brier/reliability/IC w/ CIs, independent-N gated, links ledger + paper)
	d.registerChartOverlays(mux)                                  // Stage 7: per-symbol chart-overlay markers (score extremes, regime changes, breakouts) for the candlestick chart
	d.registerSignal8(mux)                                        // Signal8 wave Stage 1: SEC filings feed + Form 4 insiders + 13F institutions + dilution flags (all reads, honest lag notes)
	d.registerCongress(mux)                                       // Signal8 wave Stage 2: congressional trades (STOCK Act disclosures via free mirrors; explicit 30-45d legal-lag note + honest mirror-health status)
	d.registerAnomalies(mux)                                      // Signal8 wave Stage 3: anomaly layer — trade imbalance + unusual vol/volume as DESCRIPTIVE z-scores vs each symbol's own baseline (stock imbalance = volume-side proxy, labeled)
	d.registerSignal8Home(mux)                                    // Signal8 wave Stage 4: home surfaces — ticker tape (index/sector ETFs + BTC + FRED VIX), movers w/ best-effort EDGAR mcap, honest FRED/EDGAR calendar (earnings = labeled ESTIMATE; IPO omitted — no free source)
	d.registerDashboard(mux)                                      // Visual-kit Stage 3: ONE-call GET /api/dashboard (tape + heatmap + gauges w/ honesty captions + movers + merged feed; 60s cache; per-user watchlist sparks only with a session)
	d.registerStage5(mux)                                         // Visual-hub Stage 5: GET /api/predictions/latest — SIGNALS hub predictions table in one batched read (latest calibrated prediction per active symbol; independent-N gate + backtested-not-live label carried in the payload)
	d.registerCompanies(mux)                                      // Signal8 wave Stage 5: COMPANIES DIRECTORY (free EDGAR company map joined to our tracked bars/fundamentals; untracked rows honest "—") + GET /api/earnings-est (filing-cadence estimate, labeled — never a confirmed date)
	d.registerNotify(mux)                                         // Stage 3 alert delivery: GET /api/notify-status — which remote transports (Discord/Telegram/webhook/Slack/SMTP) are configured + last delivery/redacted error; the LOCAL desktop row names this platform's real channel with an honest "untracked" note
	d.registerTVWebhook(mux)                                      // TradingView wave: POST /api/tv-webhook (shared-secret inbound Pine alerts) + GET /api/tv-signals (received signals, newest first)
	d.registerTVStatus(mux)                                       // TradingView wave: GET /api/tv-status — webhook ops (secret set? public tunnel host + best-effort reachability, received-signal totals, per-streamed-symbol fired counts); never echoes the secret
	d.registerShorts(mux)                                         // Stage 5 FINRA Reg SHO: GET /api/shorts — daily short sale VOLUME ratio (per-symbol series + fleet-wide latest extremes w/ stated min-volume floor); caveat verbatim in every payload: NOT short interest, includes market makers, high ratio NOT directly bearish
	// ── SIGNALS-hub overhaul (appended — keep new routes at the END of this
	// block so parallel route edits by other agents never collide) ──────────
	d.registerStream(mux)         // live-feed wave: SSE push of the newest 1s microstructure snap (GET /api/stream/snaps) so the UI keeps up at 1 Hz+ without REST polling
	d.registerComposite(mux)      // composite SignalScore: GET /api/composite (one symbol's forced-curve 1-10 + factor tiles + additive ledger) + GET /api/composite/top (ranked leaderboard w/ rank-change vs previous day); whole-pass gated below 30 usable predictions — a rank, never a probability
	d.registerTVRating(mux)       // TradingView scanner ratings: GET /api/tv-rating — the LATEST TradingView OWN technical-analysis rating for a tracked symbol (reco_all/ma/other + rsi + close + label), ingested by the tv-rating worker from TradingView's public scanner; EXTERNAL/descriptive/delayed, NOT our model and not advice (caveat verbatim)
	d.registerSelfAudit(mux)      // self-audit / drift watchdog: GET /api/self-audit — latest finding per metric (calibration drift, factor-IC sign flips, prediction bias) measured deterministically from resolved history; every check gated at n>=30 (status "insufficient" below), never a false alarm
	d.registerModelEvolution(mux) // model-evolution: GET /api/model-evolution — trailing-N-day adaptive-weight snapshots (per regime cell + leg) and per-leg factor-IC trend from self_audit, as compact chartable series; honest gaps where no data, no interpolation
	d.registerFleetHealth(mux)    // one assembled platform-health read: the simulated book's realized performance (Sortino/profit factor/current-vs-max drawdown from FIFO round trips), each model's stored grade, worker cadence + data freshness, and architectural layer coverage; every unmeasurable metric null and named in `withheld`, an empty fleet reports "unknown" and never "healthy"
	d.registerFeatureHealth(mux)  // per-INPUT scorecard + the retire set the GBM trainer honors: decay (recent vs full-record IC), sign stability across blocks, coverage, redundancy; a feature below the evidence floor is KEPT, and a wholesale retirement is reported but NOT applied
	d.registerConfidence(mux)     // GET /api/confidence?symbol&horizon — one gated object per prediction: probability + state-conditional expected return + measured adverse excursion (expected drawdown, labeled unconditional) + confidence and Wilson uncertainty, each null with a stated reason when unmeasurable
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
	// ── SMART MONEY FACTS wave (appended — keep new routes at the END of this
	// block so parallel route edits by other agents never collide) ───────────
	d.registerSmartMoney(mux) // GET /api/smart-money[?symbol&market] (one symbol's decomposed Smart Money Score — insider/squeeze/institutional factors with lines+sources) + GET /api/smart-money/top[?market&limit] (accumulation leaderboard w/ top factor); a read of what informed participants are DOING from public filings, NOT a forecast — caveat verbatim in every payload
	// ── CONFLUENCE GATE + MONEY SCOREBOARD wave (appended — keep new routes at
	// the END of this block so parallel route edits by other agents never collide) ──
	d.registerConfluence(mux)  // GET /api/confluence[?symbol&market] (one symbol's transparent confluence — every INDEPENDENT family's vote+reason, agree/dissent/score, isSetup) + GET /api/confluence/top[?market&limit&onlySetups] (leaderboard of current setups) + GET /api/confluence/track (accruing MONEY scoreboard over FORWARD-tracked resolved setups — expectancy/profit-factor, independent-N gated); no manufactured edge, no lookahead, scored by EXPECTED PROFIT not win rate — caveat verbatim
	d.registerAttribution(mux) // attribution-engine wave: GET /api/attribution[?symbol&market&horizon] — BLENDED-EVIDENCE report fusing a regime/state-conditioned HISTORICAL prior (~2y expectancy) with LIVE resolved outcomes, kept strictly separate + sample-size-weighted; reports both Ns, regime-match quality, calibrated prob + Wilson band, and whether attribution is supported or underpowered (thin live volume != no edge)
	// ── RESEARCH DISCOVERY ENGINE wave (appended — keep new routes at the END
	// of this block so parallel route edits by other agents never collide) ──
	mux.HandleFunc("GET /api/research-graph", d.researchGraph)
	d.registerLineage(mux) // Lineage spine (Layers 2+8): GET /api/lineage?kind=&id=&depth= — connected subgraph around any node (hypothesis/prediction/model/feature/trade/claim/dataset/experiment), edges stamped with the writing build's git rev // the evidence graph: every ledger hypothesis linked to its family, attacks, graded eras, and evidence kinds — why each belief stands. /api/research-ledger (above) now also carries research_weeks coverage, evidence-kind counts, live-vs-backtest split, and per-hypothesis decay
	// ── CREDIBILITY wave (appended — keep new routes at the END of this
	// block so parallel route edits by other agents never collide) ──────────
	d.registerRegimePostmortems(mux) // GET /api/regime-postmortems — latest ≤50 plain-English postmortems for HIGH-conviction regime calls that resolved WRONG (what was called, what realized + the key number, base rate computed from the CLAIMED accuracy); live regime grading itself ships inside /api/track-record's "regimes" section
	d.registerModelHealth(mux)       // every tracked model: claimed vs live, and whether it may still emit
	d.registerEvidence(mux)          // GET /api/evidence[/{id}] — the Evidence Engine: every published claim with its machine-checkable evidence items (cluster-robust n_effective, method, correction, CI vs its own null), a rule-justified tier, and an expiry the nightly sweep enforces (stale = -1 tier; refuting evidence = retired)
	d.registerSentCorr(mux)          // GET /api/sentiment-correlation — the platform's FIRST non-price signal test: does news-text sentiment predict forward returns once same-session and trailing price moves are controlled for? Leads with the PARTIAL IC + month-clustered bootstrap CI, ships the raw IC only to expose how much was price contamination; gated on obs/symbols/era coverage (nulls, never zeros); EXPERIMENTAL — wired into nothing
	d.registerExplain(mux)           // auditable regime forecast: contributors + weights + historical analog
	d.registerEarningsWindow(mux)    // GET /api/earnings-window?symbol&market — estimated next earnings from the filing-cadence heuristic (last 10-Q/10-K + ~91d), HONEST NULLS when unknown; the same math annotates /api/regimes + /api/signal-report with earningsWindow labels (forecasts never suppressed — labeled only)
	// ── WAVE 2 (crypto kinds + precompute + digest + AD live + survivorship;
	// appended — keep new routes at the END of this block so parallel route
	// edits by other agents never collide) ───────────────────────────────────
	d.registerDigest(mux) // GET /api/digest — the latest weekly digest text the weekly-digest worker composed (regime changes / live regime record / top calls / paper P&L), with generatedAt + sentAt (0 = composed but never delivered: no notify transport configured); available:false before the first Sunday-17:00-ET gate fires
	// ── CROSS-SECTIONAL FACTOR wave (appended — keep new routes at the END of
	// this block so parallel route edits by other agents never collide) ──────
	// ── HONESTY-GAP wave (appended — keep new routes at the END of this block
	// so parallel route edits by other agents never collide) ─────────────────
	d.registerHonestyGaps(mux) // the five gaps PREDICTION_PROCESS.md left open: GET /api/return-forecast (cost-aware conditional return DISTRIBUTION replacing the binary up/down target — pUp/pDown clear ±tau, pInside is the no-trade zone, expectedValue decides, pinball skill vs climatology), /api/feature-redundancy (fieldCount vs EFFECTIVE independent inputs + clusters), /api/canary (a new model version must beat the incumbent AND the naive baseline on its own interval before it may serve), /api/dataset-versions (which slices a provider REWROTE, so claims measured on them can be re-graded), /api/price-validation (opt-in second-source close comparison; convention differences labeled separately from corruption)
	// ── OPTIONS wave (appended — keep new routes at the END of this block so
	// parallel route edits by other agents never collide) ────────────────────
	d.registerOptions(mux) // GET /api/options/price (Black-Scholes-Merton value + Greeks + implied-vol inversion; refuses a vol for quotes with no vega rather than inventing one) + GET /api/options/vol-edge?symbol&market&iv= (the VALIDATED vol-regime forecast turned into a vol LEVEL from this symbol's own walk-forward history, compared against a market implied vol the USER supplies — there is no options feed here); every verdict ships its assumed variance risk premium, the premium at which it flips, and whether it survives the regime call being wrong
	// ── PAIRS wave (appended — keep new routes at the END of this block so
	// parallel route edits by other agents never collide) ────────────────────
	d.registerAccuracy(mux) // GET /api/accuracy — the registry with an explicit publication_status per row, reconciled against evidence_claims and the retirement history through publication.BuildVerdict (the one copy of those rules). Fail-closed: 503 REFUSED when the registry is unreadable or the grader marked itself refusing, 503 REFUSED_STALE when no successful grade landed inside GraderMaxAge. Referenced by three audits and never implemented until 2026-08-04; until then the path 404d while prose described its behaviour
	d.registerPrereg(mux)   // GET /api/prereg — what each structural predictor CLAIMED, frozen + hash-chained BEFORE its forecasts began resolving (first gradable 2026-08-07). Makes the advertised accuracy tables falsifiable: after the live record arrives the comparison is against a dated, hashed commitment rather than against whatever the code says at that time; the chain turns a later edit into a detectable break instead of a matter of trust, and amendments are appended, never applied in place
	d.registerStress(mux)   // Layer-4 stress lab: GET /api/stress/scenarios (composable effect-vector catalog) + POST /api/stress/run (auth-required, capped compute — joint scenarios + regime-conditional block bootstrap replayed through the REAL decide→riskgate→papertrade path; reports system behavior, never a PnL claim)

	d.registerMarketRegimes(mux) // GET /api/market-regimes — the SAME structural trend/vol/liquidity calls, grouped for the index and sector baskets (SPY/QQQ/IWM/DIA + all 11 SPDR sectors) instead of buried among ~885 single names; ships sector BREADTH per kind (one elevated sector is noise, eleven of eleven is a market state), names any basket with no call rather than letting absence read as neutral, and states that the accuracy tiers are INHERITED from the stock-universe validation and were never re-measured on baskets
	d.registerPairsStudy(mux)    // GET /api/pairs-study — the cointegration pairs-trading test that resolved ledger hypothesis H018 (CORR63) DO-NOT-SHIP: a frozen walk-forward backtest (252d formation -> 63d traded, 26 non-overlapping blocks, 918 SIC-sectored symbols, frozen hedge ratio + spread z, Engle-Granger critical values, block bootstrap, cost sweep) whose selected arm is INDISTINGUISHABLE from random same-sector pairs. Published because the mechanism is the finding — correlation rank persists (rho +0.73) while cointegration rank does not (rho -0.004), so the persistent quantity is shared market beta that no dollar-neutral spread can monetize
	d.registerXSFactor(mux)      // GET /api/xs-factor?horizon=5d|21d|63d&limit= — CROSS-SECTIONAL factor ranking (low-vol + size/liquidity + mom12-1, point-in-time from trailing bars, percentiles over the active universe, composite renormalized over PRESENT legs) with the MEASURED per-leg edge/CIs shipped as data; read-time only (no table, no worker), SWR-cached; caveat verbatim: relative rank vs the same-day universe MEDIAN, absolute direction failed 20/20, these are PUBLIC capacity-constrained factors with an implied IC of only ~0.03-0.07
	d.registerMetaLabel(mux)     // GET /api/metalabel — does FILTERING the platform's own directional calls earn its place? A secondary model trained on "did the primary's call clear cost" decides take-or-skip; graded on EXPECTANCY per decision OFFERED (never precision — a filter that is more often right while making less money is the trend21 trap) with three gates applied in order: the primary must have cost-net edge at all, the filter's trades must span enough DISTINCT DAYS to be independent, and expectancy must actually improve. First live grade: +17.7pp precision and still REJECTED, because the primary loses 0.29%/decision. Measurement only — never sizes a trade
	// ── PREDICTION-ATTRIBUTION wave (appended — keep new routes at the END
	// of this block so parallel route edits by other agents never collide) ──
	d.registerPredAttribution(mux) // Layer 6: GET /api/attribution/prediction?symbol&market&horizon[&seq] — the persisted top-8 named parts (comp_* components / gbm features / legs) of one LEDGERED prediction's raw blend, probability deltas from the 0.5 prior; method note (Saabas, not exact SHAP) shipped verbatim in-payload
	// ── DECISION ENGINE wave (appended — keep new routes at the END of this
	// block so parallel route edits by other agents never collide) ──────────
	d.registerEVDecisions(mux) // Layer 1: GET /api/ev/decisions[?decision&symbol&limit] — the EV gate's ledger over the SIMULATED paper book: every BUY/SELL and every DO_NOTHING refusal with its enumerated reason, net EV (null = unmeasurable, the gate refused rather than defaulted), net-EV rank within the pass (the opportunity-cost input), and the full has-flagged inputs snapshot; makes "what did refusing cost" a query instead of a shrug

	// ── MCP wave (appended) ─────────────────────────────────────────────────
	// POST /mcp — the Model Context Protocol server (internal/mcp): advisory
	// methodology exposition and today's BANDED structural verdicts for an AI
	// client, behind six independently-tested defense layers. Off unless
	// SIGNALDECK_MCP_ENABLED is set; the limiter instance is shared with the
	// rest of the API so a client cannot get two budgets by using two doors.
	limiter := newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst)
	d.registerMCP(mux, limiter)

	srv := d.httpServerWith(mux, limiter)
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

// Connection deadlines. The 2026-07-26 review held a connection open for 60s
// having sent 15 bytes of body, because ReadHeaderTimeout was the only limit
// set.
const (
	// requestReadTimeout bounds reading the request BODY after the headers
	// land — the slow-body case.
	requestReadTimeout = 20 * time.Second
	// responseWriteTimeout bounds writing a response to a client that reads
	// slowly. It is generous because the slowest cold cache builds measured
	// 22-45s; the point is a bound, not a tight one.
	responseWriteTimeout = 90 * time.Second
	// idleTimeout reaps keep-alive connections between requests.
	idleTimeout = 120 * time.Second
)

// httpServer builds the configured server. Deadlines are deliberately split
// between the Server and withDeadlines:
//
//   - ReadHeaderTimeout is server-wide and safe: headers arrive before any
//     handler runs, streaming included.
//   - ReadTimeout is NOT set server-wide. Go arms it for the whole request and
//     the background read that detects a closed connection then trips it, which
//     would tear down every SSE stream on a timer. withDeadlines applies it
//     per-request instead, to the routes that actually have a body to read.
//   - WriteTimeout is NOT set server-wide for the same reason in the other
//     direction: a live SSE subscriber legitimately writes for hours, and a
//     blanket write deadline kills it mid-stream. withDeadlines applies the
//     write deadline to every non-streaming route, so the JSON and CSV paths
//     are bounded and only the stream is exempt.
//   - IdleTimeout is server-wide: it only covers the gap BETWEEN requests, so
//     an in-flight stream is never affected.
func (d Deps) httpServer(mux http.Handler) *http.Server {
	return d.httpServerWith(mux, newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst))
}

// httpServerWith is httpServer with a caller-supplied limiter, so the MCP
// mount and the HTTP middleware share one bucket.
func (d Deps) httpServerWith(mux http.Handler, limiter *rateLimiter) *http.Server {
	return &http.Server{
		Addr:              d.Cfg.HTTPAddr,
		Handler:           withDeadlines(d.secureWith(mux, limiter)),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       idleTimeout,
	}
}

// streamPath reports whether a path is a long-lived streaming response, which
// must not carry a write deadline.
func streamPath(path string) bool { return strings.HasPrefix(path, "/api/stream/") }

// withDeadlines applies per-request read/write deadlines to every route except
// the streaming ones. See httpServer for why this is not a Server-wide setting.
func withDeadlines(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !streamPath(r.URL.Path) {
			rc := http.NewResponseController(w)
			// Both may fail on a wrapped ResponseWriter that does not expose
			// the connection (httptest, TimeoutHandler). A missing deadline is
			// the pre-existing behaviour, so there is nothing to report.
			_ = rc.SetReadDeadline(time.Now().Add(requestReadTimeout))
			_ = rc.SetWriteDeadline(time.Now().Add(responseWriteTimeout))
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("api: encode", "err", err)
	}
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// httpInternal is the 500 path: log the real error, return an opaque one.
//
// 190 handlers used to answer `httpInternal(w, err)`. On this codebase
// that string is a modernc.org/sqlite message, and it carries the absolute
// database path, the failing table, and often the statement — handed to whoever
// asked, on endpoints that are anonymously readable whenever PublicReads is on.
// The caller cannot act on any of it; only the operator can, and the operator
// reads the log.
//
// Deliberately NOT a wrapper that takes a message: a per-site string is how the
// leak came back last time. There is one 500 body and it says nothing. Use
// httpErr directly for 4xx, where the text is the point — a client CAN act on
// "need symbol= and market=crypto|stocks".
func httpInternal(w http.ResponseWriter, err error) {
	slog.Error("request failed", "err", err)
	httpErr(w, http.StatusInternalServerError, "internal error")
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
	// schemaContract: workers the daemon REFUSED to register at boot because
	// the database lacks a table/column they read or write. Non-empty means
	// those workers are not running AND were never going to record anything —
	// surfaced here so the absence is not mistaken for a healthy quiet fleet.
	refusals := map[string][]string{}
	if raw, err := d.St.GetMeta(r.Context(), store.SchemaContractMetaKey); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &refusals)
	}
	// Worker fleet. Without this, health was a liveness probe wearing a health
	// probe's name: it answered 200 with {"alpaca":true,...} while crypto-live
	// had failed 43 times that day (tickstream down) and hud-sync 26 (trader-hud
	// down). Any monitor pointed here reported 100% uptime through both. A
	// health endpoint that cannot go unhealthy is decoration.
	failing, werr := d.failingWorkers(r.Context())

	// Off-machine alert delivery. A local Windows toast is built in and always
	// available, but it only reaches someone sitting at this desk — and the
	// warning that nothing else was configured appeared ONLY in
	// logs/backup-offline.log, which is exactly where an unread warning goes to
	// die. It read, correctly, every night since the migration:
	//
	//   NOTIFY: SignalDeck: alerts are SILENT beyond this machine …
	//   daemon alerts stay local and go unseen when nobody is at the keyboard.
	//
	// Reporting it here makes the gap self-announcing: any monitor, the
	// dashboard, and `signaldeck status` all see it without reading a log.
	// It counts toward `degraded` because an alerting system that cannot reach
	// you is a real degradation, not a preference.
	remoteAlerts := d.Notifier.Enabled() // nil-receiver safe

	// An anonymous caller gets the SIGNAL, not the internals. This endpoint has
	// to stay reachable without a credential (see requiresAuth), so on a
	// tunnel-exposed daemon everything here is world-readable — and the fleet
	// detail added for observability is exactly the wrong thing to publish:
	// worker names map the internal architecture and the revision names the
	// exact source a reader can go and audit for holes. `degraded` alone is
	// enough for a monitor to alert on, and an operator who signs in sees why.
	if userID(r) == 0 {
		writeJSON(w, map[string]any{
			"degraded": len(failing) > 0 || len(refusals) > 0 || werr != nil || !remoteAlerts,
			"time":     time.Now().Unix(),
			// openSignup STAYS in the anonymous payload: the login page reads it
			// before anyone has a credential, to decide whether to offer
			// registration at all. Withholding it would hide the button on a
			// deployment where signup is genuinely open. It discloses nothing —
			// POSTing to /api/auth/register reveals the same thing.
			"openSignup": d.Cfg.OpenSignup,
			"detail":     "sign in or send the API token for the full breakdown",
		})
		return
	}

	body := map[string]any{
		"version": d.Version,
		// The human version string is a compile-time constant ("0.1.0-dev") and
		// says nothing about WHICH build is running. The deploy already stamps
		// the commit via -ldflags, so report it here too: "0.1.0-dev" alone on a
		// shipped binary is not an identity anyone can act on.
		"revision":        lineage.RevisionStamp(),
		"uptimeS":         int(time.Since(d.Started).Seconds()),
		"alpaca":          d.Cfg.HasAlpaca(),
		"time":            time.Now().Unix(),
		"schemaContract":  refusals,
		"workers":         failing,
		"remoteAlerts":    remoteAlerts,
		"alertTransports": d.Notifier.ConfiguredNames(),
		"degraded":        len(failing) > 0 || len(refusals) > 0 || werr != nil || !remoteAlerts,
		// So the login page can stop advertising "no account? register →" on a
		// deployment where registration is closed. Not a disclosure: anyone can
		// learn the same thing by POSTing to /api/auth/register and reading the
		// 403. Health is the right home because it is the one endpoint that is
		// reachable before you have any credential.
		"openSignup": d.Cfg.OpenSignup,
	}
	if werr != nil {
		// Not being able to READ fleet state is itself unhealthy — say so rather
		// than omitting the key and reading as "nothing wrong".
		body["workersError"] = "fleet state unreadable"
		slog.Error("health: worker fleet unreadable", "err", werr)
	}
	writeJSON(w, body)
}

// failingWorkers maps worker name → the status of its most recent COMPLETED
// run, for every worker whose latest run did not succeed.
//
// In-flight runs ("running") are skipped rather than treated as healthy: a
// worker that is currently retrying should still report the failure it is
// retrying from, or a fast-cycling worker would show green in the gap between
// its error and its next error. "orphaned" (a run whose process died) counts as
// failing for the same reason.
func (d Deps) failingWorkers(ctx context.Context) (map[string]string, error) {
	// worker_runs is pruned to a bounded size (store.PruneWorkerRuns keeps the
	// newest 20 PER worker), so a few hundred rows reliably covers one run of
	// every worker including the rare-cadence ones.
	runs, err := d.St.RecentWorkerRuns(ctx, 400)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := map[string]string{}
	for _, run := range runs { // newest first
		if run.Status == "running" || seen[run.Worker] {
			continue
		}
		seen[run.Worker] = true
		if run.Status != "ok" {
			out[run.Worker] = run.Status
		}
	}
	return out, nil
}

// ready reports whether the daemon can serve CORRECT answers, which is a
// different question from health's "the process is up". A daemon that is
// listening but whose store is unreachable, or that refused workers at boot
// because the schema was missing their tables, will answer requests — it will
// just answer them wrong or empty. 503 so a load balancer or deploy script can
// tell the two apart.
func (d Deps) ready(w http.ResponseWriter, r *http.Request) {
	reasons := []string{}

	// Store reachable? Any read that touches the DB will do.
	refusals := map[string][]string{}
	raw, err := d.St.GetMeta(r.Context(), store.SchemaContractMetaKey)
	if err != nil {
		reasons = append(reasons, "store unreachable: "+err.Error())
	} else if raw != "" {
		_ = json.Unmarshal([]byte(raw), &refusals)
	}

	// Workers refused at boot never ran and never will this process lifetime.
	for worker := range refusals {
		reasons = append(reasons, "worker refused at boot (schema): "+worker)
	}

	if !d.Cfg.HasAlpaca() {
		reasons = append(reasons, "no Alpaca credentials: equity ingestion is inert")
	}

	// A missing alert transport is deliberately NOT a readiness reason. This
	// endpoint answers "can the daemon serve CORRECT answers", and unreachable
	// alerting does not make an answer wrong — it means nobody is told when one
	// goes wrong. That is a health/`degraded` concern, and it is reported there.
	// Putting it here 503'd TestReadyWhenEverythingIsWired and would have made
	// the deploy script's readiness wait fail on a notification preference.

	// A worker whose latest run failed is not writing the rows its surfaces
	// read, so those surfaces answer from stale data — which is exactly the
	// "listening but answering wrong" state this endpoint exists to separate
	// from "up". Sorted so the reason list is stable across polls.
	if failing, err := d.failingWorkers(r.Context()); err != nil {
		reasons = append(reasons, "worker fleet state unreadable")
	} else {
		names := make([]string, 0, len(failing))
		for name := range failing {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			reasons = append(reasons, "worker not delivering ("+failing[name]+"): "+name)
		}
	}

	if len(reasons) > 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		// The STATUS CODE is the probe's answer and it is the same either way —
		// a load balancer acts on 503, not on the prose. The reasons name
		// workers, schema gaps and missing credentials, so they go only to a
		// caller who has identified themselves.
		if userID(r) == 0 {
			writeJSON(w, map[string]any{
				"ready":  false,
				"detail": "sign in or send the API token for the reasons",
			})
			return
		}
		writeJSON(w, map[string]any{"ready": false, "reasons": reasons})
		return
	}
	writeJSON(w, map[string]any{"ready": true})
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
		httpInternal(w, err)
		return
	}
	d.writeWatchRows(w, r, syms)
}

// screener returns rows for every ACTIVE symbol (global market data).
// active-only (2026-07-19): the universe prune deactivates illiquid symbols
// but leaves their rows in `symbols` — listing all 1,070 made this handler
// run ~4,300 sequential per-row queries (30s+) and showed frozen, stale
// prices for the ~750 deactivated names. Active-only is both the fast and
// the honest surface; deactivated symbols stay reachable via /intel search.
func (d Deps) screener(w http.ResponseWriter, r *http.Request) {
	// Global market data → served from a short-TTL cache (screenerCache). The
	// build runs on a detached context so a client disconnect can't abort the
	// shared rebuild that other waiters depend on.
	rows, err := screenerCacheG.get(func() ([]watchRow, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		syms, err := d.St.ListSymbols(ctx, true)
		if err != nil {
			return nil, err
		}
		return d.buildWatchRows(ctx, syms)
	})
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, rows)
}

func (d Deps) writeWatchRows(w http.ResponseWriter, r *http.Request, syms []md.Symbol) {
	rows, err := d.buildWatchRows(r.Context(), syms)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, rows)
}

// buildWatchRows assembles verdict/spark/score rows for a symbol set with three
// batched reads (VerdictStats + LastBarsBatch + LatestScoresBatch) — no
// per-symbol fan-out. Shared by the per-user watchlist and the global screener.
func (d Deps) buildWatchRows(ctx context.Context, syms []md.Symbol) ([]watchRow, error) {
	// Stage 2 (verdict cards): every row's newest calibrated 1d prediction +
	// symbol-agent tier from ONE batched read up front (never per-row).
	ids := make([]int64, len(syms))
	for i, s := range syms {
		ids[i] = s.ID
	}
	verdicts, err := d.St.VerdictStats(ctx, ids, md.H1d)
	if err != nil {
		return nil, err
	}
	// Batched reads (2026-07-20): last-30 daily bars + latest score per horizon
	// for the WHOLE set in one query each. Replaces a 4×N per-symbol fan-out
	// (~1,300 sequential queries) that made the 321-symbol screener take 20-30s
	// cold; now three queries total (these two + VerdictStats above).
	barsBy, err := d.St.LastBarsBatch(ctx, ids, md.TF1d, 30)
	if err != nil {
		return nil, err
	}
	scoresBy, err := d.St.LatestScoresBatch(ctx, ids)
	if err != nil {
		return nil, err
	}
	rows := make([]watchRow, 0, len(syms))
	for _, s := range syms {
		row := watchRow{Symbol: s, Scores: map[md.Horizon]md.Score{}, TierThreshold: symbolagent.MinPersonal}
		if vs, ok := verdicts[s.ID]; ok {
			row.CalProb1d, row.NUsed1d = vs.CalProb, vs.NUsed
			row.Tier1d, row.NSamples1d = vs.Tier, vs.NSamples
		}
		daily := barsBy[s.ID]
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
		if sm, ok := scoresBy[s.ID]; ok {
			for _, h := range md.Horizons {
				if sc, ok := sm[h]; ok {
					sc.Symbol = s.Symbol
					row.Scores[h] = sc
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
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
			httpInternal(w, err)
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
			httpInternal(w, err)
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
	// REDISTRIBUTION GUARD (2026-07-25). Raw bars are licensed vendor data;
	// serving them to a third party is redistribution, which every price feed
	// in use prohibits. Consuming them privately on loopback is fine, so the
	// guard trips only where the daemon is actually reachable by someone else.
	// Derived analytics (forecasts, regimes, risk) are unaffected — the point
	// is the raw records, not the insight computed from them.
	// The test is the REQUEST's origin, not a config flag. The first version of
	// this guard keyed off PublicReads and was inverted: PublicReads=true means
	// MORE open, so the guard only fired on locked-down deployments and stood
	// down on exposed ones — precisely backwards. It also missed the real
	// exposure surface, which is the web proxy's bind address rather than the
	// daemon's: the daemon can sit on loopback while the Next.js app in front of
	// it listens on every interface and forwards.
	//
	// Serving licensed bars to the machine they were downloaded on is personal
	// use. Serving them to anyone else is redistribution, whatever the config
	// says, so a non-loopback caller is refused unless the operator has
	// explicitly asserted the right.
	if d.rawDataRefused(w, r) {
		return
	}
	bars, err := d.St.LastBars(r.Context(), s.ID, tf, limit)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, bars)
}

// rawDataRefused is THE redistribution guard — the one /api/bars uses and the
// one every raw-row export must call. It writes the 451 + actionable notice and
// reports true when the caller must be refused.
//
// It exists as a function rather than as a block copied per handler because the
// 2026-07-26 review found the copy missing entirely from the CSV exports: the
// same licensed rows walked out through a second door with no policy on it.
// Two copies of a legal rule drift; one cannot.
func (d Deps) rawDataRefused(w http.ResponseWriter, r *http.Request) bool {
	if !d.Cfg.AllowRawExport && !requestIsLoopback(r) && !datalicense.BarsRedistributable() {
		httpErr(w, 451, datalicense.RawDataNotice())
		return true
	}
	return false
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
		httpInternal(w, err)
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
		httpInternal(w, err)
		return
	}
	writeJSON(w, snaps)
}

// ── aggregates ──────────────────────────────────────────────────────────

func (d Deps) trends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	syms, err := d.St.ListSymbols(ctx, false)
	if err != nil {
		httpInternal(w, err)
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
		httpInternal(w, err)
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
	// A-2 (2026-08-02 re-audit): this payload published an IC over 880
	// symbol-days spanning FIVE market days with no day-clustering treatment at
	// all — no distinct-day count, no interval. Deduplicating to one row per
	// (symbol, UTC-day) removes intraday pseudo-replication and leaves the
	// larger problem untouched: on any given day every symbol shares one market
	// move. /api/track-record already corrects for exactly this; the machinery
	// was simply never called here.
	//
	// The IC is a correlation, not a proportion, so clusterstat.DesignEffect —
	// which is defined on proportions — would be the wrong instrument, and
	// publishing one anyway is the "looks corrected" failure the package warns
	// about. BootstrapStat exists for this case: it resamples WHOLE DAYS and
	// recomputes the statistic, so the interval resamples the same unit as
	// every other surface.
	//
	// What is corrected is the INTERVAL, never the point estimate. Below the
	// day floor the interval is withheld (null) with the reason attached — a
	// withheld interval beats a narrow one.
	byDay := map[int64][]int{}
	var days []int64
	for i, p := range pts {
		d := md.TradingDay(p.Ts)
		if _, seen := byDay[d]; !seen {
			days = append(days, d)
		}
		byDay[d] = append(byDay[d], i)
	}
	resp["distinctDays"] = len(days)
	resp["minDistinctDays"] = clusterstat.MinDistinctDays
	resp["clusterNote"] = fmt.Sprintf(
		"%d independent symbol-days span only %d market days, and every symbol on one day shares that day's move. "+
			"icCI is a percentile interval from resampling WHOLE DAYS with replacement (clusterstat.BootstrapStat), "+
			"which is the correlation analogue of the design-effect correction /api/track-record applies to its "+
			"proportion — a row-resampled interval would reproduce the too-narrow figure by a different route. "+
			"The clustering governs the INTERVAL, not the point estimate: ic itself does not move.",
		indepN, len(days))
	// The floor is enforced HERE, deliberately: BootstrapStat is the low-level
	// resampler and accepts any numDays >= 2, while BootstrapDays is the one
	// that refuses below clusterstat.MinDistinctDays. Calling the resampler
	// directly means inheriting that refusal explicitly — five days will
	// happily produce a tight-looking interval off five market moves.
	enoughDays := len(days) >= clusterstat.MinDistinctDays
	if ci, ok := clusterstat.BootstrapStat(len(days), 2000, 0.05, func(idx []int) (float64, bool) {
		sample := make([]honestyPt, 0, len(pts))
		for _, di := range idx {
			for _, pi := range byDay[days[di]] {
				sample = append(sample, pts[pi])
			}
		}
		if len(sample) < 3 {
			return 0, false
		}
		return pearson(sample), true
	}); ok && !gated && enoughDays {
		resp["icCI"] = [2]float64{ci.Lo, ci.Hi}
	} else {
		resp["icCI"] = nil
		resp["icCINote"] = fmt.Sprintf(
			"interval withheld: %d distinct market days is below the %d-day floor for a cluster-robust interval "+
				"(or the IC itself is gated) — the point estimate stands, the strength of it is not assertable",
			len(days), clusterstat.MinDistinctDays)
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
		httpInternal(w, err)
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
				httpInternal(w, err)
				return
			}
			c.Coverage[string(tf)] = map[string]int64{"bars": n, "from": mn, "to": mx}
		}
		out = append(out, c)
	}
	events, err := d.St.RecentDQ(ctx, 100)
	if err != nil {
		httpInternal(w, err)
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
	out := map[string]any{
		"lastBackupTs":   atoi(backup.MetaLastBackupTs),
		"lastBackupFile": lastFile,
		"lastOffsiteTs":  atoi(backup.MetaLastOffsiteTs),
		"offsiteDir":     offsiteDir,
	}
	// offsiteConfigured only ever meant "a path string is set", but it reads as
	// "there is a copy on other hardware". The 2026-08-02 re-audit found it true
	// while offsiteDir was a macOS iCloud path recreated as ordinary folders on
	// the SAME physical disk as the database — 2.6 GB of supposedly off-machine
	// backups one disk failure from zero. VolumeName is the drive letter on
	// Windows and empty on Unix, so this answers definitively where it can and
	// reports "unknown" rather than guessing where it cannot; absent evidence of
	// separation, assume none.
	//
	// offsiteConfigured now REQUIRES that separation. A path string alone made
	// the field report true against a folder on C: next to the database, which
	// is the one reading a human is guaranteed to take at face value. A dashboard
	// that says "configured" when one disk failure loses everything is worse than
	// one that says nothing.
	sameVolume := any("unknown")
	if offsiteDir != "" {
		dbVol := filepath.VolumeName(d.St.Path())
		offVol := filepath.VolumeName(offsiteDir)
		if dbVol != "" || offVol != "" {
			sameVolume = strings.EqualFold(dbVol, offVol)
		}
		out["offsiteSameVolume"] = sameVolume
	}
	same, known := sameVolume.(bool)
	out["offsiteConfigured"] = offsiteDir != "" && known && !same
	return out
}

func (d Deps) agents(w http.ResponseWriter, r *http.Request) {
	runs, err := d.St.RecentWorkerRuns(r.Context(), 200)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, runs)
}

func (d Deps) hud(w http.ResponseWriter, r *http.Request) {
	payload, fetchedAt, ok, err := d.St.GetHud(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"available": false})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `{"available":true,"fetchedAt":%d,"summary":%s}`, fetchedAt, payload)
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
			httpInternal(w, err)
			return
		}
		writeJSON(w, ins)
		return
	}
	ins, err := d.St.RecentInsights(r.Context(), symbolID, limit)
	if err != nil {
		httpInternal(w, err)
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
		httpInternal(w, err)
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
		httpInternal(w, err)
		return
	}
	watchers, err := d.St.SymbolWatcherCount(r.Context(), s.ID)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if watchers == 0 {
		if err := d.St.SetSymbolActive(r.Context(), s.ID, false); err != nil {
			httpInternal(w, err)
			return
		}
		s.Active = false
	}
	writeJSON(w, s) // history is kept by design; only the live feed stops
}

// ── CSV exports ─────────────────────────────────────────────────────────
//
// Every export below is a bulk dump of stored rows, so every export goes
// through d.rawDataRefused — the SAME guard /api/bars uses. Scores and outcomes
// are included deliberately: each row is keyed to a licensed vendor bar and its
// fwd_return is arithmetic on two licensed closes, which the source agreements
// cover as derived works. A per-row dump of them is substantially-raw
// redistribution, whatever the file extension says.

// maxExportRows bounds every CSV. exportOutcomes previously asked the store for
// 100,000 rows with no symbol scoping — one unauthenticated GET dumping the
// whole outcomes table.
const maxExportRows = 20000

// exportLimit reads ?limit=, clamped into (0, maxExportRows].
func exportLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > maxExportRows {
		return maxExportRows
	}
	return n
}

func (d Deps) exportBars(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	if d.rawDataRefused(w, r) {
		return
	}
	tf := md.Timeframe(r.URL.Query().Get("tf"))
	if tf != md.TF1m && tf != md.TF1h && tf != md.TF1d {
		tf = md.TF1d
	}
	bars, err := d.St.LastBars(r.Context(), s.ID, tf, exportLimit(r))
	if err != nil {
		httpInternal(w, err)
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
	if d.rawDataRefused(w, r) {
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	now := time.Now().Unix()
	scores, err := d.St.ScoreHistory(r.Context(), s.ID, h, now-365*86400, now+1)
	if err != nil {
		httpInternal(w, err)
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
	// Scoped to one symbol like the other two exports. Symbol-less, this was a
	// whole-table dump reachable with no parameters at all.
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	if d.rawDataRefused(w, r) {
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	outcomes, err := d.St.ResolvedOutcomes(r.Context(), s.ID, h, exportLimit(r))
	if err != nil {
		httpInternal(w, err)
		return
	}
	csvStart(w, fmt.Sprintf("%s_outcomes_%s.csv", sanitize(s.Symbol), h))
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

// requestIsLoopback reports whether the caller is the local machine.
//
// Two asymmetric rules, both fail-closed (A9, 2026-07-26 re-audit):
//   - Proxy headers can never GRANT loopback status — an attacker sets those,
//     and trusting them would hand the redistribution guard to whoever asks.
//   - Proxy headers DO revoke it: an ngrok reverse tunnel connects from
//     127.0.0.1 and stamps X-Forwarded-For with the real client IP, so a
//     loopback RemoteAddr carrying any forwarding header is a remote caller
//     wearing a local address. Honoring the header here only ever denies, so
//     a forged header cannot widen access.
func requestIsLoopback(r *http.Request) bool {
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "Ngrok-Skip-Browser-Warning"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.")
}
