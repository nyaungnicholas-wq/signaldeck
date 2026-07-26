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

	scanned, flagged, repaired, failed, confirmedReal := 0, 0, 0, 0, 0
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
		// INTRADAY REPAIR (2026-07-26). Detection runs on daily bars because
		// that is the series long enough to see a split, but the corruption is
		// not confined there: the 1h and 1m series are fetched incrementally on
		// short windows, so after a split every bar older than that window keeps
		// the pre-split basis. Repairing only the daily series left ~9.2M 1m and
		// ~0.55M 1h rows welded to a stale basis with NO detector that could ever
		// see them — a 4:1 split manufactures a -75% intraday bar that the
		// maxSaneReturn guard never inspects.
		//
		// Best-effort by design: the daily repair above is the one that decides
		// the verdict below, so an intraday refetch that fails must not turn a
		// successful daily repair into a recorded failure. It is logged as a dq
		// event instead, because silently leaving known-corrupt intraday history
		// in place is exactly the state this fix exists to end.
		w.repairIntraday(ctx, s.ID, s.Symbol, now)

		// VERIFY, then believe the provider. Re-running detection on the
		// refetched bars is what separates the two cases a jump alone cannot:
		//
		//   gone  -> the stored series really was on a stale split basis, and
		//            the refetch repaired it.
		//   still -> the provider, asked for a clean two-year window on one
		//            consistent basis, returned the same move. That is the
		//            provider asserting the move is REAL, and the detector was
		//            wrong. Confirmed live on BMNR (+695% the day it announced
		//            an ETH treasury) and CIRC (+202% post-IPO) — both landed
		//            near clean ratios by coincidence.
		//
		// Recording the second case as a failure with its reason is what stops
		// the worker re-fetching a genuine move forever, and leaves a readable
		// trail of the detector's false positives.
		after, err := w.St.Bars(ctx, s.ID, md.TF1d, 0, now+86400, 0)
		if err == nil && len(after) >= 2 {
			in2 := make([]splitfix.Bar, len(after))
			for i, b := range after {
				in2[i] = splitfix.Bar{Ts: b.Ts, Close: b.Close, Volume: b.Volume}
			}
			if splitfix.Detect(in2).Recent {
				confirmedReal++
				_ = w.St.RecordSplitRepair(ctx, s.ID, worst.Date, worst.NearestName,
					len(rep.Suspects), false,
					"provider returned the same move on a clean refetch — treating as a REAL price move, not a split",
					now)
				continue
			}
		}
		repaired++
		_ = w.St.RecordSplitRepair(ctx, s.ID, worst.Date, worst.NearestName,
			len(rep.Suspects), true, "", now)
	}

	return fmt.Sprintf("scanned %d, contaminated %d, repaired %d, confirmed-real %d, failed %d",
		scanned, flagged, repaired, confirmedReal, failed), nil
}

// intradayRepairDays bounds how much intraday history is refetched after a
// split repair. Alpaca serves ~30 days of 1m to free accounts, so asking for
// more buys nothing; 1h reaches back further and is cheap on the multi-symbol
// endpoint.
const (
	intradayRepairMinuteDays = 30
	intradayRepairHourDays   = 400
)

// repairIntraday refetches the 1h and 1m series for one symbol on a single
// consistent split basis, so the intraday history stops disagreeing with the
// daily series the repair just corrected.
//
// Failures are recorded as dq events rather than propagated: the daily repair is
// what the verify-and-learn verdict is computed from, and letting a transient
// intraday fetch error mark that repair "failed" would send the symbol back
// through the queue forever.
func (w *SplitRepair) repairIntraday(ctx context.Context, symbolID int64, symbol string, now int64) {
	resolve := func(string) (int64, bool) { return symbolID, true }
	syms := []string{symbol}

	nowT := time.Unix(now, 0).UTC()
	if _, err := w.Alpaca.BackfillHourlyMulti(ctx, w.St, syms, resolve,
		nowT.AddDate(0, 0, -intradayRepairHourDays)); err != nil {
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts: now, Kind: "split_repair_intraday_failed",
			Detail: fmt.Sprintf("%s: 1h refetch after split repair: %v — daily series is "+
				"repaired but hourly history may still be on a stale basis", symbol, err),
		})
	}
	if _, err := w.Alpaca.BackfillMinuteMulti(ctx, w.St, syms, resolve,
		nowT.AddDate(0, 0, -intradayRepairMinuteDays)); err != nil {
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts: now, Kind: "split_repair_intraday_failed",
			Detail: fmt.Sprintf("%s: 1m refetch after split repair: %v — daily series is "+
				"repaired but minute history may still be on a stale basis", symbol, err),
		})
	}
}
