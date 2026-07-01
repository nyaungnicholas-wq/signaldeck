// Command signaldeckd is the SignalDeck data daemon: ingest workers (crypto +
// stocks), the signal/expectancy/insight engines, trader-hud sync, and the
// JSON API — one binary, many in-app agents (see internal/workers).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const version = "0.1.0-dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	fmt.Printf("signaldeckd v%s\n", version)
	if *showVersion {
		return
	}

	cfg := config.Load()
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		slog.Error("create data dir", "err", err)
		os.Exit(1)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		slog.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close() //nolint:errcheck

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("signaldeckd started", "db", cfg.DBPath, "http", cfg.HTTPAddr,
		"alpaca", cfg.HasAlpaca(), "hud", cfg.HudURL)

	// Workers + API are wired in as their packages land (see run.go).
	run(ctx, cfg, st)
}
