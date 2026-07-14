// CANDLESTICK-PATTERNS wave — the pattern-stats worker (24h).
//
// Once per UTC day it recomputes each HOT symbol's MEASURED candlestick edge
// from ~2 years of daily bars (internal/candles.MeasureEdges): for every
// directional pattern that fired historically, the share of firings whose
// forward K-bar move went the pattern's way, plus the mean forward return and
// the sample size. Only patterns clearing MinPatternN (=15) survive; the rest
// are withheld, so a stored row is always backed by a real sample. The
// /api/candle-patterns handler reads these back to annotate live firings with
// "has this shape ever paid on THIS symbol?".
//
// SCOPE (hot set + crypto only): the 2y daily walk detects patterns at every
// bar, which is far too heavy to run across the ~500-name broad daily-only
// universe every day. The streamed hot set plus crypto is where the candle
// view is actually surfaced, so that is what the worker measures.
//
// HONESTY: a measured tendency on this instrument's own past — descriptive,
// weak, context-only, and not advice. Nothing here enters the prediction blend;
// the model-fed pattern_bias feature (predict.go) is graded by the OOS-lift
// referee like every other feature.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/candles"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// patternDailyLookback is the daily-bar window the measured edge is computed
	// over — ~2 years, sized to cover both ~252-session stock years and
	// 365-day crypto years.
	patternDailyLookback = 750
	// patternHorizon is the forward window (bars) the edge is measured over.
	// Mirrors candles.DefaultHorizon so the stored horizon is stable.
	patternHorizon = candles.DefaultHorizon
	// patternStatsMetaKey is the once-per-UTC-day gate cursor.
	patternStatsMetaKey = "pattern_stats_day"
)

// PatternStatsRunner is the pattern-stats worker.
type PatternStatsRunner struct {
	St *store.Store
}

// Name implements workers.Worker.
func (w *PatternStatsRunner) Name() string { return "pattern-stats" }

// Interval implements workers.Worker.
func (w *PatternStatsRunner) Interval() time.Duration { return 24 * time.Hour }

// Run recomputes the measured candlestick edge for every hot symbol, at most
// once per UTC day.
func (w *PatternStatsRunner) Run(ctx context.Context) (string, error) {
	// Once-per-UTC-day gate: the 24h interval already spaces runs a day apart,
	// but a restart could re-trigger the pass; the meta cursor makes it
	// idempotent within a UTC day. Measured edge over 2y of daily bars barely
	// moves intraday, so a second same-day pass is pure duplicate work.
	today := time.Now().UTC().Format("2006-01-02")
	if prev, _ := w.St.GetMeta(ctx, patternStatsMetaKey); prev == today {
		return "pattern stats already computed today (UTC)", nil
	}

	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	symbolsDone, rowsWritten, edged := 0, 0, 0
	for _, s := range syms {
		if s.Market != md.Crypto && !s.Stream {
			continue // hot set + crypto only (see file header)
		}
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, patternDailyLookback)
		if err != nil {
			return "", fmt.Errorf("%s: %w", s.Symbol, err)
		}
		stats := candles.MeasureEdges(daily, patternHorizon)
		symbolsDone++
		if len(stats) == 0 {
			continue // no pattern cleared MinPatternN on this symbol's history
		}
		edged++
		for _, es := range stats {
			if err := w.St.UpsertPatternStat(ctx, store.PatternStat{
				SymbolID: s.ID, Pattern: es.Pattern, Horizon: es.Horizon,
				HitRate: es.HitRate, MeanFwd: es.MeanFwd, N: es.N,
			}); err != nil {
				return "", fmt.Errorf("%s %s: %w", s.Symbol, es.Pattern, err)
			}
			rowsWritten++
		}
	}
	// Mark the day done only after a clean pass, so a mid-run error simply
	// retries on the next tick.
	_ = w.St.SetMeta(ctx, patternStatsMetaKey, today)
	return fmt.Sprintf("measured candlestick edge over %d hot symbol(s): %d with a gated pattern, %d stat row(s) written",
		symbolsDone, edged, rowsWritten), nil
}
