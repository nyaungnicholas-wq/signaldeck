package ensemble

import (
	"fmt"
	"math"
	"sort"
)

// CROSS-SECTION DISPERSION — is this a per-symbol forecast, or one market call
// republished per symbol?
//
// WHY THIS EXISTS
// ---------------
// Between 2026-07-27 and 2026-08-04 the fleet-wide calibration map collapsed and
// 329 symbols were receiving 5-14 DISTINCT probability values. The hard 0.5
// threshold turned that into a near-unanimous directional call (100.0% UP on
// 07-27, 1.5% UP on 07-31, 99.1% UP on 08-03), which was then stored, resolved
// and graded as ~330 independent per-symbol predictions. Accuracy stopped
// measuring the model and started measuring the tape: it fell to 0.320 on the
// strong up-days and the registry published "FAILED - significantly worse than
// the naive baseline" with retire=true.
//
// KnotsDiscriminate already refuses a collapsed calibration MAP. This is the
// other half: the map can pass its own rank check and the EMITTED cross-section
// still be degenerate, because a map is checked against its knots while this is
// checked against what actually reached the wire.
//
// WHY TWO CONDITIONS
// ------------------
// Neither test alone is sufficient, and the live record proves it in both
// directions:
//
//   - 2026-08-03 emitted 5 distinct values across 329 symbols with a p95-p05
//     spread of 0.066. A SPREAD test alone passes it. It is plainly broken.
//   - 2026-07-15 emitted 137 distinct values across 1,048 symbols — comfortably
//     past any distinct-count test — all inside a band 0.010 wide, and called
//     98.6% of the universe DOWN. A DISTINCT-COUNT test alone passes it. It is
//     equally broken.
//
// So: enough separate values that the model is telling symbols apart, AND
// enough spread that the difference is more than arithmetic noise.
//
// WHAT THIS DELIBERATELY DOES NOT GATE
// ------------------------------------
// Agreement. A cross-section can be genuinely differentiated and still point
// mostly one way — 2026-08-06 at 1w emitted 326 distinct values over a 0.074
// band and called 92% of the universe down. That is a bearish VIEW, not a
// malfunction, and suppressing it would be the model hiding from a market it
// actually has an opinion about. The problem with a unanimous view is that it
// gets COUNTED as N independent forecasts, and counting is the grader's job:
// accuracy_registry.breadth_block reports agreement and grades the day as one
// bet. Publication refuses malfunctions; grading discounts agreement. Doing
// either job in the other place is how a real view gets censored or a broken
// one gets published.
const (
	// MinCrossSectionSpread is the smallest p95-p05 spread of emitted
	// probabilities that still describes a cross-section rather than a constant.
	//
	// Measured over every live pass 2026-07-15..08-06, collapsed cross-sections
	// span 0.000-0.066 and healthy ones 0.074-0.500. 0.05 sits inside that gap
	// and is not a tuned parameter: no value between 0.067 and 0.073 changes the
	// classification of a single observed day.
	MinCrossSectionSpread = 0.05

	// MinDistinctFloor is the absolute floor on distinct emitted values, for
	// universes too small for the proportional rule below to mean anything.
	MinDistinctFloor = 10

	// MinDistinctRatio is the proportional rule: distinct values as a fraction
	// of the cross-section, so the bar scales with the universe instead of being
	// satisfiable by a large fleet landing on a handful of blocks.
	//
	// It is the SAME ruler as forecastmon.MinDistinctRatio, the collapse
	// detector the graded window is judged by (pinned in the 2026-09-20
	// grading-window amendment). This rule used to be one value per 20 symbols
	// (5%), so a pass at 5-15% distinct could publish and then be called
	// collapsed by the detector, which refuses the whole graded window.
	// Publication must refuse anything grading would; a forecastmon test pins
	// the two together. Healthy days measure 0.42-0.99, collapsed 0.02-0.10.
	MinDistinctRatio = 0.15
)

