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
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/congress"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cryptohist"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cryptolive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/fred"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/news"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	"github.com/nyaungnicholas-wq/signaldeck/internal/maintain"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
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

// run wires the whole daemon: store-backed agents + the JSON API.
func run(ctx context.Context, cfg config.Config, st *store.Store) {
	// ── clients ─────────────────────────────────────────────────────
	var alpacaClient *alpaca.Client
	if cfg.HasAlpaca() {
		alpacaClient = alpaca.New(cfg.AlpacaKey, cfg.AlpacaSecret)
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
	llmClient := llm.New(cfg.LLMKey, cfg.LLMBaseURL, cfg.LLMModel, cfg.LLMDailyCap)
	llm.SetSpendStore(llmClient, st)
	if llmClient.Enabled() {
		slog.Info("AI layer enabled", "model", cfg.LLMModel, "dailyCap", cfg.LLMDailyCap)
	} else {
		slog.Warn("AI layer disabled — no LLM key (set SIGNALDECK_NVIDIA_KEY in daemon/.env)")
	}

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
		St: st, Dir: backupDir, Keep: 7, FirstRunDelay: 5 * time.Minute,
	})
	// Alerts + daily-briefing wave (constructor appended at the END of this
	// file) — must join the fleet BEFORE the watchdog snapshots its specs.
	fleet = append(fleet, alertBriefingWorkers(st, llmClient)...)
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
	// Tiered-storage wave (constructor appended at the END of this file) — the
	// storage governor (WAL checkpoint + threshold VACUUM); BEFORE the watchdog
	// spec snapshot so it's health-audited like every other worker.
	fleet = append(fleet, storageWorkers(st)...)
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
	})

	// ── API ─────────────────────────────────────────────────────────
	deps := api.Deps{
		St:      st,
		Cfg:     cfg,
		Version: version,
		Started: time.Now(),
		LLM:     llmClient,
		CurrentState: func(ctx context.Context, symbolID int64) (map[md.Horizon]string, error) {
			return pipeline.CurrentState(ctx, st, symbolID)
		},
		Subscribe: func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error) {
			return subscribe(ctx, st, alpacaClient, backfiller, streamer, symbol, market)
		},
	}
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

// subscribe validates + registers a symbol and kicks off its backfill.
func subscribe(ctx context.Context, st *store.Store, ac *alpaca.Client,
	bf *pipeline.Backfiller, streamer *alpaca.Streamer,
	symbol string, market md.Market,
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
	sym, err := st.UpsertSymbol(ctx, symbol, market, name)
	if err != nil {
		return md.Symbol{}, err
	}
	// Broad-universe wave: an explicit subscribe (manual add or discovery
	// auto-add) puts the symbol in the STREAMED HOT SET. Promote its stream
	// flag so it joins the live ws + full 1m pipeline (and counts against the
	// stream cap). A daily-only universe symbol being subscribed is thereby
	// promoted; a fresh symbol is created with stream=1.
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
func alertBriefingWorkers(st *store.Store, llmClient llm.Client) []workers.Worker {
	return []workers.Worker{
		&alerts.Runner{St: st},
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
	w := &discovery.Worker{St: st}
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
	if cfg.HasAlpaca() {
		p.Alpaca = ac
	}
	return []workers.Worker{p}
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
