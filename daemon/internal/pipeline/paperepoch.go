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
//
// TWO KINDS OF BOUNDARY LIVE HERE, and the Reason text says which. Most are
// STRATEGY changes, as above. One is an INTEGRITY boundary: the strategy did not
// change, but the simulator that produced the earlier rows was wrong, so the
// record before it is not a record of anything that could have happened. The
// mechanism is the same — no statistic may span the instant — and putting it
// here rather than inventing a second one keeps a single answer to "may these
// two periods be averaged together". Do not read an integrity boundary as
// evidence that the strategy changed.

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

// backdatedFillEpochTs is 2026-07-22T00:00:00Z — the first instant after which
// no fill in the book is back-dated.
//
// INTEGRITY BOUNDARY, NOT A STRATEGY CHANGE. buildStep bounded the fill bar only
// from above, so a prediction left behind by a starved stretch (LatestPrediction
// returns the newest row with n_used > 0, weeks old while the model is retired)
// filled at THAT bar's open — while the return forecast, corrToBook and the
// riskgate book were all measured at the as-of clock, and markPositions then
// marked the position at the as-of close. The whole intervening move was booked
// as one step's P&L.
//
// 46 of the book's 123 fills landed that way: 43 of flagship-1d's 77 (2026-07-07
// to 07-21) and 3 of flagship-1w's 46 (07-14), the worst back-dated by 22 days.
// flagship-1d equity printed 99,491.93 -> 103,218.71 -> 98,745.79 across one such
// batch, on what was really a small loss. The last back-dated fill is 2026-07-21
// 04:00 (paper_trades id 118); the first fill at or after this boundary is
// 2026-07-22 04:00, so the split is clean.
//
// The fix is the fill-window bound in paper.go: a step transacts only inside
// (cursor.LastBarTs, asof]. Nothing is deleted and the book is continuous here as
// at every other boundary — the cash and positions carried across are the ones
// the contaminated period actually left behind, so the equity LEVEL after this
// instant still inherits that P&L. Only per-epoch RETURNS are clean, which is
// what the scoping is for.
const backdatedFillEpochTs int64 = 1784678400

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
		Epoch: 2, FromTs: backdatedFillEpochTs, Label: "backdated-fills-fixed",
		Reason: "INTEGRITY BOUNDARY, not a strategy change: the exit and entry rules " +
			"either side of this instant are identical. Before it, buildStep placed no " +
			"lower bound on the fill bar, so a stale prediction filled at a bar the book " +
			"had already marched past while every decision input was measured at the " +
			"as-of clock — 46 of 123 fills, back-dated by up to 22 days, each booking the " +
			"intervening move as one step's P&L. No statistic may span this instant. The " +
			"book is continuous: cash and open positions carry across unchanged, so the " +
			"equity LEVEL after it still inherits the earlier fabricated P&L and only " +
			"per-epoch returns are clean.",
	},
	{
		Epoch: 3, FromTs: barrierEpochTs, Label: "triple-barrier",
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
