package pipeline

// EXIT PLANNING for the paper worker.
//
// Two independent things can close a position:
//
//	BARRIER   a risk control — the stop, the target, or the horizon expiring
//	          (internal/papertrade barriers, RISK_POLICY.md §1.2–§1.4)
//	FLIP      the signal changing its mind — cal_prob falling back through the
//	          flat threshold
//
// Both are confirmed by a CLOSE and filled at the OPEN of a strictly later bar,
// so both carry a trigger timestamp and the EARLIER one wins. "First touched"
// has to mean first in TIME, not first in whatever order the code happens to
// check — otherwise a catch-up pass covering several bars at once would report
// an exit the live book would never have taken.
//
// The barrier check runs for EVERY open position, whether or not a fresh
// prediction exists and whatever that prediction says. A stop that only fires
// when the model happens to have an opinion is not a stop; it is a second
// opinion. This is why exit planning is separated from the entry path, which
// still begins at the prediction.

import (
	"context"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// barrierATRLookback is how many bars are read to measure entry volatility:
// the ATR period plus one, because n bars yield n−1 true ranges.
func barrierATRLookback() int { return papertrade.BarrierATRPeriod() + 1 }

// exitPlan is a decided exit — which bar it fills on, and why.
type exitPlan struct {
	fillBar md.Bar
	reason  string // human-readable, lands in the trade log

	// barrier is set when a barrier caused this exit; ok reports which of the
	// two paths won, so the ledger can name it without re-deriving.
	barrier     papertrade.BarrierExit
	fromBarrier bool
}

// planExit decides whether an open position closes on this pass, and on which
// bar it fills.
//
// pred/hasPred describe the latest prediction, which may be absent — a position
// whose symbol has stopped producing predictions must still be exitable, and
// treating "no opinion" as "hold forever" is how a book acquires positions
// nothing is watching.
// flattenReason, when non-empty, is the book-wide terminal rung firing
// (riskgate.ShouldFlatten). It closes a position that no barrier and no
// probability flip would have closed. It is the WEAKEST candidate on purpose: a
// barrier or a flip that already fired is both earlier and more specific, and a
// liquidation must not overwrite the reason a position was already leaving for.
func (w *PaperTrader) planExit(
	ctx context.Context,
	s md.Symbol,
	h md.Horizon,
	pos store.PaperPosition,
	pred store.Prediction,
	hasPred bool,
	asof int64,
	flattenReason string,
) (exitPlan, bool, error) {
	// ── Candidate 1: barriers ────────────────────────────────────────────
	var barrier papertrade.BarrierExit
	var hasBarrier bool
	if papertrade.BarriersEnabled() {
		var err error
		barrier, hasBarrier, err = w.findBarrier(ctx, s, h, pos, asof)
		if err != nil {
			return exitPlan{}, false, err
		}
	}

	// ── Candidate 2: the probability flip ────────────────────────────────
	hasFlip := hasPred && papertrade.DecideTarget(pred.CalProb) == papertrade.GoFlat

	switch {
	case hasBarrier && hasFlip:
		// Both pending. The earlier CLOSE is the one the live book would have
		// acted on; a tie goes to the barrier, because a risk control that
		// loses coin flips to a signal is not a risk control.
		if barrier.TriggerTs <= pred.Ts {
			hasFlip = false
		} else {
			hasBarrier = false
		}
	case !hasBarrier && !hasFlip:
		if flattenReason == "" {
			return exitPlan{}, false, nil
		}
	}

	// FORCED FLATTEN fills at the OPEN of the most recent bar at or before the
	// as-of clock, and returns here rather than joining the trigger/fill path
	// below.
	//
	// Why that is not lookahead, and why the obvious alternative was wrong. The
	// first draft treated this bar's CLOSE as the trigger and looked for a fill
	// on the next bar, mirroring the barrier path. That can never fill: the
	// trigger is always the latest bar, so the fill bar is always in the future
	// relative to asof, and every pass re-derives the same unfillable plan as
	// asof advances. A test caught it — the rung was wired and would have
	// liquidated nothing, forever.
	//
	// The correct rule follows from WHERE the drawdown comes from. riskBook
	// reads the stored equity CURVE, whose points were marked on previous
	// passes from previous closes. So the decision to flatten is already fully
	// determined by information older than this bar, and filling at this bar's
	// OPEN consumes nothing the bar itself produced. It is the same discipline
	// entries obey: decide on closed information, fill at an open.
	if !hasBarrier && !hasFlip {
		fb, ok, err := w.St.BarAtOrBefore(ctx, s.ID, md.TF1d, asof)
		if err != nil {
			return exitPlan{}, false, err
		}
		// A position cannot be liquidated at the open it was opened at, and a
		// bar with no usable open cannot fill anything.
		if !ok || fb.Open <= 0 || fb.Ts <= pos.OpenedTs {
			return exitPlan{}, false, nil
		}
		return exitPlan{fillBar: fb, reason: flattenReason}, true, nil
	}

	triggerTs := pred.Ts
	if hasBarrier {
		triggerTs = barrier.TriggerTs
	}

	// NO LOOKAHEAD: fill at the OPEN of the first daily bar STRICTLY AFTER the
	// close that triggered the exit. The trigger bar's own open is in the past
	// by then, and its close is the thing being reacted to — filling on it
	// would be trading on information the bar itself produced.
	fillBar, ok, err := w.St.BarAtOrAfter(ctx, s.ID, md.TF1d, triggerTs+1)
	if err != nil {
		return exitPlan{}, false, err
	}
	if !ok || fillBar.Open <= 0 || fillBar.Ts > asof {
		// The confirming bar exists but the bar that would fill it has not
		// arrived (or is past the as-of clock). The exit is DECIDED and simply
		// not yet fillable: it fires on the next pass, at that bar's open. It is
		// never back-dated onto the trigger bar.
		return exitPlan{}, false, nil
	}

	if hasBarrier {
		return exitPlan{
			fillBar: fillBar, barrier: barrier, fromBarrier: true,
			reason: barrierReason(barrier, pos.AvgPx),
		}, true, nil
	}
	return exitPlan{
		fillBar: fillBar,
		reason: fmt.Sprintf("cal_prob %.3f <= flat %.2f",
			pred.CalProb, papertrade.FlatThreshold()),
	}, true, nil
}

// findBarrier measures the position's entry volatility and scans its holding
// window for the first barrier reached.
func (w *PaperTrader) findBarrier(
	ctx context.Context,
	s md.Symbol,
	h md.Horizon,
	pos store.PaperPosition,
	asof int64,
) (papertrade.BarrierExit, bool, error) {
	// Entry volatility, from bars that had already CLOSED when the entry filled.
	// The entry fills at the open of the bar at OpenedTs, whose high, low and
	// close are unknown at that instant — BarsBefore enforces the strictness in
	// SQL so it cannot be lost here.
	prior, err := w.St.BarsBefore(ctx, s.ID, md.TF1d, pos.OpenedTs, barrierATRLookback())
	if err != nil {
		return papertrade.BarrierExit{}, false, err
	}
	atr, ok := papertrade.ATR(prior, papertrade.BarrierATRPeriod())
	if !ok {
		// Unmeasurable volatility. The PRICE barriers cannot be placed — but the
		// horizon has nothing to do with volatility, so the time stop still
		// applies. A zero ATR would collapse both price barriers onto the entry
		// and stop the position out on its first close.
		atr = 0
	}

	// The holding window: every bar from the entry bar through the as-of clock.
	// held[0] is the entry bar itself — a position opened at that bar's open is
	// exposed to that bar's close, so that close can stop it out.
	held, err := w.St.Bars(ctx, s.ID, md.TF1d, pos.OpenedTs, asof+1, 0)
	if err != nil {
		return papertrade.BarrierExit{}, false, err
	}
	// BOTH LEGS ON ONE BASIS.
	//
	// pos.AvgPx was stored when the position opened; `held` is read now. `bars`
	// is written INSERT OR REPLACE and a detected split re-backfills a symbol's
	// whole series, so a stored entry and a freshly read bar can sit on different
	// price bases — and the stop and target are placed by comparing exactly those
	// two. The rescale factor would land whole in the barrier levels: a 100:1
	// reverse split turns a 2xATR stop into an unreachable one, or into one the
	// entry bar itself is already through.
	//
	// This is the confluence defect (entry_px divided into a live exit close,
	// DFNS published at +8541%) in a second place. The repair is the same: prefer
	// the ENTRY BAR, which is read live alongside the rest of the window, so both
	// legs move together whenever the series is rewritten. held[0] IS that bar —
	// the fill was at its open — so this costs no extra read.
	entryPx, basis, err := w.entryPxOnCurrentBasis(ctx, s.ID, pos, held)
	if err != nil {
		return papertrade.BarrierExit{}, false, err
	}
	if basis == basisUnresolvable {
		// The entry bar is gone AND the series has been rescaled since: there is
		// no price on the current basis to place a level from. Zeroing the ATR
		// disables the PRICE barriers only — the horizon stop has nothing to do
		// with price and still applies, so the position stays exitable rather
		// than being held forever by a data problem.
		atr = 0
	}
	exit, found := papertrade.FindBarrierExit(entryPx, atr, evHoldBarsFor(h), held)
	return exit, found, nil
}

// Basis outcomes for a position's entry price.
const (
	basisEntryBar     = "entry-bar"    // re-read live: both legs agree by construction
	basisStoredClean  = "stored-clean" // stored price, no rescale since it was stored
	basisUnresolvable = "unresolvable" // stored price is on a dead basis and the bar is gone
)

// entryPxOnCurrentBasis returns the position's entry price expressed on the SAME
// basis as the bars it will be compared against.
//
// Order matters. The entry bar is preferred whenever it is present, because it
// is read in the same query as the rest of the holding window and therefore
// cannot disagree with it. The stored avg_px is used only when that bar is
// missing, and only after confirming no rescale has happened since the position
// opened — otherwise the honest answer is that no comparable price exists.
func (w *PaperTrader) entryPxOnCurrentBasis(
	ctx context.Context, symbolID int64, pos store.PaperPosition, held []md.Bar,
) (float64, string, error) {
	if len(held) > 0 && held[0].Ts == pos.OpenedTs && held[0].Open > 0 {
		return held[0].Open, basisEntryBar, nil
	}
	rescaled, _, err := w.St.SeriesRescaledSince(ctx, symbolID, pos.OpenedTs)
	if err != nil {
		return 0, "", err
	}
	if rescaled {
		return pos.AvgPx, basisUnresolvable, nil
	}
	return pos.AvgPx, basisStoredClean, nil
}

// barrierReason renders the sizing rationale a reader of the trade log needs:
// which barrier, at what level, how far that was from entry, and how long the
// position was held.
func barrierReason(b papertrade.BarrierExit, entryPx float64) string {
	switch b.Kind {
	case papertrade.BarrierExpiry:
		return fmt.Sprintf(
			"horizon expiry after %d bar(s) — the forecast's claim has resolved; close %.4f vs entry %.4f",
			b.HeldBars, b.ClosePx, entryPx)
	default:
		return fmt.Sprintf(
			"%s barrier: close %.4f through %.4f (entry %.4f, %.1f×ATR %.4f) after %d bar(s) — confirmed on the close, filled at the next open",
			b.Kind, b.ClosePx, b.Level, entryPx, barrierMult(b.Kind), b.ATR, b.HeldBars)
	}
}

func barrierMult(k papertrade.BarrierKind) float64 {
	if k == papertrade.BarrierFavorable {
		return papertrade.FavorableATRMult()
	}
	return papertrade.AdverseATRMult()
}
