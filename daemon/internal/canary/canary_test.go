package canary

import (
	"math"
	"testing"
)

const day = int64(86400)

func rec(version string, n, correct int, days int64, baseline float64) Record {
	return Record{
		Version: version, N: n, Correct: correct,
		FirstTs: 0, LastTs: days * day, BaselineAccuracy: baseline,
	}
}

// A thin challenger must HOLD, however good it looks. This is the whole point:
// the old behavior was to serve it immediately.
func TestThinChallengerHolds(t *testing.T) {
	inc := rec("v1", 500, 250, 200, 0.5)
	ch := rec("v2", 10, 10, 60, 0.5) // 100% accurate on ten observations
	v := Evaluate(inc, ch)
	if v.Decision != DecisionHold {
		t.Fatalf("decision = %s, want hold", v.Decision)
	}
	if v.Serving != "v1" || v.Shadow != "v2" {
		t.Fatalf("serving=%q shadow=%q, want v1/v2", v.Serving, v.Shadow)
	}
	if v.ObservationsNeeded != MinObservations-10 {
		t.Fatalf("ObservationsNeeded = %d, want %d", v.ObservationsNeeded, MinObservations-10)
	}
}

// Enough rows but too short a window must also hold — a challenger that has seen
// one regime has not been tested.
func TestShortWindowHolds(t *testing.T) {
	inc := rec("v1", 500, 250, 200, 0.5)
	ch := rec("v2", 400, 380, 3, 0.5)
	v := Evaluate(inc, ch)
	if v.Decision != DecisionHold {
		t.Fatalf("decision = %s, want hold on a 3-day window", v.Decision)
	}
	if v.DaysNeeded <= 0 {
		t.Fatalf("DaysNeeded = %v, want > 0", v.DaysNeeded)
	}
}

// A genuinely better challenger, observed properly, gets promoted.
func TestClearlyBetterChallengerIsPromoted(t *testing.T) {
	inc := rec("v1", 2000, 960, 400, 0.5) // 48% — the real directional record
	ch := rec("v2", 600, 390, 90, 0.5)    // 65% over 600 obs across 90 days
	v := Evaluate(inc, ch)
	if v.Decision != DecisionPromote {
		t.Fatalf("decision = %s (%s), want promote", v.Decision, v.Reason)
	}
	if v.Serving != "v2" || v.Shadow != "" {
		t.Fatalf("serving=%q shadow=%q, want v2 and no shadow", v.Serving, v.Shadow)
	}
	if v.ChallengerLower <= v.IncumbentAccuracy {
		t.Fatalf("promoted without clearing the incumbent: lower=%v inc=%v", v.ChallengerLower, v.IncumbentAccuracy)
	}
}

// A hairline lead on the point estimate must NOT promote. Promoting on noise is
// the failure this package exists to prevent.
func TestHairlineLeadHolds(t *testing.T) {
	inc := rec("v1", 5000, 2600, 400, 0.5) // 52.0%
	ch := rec("v2", 200, 105, 90, 0.5)     // 52.5% — inside its own interval
	v := Evaluate(inc, ch)
	if v.Decision != DecisionHold {
		t.Fatalf("decision = %s, want hold on a hairline lead", v.Decision)
	}
	if v.ChallengerAccuracy <= v.IncumbentAccuracy {
		t.Fatal("fixture must have the challenger leading on the point estimate")
	}
}

// A challenger that cannot beat the naive baseline must not be promoted even
// when it beats a failing incumbent — otherwise "better than broken" ships.
func TestBeatingAFailingIncumbentIsNotEnough(t *testing.T) {
	inc := rec("v1", 2000, 900, 400, 0.55) // 45% — failing
	ch := rec("v2", 800, 408, 90, 0.55)    // 51% — beats it, still below baseline
	v := Evaluate(inc, ch)
	if v.Decision == DecisionPromote {
		t.Fatalf("promoted a below-baseline challenger: %+v", v)
	}
	if v.Decision != DecisionHold {
		t.Fatalf("decision = %s, want hold", v.Decision)
	}
}

