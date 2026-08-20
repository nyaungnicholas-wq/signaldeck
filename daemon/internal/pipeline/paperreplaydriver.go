package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// replayBusyRetries is how many times a session is re-attempted when the write
// lock is held elsewhere.
//
// A replay is a long batch job running against a database a live fleet is still
// writing to. The store already sets busy_timeout(5000), so reaching this code
// means five seconds of contention, not a missing pragma — the first attempt at
// this cost the run 0 of 78 sessions. Re-attempting a session is safe because
// ApplyPaperStep is atomic and its cursor guard makes re-running the same bar a
// no-op, so a retry can only complete work or do nothing.
const replayBusyRetries = 10

// busy reports the one error worth retrying. Anything else is a real fault and
// must stop the run rather than be papered over by a loop.
func busy(err error) bool {
	return err != nil && strings.Contains(err.Error(), "database is locked")
}

// ── THE REPLAY DRIVER ────────────────────────────────────────────────────────
//
// The live worker takes ONE step per invocation, at the newest daily bar. That
// is why a replay cannot be performed by resetting the cursor and running it:
// it would take a single step from the reset point to today and mark equity
// once, producing a two-point line rather than a curve.
//
// This walks the sessions in ascending order and takes one pass per bar, which
// is the only ordering under which the book's reads of its OWN tables are
// point-in-time by construction.

// ReplayReport is what a reconstruction produced, and what it refused.
type ReplayReport struct {
	Strategies []string
	FromTs     int64
	ToTs       int64
	Bars       int // sessions stepped
	Retries    int // sessions re-attempted because the write lock was held
	Acted      int // passes that applied a step
	Refused    int // entries the EV engine or the risk gate turned down
	Stranded   int // wanted exits the execution model could not price
	Statuses   []string
}

// ReplayRange reconstructs the book across [from, to] and returns what it did.
//
// It REFUSES up front rather than partway through, on three counts:
//   - an empty StrategySuffix, which would overwrite the book that actually ran;
//   - a destination that already holds rows, which would append this run to a
//     previous one and silently double the record;
//   - a window the universe record does not cover, because a bar whose universe
//     was never recorded cannot be reconstructed and falling back to today's
//     active flag would judge a past day against today's survivors.
//
// Refusing before the first write matters: a partial reconstruction is worse
// than none, because it looks like a complete curve.
func (w *PaperTrader) ReplayRange(ctx context.Context, from, to int64, cfg ReplayConfig) (ReplayReport, error) {
	rep := ReplayReport{FromTs: from, ToTs: to}
	if cfg.StrategySuffix == "" {
		return rep, fmt.Errorf("replay refused: StrategySuffix is empty, which would write over the live book")
	}
	if from > to {
		return rep, fmt.Errorf("replay refused: from=%d is after to=%d", from, to)
	}

	for _, s := range paperStrategies {
		name := s.Name + cfg.StrategySuffix
		rep.Strategies = append(rep.Strategies, name)
		trades, equity, positions, err := w.St.CountPaperRows(ctx, name)
		if err != nil {
			return rep, err
		}
		if trades+equity+positions > 0 {
			return rep, fmt.Errorf("replay refused: %s already holds %d trade(s), %d equity mark(s) "+
				"and %d position(s) from an earlier run — delete them or choose another suffix, "+
				"because appending would double the record", name, trades, equity, positions)
		}
	}

	// Universe coverage, checked against the WINDOW before anything is written.
	first, last, days, err := w.St.UniverseMembershipCoverage(ctx)
	if err != nil {
		return rep, err
	}
	if days == 0 {
		return rep, fmt.Errorf("replay refused: universe_membership is empty, so no past bar's " +
			"universe is knowable")
	}
	if from < first || to > last {
		return rep, fmt.Errorf("replay refused: universe_membership covers [%d, %d] (%d days) but the "+
			"window is [%d, %d]. Bars outside that range cannot be reconstructed; narrow the window, "+
			"or rebuild membership with `sdmaint build-universe` first", first, last, days, from, to)
	}

	bars, err := w.St.DailyBarTimesBetween(ctx, string(md.TF1d), from, to)
	if err != nil {
		return rep, err
	}
	if len(bars) == 0 {
		return rep, fmt.Errorf("replay refused: no daily bars in [%d, %d]", from, to)
	}

	// Restore live mode on the way out, so a driver failure cannot leave the
	// worker pinned to a past bar if the same instance is reused.
	prev := w.Replay
	defer func() { w.Replay = prev }()

	for _, ts := range bars {
		step := cfg
		step.AsOf = ts
		w.Replay = &step
		var status string
		var err error
		for attempt := 0; ; attempt++ {
			status, err = w.Run(ctx)
			if err == nil || !busy(err) || attempt >= replayBusyRetries {
				break
			}
			rep.Retries++
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
		}
		if err != nil {
			return rep, fmt.Errorf("replay stopped at bar %d after %d of %d session(s) "+
				"(%d retr(ies) spent): %w", ts, rep.Bars, len(bars), rep.Retries, err)
		}
		rep.Bars++
		rep.Statuses = append(rep.Statuses, fmt.Sprintf("%d: %s", ts, status))
	}
	return rep, nil
}
