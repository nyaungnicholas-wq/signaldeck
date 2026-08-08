// forecastmon runs the forecast-integrity check once against the live database
// and prints what it found.
//
// The same code the daemon's forecast-monitor worker runs, on demand. It exists
// because "the monitor is wired in" and "the monitor detects anything" are
// different claims, and this session's rule is that merging code proves neither.
// Run it after any change to the forecaster and read the output.
//
//	go run ./cmd/forecastmon --days 30
//	go run ./cmd/forecastmon --days 3        # the post-collapse holdout only
//
// Exit status is 1 when a check trips, so it composes into a shell gate.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/forecastmon"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func main() {
	days := flag.Int("days", 14, "lookback window in days")
	horizon := flag.String("horizon", "1d", "forecast horizon")
	dbPath := flag.String("db", "", "database path (default: config)")
	flag.Parse()

	path := *dbPath
	if path == "" {
		path = config.Load().DBPath
	}
	st, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", path, err)
		os.Exit(2)
	}
	defer st.Close() //nolint:errcheck

	fmt.Printf("database : %s\n", filepath.Clean(path))
	fmt.Printf("horizon  : %s over the last %d day(s)\n\n", *horizon, *days)

	// Print the cross-section first: it is the evidence behind any verdict, and
	// a verdict whose inputs are not shown is how the last failure survived.
	src := forecastmon.NewStoreSource(st)
	since := time.Now().AddDate(0, 0, -*days)
	stats, err := src.DayStats(context.Background(), *horizon, since)
	if err != nil {
		fmt.Fprintf(os.Stderr, "day stats: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("%-12s %8s %10s %8s  %s\n", "DAY", "SYMBOLS", "DISTINCT", "RATIO", "")
	for _, d := range stats {
		flag := ""
		if d.Collapsed() {
			flag = "  <-- COLLAPSED"
		}
		fmt.Printf("%-12s %8d %10d %8.3f%s\n", d.Day, d.Symbols, d.DistinctProbs, d.DistinctRatio(), flag)
	}

	m := &forecastmon.Monitor{Src: src, Horizon: *horizon, Window: time.Duration(*days) * 24 * time.Hour}
	detail, err := m.Run(context.Background())
	fmt.Printf("\n%s\n", detail)
	if err != nil {
		fmt.Printf("\nFAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nOK: no collapse, no inversion.")
}