// Positive evidence of being worse must end the trial, not hold it forever.
func TestClearlyWorseChallengerIsRejected(t *testing.T) {
	inc := rec("v1", 2000, 1200, 400, 0.5) // 60%
	ch := rec("v2", 600, 240, 90, 0.5)     // 40% — whole interval below
	v := Evaluate(inc, ch)
	if v.Decision != DecisionReject {
		t.Fatalf("decision = %s (%s), want reject", v.Decision, v.Reason)
	}
	if v.Shadow != "" {
		t.Fatalf("Shadow = %q after rejection, want empty", v.Shadow)
	}
	if v.ChallengerUpper >= v.IncumbentAccuracy {
		t.Fatal("fixture must place the challenger's whole interval below the incumbent")
	}
}

// Every verdict must carry its own explanation and a coherent serving decision.
func TestVerdictAlwaysExplainsItself(t *testing.T) {
	cases := []struct{ inc, ch Record }{
		{rec("v1", 500, 250, 200, 0.5), rec("v2", 5, 3, 60, 0.5)},
		{rec("v1", 500, 250, 200, 0.5), rec("v2", 400, 380, 1, 0.5)},
		{rec("v1", 500, 240, 200, 0.5), rec("v2", 600, 400, 90, 0.5)},
		{rec("v1", 500, 400, 200, 0.5), rec("v2", 600, 200, 90, 0.5)},
		{Record{}, Record{}},
	}
	for i, c := range cases {
		v := Evaluate(c.inc, c.ch)
		if v.Reason == "" {
			t.Fatalf("case %d produced no reason", i)
		}
		switch v.Decision {
		case DecisionPromote:
			if v.Serving != c.ch.Version {
				t.Fatalf("case %d promoted but serves %q", i, v.Serving)
			}
		case DecisionHold:
			if v.Serving != c.inc.Version || v.Shadow != c.ch.Version {
				t.Fatalf("case %d held but serving=%q shadow=%q", i, v.Serving, v.Shadow)
			}
		case DecisionReject:
			if v.Serving != c.inc.Version || v.Shadow != "" {
				t.Fatalf("case %d rejected but serving=%q shadow=%q", i, v.Serving, v.Shadow)
			}
		}
	}
}

func TestWilsonInterval(t *testing.T) {
	// Known case: 50/100 → approximately [0.404, 0.596].
	lo, hi := WilsonInterval(50, 100)
	if math.Abs(lo-0.4038) > 0.001 || math.Abs(hi-0.5962) > 0.001 {
		t.Fatalf("WilsonInterval(50,100) = [%v, %v]", lo, hi)
	}
	// n=0 must be maximally uninformative, never a point estimate.
	if lo, hi := WilsonInterval(0, 0); lo != 0 || hi != 1 {
		t.Fatalf("WilsonInterval(0,0) = [%v, %v], want [0,1]", lo, hi)
	}
	// Bounds stay inside [0,1] at the extremes, and the interval narrows with n.
	for _, n := range []int{1, 10, 1000, 100000} {
		lo, hi := WilsonInterval(n, n)
		if lo < 0 || hi > 1 || lo > hi {
			t.Fatalf("n=%d gave [%v, %v]", n, lo, hi)
		}
	}
	loSmall, hiSmall := WilsonInterval(30, 60)
	loBig, hiBig := WilsonInterval(3000, 6000)
	if (hiBig - loBig) >= (hiSmall - loSmall) {
		t.Fatal("interval did not narrow with sample size")
	}
}

func TestRecordAccessors(t *testing.T) {
	if got := (Record{}).Accuracy(); got != 0 {
		t.Fatalf("empty accuracy = %v", got)
	}
	r := rec("v", 10, 7, 14, 0.5)
	if math.Abs(r.Accuracy()-0.7) > 1e-12 {
		t.Fatalf("accuracy = %v", r.Accuracy())
	}
	if math.Abs(r.WindowDays()-14) > 1e-9 {
		t.Fatalf("windowDays = %v", r.WindowDays())
	}
	if got := (Record{FirstTs: 100, LastTs: 50}).WindowDays(); got != 0 {
		t.Fatalf("inverted window = %v, want 0", got)
	}
}
