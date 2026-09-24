package ensemble

import "testing"

// THE REAL DAYS. Every degenerate 1d cross-section measured on the live record
// 2026-08-01..08-08, and the one healthy day among them, with the exact
// (n, distinct, p95-p05) each actually carried.
//
// Usable() was never the defect: between its two rules it classifies all of
// these correctly. The defect was upstream — the gate judged a day from a
// single pass's slice rather than the day's real cross-section, so a 12-row
// sample stood in for 329 symbols and the rules were evaluated on data that
// could not fail them. These cases pin the rules so that when the input was
// fixed the verdicts were known to be right.
func TestUsableOnTheRealCollapsedDays(t *testing.T) {
	for _, tc := range []struct {
		day      string
		n        int
		distinct int
		spread   float64
		want     bool
		why      string
	}{
		{"2026-08-01", 329, 6, 0.0200, false, "6 distinct across 329 symbols"},
		{"2026-08-02", 329, 8, 0.0176, false, "8 distinct, and flat"},
		{"2026-08-03", 329, 5, 0.0660, false, "5 distinct — the worst measured"},
		{"2026-08-04", 329, 12, 0.3548, false, "12 distinct; wide, but 12 values is not a cross-section"},
		{"2026-08-05", 329, 180, 0.2884, true, "the one healthy day: 180 distinct, real spread"},
		{"2026-08-06", 329, 34, 0.0263, false, "34/329 = 0.10 distinct, under the collapse detector's 0.15; flat too"},
		{"2026-08-07", 329, 26, 0.0202, false, "the live collapse — spread 0.02"},
		{"2026-08-08", 65, 25, 0.0405, false, "thin universe, still flat"},
	} {
		cs := CrossSection{N: tc.n, Distinct: tc.distinct, Spread: tc.spread}
		got, reason := cs.Usable()
		if got != tc.want {
			t.Errorf("%s (n=%d distinct=%d spread=%.4f): Usable()=%v want %v — %s [%s]",
				tc.day, tc.n, tc.distinct, tc.spread, got, tc.want, tc.why, reason)
		}
	}
}

// The thin-sample hole itself. A 12-row slice of a 329-symbol day satisfies both
// rules trivially: the distinct rule needs 6 and the spread only has to clear
// 0.05, which the real 2026-08-08 record did by 0.00002. Publishing decisions
// must never be taken on a sample this size, which is why the gate now measures
// the prior day's full published cross-section instead.
func TestAThinSampleTriviallySatisfiesBothRules(t *testing.T) {
	sample := CrossSection{N: 12, Distinct: 12, Spread: 0.050020864405201704}
	if ok, _ := sample.Usable(); !ok {
		t.Fatal("the measured 2026-08-08 record no longer passes; this test pins the hole, " +
			"so if it now fails the rules changed and the comment above needs updating")
	}
	// The SAME day, measured properly, is refused.
	real := CrossSection{N: 65, Distinct: 25, Spread: 0.0405}
	if ok, reason := real.Usable(); ok {
		t.Errorf("the full 2026-08-08 cross-section passed: %s", reason)
	}
}
