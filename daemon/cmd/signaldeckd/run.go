package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/alerts"
	"github.com/nyaungnicholas-wq/signaldeck/internal/anomaly"
	"github.com/nyaungnicholas-wq/signaldeck/internal/api"
	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/backup"
	"github.com/nyaungnicholas-wq/signaldeck/internal/briefing"
	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/discovery"
	"github.com/nyaungnicholas-wq/signaldeck/internal/health"
	"github.com/nyaungnicholas-wq/signaldeck/internal/hud"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cboe"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cftc"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/congress"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cryptohist"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cryptolive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/finra"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/fred"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/hyperliquid"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/news"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/stocktwits"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/tvscanner"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/wikimedia"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	"github.com/nyaungnicholas-wq/signaldeck/internal/maintain"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/universe"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// seedStocks is the first-boot watchlist — real-life defaults the user
// actually cares about (PUSH-20 world). Purely a starting point: any US
// symbol can be added on demand from the UI.
var seedStocks = []string{"SPY", "QQQ", "AAPL", "NVDA", "TSLA"}

// streamWorker adapts the Alpaca websocket streamer to the worker framework.
type streamWorker struct{ s *alpaca.Streamer }

func (w streamWorker) Name() string            { return "stock-streamer" }
func (w streamWorker) Interval() time.Duration { return 0 }
func (w streamWorker) Run(ctx context.Context) (string, error) {
	return "stream ended", w.s.Run(ctx)
}

// apiReadConns is the size of the API's PRIVATE read pool (see the ReaderClone
// call below). Kept small: WAL readers don't block each other, so this only has
// to cover concurrent interactive requests on a single-user local app — its job
// is isolation from the worker fleet, not throughput.
const apiReadConns = 4

