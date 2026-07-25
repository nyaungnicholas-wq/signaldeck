// Split-corruption repair worker (2026-07-24).
//
// Incremental bar fetches ask the provider for bars NEWER than what we hold,
// with adjustment=split. The provider re-adjusts its entire history whenever a
// split occurs; we only take the tail. Stored history therefore keeps the OLD
// price basis while new bars arrive on the new one, welding a permanent fake
// ±50-95% move into the series at the split date.
//
// Measured on the live DB at build time: 521 such discontinuities across 185
// symbols, 172 of them inside the last 400 sessions across 107 symbols — i.e.
// actively inside the window every shipped predictor reads.
//
// Repair is simply a FULL re-backfill: the provider returns two years on one
// consistent basis and the upsert overwrites the stale-basis rows. No delete is
// needed, so a failed repair leaves the old data rather than a hole.
//
// The worker is deliberately conservative. It repairs only symbols whose
// corruption is RECENT (inside the predictors' lookback), rate-limits itself so
// a first pass over a large universe cannot exhaust the free-tier API budget,
// and records every repair so the same symbol is not re-fetched forever.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/splitfix"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// SplitRepair scans stored daily bars for unadjusted-split discontinuities and
// re-backfills the affected symbols.
type SplitRepair struct {
	St     *store.Store
	Alpaca *alpaca.Client

	// MaxRepairsPerPass bounds API spend on any single pass. Zero uses the
	// default; the backlog drains over subsequent passes.
	MaxRepairsPerPass int

	// RecheckAfter is how long a repaired symbol is left alone before it is
	// eligible again — a symbol that legitimately splits twice still gets
	// fixed, but a repair that fails to clear the flag cannot spin.
	RecheckAfter time.Duration
}

func (w *SplitRepair) Name() string            { return "split-repair" }
func (w *SplitRepair) Interval() time.Duration { return 6 * time.Hour }

const (
	defaultMaxRepairsPerPass = 12
	defaultRecheckAfter      = 72 * time.Hour
)

func (w *SplitRepair) Run(ctx context.Context) (string, error) {
	if w.Alpaca == nil {
		return "skip — no Alpaca client (stock backfills unavailable)", nil
	}
	maxRepairs := w.MaxRepairsPerPass
	if maxRepairs <= 0 {
		maxRepairs = defaultMaxRepairsPerPass
	}
	recheck := w.RecheckAfter
	if recheck <= 0 {
		recheck = defaultRecheckAfter
	}

	syms, err := w.St.ActiveStockSymbols(ctx, nil)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	cutoff := now - int64(recheck.Seconds())

	scanned, flagged, repaired, failed := 0, 0, 0, 0
	for _, s := range syms {
		select {
		case <-ctx.Done():
			return fmt.Sprintf("canceled after %d scanned", scanned), ctx.Err()
		default:
		}
		scanned++

		bars, err := w.St.Bars(ctx, s.ID, md.TF1d, 0, now+86400, 0)
		if err != nil || len(bars) < 2 {
			continue
		}
		in := make([]splitfix.Bar, len(bars))
		for i, b := range bars {
			in[i] = splitfix.Bar{Ts: b.Ts, Close: b.Close, Volume: b.Volume}
		}
		rep := splitfix.Detect(in)
		if !rep.Recent {
			continue // clean, or damage older than any predictor reads
		}
		flagged++

		// Skip symbols repaired recently: if a repair did not clear the flag,
		// hammering the provider will not change that, and the honest state is
		// a persistent record rather than an infinite retry.
		if last, ok, err := w.St.LastSplitRepair(ctx, s.ID); err == nil && ok && last > cutoff {
			continue
		}
		if repaired+failed >= maxRepairs {
			continue // budget spent; the rest drains next pass
		}

		worst := rep.Suspects[len(rep.Suspects)-1]
		if _, err := w.Alpaca.BackfillDaily(ctx, w.St, s.ID, s.Symbol); err != nil {
			failed++
			_ = w.St.RecordSplitRepair(ctx, s.ID, worst.Date, worst.NearestName,
				len(rep.Suspects), false, err.Error(), now)
			continue
		}
		repaired++
		_ = w.St.RecordSplitRepair(ctx, s.ID, worst.Date, worst.NearestName,
			len(rep.Suspects), true, "", now)
	}

	return fmt.Sprintf("scanned %d, contaminated %d, repaired %d, failed %d",
		scanned, flagged, repaired, failed), nil
}
