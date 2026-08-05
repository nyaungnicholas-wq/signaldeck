package papertrade

// TRIPLE-BARRIER EXITS — the position-level risk control (RISK_POLICY.md
// §1.2–§1.4, EXECUTION_SPEC.md §3).
//
// A position carries three exits and the first one reached wins:
//
//	favorable   entry + FavorableATRMult × ATR   take profit
//	adverse     entry − AdverseATRMult   × ATR   hard stop
//	expiry      after holdBars bars               the forecast's own horizon
//
// THE HAZARD THIS FILE EXISTS TO AVOID. A daily bar records open, high, low and
// close. It does NOT record whether the low came before the high. A position
// with a stop below and a target above may have reached either first, and the
// bar cannot say which. Filling a barrier "intrabar" therefore requires an
// assumption about path order, and the flattering assumption manufactures
// profit that never existed — the single most common way a stop makes a
// backtest better than the strategy.
//
// THE RULE HERE, and it is not negotiable: a barrier is confirmed by a CLOSE
// and filled at the OPEN of a strictly later bar. Highs and lows are never
// consulted for the trigger. Two consequences follow, both accepted
// deliberately:
//
//   - Wicks do not stop the book out. A bar that traded through the stop and
//     closed back above it is not an exit. This is a real behavioural
//     difference from an intrabar stop, not an approximation of one.
//   - Gaps are paid in full. A close far through the stop fills at the next
//     open, wherever that is. This is the honest cost of a daily-bar stop and
//     it must not be modelled away.
//
// Both barriers can never fire on the same close: the favorable level is above
// entry and the adverse level below it, and a close is one price. The
// ambiguity that forces every other daily-bar barrier scheme into an assumption
// simply does not arise.