// run wires the whole daemon: store-backed agents + the JSON API.
func run(ctx context.Context, cfg config.Config, st *store.Store) {
	// ── clients ─────────────────────────────────────────────────────
	var alpacaClient *alpaca.Client
	if cfg.HasAlpaca() {
		alpacaClient = alpaca.New(cfg.AlpacaKey, cfg.AlpacaSecret)
		alpacaClient.Feed = cfg.AlpacaFeed // "" → default "sip"; paid upgrade needs no code change
	} else {
		slog.Warn("no Alpaca keys found — stock ingestion disabled (set ALPACA_KEY/SECRET or keep them in stock-trader/.env)")
	}
	krakenClient := cryptohist.New()
	backfiller := pipeline.NewBackfiller(st, alpacaClient, krakenClient)

	// ONE EDGAR client for the WHOLE daemon. SEC's fair-access policy caps
	// clients at 10 req/s; the limiter that enforces that lives INSIDE
	// *edgar.Client (mutex-serialized min-interval pacing), so every worker
	// that talks to EDGAR must share this instance. Two independent clients
	// (fundamentals fetcher + filings/13F pollers) would each pace themselves
	// correctly yet sum to ~13.3 req/s when their runs overlap.
	edgarClient := edgar.New()

	// Cold-archive sink (tiered-storage wave): every row past retention is
	// exported here to gzip-CSV before it is pruned, so nothing is ever truly
	// deleted. Root: SIGNALDECK_ARCHIVE_DIR, else <db-dir>/archive.
	archiver := archive.New(archive.Dir(cfg.DBPath))

	// LLM provider (NVIDIA by default). Disabled/no-op until a key is set.
	// The daily call counter is persisted in SQLite so restarts can't reset
	// the spend cap.
	llmClient := llm.New(cfg.LLMKeys, cfg.LLMBaseURL, cfg.LLMModel, cfg.LLMModelDeep, cfg.LLMModelFast, cfg.LLMDailyCap)
	llm.SetSpendStore(llmClient, st)
	if llmClient.Enabled() {
		slog.Info("AI layer enabled", "model", cfg.LLMModel, "deep", cfg.LLMModelDeep, "keys", len(cfg.LLMKeys), "dailyCap", cfg.LLMDailyCap)
	} else {
		slog.Warn("AI layer disabled — no LLM key (set SIGNALDECK_NVIDIA_KEY or SIGNALDECK_NVIDIA_KEYS in daemon/.env)")
	}

	// Stage 3 — alert delivery beyond the Mac: ONE daemon-wide outbound
	// notifier (Discord/Telegram/generic webhook, all env-configured, all
	// optional) shared by the alert-runner, the watchdog, and
	// /api/notify-status. Constructor appended at the END of this file.
	remote := remoteNotifier(st)

	// ── multi-user bootstrap: seed the "local" account on first boot ────
	if err := bootstrapUsers(ctx, st); err != nil {
		slog.Error("bootstrap users", "err", err)
		return
	}

	// ── symbols: consolidated crypto always; seed stocks on first boot ──
	cryptoSym, err := st.UpsertSymbol(ctx, cfg.CryptoSymbol, md.Crypto,
		"Bitcoin/USD (Coinbase+Kraken consolidated via TickStream)")
	if err != nil {
		slog.Error("seed crypto symbol", "err", err)
		return
	}
	if seeded, _ := st.GetMeta(ctx, "seeded_v1"); seeded == "" {
		_ = backfiller.Enqueue(cryptoSym)
		if alpacaClient != nil {
			for _, s := range seedStocks {
				sym, err := st.UpsertSymbol(ctx, s, md.Stocks, "")
				if err == nil {
					// The seed stocks are the initial STREAMED hot set (live ws
					// + full 1m pipeline); mark them stream=1 so the streamer
					// and the stream-cap accounting pick them up.
					_ = st.SetSymbolStream(ctx, sym.ID, true)
					_ = backfiller.Enqueue(sym)
				}
			}
		}
		_ = st.SetMeta(ctx, "seeded_v1", time.Now().Format(time.RFC3339))
		slog.Info("first boot: seeded watchlist", "crypto", cfg.CryptoSymbol, "stocks", seedStocks)
	}

	// ── broad-universe wave: seed the BROAD DAILY-ONLY universe once ────
	// Hundreds of curated liquid US names registered as active-for-daily but
	// NOT streamed (stream=0), backfilled ~2y of daily bars via Alpaca's free
	// multi-symbol endpoint. Guarded by its own meta key so it runs exactly
	// once — independent of seeded_v1, so an already-seeded install still gets
	// the universe on the next boot after this ships. The deep backfill runs in
	// a goroutine so it never blocks daemon startup (a few tens of free
	// requests, paced under the 200/min limit).
	if seeded, _ := st.GetMeta(ctx, "seeded_universe_v1"); seeded == "" {
		go func() {
			registered, bars, err := universe.Seed(context.Background(), st, alpacaClient, universe.UniverseCap())
			if err != nil {
				// Don't set the meta key on failure — retry on the next boot.
				slog.Warn("broad-universe seed", "err", err, "registered", registered)
				return
			}
			_ = st.SetMeta(context.Background(), "seeded_universe_v1", time.Now().Format(time.RFC3339))
			slog.Info("first boot: seeded broad daily universe",
				"symbols", registered, "cap", universe.UniverseCap(), "backfilledBars", bars)
		}()
	}

	// ── streamer (live stock minute bars) ───────────────────────────
	var streamer *alpaca.Streamer
	if alpacaClient != nil {
		resolve := func(symbol string) (int64, bool) {
			s, err := st.GetSymbol(context.Background(), symbol, md.Stocks)
			if err != nil {
				return 0, false
			}
			return s.ID, true
		}
		streamer = alpaca.NewStreamer(alpacaClient, st, resolve, "")
		refreshStreamerSymbols(ctx, st, streamer)
	}

	// ── the agent fleet ─────────────────────────────────────────────
	fleet := []workers.Worker{
		cryptolive.New(st, cfg.TickstreamURL, cryptoSym.ID),
		&pipeline.CryptoBars{St: st, Kraken: krakenClient},
		backfiller,
		&pipeline.BackfillReconciler{St: st, BF: backfiller},
		&pipeline.SignalRunner{St: st},
		&pipeline.ExpectancyRunner{St: st},
		&pipeline.ForecastTrainer{St: st},
		&pipeline.InsightWriter{St: st},
		&maintain.Downsampler{St: st, Arc: archiver},
		&maintain.OutcomeResolver{St: st},
		&maintain.DQAuditor{St: st},
		hud.New(st, cfg.HudURL),
		&pipeline.AnalystWorker{St: st, LLM: llmClient},
		&pipeline.WatcherWorker{St: st, LLM: llmClient},
		&pipeline.PredictionRunner{St: st},
		&pipeline.PredictionResolver{St: st},
		&pipeline.RegimeRunner{St: st},
		&pipeline.RankingRunner{St: st},
		&pipeline.BreakoutRunner{St: st},
		&pipeline.SentimentTagger{St: st, LLM: llmClient},
		&pipeline.SectorRotator{St: st},
	}
	if alpacaClient != nil {
		fleet = append(fleet, &pipeline.NewsFetcher{St: st, Client: news.New(cfg.AlpacaKey, cfg.AlpacaSecret)})
	}
	if streamer != nil {
		fleet = append(fleet, streamWorker{streamer}, &pipeline.StockBars{St: st, Alpaca: alpacaClient})
	}

	// ── ops reliability: nightly backup + watchdog ──────────────────
	backupDir := os.Getenv("SIGNALDECK_BACKUP_DIR")
	if backupDir == "" {
		backupDir = filepath.Join(filepath.Dir(cfg.DBPath), "backups")
	}
	fleet = append(fleet, &backup.Worker{
		St: st, Dir: backupDir, OffsiteDir: offsiteBackupDir(), Keep: 7, FirstRunDelay: 5 * time.Minute,
	})
	// Alerts + daily-briefing wave (constructor appended at the END of this
	// file) — must join the fleet BEFORE the watchdog snapshots its specs.
	fleet = append(fleet, alertBriefingWorkers(st, llmClient, remote)...)
	// Universe-discovery wave (constructor appended at the END of this file) —
	// also BEFORE the watchdog spec snapshot so it's health-audited.
	fleet = append(fleet, discoveryWorkers(cfg, st, alpacaClient, backfiller, streamer)...)
	// Broad-universe wave (constructor appended at the END of this file) — the
	// universe-poller (6h, staggered) that keeps the BROAD DAILY-ONLY universe
	// fed with free multi-symbol daily bars; BEFORE the watchdog spec snapshot
	// so it's health-audited like every other worker.
	fleet = append(fleet, universeWorkers(cfg, st, alpacaClient)...)
	// Learning-flywheel wave (constructor appended at the END of this file) —
	// also BEFORE the watchdog spec snapshot so it's health-audited.
	fleet = append(fleet, learningWorkers(st)...)
	// Per-symbol agents wave (constructor appended at the END of this file) —
	// the per-symbol-learner (1h) that gives each symbol its OWN learned model;
	// BEFORE the watchdog spec snapshot so it's health-audited like every other
	// worker.
	fleet = append(fleet, perSymbolWorkers(st)...)
	// Stage-6 edge-modeling wave (constructor appended at the END of this file) —
	// the gbm-trainer (1h) that trains the non-linear GBM + gated mean-reversion
	// model legs from the feature store, walk-forward + OOS-graded, and stores
	// each leg's prob+lift so the PredictionRunner blends it only when lift>0.
	// BEFORE the watchdog spec snapshot so it's health-audited like every worker.
	fleet = append(fleet, edgeModelWorkers(st)...)
	// Credibility wave (constructor appended at the END of this file) — the
	// regime-outcome-runner (6h) that freezes every regime forecast into an
	// ungraded outcome row (once per symbol/kind/UTC-day), later grades it with
	// the exact engine math, and writes plain-English postmortems for
	// high-conviction misses. BEFORE the watchdog spec snapshot so it's
	// health-audited like every other worker.
	fleet = append(fleet, regimeOutcomeWorkers(st)...)
	// Tiered-storage wave (constructor appended at the END of this file) — the
	// storage governor (WAL checkpoint + threshold VACUUM); BEFORE the watchdog
	// spec snapshot so it's health-audited like every other worker.
	fleet = append(fleet, storageWorkers(st)...)
	// Data-integrity wave (2026-07-24) — split-repair (6h): incremental fetches
	// leave stored history on a stale price basis after a split, welding a fake
	// +/-50-95% move into the series that every predictor then has to refuse.
	// Measured 521 discontinuities over 185 symbols (107 inside the live
	// forecast window) before this shipped. Detects and re-backfills, budgeted
	// so a first pass cannot exhaust the free-tier API allowance.
	fleet = append(fleet, &pipeline.SplitRepair{St: st, Alpaca: alpacaClient})
	// Model-health gate (2026-07-24, 1h) — grades every emitting model against
	// its own live record and writes a verdict the prediction path honours. The
	// directional ensemble is why this exists: it shipped through 18,762
	// independent observations of NEGATIVE skill because nothing in the system
	// had the authority to switch a model off.
	fleet = append(fleet, &pipeline.ModelHealthWorker{St: st})
	// Autonomous research loop (2026-07-25, 24h) — generate -> test -> judge ->
	// ledger -> kill, without a human starting it. Hypotheses tested per week was
	// the rate limiter on finding edge, and it equalled how often someone sat
	// down and asked. Bonferroni-corrected over the grid, era-covered,
	// non-overlapping weekly observations, and deliberately unable to promote its
	// own findings past `shadow`.
	fleet = append(fleet, &pipeline.ResearchLoop{St: st})
	// Free-data wave / Stage 2 (constructor appended at the END of this file) —
	// fred-poller (6h, keyless FRED macro) + edgar-fetcher (24h, SEC EDGAR
	// fundamentals, gated on Alpaca keys only so the equity universe exists);
	// BEFORE the watchdog spec snapshot so both are health-audited.
	fleet = append(fleet, freeDataWorkers(cfg, st, edgarClient)...)
	// Paper-trading wave / Stage 4 (constructor appended at the END of this file)
	// — paper-trader (1h): the INTERNAL SIMULATED book driven by the flagship
	// calibrated predictions. It NEVER contacts a broker; every fill is computed
	// from stored bars. BEFORE the watchdog spec snapshot so it's health-audited.
	fleet = append(fleet, paperWorkers(st)...)
	// Signal8 wave / Stage 1 (constructor appended at the END of this file) —
	// filings-poller (2h, EDGAR submissions → filings feed + Form 4 insider
	// parse + dilution flags) and 13f-poller (24h, curated notable managers'
	// 13F-HR holdings). BEFORE the watchdog spec snapshot so both are
	// health-audited like every other worker.
	fleet = append(fleet, signal8Workers(cfg, st, edgarClient)...)
	// Signal8 wave / Stage 2 (constructor appended at the END of this file) —
	// congress-poller (12h): free public congressional stock-disclosure
	// mirrors (Senate + House Stock Watcher dumps). Needs no API key; degrades
	// gracefully (dq event + honest status) when a mirror is dead — which both
	// currently are (verified 2026-07-04). BEFORE the watchdog spec snapshot
	// so it's health-audited like every other worker.
	fleet = append(fleet, congressWorkers(st)...)
	// Signal8 wave / Stage 3 (constructor appended at the END of this file) —
	// anomaly-scanner (5m): trade-imbalance + unusual-volatility/volume
	// detection as DESCRIPTIVE z-scores vs each symbol's own trailing
	// baseline (crypto imbalance from real snapshots_1s; stock imbalance is
	// a labeled volume-side proxy). Hot set every tick (stock 1m scans gated
	// on marketcal), broad daily-only universe once per ET trading day.
	// BEFORE the watchdog spec snapshot so it's health-audited like every
	// other worker.
	fleet = append(fleet, anomalyWorkers(st)...)
	// Signal8 wave / Stage 4 (constructor appended at the END of this file) —
	// tape-seeder (6h): registers the home ticker-tape's index/sector ETFs
	// (SPY/QQQ/DIA/IWM + XLK/XLF/XLE/XLV) into the BROAD DAILY-ONLY universe
	// (stream=0 — never the streamed hot set) and deep-backfills daily bars
	// for any that have none; thereafter the universe-poller keeps them fresh
	// like every other daily-only symbol, so steady-state runs are no-op
	// sweeps. BEFORE the watchdog spec snapshot so it's health-audited like
	// every other worker.
	fleet = append(fleet, tapeWorkers(cfg, st, alpacaClient)...)
	// Signal8 wave / Stage 5 (constructor appended at the END of this file) —
	// companies-sync (24h): refreshes the FULL SEC company directory
	// (cik/name/ticker/exchange, ~10.4k rows) from ONE free EDGAR request per
	// run, feeding the /intel/companies directory. SIC enrichment costs zero
	// extra requests (it rides the filings-poller's existing submissions
	// fetches). Always enabled — depends on nothing but EDGAR. Shares the ONE
	// process-wide edgar limiter. BEFORE the watchdog spec snapshot so it's
	// health-audited like every other worker.
	fleet = append(fleet, companiesWorkers(st, edgarClient)...)
	// Stage 4 — SIC/SECTOR COVERAGE wave (constructor appended at the END of
	// this file) — sic-bulk-sync (12h tick): classifies the WHOLE companies
	// directory from SEC's official nightly bulk submissions.zip (~1.5 GB,
	// stream-downloaded to SIGNALDECK_TMP and deleted after) in ONE request.
	// Gate: boot catch-up when SIC coverage < 50% (≤1 attempt/UTC day), else
	// once per UTC month; 403/moved/corrupt degrades to the filings-poller
	// rotation with a dq note, never failing the fleet. Shares the ONE
	// process-wide edgar limiter + UA. BEFORE the watchdog spec snapshot so
	// it's health-audited like every other worker.
	fleet = append(fleet, sicBulkWorkers(st, edgarClient)...)
	// Stage-2 "make the proof visible" wave (constructor appended at the END of
	// this file) — weekly-report (Sun ~5pm ET) + signalbt-weekly pin (Sun ~6pm
	// ET), both once-per-NY-week with meta week-key dedup. BEFORE the watchdog
	// spec snapshot so both are health-audited like every other worker.
	fleet = append(fleet, weeklyProofWorkers(st, llmClient)...)
	// Stage 5 — FINRA Reg SHO wave (constructor appended at the END of this
	// file) — finra-shorts (6h tick): free, registration-less daily short sale
	// volume files (cdn.finra.org Consolidated NMS), NY ~18:30 publish gate +
	// meta day-key dedup, ~30-trading-day first-run backfill, universe-scoped
	// storage. Missing/late files degrade to a dq event, never a fleet
	// failure. BEFORE the watchdog spec snapshot so it's health-audited like
	// every other worker.
	fleet = append(fleet, finraShortsWorkers(st)...)
	// SIGNALS-hub overhaul (constructor appended at the END of this file) —
	// composite-scorer (10m): the per-symbol SignalScore — forced 1-10 curve
	// over the latest calibrated predictions' edges + factor tiles + additive
	// ledger; whole pass gated below a 30-symbol cross-section. BEFORE the
	// watchdog spec snapshot so it's health-audited like every other worker.
	fleet = append(fleet, compositeWorkers(st)...)
	// TradingView scanner-ratings wave (constructor appended at the END of this
	// file) — tv-rating (15m): persists TradingView's OWN technical-analysis
	// RATING for our tracked symbols from its PUBLIC scanner (no account/key)
	// into tv_ratings, resolving each stock's exchange via TradingView's public
	// symbol-search first. An EXTERNAL, descriptive, delayed signal — NOT our
	// model and not advice. Resolution/network hiccups degrade honestly, never
	// failing the fleet. BEFORE the watchdog spec snapshot so it's health-
	// audited like every other worker.
	fleet = append(fleet, tvRatingWorkers(st)...)
	// Tiered-storage wave phase 2 (constructor appended at the END of this file)
	// — derived-retention (1h): archive-before-prune for the high-volume DERIVED
	// tables (scores/score_outcomes/features) that grow unbounded, with the same
	// fail-safe contract as the Downsampler; predictions + prediction_outcomes
	// are kept forever. Needs the cold-archive sink. BEFORE the watchdog spec
	// snapshot so it's health-audited like every other worker.
	fleet = append(fleet, derivedRetentionWorkers(st, archiver)...)
	// Self-audit / drift-watchdog wave (constructor appended at the END of this
	// file) — self-audit (6h, gated once/UTC-day): MEASURES the system's own
	// honesty (calibration drift, factor-IC sign flips, prediction bias) from
	// resolved history and writes findings to self_audit + insights, every check
	// gated at n>=30. BEFORE the watchdog spec snapshot so it's health-audited.
	fleet = append(fleet, selfAuditWorkers(st)...)
	// DATA-EXPANSION wave (constructor appended at the END of this file) — six
	// free, keyless external context pollers: finra-shortint (bi-monthly short
	// interest), crypto-perp (Hyperliquid funding/OI), cot-poller (CFTC weekly
	// positioning), stocktwits-fetcher + wiki-attention (watchlist/hot-set
	// scoped attention proxies), cboe-pc (market-wide put/call). ALL stored +
	// served as DESCRIPTIVE context with explicit caveats — nothing becomes a
	// scored factor in this wave. Also tv-quotes (60s market-hours quote tape
	// from TradingView's public scanner — real-time rtc composite when
	// present, else labeled 15m-delayed close). BEFORE the watchdog spec
	// snapshot so every one is health-audited like every other worker.
	fleet = append(fleet, dataExpansionWorkers(st)...)
	// News-trends + strategy-lab wave (constructor appended at the END of this
	// file) — news-trends (30m, headline-volume z + fleet trending tokens) and
	// strategy-lab (24h, once/day gate; 8 classic published strategies through
	// the bias-free backtester on our own bars). BEFORE the watchdog spec
	// snapshot so both are health-audited like every other worker.
	fleet = append(fleet, newsTrendsStrategyLabWorkers(st)...)
	// Cross-sectional alpha wave (constructor appended at the END of this
	// file) — alpha-trainer (6h): ONE pooled model per horizon over the WHOLE
	// universe's labeled feature rows, labeling each row vs its same-UTC-day
	// cross-section median (relative alpha), graded by purged walk-forward
	// day-splits with a 2-day embargo; per-symbol current scores are stored
	// (model_forecasts, model="alphax") ONLY while measured OOS lift > 0 and
	// the PredictionRunner does NOT consume them yet (later wave, after the
	// grade proves out). BEFORE the watchdog spec snapshot so it's
	// health-audited like every other worker.
	fleet = append(fleet, alphaXWorkers(st)...)
	// DATA-SOURCE FRESHNESS wave (constructor appended at the END of this file) —
	// source-audit (1h): checks the age of every EXTERNAL source's newest row
	// against a market-calendar-aware staleness budget and records a
	// dq_events(source_stale) per quietly-dead source (deduped per source per UTC
	// day); served live at GET /api/source-health. BEFORE the watchdog spec
	// snapshot so it's health-audited like every other worker.
	fleet = append(fleet, sourceAuditWorkers(cfg, st)...)
	// Candlestick-patterns wave (constructor appended at the END of this file) —
	// pattern-stats (24h, once/UTC-day gate): recomputes each hot symbol's
	// MEASURED candlestick edge from ~2y of daily bars and stores the gated
	// (n>=15) hit-rate/mean-forward per pattern, read back by
	// /api/candle-patterns. BEFORE the watchdog spec snapshot so it's
	// health-audited like every other worker.
	fleet = append(fleet, patternStatsWorkers(st)...)
	// SMART MONEY FACTS wave (constructor appended at the END of this file) —
	// smart-money-scorer (1h): turns ALREADY-INGESTED positioning data (Form 4
	// open-market insiders, FINRA short interest / Reg SHO short volume, crypto
	// perp funding, 13F holdings) into ONE transparent, decomposed per-symbol
	// Smart Money Score + two deduped events (insider-cluster buy, squeeze
	// setup). A read of what informed participants are DOING, NOT a forecast.
	// BEFORE the watchdog spec snapshot so it's health-audited like every
	// other worker.
	fleet = append(fleet, smartMoneyWorkers(st)...)
	// CONFLUENCE GATE + MONEY SCOREBOARD wave (constructor appended at the END of
	// this file) — confluence-scorer (30m): flags a SETUP only when several
	// INDEPENDENT signal families (smart-money, trend, prediction, relative-
	// strength, breakout) AGREE on a direction, stored transparently, and
	// forward-tracks each flagged setup with NO lookahead; confluence-resolver
	// (15m): grades matured setups against realized bars to feed the money
	// scoreboard (scored by EXPECTED PROFIT, not win rate). BEFORE the watchdog
	// spec snapshot so both are health-audited like every other worker.
	fleet = append(fleet, confluenceWorkers(st)...)
	// RESEARCH DISCOVERY ENGINE wave: hist-backfill (once/UTC-day) deepens the
	// stock universe's daily bars to 2019 and rebuilds the research_weeks
	// evidence base (point-in-time weekly features + realized labels across
	// COVID/bull/bear/AI-rally eras); research-engine (once/UTC-day) grades the
	// ledger's machine-readable hypotheses era by era as BACKTEST evidence
	// (survivorship penalized), runs counterfactual + fragile-threshold +
	// regime-survival attacks, seeds H002-R1, auto-discovers bounded new
	// candidates under a Bonferroni bar, and sweeps decay. Audit surface only —
	// mutates nothing live. BEFORE the watchdog spec snapshot so both are
	// health-audited like every other worker.
	fleet = append(fleet, discoveryEngineWorkers(st, alpacaClient)...)
	// WAVE 2 — cold-load precompute + weekly digest (constructors appended at
	// the END of this file). The cache-warmer's target is set AFTER the API
	// deps are built (the shared caches live in the api package and warm
	// against the same reader pool the handlers use), so it takes a settable
	// indirection here; until wired each tick is an honest no-op. BEFORE the
	// watchdog spec snapshot so both are health-audited like every worker.
	var warmTarget func(context.Context) error
	fleet = append(fleet, cacheWarmWorkers(&warmTarget)...)
	fleet = append(fleet, digestWorkers(st, remote)...)
	// Snapshot the fleet's specs BEFORE appending the watchdog, so it never
	// audits itself; its own health shows on the Agents page like any worker.
	specs := make([]health.WorkerSpec, 0, len(fleet))
	for _, w := range fleet {
		specs = append(specs, health.WorkerSpec{Name: w.Name(), Interval: w.Interval()})
	}
	fleet = append(fleet, &health.Watchdog{
		St:         st,
		Specs:      specs,
		StatusPath: filepath.Join(filepath.Dir(cfg.DBPath), "health.json"),
		Remote:     remote, // Stage 3: unhealthy transition also delivered beyond the Mac (same 6h cooldown)
	})

	// ── API ─────────────────────────────────────────────────────────
	// Interactive reads get their OWN connection pool. The shared 4-connection
	// read pool was serving ~30 batch workers AND every handler, so a fleet
	// scanning the multi-GB database held all four and API reads queued behind
	// multi-second scans (measured 2026-07-16: /api/honesty at 61s during a
	// 10-worker storm). The clone shares the single WRITE connection, so the
	// no-SQLITE_BUSY discipline is untouched; only reads are isolated.
	// Best-effort: if the clone cannot open, serve from the shared pool rather
	// than fail the daemon.
	apiSt := st
	if clone, err := st.ReaderClone(apiReadConns); err != nil {
		slog.Error("api read pool: falling back to the shared pool", "err", err)
	} else {
		defer clone.Close() //nolint:errcheck
		apiSt = clone
	}
	deps := api.Deps{
		St:       apiSt,
		Cfg:      cfg,
		Version:  version,
		Started:  time.Now(),
		LLM:      llmClient,
		Notifier: remote, // Stage 3: /api/notify-status transport visibility
		CurrentState: func(ctx context.Context, symbolID int64) (map[md.Horizon]string, error) {
			return pipeline.CurrentState(ctx, st, symbolID)
		},
		Subscribe: func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error) {
			return subscribe(ctx, st, alpacaClient, backfiller, streamer, symbol, market)
		},
		// Monitor registers a symbol WITHOUT claiming a live-websocket slot: a
		// stock joins the broad daily+minute-POLLED universe (stream=0), still
		// fully scored/predicted/charted but bounded by the large universe cap
		// instead of the small free-ws stream cap. This is what lets every
		// discovered candidate be monitored even when the hot set is full.
		Monitor: func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error) {
			return monitor(ctx, st, alpacaClient, backfiller, streamer, symbol, market)
		},
	}
	// Cold-load precompute: the cache-warmer (appended to the fleet above) now
	// warms the SAME shared api caches these deps serve from, on the same
	// isolated reader pool.
	warmTarget = deps.WarmCaches
	go func() {
		if err := api.Serve(ctx, deps); err != nil {
			slog.Error("api server exited", "err", err)
		}
	}()

	workers.NewRunner(st, fleet...).Start(ctx)
}

