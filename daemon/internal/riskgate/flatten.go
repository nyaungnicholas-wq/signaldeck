package riskgate

import (
	"fmt"
	"math"
	"os"
	"strconv"
)

// The terminal rung. Admit() halts NEW entries; this decides whether the book
// must be CLOSED. RISK_POLICY.md §3 carried "-12% peak-to-trough -> flatten and
// halt" as SPEC ONLY with the note "nothing in this repository can flatten a
// book", which made the ladder's last rung — the one that matters — a sentence
// rather than a control.
//
// TWO THINGS HAD TO BE DECIDED BEFORE THIS COULD EXIST.
//
// 1. THE LEVEL. The specified ladder reads -8% suspend, -12% flatten. The
// ENFORCED suspend is -20% (Limits.MaxDrawdown). Implementing flatten at -12%
// against a -20% suspend would liquidate the book while it was still opening
// new positions, which is not a ladder but a contradiction. A flatten rung must
// sit at or beyond the suspend rung, so the default here is 25%: suspend at 20,
// flatten at 25. The tighter specified pair (-8/-12) stays SPEC ONLY and moving
// to it means moving BOTH rungs together, not this one alone.
//
// 2. WHICH WAY IT FAILS. Admit() fails CLOSED on an unmeasurable book: an
// unknown drawdown halts entries, because "could not measure" and "measured
// zero" must never collapse into the same allow. This rung deliberately fails
// the OTHER way — an unknown drawdown does NOT flatten.
//
// That is not an inconsistency, it is the asymmetry between a brake and a
// liquidation. Halting entries on bad data costs opportunity and is instantly
// reversible. Liquidating on bad data realises losses, pays spread and impact,
// and cannot be undone by discovering the data was wrong. The safe default for
// a brake is ON; the safe default for a liquidation is OFF.
//
// AN EARLIER DRAFT OF THIS COMMENT JUSTIFIED THAT WITH "an unknown drawdown
// already trips Admit()'s fail-closed halt, so the book is frozen anyway."
// THAT WAS FALSE, and a test written to assert it is what caught it: Admit()
// does NOT halt on an unknown drawdown. It allows, and records "drawdown
// breaker unarmed" in its reasons.
//
// The true argument is narrower and does not need the halt at all.
// Book.DrawdownKnown is set only when papertrade.CurrentDrawdown returns a
// value, which requires an equity curve; a read failure upstream returns an
// error rather than a book with the flag cleared. So DrawdownKnown == false
// means there is NO PEAK YET, not that a peak was lost. A book with no peak
// cannot be below one, and declining to liquidate it is the only correct
// answer rather than a tolerated risk.
//
// The distinction matters because the two arguments fail differently. "The
// halt covers it" would have been load-bearing and wrong — it would have
// licensed this rung to stay silent on a book that was genuinely down and
// merely unmeasured. "There is no peak yet" is a statement about what the flag
// means, and it is checkable.

// FlattenDrawdown is the peak-to-trough drawdown at which the book is closed
// rather than merely stopped from growing. Must sit at or beyond
// Limits.MaxDrawdown; see the file comment.
const DefaultFlattenDrawdown = 0.25

// FlattenDecision is the terminal-rung verdict. Reason is always populated when
// Flatten is true, because a forced liquidation with no stated cause is
// indistinguishable from a bug.
type FlattenDecision struct {
	Flatten bool
	Reason  string

	// Threshold and Measured are echoed so the ledger records what the rule saw
	// rather than re-deriving it later against a book that has since moved.
	Threshold float64
	Measured  float64
}

// FlattenLimit reads the configured flatten threshold. It is floored at
// MaxDrawdown so a misconfiguration cannot invert the ladder: a flatten rung
// tighter than the suspend rung would close the book while it was still
// entering, and silently accepting that would be worse than ignoring the
// setting.
func FlattenLimit(lim Limits) float64 {
	lim = lim.withDefaults()
	v, _, _ := resolveFlattenDrawdown()
	if v < lim.MaxDrawdown {
		v = lim.MaxDrawdown
	}
	return v
}

// flattenDrawdownKey is the env override for the terminal rung.
const flattenDrawdownKey = "SIGNALDECK_RISK_FLATTEN_DRAWDOWN"

// resolveFlattenDrawdown is the ONE parse of the flatten override, shared by
// FlattenLimit (which uses the value) and DescribeLimits (which puts a rejected
// value on the record). It returns the resolved threshold, the raw env string
// ("" when unset), and a rejection reason ("" when accepted or unset).
//
// WHY IT IS SHARED. This key used to be parsed here and nowhere else, while the
// other ten SIGNALDECK_RISK_* keys went through DescribeLimits' table — so it
// was the one risk limit whose rejected override was discarded in total silence.
// An operator tightening the rung that force-liquidates the entire book, who
// typed 15 instead of 0.15, ran the 0.25 default while the pass log printed a
// "risk-limits: N default, M env, R rejected" summary that never mentioned the
// key. The two parsers also disagreed on domain — the table accepted (0,1] and
// this accepted (0,1), so =1 was honoured for MAX_DRAWDOWN and dropped here.
// One function, one domain, one verdict.
//
// The domain is (0,1) exclusive at both ends: 0 would flatten a book that has
// not moved, and 1 is a drawdown the book cannot reach, i.e. a rung spelled as
// a number that silently means "never" — if the intent is to disable the rung,
// that has to be said out loud rather than encoded as an unreachable threshold.
func resolveFlattenDrawdown() (value float64, raw string, rejectReason string) {
	raw = os.Getenv(flattenDrawdownKey)
	if raw == "" {
		return DefaultFlattenDrawdown, "", ""
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return DefaultFlattenDrawdown, raw, fmt.Sprintf("parse error: %v", err)
	}
	if f <= 0 || f >= 1 {
		return DefaultFlattenDrawdown, raw, "out of (0,1)"
	}
	return f, raw, ""
}

// ShouldFlatten reports whether every open position must be closed.
//
// Pure: no I/O, no clock, no state. The caller supplies the book and acts on
// the answer, so the rule can be tested without a database and cannot fire as a
// side effect of being asked.
func ShouldFlatten(b Book, lim Limits) FlattenDecision {
	thr := FlattenLimit(lim)
	d := FlattenDecision{Threshold: thr, Measured: b.CurrentDrawdown}

	// Unusable equity: refuse to flatten. Admit() has already refused entries.
	if b.Equity <= 0 || math.IsNaN(b.Equity) || math.IsInf(b.Equity, 0) {
		return d
	}
	// Unknown drawdown: refuse to flatten. See the file comment — the brake is
	// already on, and liquidating on an unmeasured book is the one error here
	// that cannot be walked back.
	if !b.DrawdownKnown || math.IsNaN(b.CurrentDrawdown) {
		return d
	}
	if b.CurrentDrawdown < thr {
		return d
	}
	// Nothing to close is not a flatten. Reporting one would put a liquidation
	// in the ledger that closed no position.
	if b.OpenPositions <= 0 {
		return d
	}

	d.Flatten = true
	d.Reason = fmt.Sprintf(
		"drawdown flatten: the book is %.1f%% below its peak, at or past the %.1f%% terminal rung — closing all %d open position(s); this is not a halt, it is a liquidation",
		b.CurrentDrawdown*100, thr*100, b.OpenPositions)
	return d
}
