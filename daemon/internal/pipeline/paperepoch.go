package pipeline

// THE PAPER BOOK'S EPOCH SCHEDULE — where a track record splits because the
// strategy changed underneath it.
//
// A track record is a record OF something. When the exit rules change, the
// numbers before and after describe two different strategies, and one average
// across the change reports a strategy nobody ran. The schedule below is the
// list of those changes, declared in CODE rather than typed into a database, so
// that a rebuilt or cloned database reconstructs the identical boundaries and
// no reader has to trust a row somebody inserted by hand.
//
// THE BOOK IS CONTINUOUS ACROSS A BOUNDARY. Same cash, same open positions,
// same equity curve — nothing is reset, closed out, or re-based. Only the
// MEASUREMENT splits. This is why an epoch is not a new strategy id: the
// capital did not restart, and a ledger that pretended it did would be
// inventing a fact to make a chart tidier.
//
// Appending to this list is a deliberate act. It says: everything after this
// instant is a different strategy, and no statistic may span it.

import (
	"context"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// barrierEpochTs is 2026-08-04T00:00:00Z — the instant the triple-barrier exits
// took effect (proofs/P4D_BARRIER_EXITS.md).
//
// Before it, a position was held open-endedly and exited only when cal_prob
// fell back through the flat threshold. After it, every position carries a hard
// stop, a take-profit and a horizon expiry — and on flagship-1d the 1-bar
// horizon makes the probability-flip exit unreachable, so the book went from
// open-ended holds to a strict one-bar strategy. That is not a parameter
// change; it is a different strategy wearing the same name.
const barrierEpochTs int64 = 1785801600

// paperEpochSchedule applies to every simulated book. Both flagship books
// changed on the same commit, so they share one schedule; a future change that
// touches only one horizon would key this by strategy.
var paperEpochSchedule = []store.PaperEpoch{
	{
		Epoch: 1, FromTs: 0, Label: "flip-exit",
		Reason: "Open-ended holds. A position was entered on a cal_prob crossing and " +
			"exited only when cal_prob fell back through the flat threshold. No stop, " +
			"no take-profit, no time stop; holding period unbounded.",
	},
	{
		Epoch: 2, FromTs: barrierEpochTs, Label: "triple-barrier",
		Reason: "Triple-barrier exits with next-open fills (P4D): hard stop at 2.0xATR(20), " +
			"take-profit at 3.0xATR(20), horizon expiry at 1 bar (1d) / 5 bars (1w). " +
			"On flagship-1d the 1-bar horizon dominates, so positions now open at one " +
			"bar's open and close at the next and the probability-flip exit is " +
			"unreachable. Different strategy, same book: cash and open positions carried " +
			"across this boundary unchanged.",
	},
}

// PaperEpochSchedule exposes the schedule so `sdmaint paper-epochs` can apply
// and report the same boundaries the worker does, from one definition.
func PaperEpochSchedule() []store.PaperEpoch {
	out := make([]store.PaperEpoch, len(paperEpochSchedule))
	copy(out, paperEpochSchedule)
	return out
}

// PaperStrategyNames lists the simulated books, for the same reason.
func PaperStrategyNames() []string {
	out := make([]string, 0, len(paperStrategies))
	for _, s := range paperStrategies {
		out = append(out, s.Name)
	}
	return out
}

// ensureEpochs writes the schedule for one strategy. Idempotent, and cheap
// enough to run every pass — which is the point: the boundaries exist on any
// database the daemon has ever touched, including a fresh one, without a
// migration step somebody has to remember.
func (w *PaperTrader) ensureEpochs(ctx context.Context, strategy string) error {
	for _, e := range paperEpochSchedule {
		e.Strategy = strategy
		if err := w.St.UpsertPaperEpoch(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

// epochWindow is the half-open [from, to) window of the epoch in force at `at`.
//
// BOTH BOUNDS, not just the lower one. A lower bound alone is correct only
// while `at` is the present — the newest epoch has nothing after it — and
// silently wrong the moment anything measures a PAST epoch: every trade from
// every later strategy would leak into it. That is a lookahead bug wearing the
// costume of a scoping filter, and it is exactly the class of error this whole
// mechanism exists to prevent, so the window is closed at both ends here rather
// than trusted to the caller's choice of `at`.
//
// `to` is 0 for the newest epoch, meaning open-ended. from=0 and to=0 together
// mean "no epoch declared, do not scope" — a database with no boundaries has
// one continuous record, and inventing a boundary would be worse than none.
func (w *PaperTrader) epochWindow(ctx context.Context, strategy string, at int64) (from, to int64, err error) {
	epochs, err := w.St.PaperEpochs(ctx, strategy)
	if err != nil || len(epochs) == 0 {
		return 0, 0, err
	}
	for i := range epochs {
		f, t := store.EpochBounds(epochs, i)
		if store.InEpoch(at, f, t) {
			return f, t, nil
		}
	}
	// `at` precedes every declared boundary: it belongs to the first epoch,
	// whose start is inception.
	_, t := store.EpochBounds(epochs, 0)
	return 0, t, nil
}