// bootstrapUsers migrates a pre-multi-user database: when no accounts exist
// yet, it creates an admin user "local" with a random password (printed ONCE
// to stderr — change it or register your own account), adopts every currently
// active symbol into that user's watchlist, and assigns any unowned paper
// positions to it, so existing single-user behavior continues unchanged.
func bootstrapUsers(ctx context.Context, st *store.Store) error {
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	password := hex.EncodeToString(raw)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	uid, err := st.CreateUser(ctx, "local", string(hash), true)
	if err != nil {
		return err
	}
	if err := st.AdoptActiveSymbols(ctx, uid); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "signaldeck: created initial admin user %q with password %q — log in and keep it safe (this is printed only once)\n", "local", password)
	return nil
}

// subscribe validates + registers a symbol into the STREAMED hot set (stream=1)
// and kicks off its backfill. Use it for an explicit "give this a live slot"
// add (manual subscribe, discovery auto-add) while under the free-ws cap.
func subscribe(ctx context.Context, st *store.Store, ac *alpaca.Client,
	bf *pipeline.Backfiller, streamer *alpaca.Streamer,
	symbol string, market md.Market,
) (md.Symbol, error) {
	return registerSymbol(ctx, st, ac, bf, streamer, symbol, market, true)
}

// monitor validates + registers a symbol into the broad POLLED universe
// (stream=0 for stocks) without claiming a live-websocket slot. The symbol is
// still fully backfilled, scored, predicted, and charted — the universe-poller
// (6h deep) and universe-live poller (60s during market hours) keep its daily,
// hourly, and minute bars fresh — it just isn't real-time ws-streamed. This is
// what lets monitoring scale past the small free-ws stream cap: it is bounded
// only by the (large) universe cap. Crypto always streams (Kraken's public feed
// is free), so monitor and subscribe are equivalent for crypto.
func monitor(ctx context.Context, st *store.Store, ac *alpaca.Client,
	bf *pipeline.Backfiller, streamer *alpaca.Streamer,
	symbol string, market md.Market,
) (md.Symbol, error) {
	return registerSymbol(ctx, st, ac, bf, streamer, symbol, market, false)
}