import (
	"math"
	"os"
	"strconv"
	"strings"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// BarrierKind names which of the three exits fired.
type BarrierKind string

const (
	// BarrierFavorable is the take-profit: a close at or above entry + k·ATR.
	BarrierFavorable BarrierKind = "favorable"
	// BarrierAdverse is the hard stop: a close at or below entry − k·ATR.
	BarrierAdverse BarrierKind = "adverse"
	// BarrierExpiry is the time stop: the forecast's horizon has elapsed and
	// neither price barrier was reached. The claim that justified the position
	// has expired, so the position does too.
	BarrierExpiry BarrierKind = "expiry"
)

// BarriersEnabled reports whether barrier exits are in force. Default true.
//
// The switch exists because turning barriers on changes the strategy the paper
// book is tracking, and a track record has to be able to say which strategy it
// is a record OF. Flipping this is a methodology change, not a tuning knob.
func BarriersEnabled() bool { return envBool("SIGNALDECK_PAPER_BARRIERS", true) }

// BarrierATRPeriod is how many bars the volatility estimate averages. 20 is one
// trading month: long enough that a single wild session does not set the stop,
// short enough to track a name whose volatility regime has changed.
func BarrierATRPeriod() int {
	if v := int(envFloat("SIGNALDECK_PAPER_BARRIER_ATR_PERIOD", 20)); v > 1 {
		return v
	}
	return 20
}

// FavorableATRMult and AdverseATRMult set the barrier distances, in ATRs.
//
// 3.0 and 2.0 give 1.5 : 1 reward to risk. The adverse multiple is 2.0 rather
// than something tighter because a stop inside ordinary daily noise is not a
// risk control, it is a fee: it converts variance into realized losses without
// changing the distribution of the underlying idea. The favorable multiple is
// not wider than 3.0 because the edge being harvested is a directional
// probability over a fixed horizon, not a trend claim — a target the horizon
// cannot plausibly reach is a decoration, and the expiry barrier would collect
// the position long before price got there.
func FavorableATRMult() float64 {
	return envFloat("SIGNALDECK_PAPER_BARRIER_FAVORABLE_ATR", 3.0)
}

// AdverseATRMult is the stop distance in ATRs. See FavorableATRMult.
func AdverseATRMult() float64 {
	return envFloat("SIGNALDECK_PAPER_BARRIER_ADVERSE_ATR", 2.0)
}

// BarrierExit is one decided barrier exit.
//
// TriggerTs is the CLOSE that confirmed it, never the fill. The caller fills at
// the open of a strictly later bar, and keeping the two timestamps distinct is
// what makes that separation auditable after the fact.
type BarrierExit struct {
	Kind      BarrierKind `json:"kind"`
	TriggerTs int64       `json:"triggerTs"`
	Level     float64     `json:"level"`     // the barrier price (0 for expiry)
	ClosePx   float64     `json:"closePx"`   // the close that confirmed it
	ATR       float64     `json:"atr"`       // the volatility the levels were sized from
	HeldBars  int         `json:"heldBars"`  // bars held when it fired, 1-based
}

// Levels returns the two price barriers for an entry at entryPx with volatility
// atr. Both are zero when the inputs cannot support a level, which the caller
// must treat as "no price barrier", never as "a barrier at zero".
func Levels(entryPx, atr float64) (favorable, adverse float64, ok bool) {
	if !usable(entryPx) || entryPx <= 0 || !usable(atr) || atr <= 0 {
		return 0, 0, false
	}
	favorable = entryPx + FavorableATRMult()*atr
	adverse = entryPx - AdverseATRMult()*atr
	// A stop at or below zero is not a stop; it is an unstopped position wearing
	// the label of one. Refuse the level rather than pretend.
	if adverse <= 0 || !usable(favorable) || !usable(adverse) {
		return 0, 0, false
	}
	return favorable, adverse, true
}

// ATR is the average true range over the last `period` bars of `bars`.
//
// True range needs the PREVIOUS close, so n bars yield n−1 true ranges and the
// caller must supply period+1 bars to get a period-length average. Fewer than
// two usable bars gives ok=false — an unmeasurable volatility is not zero
// volatility, and a zero would collapse both barriers onto the entry price and
// stop the position out instantly.
func ATR(bars []md.Bar, period int) (float64, bool) {
	if period < 1 || len(bars) < 2 {
		return 0, false
	}
	// Walk backwards over at most `period` true ranges.
	sum, n := 0.0, 0
	for i := len(bars) - 1; i > 0 && n < period; i-- {
		cur, prev := bars[i], bars[i-1]
		if !usable(cur.High) || !usable(cur.Low) || !usable(prev.Close) || cur.High < cur.Low {
			continue
		}
		tr := math.Max(cur.High-cur.Low,
			math.Max(math.Abs(cur.High-prev.Close), math.Abs(cur.Low-prev.Close)))
		if !usable(tr) || tr < 0 {
			continue
		}
		sum += tr
		n++
	}
	if n == 0 {
		return 0, false
	}
	atr := sum / float64(n)
	if !usable(atr) || atr <= 0 {
		return 0, false
	}
	return atr, true
}

// FindBarrierExit scans a position's holding window for the first barrier
// reached, and returns the CLOSE that confirmed it.
//
//   - entryPx is the position's average fill price.
//   - atr is the volatility measured at entry, from bars that had already
//     closed when the entry filled. It is FIXED for the life of the position:
//     these are barriers, not a trailing stop, and re-measuring them each pass
//     would let a quiet market ratchet the stop toward a position it never
//     agreed to hold that tightly.
//   - holdBars is the forecast's horizon in bars (1 for a 1-day claim, 5 for a
//     week). The expiry barrier fires once that many bars have been held.
//   - held is the position's bars, ASCENDING, starting with the bar the entry
//     filled on. held[0] is the entry bar: a position opened at its open is
//     exposed to its close, so that close can stop it out.
//
// ok=false means no barrier has been reached yet — keep holding.
//
// Price barriers are checked BEFORE expiry on the same bar. The fill is
// identical either way (both fill at the next open), so this only decides which
// reason the trade log records — and "the stop was hit on the last day" is a
// more useful fact than "it expired".
func FindBarrierExit(entryPx, atr float64, holdBars int, held []md.Bar) (BarrierExit, bool) {
	if len(held) == 0 || holdBars < 1 {
		return BarrierExit{}, false
	}
	favorable, adverse, levelsOK := Levels(entryPx, atr)

	for i, b := range held {
		heldBars := i + 1 // 1-based: after this bar closes, the position has held i+1 bars

		// PRICE BARRIERS — confirmed on the CLOSE only. Highs and lows are
		// deliberately not read: consulting them is the intrabar assumption this
		// whole design refuses to make.
		if levelsOK && usable(b.Close) && b.Close > 0 {
			if b.Close >= favorable {
				return BarrierExit{
					Kind: BarrierFavorable, TriggerTs: b.Ts, Level: favorable,
					ClosePx: b.Close, ATR: atr, HeldBars: heldBars,
				}, true
			}
			if b.Close <= adverse {
				return BarrierExit{
					Kind: BarrierAdverse, TriggerTs: b.Ts, Level: adverse,
					ClosePx: b.Close, ATR: atr, HeldBars: heldBars,
				}, true
			}
		}

		// TIME BARRIER. The forecast was a claim about holdBars bars; once that
		// many have been held, the claim has been resolved either way and the
		// position is no longer supported by anything measured.
		if heldBars >= holdBars {
			return BarrierExit{
				Kind: BarrierExpiry, TriggerTs: b.Ts, ClosePx: b.Close,
				ATR: atr, HeldBars: heldBars,
			}, true
		}
	}
	return BarrierExit{}, false
}

// usable rejects the values that make a price comparison meaningless.
func usable(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// envBool reads a boolean, keeping the default on anything unrecognised.
func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	switch v {
	case "yes", "on":
		return true
	case "no", "off":
		return false
	}
	return def
}
