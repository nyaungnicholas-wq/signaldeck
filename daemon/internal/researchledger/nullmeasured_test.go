package researchledger

import "testing"

// mustBF unwraps a Bayes factor whose null is expected to be measured. A false
// ok here is a test-setup error, not a soft condition.
func mustBF(bf float64, ok bool) float64 {
	if !ok {
		panic("BayesFactor called with an UNMEASURED null")
	}
	return bf
}

// An unmeasured null yields no Bayes factor at all. This is the property that
// makes the defect class unrepresentable: a grading site cannot fall back to
// an assumed rate, because there is no value to fall back to.
func TestUnmeasuredNullYieldsNoBayesFactor(t *testing.T) {
	for _, null := range []MeasuredNull{
		{},                         // zero value
		NullFromArm(0, 0, "empty"), // arm graded no trials
		TranscribedNull(0.55, 0, "no sample"),
	} {
		if null.Measured() {
			t.Fatalf("null %v reports measured", null)
		}
		if bf, ok := BayesFactorAbove(30, 40, null, WeekTrialMaxEdge); ok || bf != 0 {
			t.Errorf("above: got (%v,%v), want (0,false)", bf, ok)
		}
		if bf, ok := BayesFactorBelow(10, 40, null, WeekTrialMaxEdge); ok || bf != 0 {
			t.Errorf("below: got (%v,%v), want (0,false)", bf, ok)
		}
	}
}

// The 0.5 floor makes the substitution strictly restrictive: a measured null
// can only raise the bar a hypothesis must clear, never lower it, so no
// posterior can rise because of this change.
func TestMeasuredNullIsFlooredAndOnlyEverHarder(t *testing.T) {
	low := NullFromArm(3, 20, "null-matched week") // 0.15 measured
	if low.P0() != 0.5 {
		t.Fatalf("floor: P0 = %v, want 0.5", low.P0())
	}
	high := NullFromArm(13, 20, "null-matched week") // 0.65 measured
	if high.P0() != 0.65 || high.Trials() != 20 {
		t.Fatalf("measured null = (%v,%v), want (0.65,20)", high.P0(), high.Trials())
	}
	atFloor, _ := BayesFactorAbove(15, 20, low, WeekTrialMaxEdge)
	above, _ := BayesFactorAbove(15, 20, high, WeekTrialMaxEdge)
	if above > atFloor {
		t.Errorf("a harder null produced MORE evidence: %v > %v", above, atFloor)
	}
}