// registerSymbol is the shared validate+register+backfill path. stream=true
// promotes a stock into the live-ws hot set (counts against the stream cap);
// stream=false registers it as a daily-only broad-universe symbol (polled, not
// streamed). Crypto ignores the flag — it always streams via Kraken.
func registerSymbol(ctx context.Context, st *store.Store, ac *alpaca.Client,
	bf *pipeline.Backfiller, streamer *alpaca.Streamer,
	symbol string, market md.Market, stream bool,
) (md.Symbol, error) {
	name := ""
	switch market {
	case md.Stocks:
		if ac == nil {
			return md.Symbol{}, fmt.Errorf("stock data needs Alpaca keys (none configured)")
		}
		n, ok, err := ac.ValidateSymbol(ctx, symbol)
		if err != nil {
			return md.Symbol{}, fmt.Errorf("validate %s: %w", symbol, err)
		}
		if !ok {
			return md.Symbol{}, fmt.Errorf("%q is not an active US equity on Alpaca", symbol)
		}
		name = n
	case md.Crypto:
		// Kraken pairs are BASE/QUOTE; a quick shape check — the backfill
		// itself is the real validation (unknown pairs error visibly).
		if !strings.Contains(symbol, "/") {
			return md.Symbol{}, fmt.Errorf("crypto symbols are BASE/QUOTE, e.g. ETH/USD")
		}
	}

	// MONITOR path (stock, stream=false): register as a broad-universe daily
	// symbol (stream=0) — polled, never ws-streamed, so it never consumes a
	// free-ws slot. UpsertDailyUniverseSymbol never DEMOTES an already-streamed
	// name, so re-adding a hot-set symbol as "monitor" is a safe no-op on its
	// stream flag.
	if market == md.Stocks && !stream {
		sym, err := st.UpsertDailyUniverseSymbol(ctx, symbol, name)
		if err != nil {
			return md.Symbol{}, err
		}
		// Immediate backfill is BEST-EFFORT for a monitored symbol: it is already
		// registered in the daily universe, so the universe-poller (60s live / 6h
		// deep) fills its bars even if the backfill queue is momentarily full
		// (e.g. right after a bulk "monitor all"). A full queue must NOT fail the
		// monitor — that would leave the user staring at a dead button.
		if err := bf.Enqueue(sym); err != nil {
			slog.Warn("monitor: immediate backfill skipped, universe poller will fill bars",
				"symbol", symbol, "err", err)
		}
		return sym, nil
	}

	sym, err := st.UpsertSymbol(ctx, symbol, market, name)
	if err != nil {
		return md.Symbol{}, err
	}
	// STREAM path: an explicit subscribe (manual add or discovery auto-add)
	// puts the symbol in the STREAMED HOT SET. Promote its stream flag so it
	// joins the live ws + full 1m pipeline (and counts against the stream cap).
	// A daily-only universe symbol being subscribed is thereby promoted; a
	// fresh symbol is created with stream=1.
	if market == md.Stocks {
		if err := st.SetSymbolStream(ctx, sym.ID, true); err == nil {
			sym.Stream = true
		}
	}
	if err := bf.Enqueue(sym); err != nil {
		return md.Symbol{}, err
	}
	if market == md.Stocks && streamer != nil {
		refreshStreamerSymbols(ctx, st, streamer)
	}
	return sym, nil
}

// refreshStreamerSymbols pushes the current STREAMED hot set to the ws stream.
// Only stream=1 stocks are subscribed, so the live websocket stays within the
// free-tier concurrent-symbol cap even though hundreds of daily-only universe
// symbols are also active.
func refreshStreamerSymbols(ctx context.Context, st *store.Store, streamer *alpaca.Streamer) {
	streamed := true
	syms, err := st.ActiveStockSymbols(ctx, &streamed)
	if err != nil {
		slog.Warn("refresh streamer symbols", "err", err)
		return
	}
	stocks := make([]string, 0, len(syms))
	for _, s := range syms {
		stocks = append(stocks, s.Symbol)
	}
	streamer.SetSymbols(stocks)
}

// ─────────────────────────────────────────────────────────────────────────
// ALERTS + DAILY-BRIEFING WAVE (appended block).
// alertBriefingWorkers returns the wave's workers:
//   - alert-runner (5m): watchlist-scoped alerts from breakouts, regime
//     changes, and calibrated predictions crossing SIGNALDECK_ALERT_HI/LO,
//     with a batched macOS notification;
//   - daily-briefing (10m tick, fires once per day at ~7:00am ET): one
//     honest market insight composed from stored data only (LLM-polished
//     when a key is configured, deterministic template otherwise).
//
// Stage 3 (alert delivery beyond the Mac): the alert-runner also carries the
// shared remote notifier — ONE batched message per sweep to every configured
// transport, under the same 30m cooldown as the macOS popup.
func alertBriefingWorkers(st *store.Store, llmClient llm.Client, remote *notify.Notifier) []workers.Worker {
	return []workers.Worker{
		&alerts.Runner{St: st, Remote: remote},
		&briefing.Worker{St: st, LLM: llmClient},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// UNIVERSE-DISCOVERY WAVE (appended block).
// discoveryWorkers returns the wave's worker: universe-discovery (6h) sweeps
// Alpaca's most-actives + movers screeners into the candidates table and,
// while under the SIGNALDECK_SYMBOL_CAP budget, auto-adds persistent
// high-dollar-volume candidates through the SAME subscribe path the API
// uses (validate + upsert + activate + backfill), onto the admin watchlist.
// Degrades to a skip when no Alpaca keys are configured (like NewsFetcher).
func discoveryWorkers(cfg config.Config, st *store.Store, ac *alpaca.Client,
	bf *pipeline.Backfiller, streamer *alpaca.Streamer,
) []workers.Worker {
	// WHOLE-MARKET extension: TradingView's public scanner needs no keys, so
	// the TV source is always wired — each sweep also screens the entire US
	// market (top volume + top |change|) into the same candidates pipeline.
	w := &discovery.Worker{St: st, TV: tvscanner.New()}
	if cfg.HasAlpaca() {
		w.Client = discovery.NewClient(cfg.AlpacaKey, cfg.AlpacaSecret)
		w.Subscribe = func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error) {
			return subscribe(ctx, st, ac, bf, streamer, symbol, market)
		}
	}
	return []workers.Worker{w}
}

// ─────────────────────────────────────────────────────────────────────────
// LEARNING-FLYWHEEL WAVE (appended block).
// learningWorkers returns the wave's workers:
//   - sentiment-aggregator (30m): rolls rated headlines up into the permanent
//     sentiment_daily archive (today + yesterday, idempotent recompute) — the
//     4th ensemble component's feature source;
//   - adaptive-weights (6h): re-attributes resolved outcomes to component
//     legs per regime cell (hit-rate + IC), derives honesty-gated blend
//     weights (n>=30 per cell; sentiment needs its own n>=30), persists them
//     versioned in meta, and writes an insight when weights move materially.
func learningWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.SentimentAggregator{St: st},
		&pipeline.AdaptiveWeightsWorker{St: st},
		// Research Lab: postmortem-runner (1h) attributes every resolved WRONG,
		// meaningfully-convicted prediction to a ranked failure taxonomy and
		// stores it, so misses can be clustered and mined for new hypotheses.
		pipeline.NewPostmortemWorker(st),
		// Research Lab: research-lab (6h heartbeat, gated to once/UTC-day) mines
		// hypotheses from the failure clusters, grades each by strict walk-forward
		// OOS with a Bonferroni-corrected Wilson floor, shadows survivors, and
		// promotes only after a sustained streak of wins on fresh data. Nothing
		// it produces mutates live predictions — promotions are advisory.
		pipeline.NewResearchLabWorker(st),
		// Research Ledger: research-ledger (6h heartbeat, once/UTC-day) is the
		// BAYESIAN belief layer above the lab — named program-level discoveries
		// with prior->posterior evidence chains. Seeds the Pressure chapter once,
		// then re-grades open discoveries (H002/H008) on fresh, disjoint data
		// windows; every grade runs a self-attack battery whose failures enter
		// the same evidence chain. Audit surface only — mutates nothing live.
		pipeline.NewResearchLedgerWorker(st),
	}
}