// CrossSection is the measured shape of one pass's emitted probabilities.
type CrossSection struct {
	N        int     `json:"n"`
	Distinct int     `json:"distinct"`
	Spread   float64 `json:"spread"` // p95 - p05
	UpFrac   float64 `json:"upFrac"` // fraction called up at the 0.5 threshold
	// Agreement is max(UpFrac, 1-UpFrac): the fraction of calls pointing the
	// same way. REPORTED, never gated on — see the note above.
	Agreement float64 `json:"agreement"`
}

// MeasureCrossSection describes a pass's emitted probabilities. Values outside
// [0,1] are not possible from a probability and are not defended against here;
// the callers all pass calibrated output.
func MeasureCrossSection(probs []float64) CrossSection {
	cs := CrossSection{N: len(probs)}
	if cs.N == 0 {
		return cs
	}
	sorted := append([]float64(nil), probs...)
	sort.Float64s(sorted)

	seen := make(map[float64]struct{}, cs.N)
	up := 0
	for _, p := range probs {
		seen[p] = struct{}{}
		if p >= 0.5 { // the same threshold every grader in this tree counts a positive at
			up++
		}
	}
	cs.Distinct = len(seen)
	cs.Spread = quantile(sorted, 0.95) - quantile(sorted, 0.05)
	cs.UpFrac = float64(up) / float64(cs.N)
	cs.Agreement = max(cs.UpFrac, 1-cs.UpFrac)
	return cs
}

// Usable reports whether this cross-section is a per-symbol forecast worth
// publishing as one. The reason is returned for the log and the health row: a
// refusal nobody can explain gets switched off the first time it inconveniences
// someone.
func (cs CrossSection) Usable() (ok bool, reason string) {
	if cs.N == 0 {
		return false, "no emitted probabilities"
	}
	// NOT APPLICABLE below the distinct floor, and that is different from
	// passing. A universe of 5 symbols cannot carry 10 distinct values, so
	// applying the rule there would refuse every pass forever with a condition
	// no code path could ever satisfy — the same self-locking shape as a guard
	// whose obligation nothing in the repo can discharge.
	//
	// This fails OPEN on a small fleet deliberately. The harm being prevented is
	// one market call being graded as N independent forecasts, and at N < 10 that
	// arithmetic barely moves; the grading side (accuracy_registry.breadth_block)
	// still reports agreement and grades the day as one bet. Wedging the whole
	// predictor shut to defend against 9 correlated rows is the worse trade.
	if cs.N < MinDistinctFloor {
		return true, ""
	}
	need := int(math.Ceil(MinDistinctRatio * float64(cs.N)))
	if need < MinDistinctFloor {
		need = MinDistinctFloor
	}
	// Never demand more than half the universe be distinct. Without this the
	// floor is wildly uneven with fleet size — at n=324 it asks for 15% unique
	// values, at n=12 it asks for 83%. 2026-07-04 published 12 symbols over 8
	// distinct values spanning the FULL [0,1] range and would have been refused
	// as "collapsed", which is the opposite of what that day was.
	if half := cs.N / 2; need > half {
		need = half
	}
	if cs.Distinct < need {
		return false, fmt.Sprintf(
			"collapsed cross-section: %d distinct value(s) across %d symbols, need %d",
			cs.Distinct, cs.N, need)
	}
	if cs.Spread < MinCrossSectionSpread {
		return false, fmt.Sprintf(
			"flat cross-section: p95-p05 spread %.4f below %.4f — the values differ "+
				"but not by enough to be a forecast", cs.Spread, MinCrossSectionSpread)
	}
	return true, ""
}

// quantile reads a percentile off a sorted slice by linear interpolation. Same
// rule as clusterstat.quantile and tools/accuracy_registry.py, deliberately: a
// publication gate and a grading verdict must not disagree about the same spread.
func quantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[n-1]
	}
	pos := q * float64(n-1)
	lo, hi := int(pos), int(pos)+1
	if hi >= n {
		return sorted[n-1]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
