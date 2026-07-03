package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/api"
	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/hud"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cryptohist"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cryptolive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	"github.com/nyaungnicholas-wq/signaldeck/internal/maintain"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
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

	// LLM provider (NVIDIA by default). Disabled/no-op until a key is set.
	llmClient := llm.New(cfg.LLMKey, cfg.LLMBaseURL, cfg.LLMModel, cfg.LLMDailyCap)
	if llmClient.Enabled() {
		slog.Info("AI layer enabled", "model", cfg.LLMModel, "dailyCap", cfg.LLMDailyCap)
	} else {
		slog.Warn("AI layer disabled — no LLM key (set SIGNALDECK_NVIDIA_KEY in daemon/.env)")
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
					_ = backfiller.Enqueue(sym)
				}
			}
		}
		_ = st.SetMeta(ctx, "seeded_v1", time.Now().Format(time.RFC3339))
		slog.Info("first boot: seeded watchlist", "crypto", cfg.CryptoSymbol, "stocks", seedStocks)
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
		&maintain.Downsampler{St: st},
		&maintain.OutcomeResolver{St: st},
		&maintain.DQAuditor{St: st},
		hud.New(st, cfg.HudURL),
		&pipeline.AnalystWorker{St: st, LLM: llmClient},
		&pipeline.WatcherWorker{St: st, LLM: llmClient},
	}
	if streamer != nil {
		fleet = append(fleet, streamWorker{streamer}, &pipeline.StockBars{St: st, Alpaca: alpacaClient})
	}

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
	if err := bf.Enqueue(sym); err != nil {
		return md.Symbol{}, err
	}
	if market == md.Stocks && streamer != nil {
		refreshStreamerSymbols(ctx, st, streamer)
	}
	return sym, nil
}

// refreshStreamerSymbols pushes the current active stock set to the ws stream.
func refreshStreamerSymbols(ctx context.Context, st *store.Store, streamer *alpaca.Streamer) {
	syms, err := st.ListSymbols(ctx, true)
	if err != nil {
		slog.Warn("refresh streamer symbols", "err", err)
		return
	}
	var stocks []string
	for _, s := range syms {
		if s.Market == md.Stocks {
			stocks = append(stocks, s.Symbol)
		}
	}
	streamer.SetSymbols(stocks)
}