// ─────────────────────────────────────────────────────────────────────────
// TIERED-STORAGE WAVE (appended block).
// storageWorkers returns the wave's worker: storage-governor (1h) checkpoints
// the WAL (TRUNCATE) every pass to bound the -wal sidecar and, when the DB has
// grown past SIGNALDECK_VACUUM_THRESHOLD_MB (default 2048), VACUUMs to return
// pages freed by retention deletes to the filesystem (rate-limited to once/day
// via a meta cursor). Tiered retention + archive-before-prune themselves live
// in the Downsampler (given the cold-archive sink above).
func storageWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&maintain.StorageGovernor{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// BROAD-UNIVERSE WAVE (appended block).
// universeWorkers returns the wave's worker: universe-poller (6h, staggered)
// that keeps the BROAD DAILY-ONLY universe fed — every pass it pulls
// split-adjusted DAILY bars for the whole registered daily universe (hundreds
// of curated liquid US names) via Alpaca's FREE multi-symbol bars endpoint
// (batched ≤100 symbols/request, paced under 200/min, 429-backoff) and upserts
// them. Those symbols are active-for-daily but NOT streamed, so the streamer
// stays within the free ws cap while ranking/correlation/regime + the
// per-symbol daily models get wide cross-sectional coverage at zero cost.
// Degrades to a skip when no Alpaca keys are configured (like NewsFetcher).
func universeWorkers(cfg config.Config, st *store.Store, ac *alpaca.Client) []workers.Worker {
	p := &universe.Poller{St: st}
	lp := &universe.LivePoller{St: st}
	if cfg.HasAlpaca() {
		p.Alpaca = ac
		// The live poller needs the REAL-TIME trailing window, which free-tier
		// SIP cannot serve (sipEndGuard) — it gets its own IEX-feed client.
		// The 16-minute IEX tail is healed to full-volume SIP bars by the deep
		// pollers on their next pass (idempotent upserts overwrite).
		iex := *ac
		iex.Feed = "iex"
		lp.Alpaca = &iex
	}
	// live-everything wave: universe-live (60s, marketcal-gated) keeps EVERY
	// stock's 1m bars current during market hours; the 6h deep poller stays
	// as heal + off-hours coverage.
	return []workers.Worker{p, lp}
}

// ─────────────────────────────────────────────────────────────────────────
// PER-SYMBOL AGENTS WAVE (appended block).
// perSymbolWorkers returns the wave's worker: per-symbol-learner (1h) that
// re-derives every active symbol's OWN model — blend weights, calibration,
// per-component skill, plain-English personality, and honest evidence tier —
// from that symbol's own resolved outcomes (prequential, no leakage), and
// cheaply upserts one symbol_models row per horizon. ONE worker computes all
// symbols; there is no process/goroutine per stock. Writes one insight the
// first time a symbol graduates to its own "personal" model.
func perSymbolWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.PerSymbolLearner{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// FREE-DATA WAVE / STAGE 2 (appended block).
// freeDataWorkers returns the wave's two zero-cost, no-vendor context workers:
//   - fred-poller (6h): pulls the FRED macro set (VIXCLS/DGS10/T10Y2Y/DFF) via
//     the KEYLESS CSV endpoint so it always runs; an optional
//     SIGNALDECK_FRED_KEY switches the client to the JSON API. Feeds
//     macro_series (LatestMacro / LatestVIX) for the Stage-6 feature layer.
//   - edgar-fetcher (24h): pulls SEC EDGAR company-facts (Revenues/EPS/shares/
//     latest-filing) for the equity universe, rate-limited (<=10 req/s) with a
//     descriptive User-Agent and 429/503 backoff, sweeping the universe over
//     successive daily runs via a persisted cursor. Gated on Alpaca keys only
//     because that's what populates the stock universe to fetch fundamentals
//     for; EDGAR itself needs no key. Degrades to a no-op with no key/universe.
//
// ec is the daemon-wide SHARED EDGAR client (see run()): its single
// mutex-serialized limiter spaces this fetcher's requests against the
// signal8 pollers' too, keeping the whole process under SEC's 10 req/s.
func freeDataWorkers(cfg config.Config, st *store.Store, ec *edgar.Client) []workers.Worker {
	fp := &pipeline.FredPoller{St: st, Client: fred.New(os.Getenv("SIGNALDECK_FRED_KEY"))}
	ef := &pipeline.EdgarFetcher{St: st}
	if cfg.HasAlpaca() {
		// Only fetch fundamentals when there's a stock universe to fetch for.
		ef.Client = ec
	}
	return []workers.Worker{fp, ef}
}

// ─────────────────────────────────────────────────────────────────────────
// PAPER-TRADING WAVE / STAGE 4 (appended block).
// paperWorkers returns the wave's worker: paper-trader (1h) that runs the
// INTERNAL, SELF-CONTAINED, SIMULATED paper-trading book(s) — one per
// prediction horizon (flagship-1d, flagship-1w) — driven by the platform's OWN
// latest calibrated predictions. When a symbol's cal_prob crosses the long
// threshold the book targets a long; when it crosses the flat threshold it
// exits; entries/exits fill at the NEXT bar's OPEN (no lookahead) and pay a
// realistic per-side cost. It tracks realized + unrealized P&L, an equity curve,
// and a trade log — an honest, costed, out-of-sample track record of the signal.
// CRITICAL: it NEVER places a real order or contacts any broker/trading API;
// every fill is pure arithmetic over stored bar data. Acts only on genuinely new
// bars and is idempotent per bar (a re-run double-trades nothing).
func paperWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.PaperTrader{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 6 — EDGE-MODELING WAVE (appended block).
// edgeModelWorkers returns the wave's worker: gbm-trainer (1h) that, for every
// active symbol+horizon, trains two additional directional model legs from the
// FEATURE STORE — a from-scratch pure-Go gradient-boosted decision tree (the
// non-linear sibling of the linear logit) and a gated mean-reversion leg (the
// inverted-momentum counterweight) — each graded strictly WALK-FORWARD and
// OUT-OF-SAMPLE (the mean-reversion leg additionally NET OF COST). It stores
// each leg's latest prob + OOS grade in model_forecasts; the PredictionRunner
// then folds a leg into the calibrated blend ONLY when its stored lift > 0, the
// identical honesty gate the logit forecast passes. A leg with no measured edge
// is dropped, never down-weighted — an honest "no edge yet" is the correct
// output on the current (tiny) resolved-outcome history.
func edgeModelWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.GBMTrainer{St: st},
		// pressure-trainer (1h): grades the ensemble's oldest base leg (the
		// composite Pressure Score) walk-forward OOS and stores its lift like a
		// model leg, so the PredictionRunner benches the leg when its measured
		// lift is <=0 — the same honesty gate the model legs pass.
		&pipeline.PressureTrainer{St: st},
		// vol-regime-runner (6h): the platform's ONE validated-edge forecast —
		// per-stock next-quarter volatility regime (elevated/calm) with MEASURED
		// walk-forward accuracy (74-76% high-conviction). NOT price direction.
		&pipeline.VolRegimeRunner{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 1: SEC FILINGS INTELLIGENCE (appended block).
// signal8Workers returns the wave's two workers:
//   - filings-poller (2h): sweeps a rotating window of universe stocks
//     through the EDGAR submissions API into the plain-English filings feed,
//     fetches + parses NEW Form 4 documents into insider_trades (bounded per
//     run), and re-derives per-symbol dilution flags (S-1/S-3/424B in 180d +
//     shares-outstanding growth >2%). Gated on Alpaca keys only because
//     that's what populates the stock universe to sweep — EDGAR itself needs
//     no key and the worker no-ops cleanly with no stocks.
//   - 13f-poller (24h): rotates through the curated notable-manager list
//     (~25 hardcoded CIKs — Berkshire, Bridgewater, RenTech, Citadel, …),
//     storing each manager's LATEST 13F-HR information table once per report
//     period. Always enabled: it depends on nothing but EDGAR.
//
// Both share the edgar package's rate limiter (150ms min interval, ≤10 req/s
// per SEC policy), descriptive User-Agent, and 429/503 backoff. Everything
// stored is public-domain government data; the API labels its legal lags
// (Form 4 ~2 business days; 13F quarterly + ≤45 days) honestly.
//
// ec is the daemon-wide SHARED EDGAR client (see run()): wrapping it (not a
// fresh edgar.New()) means the filings/13F pollers AND the fundamentals
// fetcher all pace through ONE limiter — separate limiters would each be
// individually compliant yet sum past SEC's 10 req/s when runs overlap.
func signal8Workers(cfg config.Config, st *store.Store, ec *edgar.Client) []workers.Worker {
	fc := &edgar.FilingsClient{Client: ec}
	fp := &pipeline.FilingsPoller{St: st}
	if cfg.HasAlpaca() {
		fp.Client = fc
	}
	tf := &pipeline.ThirteenFPoller{St: st, Client: fc}
	return []workers.Worker{fp, tf}
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 2: CONGRESSIONAL TRADES (appended block).
// congressWorkers returns the wave's worker: congress-poller (12h) that pulls
// US congressional stock-transaction disclosures (public-domain STOCK Act
// data) from the FREE Stock Watcher community mirrors — Senate + House JSON
// dumps on S3 — hashes each row to a deterministic id (INSERT OR IGNORE ⇒
// idempotent re-downloads), and maps disclosed tickers onto tracked stocks
// (unknown tickers keep symbol_id NULL, honestly). No API key needed.
// MIRROR REALITY (verified 2026-07-04 via Firecrawl): senatestockwatcher.com
// and housestockwatcher.com no longer resolve, and both S3 dumps return 403 —
// the poller records a dq event + an honest per-chamber status in meta every
// run and NEVER fails the fleet; /api/congress keeps serving stored history
// and surfaces the outage. SIGNALDECK_SENATE_TRADES_URL /
// SIGNALDECK_HOUSE_TRADES_URL override the mirror URLs the moment a live
// mirror (or a self-hosted export) exists, with zero code change.
// HONESTY: disclosures lag 30-45 days BY LAW; amounts are ranges. The API's
// lagNote states both.
func congressWorkers(st *store.Store) []workers.Worker {
	c := congress.New()
	if u := os.Getenv("SIGNALDECK_SENATE_TRADES_URL"); u != "" {
		c.SenateURL = u
	}
	if u := os.Getenv("SIGNALDECK_HOUSE_TRADES_URL"); u != "" {
		c.HouseURL = u
	}
	return []workers.Worker{&pipeline.CongressPoller{St: st, Client: c}}
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 3: ANOMALY LAYER (appended block).
// anomalyWorkers returns the wave's worker: anomaly-scanner (5m) — the
// user's explicit ask: trade-imbalance + unusual-volatility/volume detection
// as a DESCRIPTIVE layer (z-scores vs each symbol's OWN trailing baseline;
// every stored detail states its window + baseline), never predictions.
//   - HOT SET every tick: crypto order-book imbalance from snapshots_1s
//     (real book data) + realized-vol/TR-spike + same-time-of-day volume on
//     1m bars; streamed stocks get the same on 1m bars with imbalance as a
//     LABELED volume-side proxy (no order book exists on free stock data).
//     Stock scans respect marketcal (skipped entirely when closed).
//   - DAILY-ONLY UNIVERSE once per ET trading day: vol + volume on daily
//     bars (no intraday data ⇒ no imbalance proxy fabricated).
//
// Detections land in the anomalies table (hour-deduped per symbol+kind) and
// the alert-runner fans them out to watchlists as anomaly_* alert kinds.
// |z| threshold: SIGNALDECK_ANOM_Z (default 2.5).
func anomalyWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&anomaly.Scanner{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 4: SIGNAL8-STYLE HOME (appended block).
// tapeWorkers returns the wave's worker: tape-seeder (6h) — a tiny,
// idempotent, self-healing seeder for the home ticker tape's equity members
// (index ETFs SPY/QQQ/DIA/IWM + sector ETFs XLK/XLF/XLE/XLV). Every run it
// re-asserts each ETF's registration in the BROAD DAILY-ONLY universe
// (UpsertDailyUniverseSymbol never demotes an already-streamed symbol, so
// hot-set SPY/QQQ keep streaming) and deep-backfills ~2y of daily bars ONLY
// for members with zero daily bars — a single free multi-symbol request at
// most. Once registered, the regular universe-poller refreshes them with the
// rest of the daily universe, so the steady-state run is a no-op sweep.
// BTC and VIX complete the strip in the API layer (/api/tape) from crypto
// bars + the stored FRED VIXCLS series — no equity seeding needed for them.
// Without Alpaca keys the worker still registers the symbols and reports
// honestly that it cannot backfill.
func tapeWorkers(cfg config.Config, st *store.Store, ac *alpaca.Client) []workers.Worker {
	w := &universe.TapeSeeder{St: st}
	if cfg.HasAlpaca() {
		w.Alpaca = ac
	}
	return []workers.Worker{w}
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 5: COMPANIES DIRECTORY (appended block).
// companiesWorkers returns the wave's worker: companies-sync (24h) — mirrors
// the free SEC file www.sec.gov/files/company_tickers_exchange.json (the full
// registered-company map: cik/name/ticker/exchange, ~10.4k rows) into the
// companies table with ONE request per run, upserted in one transaction.
// Always enabled: it depends on nothing but EDGAR (no key, no Alpaca gate).
// SIC industry enrichment deliberately does NOT live here — the
// filings-poller extracts sic/sicDescription from the submissions responses
// it ALREADY fetches per swept universe symbol, so sector coverage grows at
// zero added request volume.
//
// ec is the daemon-wide SHARED EDGAR client (see run()): its single
// mutex-serialized limiter paces this worker's one request against the
// fundamentals fetcher and the filings/13F pollers, keeping the whole
// process under SEC's 10 req/s policy.
func companiesWorkers(st *store.Store, ec *edgar.Client) []workers.Worker {
	return []workers.Worker{
		&pipeline.CompaniesSync{St: st, Client: ec},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 2 — MAKE THE PROOF VISIBLE (appended block).
// weeklyProofWorkers returns the wave's two once-per-NY-week workers (both
// tick every 30m; the meta week-key dedup + Sunday-hour gate does the pacing,
// with catch-up later in the week if the daemon was down at the time):
//   - weekly-report (Sun ≥5pm ET): ONE insight (kind weekly_report) measuring
//     the platform's own week from stored data only — resolutions added by
//     horizon (raw + independent symbol-days), adaptive weights now vs last
//     week's snapshot (meta adaptive_weights_prev; first run = baseline
//     recorded), paper-book P&L change + trades, the per-symbol agents nearest
//     personal-model graduation (n/40), sentiment coverage, and the week's
//     anomaly count. Deterministic template; optional LLM polish under the
//     daily briefing's facts-are-untrusted-DATA rule.
//   - signalbt-weekly (Sun ≥6pm ET): runs the internal/signalbt evaluation per
//     horizon with the SAME parameters as /api/signal-backtest, stores the full
//     Result JSON in meta (signalbt_weekly:<sunday> + signalbt_latest — served
//     by ?pinned=1), and writes one honest insight (IC, quintile spread, N,
//     gated-or-not; always labeled backtested, never live).
func weeklyProofWorkers(st *store.Store, llmClient llm.Client) []workers.Worker {
	return []workers.Worker{
		&briefing.WeeklyWorker{St: st, LLM: llmClient},
		&briefing.SignalBTPinWorker{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 3 — ALERT DELIVERY BEYOND THE MAC (appended block).
// remoteNotifier builds the ONE daemon-wide outbound notifier
// (internal/notify): three optional, env-configured transports —
//   - Discord   SIGNALDECK_DISCORD_WEBHOOK            (webhook JSON {content})
//   - Telegram  SIGNALDECK_TELEGRAM_BOT_TOKEN
//   - SIGNALDECK_TELEGRAM_CHAT_ID         (Bot API sendMessage)
//   - Webhook   SIGNALDECK_WEBHOOK_URL                (POST {title,body,kind,ts})
//
// Contract: 5s timeout + 1 retry per delivery; failures become dq events
// (kind notify_failed, secrets redacted) and NEVER block the alert sweep or
// the watchdog; with nothing configured every Send is a no-op and alerts stay
// macOS-only. The same instance backs the alert-runner's batched sweep
// message, the watchdog's unhealthy-transition ping, and the read-only
// GET /api/notify-status. Email is deliberately NOT a transport — it needs
// SMTP credentials or a provider account (documented as future work).
func remoteNotifier(st *store.Store) *notify.Notifier {
	n := notify.NewFromEnv(st)
	if n.Enabled() {
		slog.Info("remote notify enabled", "transports", n.ConfiguredNames())
	} else {
		slog.Info("remote notify: no transports configured — alerts stay macOS-only " +
			"(set SIGNALDECK_DISCORD_WEBHOOK, SIGNALDECK_TELEGRAM_BOT_TOKEN+SIGNALDECK_TELEGRAM_CHAT_ID, or SIGNALDECK_WEBHOOK_URL in daemon/.env)")
	}
	return n
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 4 — SIC/SECTOR COVERAGE FOR THE WHOLE DIRECTORY (appended block).
// sicBulkWorkers returns the wave's worker: sic-bulk-sync (12h tick) — closes
// the directory's SIC gap (533/10,415 classified at ship time) with ONE free
// download of SEC's official nightly bulk export of the Submissions API,
//
//	https://www.sec.gov/Archives/edgar/daily-index/bulkdata/submissions.zip
//
// (documented on sec.gov "EDGAR Application Programming Interfaces"; ~1.5 GB,
// verified live 2026-07-06). Every CIK##########.json entry carries the same
// sic/sicDescription header the filings-poller extracts one company at a
// time, so one archive classifies the whole directory.
//   - Gate (pure, tested): boot catch-up when coverage < 50% (at most one
//     attempt per UTC day), else once per UTC month.
//   - Discipline: stream-download to SIGNALDECK_TMP (else os.TempDir(); temp
//     file deleted after, success or failure), > 5 GiB free-disk guard, only
//     directory CIKs decompressed, one batched transaction to update
//     companies.sic/sic_desc, coverage logged before/after.
//   - Degradation: 403/moved/corrupt archive ⇒ dq event (sic_bulk_unavailable)
//   - honest detail; the filings-poller SIC rotation keeps enriching; the
//     fleet NEVER fails. Blank SICs are honest absence and never stored.
//
// ec is the daemon-wide SHARED EDGAR client (see run()): the one download
// paces through the same limiter + declarative UA as every other SEC call.
func sicBulkWorkers(st *store.Store, ec *edgar.Client) []workers.Worker {
	return []workers.Worker{
		&pipeline.SICBulkSync{St: st, Client: ec},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 5 — FINRA REG SHO DAILY SHORT SALE VOLUME (appended block).
// finraShortsWorkers returns the wave's worker: finra-shorts (6h tick) —
// ingests FINRA's FREE, no-registration Consolidated NMS daily short sale
// volume file (cdn.finra.org/equity/regsho/daily/CNMSshvolYYYYMMDD.txt,
// verified live 2026-07-06; posted by ~6pm ET on the trade date) into the
// universe-scoped short_volume table (tracked symbols only, ~500 of ~12k
// rows/file). The tick is a heartbeat: the pure TargetShortVolDay gate only
// expects a day's file after ~18:30 ET on that TRADING day (weekends + full
// NYSE holidays step back via marketcal) and a meta day-key dedups each trade
// date to exactly one ingest; the first run backfills ~30 trading days (30
// paced requests). The finra client carries its own declarative UA (reusing
// edgar.ResolveUA()) and 500ms min-interval pacer; FINRA's CDN 403s absent
// days, which maps to an honest skip. A missing file past the deadline or a
// fetch error records a dq event (finra_shorts_unavailable/_error) and
// retries next tick — NEVER a fleet failure, and nothing fabricated.
// HONESTY: the derived short_pct is the daily short sale VOLUME ratio — NOT
// short interest; it includes market-maker activity and a high ratio is NOT
// directly bearish. /api/shorts and the UI carry that caveat verbatim.
func finraShortsWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.ShortVolPoller{St: st, Client: finra.New()},
	}
}

// runSICBulkOnce is the MANUAL one-shot entry behind `signaldeckd
// -sic-bulk-sync`: one forced sync (gate bypassed; disk guard still applies)
// against the configured store, returning the worker's honest detail line.
// Stop the daemon first — two processes contending for SQLite writes will
// see busy timeouts.
func runSICBulkOnce(ctx context.Context, st *store.Store) (string, error) {
	w := &pipeline.SICBulkSync{St: st, Client: edgar.New(), Force: true}
	return w.Run(ctx)
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNALS-HUB OVERHAUL — COMPOSITE SIGNALSCORE (appended block).
// compositeWorkers returns the wave's worker: composite-scorer (10m) — the
// per-symbol SignalScore engine (internal/composite). Every pass it reads the
// LATEST stored calibrated 1d predictions (never recomputing the ensemble),
// ranks their edges on a FORCED cross-sectional curve (top 5% = 10 … bottom
// 5% = 1), and persists one composite_scores row per symbol with the full
// evidence payload: 11 factor tiles (verdict + raw-number evidence + measured
// skill chip from the adaptive attribution + explicit gate reasons) and the
// additive ledger decomposing (calProb − 0.5) per leg — labeled
// "proportional attribution" whenever exactness can't be reconstructed from
// the stored row, never faked. Cadence mirrors the PredictionRunner: hot set
// (crypto + streamed stocks) every run, broad daily-only universe once per
// UTC day (meta key composite_universe_day). HONESTY GATE: with fewer than 30
// symbols carrying fresh usable predictions the whole pass stores NOTHING and
// the worker detail says why — a forced curve over a thin cross-section would
// fabricate 10s and 1s.
func compositeWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.CompositeScorer{St: st},                  // 1d (default)
		&pipeline.CompositeScorer{St: st, Horizon: md.H1w}, // 1w — see EDGE_PLAN.md: momentum/estimate-revision edges are more plausible at 1w than 1d
	}
}

// ─────────────────────────────────────────────────────────────────────────
// TRADINGVIEW SCANNER RATINGS (appended block).
// tvRatingWorkers returns the wave's worker: tv-rating (15m tick) — persists
// TradingView's OWN technical-analysis RATING for our tracked symbols from its
// PUBLIC scanner endpoint (scanner.tradingview.com/{screener}/scan — no
// account, no key; verified live 2026-07-07) into the append-only tv_ratings
// table. Each pass first RESOLVES up to 60 missing stock exchanges via
// TradingView's public symbol-search (our symbols table has no exchange, and
// the scanner needs EXCHANGE:SYMBOL tickers — DRAM/SNXX resolve to CBOE, so
// nothing is hardcoded), caching them in tv_exchange; then BATCH-SCANS the
// "america" screener in one (chunked ≤200) POST over every active stock that
// has a resolved exchange and upserts a rating row with Label(reco_all). Crypto
// is special-cased: our only pair, BTC/USD, maps to BITSTAMP:BTCUSD on the
// "crypto" screener, and its status is reported honestly in the detail string.
// Resolution failures are non-fatal and a scanner network error is logged by
// the supervisor — the fleet NEVER fails, and nothing is fabricated.
// HONESTY: reco_* are TradingView's OWN descriptive TA scores on DELAYED data —
// an EXTERNAL, independent signal, NOT SignalDeck's model and NOT advice.
// /api/tv-rating and the UI carry that caveat verbatim.
func tvRatingWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.TVRatingPoller{St: st, TV: tvscanner.New()},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// TIERED-STORAGE WAVE — PHASE 2: DERIVED-TABLE RETENTION (appended block).
// derivedRetentionWorkers returns the wave's worker: derived-retention (1h) —
// the Downsampler's sibling for the DERIVED tables that grow unbounded
// (scores/score_outcomes past SIGNALDECK_SCORES_RETENTION_D=90d; resolved
// features past SIGNALDECK_FEATURES_RETENTION_D=180d). Every row is exported to
// the cold gzip-CSV archive BEFORE it is pruned; on any archive error the prune
// is SKIPPED and a dq_events(archive_skip) records the fail-safe, so retention
// never silently loses data. predictions + prediction_outcomes (the live track
// record) are NEVER pruned, and unlabeled feature rows are never deleted. Shares
// the daemon-wide cold-archive sink (archiver) with the Downsampler.
func derivedRetentionWorkers(st *store.Store, arc *archive.Archiver) []workers.Worker {
	return []workers.Worker{
		&maintain.DerivedRetention{St: st, Arc: arc},
		// scores-compactor (1h): the NEAR-tier sibling — archives then strips
		// the per-row JSON blobs (scores.components / composite_scores.payload)
		// past 2d and daily-downsamples intraday rows past 30d. The blobs are
		// ~60% of the database file yet only the latest row per symbol renders
		// them; DerivedRetention's 90d far tier can't touch rows that young.
		&maintain.ScoresCompactor{St: st, Arc: arc},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// SELF-AUDIT / DRIFT-WATCHDOG WAVE (appended block).
// selfAuditWorkers returns the wave's worker: self-audit (6h tick, gated to
// once per UTC day via meta self_audit_day) — the honesty watchdog that
// MEASURES the platform's own reliability from resolved history and records
// findings, deterministically (no LLM), to the queryable self_audit table +
// insights(kind self_audit):
//   - CALIBRATION DRIFT per horizon: reliability (mean |cal_prob − realized|)
//     this window vs the last audit — flagged "degrading" when it worsens past
//     a threshold;
//   - FACTOR-IC SIGN FLIP per ensemble leg: the adaptive attribution's IC
//     flipping sign vs the last audit (model instability);
//   - PREDICTION BIAS per horizon: mean(cal_prob) vs realized base rate —
//     flagged over/under-confident.
//
// Every check is gated at n>=30 independent resolutions (below → status
// "insufficient", never a false alarm). Served read-only at GET /api/self-audit.
func selfAuditWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.SelfAuditor{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// DATA-EXPANSION WAVE (appended block).
// dataExpansionWorkers returns the wave's six workers — every source FREE and
// KEYLESS, every dataset stored + served as DESCRIPTIVE context with its
// caveat verbatim (nothing becomes a scored factor in this wave), every
// worker degrading honestly (skip-with-reason / dq event, NEVER a fleet
// failure), every fetch carrying the shared declarative UA + a timeout:
//   - finra-shortint (12h): FINRA's BI-MONTHLY short interest files
//     (settlement dates = 15th/EOM; publication lags ~9 business days, so
//     each run probes up to 3 cycles newest-first until one 200s; 403 = not
//     yet published = honest skip). Universe-scoped like finra-shorts.
//     CAVEAT: settlement-dated, ~2wks lagged — positioning, not advice.
//   - crypto-perp (15m): ONE POST per pass to Hyperliquid's public info API
//     (metaAndAssetCtxs, index-aligned arrays) snapshots funding/OI/mark for
//     tracked crypto pairs. CAVEAT: one DEX venue — a positioning proxy.
//   - cot-poller (24h): CFTC Commitments of Traders legacy futures-only
//     report via the free Socrata API for a curated contract set (E-mini
//     S&P, Nasdaq mini, bitcoin/ether complex); ~1y chunked backfill on
//     first run. CAVEAT: weekly lag — positioning, NOT prediction.
//   - stocktwits-fetcher (15m): page-snapshot sentiment tallies (~30 newest
//     messages) for watchlist + hot-set stocks only (news-fetcher-style
//     scope), paced ≤1 req/2s; 404s cached so unlisted symbols are never
//     re-hammered. CAVEAT: self-selected retail crowd, descriptive only.
//   - wiki-attention (24h): daily Wikipedia page views (official Wikimedia
//     REST, agent=user) for the same scope, company-name → article via a
//     suffix-stripping heuristic with FAILURES CACHED in wiki_article so
//     unresolved names are never re-hammered. CAVEAT: attention proxy — not
//     a trading signal.
//   - cboe-pc (6h): CBOE's per-day options market statistics (market-wide
//     put/call ratios + volumes), reusing the finra-shorts publish gate
//     (~18:30 ET) + meta day-key dedup + ~30-trading-day first-run backfill;
//     CBOE's CDN 403s absent days ⇒ ErrNotAvailable ⇒ honest skip. CAVEAT:
//     index puts are largely hedges — a high index P/C is NOT directly
//     bearish.
//
// All endpoint shapes were probed + verified live 2026-07-10 before this
// wave shipped; every client lives in internal/ingest/* with fixture-driven
// httptest coverage (NO live-network tests).
func dataExpansionWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.ShortInterestPoller{St: st, Client: finra.NewSI()},
		&pipeline.CryptoPerpPoller{St: st, Client: hyperliquid.New()},
		&pipeline.COTPoller{St: st, Client: cftc.New()},
		&pipeline.StocktwitsFetcher{St: st, Client: stocktwits.New()},
		&pipeline.WikiAttention{St: st, Client: wikimedia.New()},
		&pipeline.CboePCPoller{St: st, Client: cboe.New()},
		// tv-quotes (60s, market-hours gated; crypto 24/7): near-real-time
		// quote tape from TradingView's public scanner — rtc real-time Cboe
		// One composite when present, else the 15-min-delayed close, per-row
		// flagged; full-market day volume. A TAPE (rows >2h pruned inline) —
		// bars stay the durable record. CAVEAT: descriptive supplement to the
		// bar record, not a replacement.
		&pipeline.TVQuotesPoller{St: st, TV: tvscanner.New()},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// NEWS-TRENDS + STRATEGY-LAB wave (appended block).
// newsTrendsStrategyLabWorkers returns the wave's two workers:
//   - news-trends (30m): per-symbol daily headline-volume z vs the symbol's
//     OWN trailing-30d baseline (gate: >=10 prior days with any news, else
//     the z is an honest NULL) stored in news_trends and joined into the
//     prediction feature vector as news_vol_z (featureVersion 4 — the GBM
//     leg's OOS-lift gate decides whether it ever influences the live
//     blend); plus, once per 6h, ONE deterministic fleet insight (kind
//     news_trends) listing the top-10 trending headline tokens. CAVEAT
//     (verbatim everywhere): headline-frequency trend — descriptive
//     attention, not a forecast.
//   - strategy-lab (24h tick, once/day meta gate): replays 8 classic
//     PUBLISHED strategies (golden cross, Donchian 20, Connors RSI-2,
//     Jegadeesh-Titman 12-1 momentum, MACD, Bollinger mean-reversion,
//     52w-high breakout, absolute dual momentum — internal/stratlib, each
//     citing its origin, strictly no-lookahead) through the EXISTING
//     bias-free next-bar-fill backtest engine on ~2y of our own daily bars
//     with per-market costs, scoped to the streamed hot set + crypto only.
//     Results honor the engine's CAGRReported/WinRateMeaningful honesty
//     flags; one weekly insight (kind strategy_lab) names the fleet's top-3
//     by median Sharpe. CAVEAT (verbatim everywhere): in-sample history on
//     our own bars, not live performance and not advice.
func newsTrendsStrategyLabWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.NewsTrends{St: st},
		&pipeline.StrategyLab{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// CROSS-SECTIONAL ALPHA wave (appended block).
// alphaXWorkers returns the wave's worker: alpha-trainer (6h) — the pooled
// CROSS-SECTIONAL model, the shape every serious competitor (Danelfin's GBT
// ensemble, Zacks rank, the quant funds) actually uses: ONE gbm trained over
// the WHOLE universe's labeled feature rows at once (v3+ versions pooled —
// union-of-keys flatten; absent-vs-zero dilution can only dampen a grade,
// never flatter it), where each row's label is "beat the same-UTC-day
// cross-section MEDIAN" — relative alpha, with the market component cancelled
// out of the label. Days with <10 resolved rows are dropped (thin
// cross-section); grading is PURGED WALK-FORWARD BY DAY (2-trading-day
// embargo, de Prado) and refuses below 1000 pooled train rows / 200 OOS test
// rows. Each pass stores the model + grade in alphax_models and — ONLY when
// the measured OOS lift > 0 — each symbol's current P(top half) into
// model_forecasts (model="alphax"), deleting stale scores whenever a regrade
// lands gated. The PredictionRunner/ensemble deliberately does NOT consume
// alphax yet: that integration is a later wave, after the stored grade proves
// out (core doctrine — a leg enters the live blend ONLY with measured OOS
// lift > 0). CAVEAT (verbatim everywhere): pooled cross-sectional model —
// predicts RELATIVE outperformance vs the same-day universe median; gated off
// until measured OOS lift > 0; backtested, not a live track record.
func alphaXWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.AlphaXTrainer{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// DATA-SOURCE FRESHNESS wave (appended block).
// sourceAuditWorkers returns the wave's worker: source-audit (1h) — the
// "self-honest, not self-healing" watchdog for EXTERNAL data. The fleet
// watchdog (internal/health) flags workers that stop RUNNING; this flags
// sources that stop PRODUCING while their worker keeps returning ok (a scraper
// that drifts, a source that goes quietly dead). Every hour it asks
// internal/srchealth how old each registered source's newest row is against a
// market-calendar-aware budget and records a dq_events(source_stale) per stale
// source (deduped per source per UTC day). Market-gated stock sources are NOT
// flagged when the exchange is legitimately closed; crypto/24-7 sources are
// always checked. The tv_signals webhook source is judged ONLY when a webhook
// secret is configured AND the market is open — the case that catches a silently
// dead tunnel/webhook while trading is live. Served live at GET
// /api/source-health. BEFORE the watchdog spec snapshot so it's health-audited.
func sourceAuditWorkers(cfg config.Config, st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.SourceAuditor{St: st, WebhookSecretSet: cfg.TVWebhookSecret != ""},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// CANDLESTICK-PATTERNS wave (appended block).
// patternStatsWorkers returns the wave's worker: pattern-stats (24h, gated
// once per UTC day) — recomputes each HOT symbol's (streamed hot set + crypto)
// MEASURED candlestick edge from ~2 years of daily bars
// (internal/candles.MeasureEdges): the share of each directional pattern's
// historical firings whose forward 5-bar move went the pattern's way, plus the
// mean forward return and the sample size, stored per (symbol, pattern,
// horizon) ONLY when the sample clears n>=15. The /api/candle-patterns handler
// reads these back to annotate live firings; the model-fed pattern_bias
// feature (featureVersion 7) is graded by the OOS-lift referee like every other
// feature. CAVEAT (verbatim everywhere): candlestick patterns are WEAK,
// context-only signals; the measured hit-rate is descriptive, not advice.
func patternStatsWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.PatternStatsRunner{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// SMART MONEY FACTS wave (appended block).
// smartMoneyWorkers returns the wave's worker: smart-money-scorer (1h) — turns
// ALREADY-INGESTED positioning data (SEC Form 4 open-market insider trades,
// FINRA short interest / Reg SHO short volume, crypto perp funding, SEC 13F
// holdings) into ONE transparent, decomposed per-symbol Smart Money Score via
// the PURE internal/smartmoney engine, and emits two day-deduped positioning
// events (insider_cluster, squeeze_setup). Best-effort per source (an absent
// source drops out, never imputed as 0); a symbol with no positioning data is
// simply not scored. HONESTY: a read of what informed participants are DOING,
// NOT a price forecast — /api/smart-money carries that caveat verbatim.
func smartMoneyWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.SmartMoneyScorer{St: st},
	}
}

// confluenceWorkers builds the CONFLUENCE GATE + MONEY SCOREBOARD wave workers:
// the scorer (30m — flags setups only on INDEPENDENT-family agreement + forward-
// tracks them) and the resolver (15m — grades matured setups against realized
// bars for the expected-profit scoreboard). Both are pure glue over the PURE
// internal/confluence engine + the store; no broker, no lookahead.
func confluenceWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.ConfluenceScorer{St: st},
		&pipeline.ConfluenceResolver{St: st},
	}
}

// offsiteBackupDir resolves the OFF-MACHINE backup destination for the nightly
// backup worker. SIGNALDECK_OFFSITE_BACKUP_DIR overrides; the value "off" (or
// "none") explicitly disables the offsite copy. Default: the iCloud Drive
// SignalDeckBackups folder (verified to exist), so a total-loss event — the
// single SQLite file on the one Mac dying — is survivable out of the box. An
// unresolvable home dir degrades to disabled (local backup still runs).
func offsiteBackupDir() string {
	switch v := strings.TrimSpace(os.Getenv("SIGNALDECK_OFFSITE_BACKUP_DIR")); strings.ToLower(v) {
	case "off", "none", "-":
		return ""
	case "":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "SignalDeckBackups")
	default:
		return v
	}
}

// discoveryEngineWorkers is the research discovery engine wave: the historical
// evidence-base builder and the era-grading research engine. alpacaClient may
// be nil (no keys) — hist-backfill then computes rows from whatever daily bars
// already exist instead of deepening them.
func discoveryEngineWorkers(st *store.Store, alpacaClient *alpaca.Client) []workers.Worker {
	return []workers.Worker{
		&pipeline.HistoryBackfillWorker{St: st, Alpaca: alpacaClient},
		&pipeline.ResearchEngineWorker{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// CREDIBILITY WAVE — LIVE REGIME-FORECAST GRADING (appended block).
// regimeOutcomeWorkers returns the wave's worker: regime-outcome-runner (6h)
// that (1) freezes every current regime forecast (trend21/trend63/liquidity21/
// vol21) into regime_outcomes at most once per (symbol, kind, UTC-day) — call,
// conviction and CLAIMED accuracy captured before the answer is known; (2) once
// the horizon has elapsed (horizon_days*1.45 calendar days AND enough newer
// daily bars), recomputes the REALIZED regime label with the exact engine
// arithmetic (internal/structregime Resolve*At) and grades the call; and (3)
// writes a deterministic plain-English postmortem for every HIGH-conviction
// (>=0.8) miss, including the base rate the claimed accuracy itself implies.
// This is the loop that lets /api/track-record show LIVE regime accuracy next
// to the claimed walk-forward tiers — the credibility surface.
func regimeOutcomeWorkers(st *store.Store) []workers.Worker {
	return []workers.Worker{
		&pipeline.RegimeOutcomeWorker{St: st},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// WAVE 2 — COLD-LOAD PRECOMPUTE + WEEKLY DIGEST (appended block).
// cacheWarmWorkers returns the cache-warmer (60s): each tick calls through
// the settable *target into api.Deps.WarmCaches, rebuilding the shared
// dashboard cache and the default /api/movers response-cache entry exactly
// the way the handlers would — so the first request after a restart (or
// during a worker write-sweep) is a 3ms cache hit, never a 30-55s cold
// build. The indirection exists because the fleet is assembled (and the
// watchdog snapshot taken) before the API deps — and their isolated reader
// pool — are built; until the target is set each tick is an honest no-op.
func cacheWarmWorkers(target *func(context.Context) error) []workers.Worker {
	return []workers.Worker{
		&pipeline.CacheWarmer{Warm: func(ctx context.Context) error {
			if *target == nil {
				return nil
			}
			return (*target)(ctx)
		}},
	}
}

// digestWorkers returns the weekly-digest worker (1h tick; fires once per NY
// week from Sunday 17:00 ET with meta week-key dedup, catching up later in
// the week if the daemon was down): composes a plain-text digest from STORED
// data only — per watchlist-active-symbol regime changes this week
// (regime_outcomes earliest frozen call vs the current regime_forecasts row),
// resolved regime outcomes + live accuracy so far (per kind, 30-resolution
// honesty gate), the top 3 current highest-conviction validated calls, and
// the flagship paper books' week P&L — stores it in meta digest_last_text
// (served at GET /api/digest), and delivers it via the ONE daemon-wide
// notifier. No transport configured = honest no-op ("digest skipped: no
// transport"), never a failure.
func digestWorkers(st *store.Store, remote *notify.Notifier) []workers.Worker {
	return []workers.Worker{
		&briefing.DigestWorker{St: st, Notifier: remote},
	}
}
