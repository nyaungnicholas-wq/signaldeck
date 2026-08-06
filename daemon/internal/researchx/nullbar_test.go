package researchx

import (
	"math"
	"testing"
)

// THE BUG THE 0.5 FLOOR WAS HIDING. nullDir draws an independent coin per
// (symbol, week), which flattens the weekly cross-sectional accuracy to 0.5 and
// so almost never clears a strict bar at 0.5 or above. On data with NO signal in
// it the arm therefore scores ~0 while the real arm — whose directions are
// correlated within the week — scores far above it. A null that reads zero on
// pure noise is not measuring chance, and gating on it admits noise.
func TestNullMatchedUnderstatesChanceOnPureNoise(t *testing.T) {
	obs := noiseObs(80, 40, 0)
	r := Rule{Conds: []Cond{{Key: "consec_dir", Op: ">=", Val: 0.4}}, Call: "follow_pressure"}
	cf := Counterfactual(obs, r, 10, 30)

	if cf.NullMatched.WinRate > 0.05 {
		t.Fatalf("fixture drifted: null-matched was supposed to be pathologically low, got %.4f", cf.NullMatched.WinRate)
	}
	if cf.Full.WinRate < 0.25 {
		t.Fatalf("fixture drifted: the real arm should win plenty of weeks on noise, got %.4f", cf.Full.WinRate)
	}
	if cf.NullCoherent.WinRate <= cf.NullMatched.WinRate {
		t.Fatalf("the coherent null must score ABOVE the per-symbol one: %.4f vs %.4f",
			cf.NullCoherent.WinRate, cf.NullMatched.WinRate)
	}
	// The whole point: on noise the coherent null must land near the real arm,
	// because on noise there is nothing to tell them apart.
	if math.Abs(cf.NullCoherent.WinRate-cf.Full.WinRate) > 0.20 {
		t.Fatalf("on pure noise the coherent null should track the real arm; got null %.4f vs real %.4f",
			cf.NullCoherent.WinRate, cf.Full.WinRate)
	}
}

// The bar must be reachable. A flat 0.5 was not: measured on the live corpus the
// 48-rule grid's week-win rates topped out at 0.246, so every rule died at the
// first gate and nothing ever reached RegimeSurvival, Fragile, Counterfactual or
// the holdout. If this ever fails, the door has closed again.
func TestNullBarIsReachable(t *testing.T) {
	obs := noiseObs(80, 40, 0)
	r := Rule{Conds: []Cond{{Key: "consec_dir", Op: ">=", Val: 0.4}}, Call: "follow_pressure"}
	cf := Counterfactual(obs, r, 10, 30)
	bar := nullBar(cf.NullCoherent)
	if bar >= 1 {
		t.Fatalf("a measured null must not produce an unclearable bar, got %.4f", bar)
	}
	if bar <= cf.NullCoherent.WinRate {
		t.Fatalf("the bar must sit ABOVE the null's point estimate, got bar %.4f vs null %.4f",
			bar, cf.NullCoherent.WinRate)
	}
}

// The floor's real job was that an unmeasurable null must not wave a rule
// through. The upper bound keeps doing that job, and does it proportionately.
func TestUnmeasuredNullIsUnclearable(t *testing.T) {
	if got := wilsonUpper(0, 0, 1.96); got != 1 {
		t.Fatalf("a null with no week-trials must be unclearable, got %.4f", got)
	}
	if got := nullBar(CFArm{WinRate: 0, Grade: WeekGrade{Weeks: 0}}); got != 1 {
		t.Fatalf("nullBar must inherit that, got %.4f", got)
	}
	thin, thick := wilsonUpper(0.1, 5, 1.96), wilsonUpper(0.1, 500, 1.96)
	if thin <= thick {
		t.Fatalf("a thinly measured null must demand MORE, not less: 5 weeks %.4f vs 500 weeks %.4f", thin, thick)
	}
	if thin < 0.3 {
		t.Fatalf("five week-trials should still be a stiff bar, got %.4f", thin)
	}
}

func TestWilsonUpperBracketsWilsonLower(t *testing.T) {
	for _, n := range []int{5, 40, 300} {
		for _, p := range []float64{0, 0.1, 0.43, 0.5, 0.9, 1} {
			lo, hi := wilsonLower(p, n, 1.96), wilsonUpper(p, n, 1.96)
			if lo > hi {
				t.Fatalf("p=%v n=%d: lower %.4f above upper %.4f", p, n, lo, hi)
			}
			if lo < 0 || hi > 1 {
				t.Fatalf("p=%v n=%d: interval [%.4f,%.4f] escaped [0,1]", p, n, lo, hi)
			}
		}
	}
}

// nullDirWeek must preserve the week's shape, not just its randomness: same
// observations graded, same weekly up-rate, therefore the same folded baseline.
// Only the sign of the whole week's direction vector may move.
func TestNullDirWeekPreservesWithinWeekStructure(t *testing.T) {
	obs := noiseObs(60, 30, 7)
	r := Rule{Conds: []Cond{{Key: "ext_score", Op: ">=", Val: 0.5}}, Call: "follow_pressure"}
	real := GradeWeeks(obs, r, 10)
	null := gradeArm(obs, r.Conds, nullDirWeek(callDir(r.Call)), 10)

	if real.Weeks != null.Weeks || real.TotalObs != null.TotalObs {
		t.Fatalf("the coherent null must grade the SAME obs: weeks %d/%d obs %d/%d",
			real.Weeks, null.Weeks, real.TotalObs, null.TotalObs)
	}
	for i := range real.Trials {
		if real.Trials[i].Ups != null.Trials[i].Ups || real.Trials[i].N != null.Trials[i].N {
			t.Fatalf("week %d: up-rate moved, so the folded baseline moved too", real.Trials[i].Week)
		}
		// Whole-week sign flip: wins are either kept or exactly complemented.
		w, nw, n := real.Trials[i].Wins, null.Trials[i].Wins, real.Trials[i].N
		if nw != w && nw != n-w {
			t.Fatalf("week %d: wins %d became %d, which is neither kept nor flipped (n=%d)",
				real.Trials[i].Week, w, nw, n)
		}
	}
}
