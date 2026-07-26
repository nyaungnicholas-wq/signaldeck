package canary

import (
	"math"
	"strings"
	"testing"
)

const day = int64(86400)

// rec builds an arm observed on every day of its window — the ordinary case.
// Records where rows and days come apart are built with spanRec below.
func rec(version string, n, correct int, days int64, baseline float64) Record {
	return Record{
		Version: version, N: n, Correct: correct, Days: int(days),
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
	// This hold is a considered one — both arms cleared the floors and the
	// comparison was actually made. If the symmetric gate ever swallowed this
	// case the decision would still read "hold" and the distinction would be
	// lost, which is the confusion Comparable exists to prevent.
	if !v.Comparable {
		t.Fatalf("a graded comparison reported itself as impossible: %q", v.Reason)
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
	if !v.Comparable {
		t.Fatalf("held on the baseline without a comparison having been made: %q", v.Reason)
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

// ── The window rule must bind BOTH arms ──────────────────────────────────
//
// Live reproduction, data/signaldeck.db on 2026-07-26, horizon 1d, using the
// CanaryRunner's own aggregation:
//
//	ver   n     distinct_days  span_days  acc
//	8     1046  2              0.174      0.4618   <- the INCUMBENT, the bar
//	10    6957  11             9.417      0.4946   <- the challenger
//
// The incumbent's entire live record is four hours and ten minutes long. The
// 14-day rule was applied to the challenger only, so the moment the
// challenger's span crossed 14 days it would have been promoted against four
// hours of one market state — and had those four hours fallen the other way it
// would have been rejected instead. Whichever direction the noise in a
// four-hour incumbent points is what decides which model serves, which is not
// a comparison at all.

// spanRec builds an arm with an explicit distinct-day count and span, so a
// record can be short in days while long in rows — the live v8 shape.
func spanRec(version string, n, correct, distinctDays int, spanDays, baseline float64) Record {
	return Record{
		Version: version, N: n, Correct: correct, Days: distinctDays,
		FirstTs: 0, LastTs: int64(spanDays * 86400), BaselineAccuracy: baseline,
	}
}

func TestIncumbentObservedForFourHoursCannotBeTheBar(t *testing.T) {
	inc := spanRec("v8", 1046, 483, 2, 0.174, 0.47) // 46.18%, the live shape
	ch := spanRec("v10", 6957, 3441, 90, 90, 0.47)  // 49.46% over 90 days
	v := Evaluate(inc, ch)
	if v.Decision == DecisionPromote {
		t.Fatalf("promoted against an incumbent observed for %.3f days: %+v", inc.WindowDays(), v)
	}
	if v.Comparable {
		t.Fatal("reported a comparison as made when one arm cleared no floor")
	}
	if !strings.Contains(v.Reason, "incumbent") {
		t.Fatalf("reason does not name the incumbent as the blocker: %q", v.Reason)
	}
	if v.Serving != "v8" || v.Shadow != "v10" {
		t.Fatalf("serving=%q shadow=%q, want the incumbent still serving and the challenger still shadowing", v.Serving, v.Shadow)
	}
}

// The asymmetry cuts the other way too, and that direction is worse: reject is
// terminal. An incumbent that happened to be right for four hours ends the
// trial of a challenger with a real 60% record over three months.
func TestFourHourIncumbentCannotRejectAChallenger(t *testing.T) {
	inc := spanRec("v8", 1046, 890, 2, 0.174, 0.5) // 85.1% over four hours
	ch := spanRec("v10", 600, 360, 90, 90, 0.5)    // 60.0% over 90 days
	v := Evaluate(inc, ch)
	if v.Decision == DecisionReject {
		t.Fatalf("ended a trial on four hours of incumbent record: %+v", v)
	}
	if v.Shadow != "v10" {
		t.Fatalf("Shadow = %q, want the challenger still recording", v.Shadow)
	}
}

// The floors must be one function applied twice, not two rules. Any record too
// thin to be a challenger is too thin to be a bar, so a record that fails in
// one role must fail in the other.
func TestFloorsAreSymmetric(t *testing.T) {
	good := spanRec("good", 600, 390, 90, 90, 0.5)
	thin := []Record{
		spanRec("fewObs", 10, 6, 90, 90, 0.5),         // under the observation floor
		spanRec("shortSpan", 600, 390, 2, 0.174, 0.5), // under the window floor
		spanRec("fewDays", 600, 390, 2, 40, 0.5),      // long span, two days of data
		{Version: "noDays", N: 600, Correct: 390, FirstTs: 0, LastTs: 90 * day, BaselineAccuracy: 0.5},
	}
	for _, r := range thin {
		asChallenger := Evaluate(good, r)
		asIncumbent := Evaluate(r, good)
		if asChallenger.Comparable || asIncumbent.Comparable {
			t.Fatalf("%s: comparable as challenger=%v as incumbent=%v — the floor binds one side only",
				r.Version, asChallenger.Comparable, asIncumbent.Comparable)
		}
		for _, v := range []Verdict{asChallenger, asIncumbent} {
			if v.Decision != DecisionHold {
				t.Fatalf("%s: decision = %s (%s), want hold when no comparison was possible", r.Version, v.Decision, v.Reason)
			}
			if !strings.Contains(v.Reason, "no comparison possible") {
				t.Fatalf("%s: reason reads like a considered decision: %q", r.Version, v.Reason)
			}
		}
	}
}

// A long span is not a long record. 1,046 observations that all land on two
// days are two days of evidence however far apart those days sit, so the
// window floor counts DISTINCT days — the same days-not-rows rule the rest of
// the platform resamples on.
func TestWindowFloorCountsDistinctDaysNotSpan(t *testing.T) {
	inc := spanRec("v1", 2000, 960, 200, 200, 0.5)
	ch := spanRec("v2", 6000, 3900, 2, 40, 0.5) // 65% on two days, 40 days apart
	v := Evaluate(inc, ch)
	if v.Decision == DecisionPromote {
		t.Fatalf("promoted a two-day record because its span was 40 days: %+v", v)
	}
	if v.DaysNeeded <= 0 {
		t.Fatalf("DaysNeeded = %v, want the distinct-day shortfall stated", v.DaysNeeded)
	}
}

// An arm that does not say how many distinct days it covers cannot be checked
// against the window floor, and an unchecked floor is not a floor. Withhold
// the comparison and say so, rather than assuming the span stands in for it.
func TestUnreportedDayCountWithholdsTheComparison(t *testing.T) {
	inc := spanRec("v1", 2000, 960, 200, 200, 0.5)
	ch := Record{Version: "v2", N: 6000, Correct: 3900, FirstTs: 0, LastTs: 90 * day, BaselineAccuracy: 0.5}
	v := Evaluate(inc, ch)
	if v.Decision != DecisionHold || v.Comparable {
		t.Fatalf("graded an arm whose distinct-day count is unknown: %+v", v)
	}
	if !strings.Contains(v.Reason, "distinct days") {
		t.Fatalf("reason does not state what is missing: %q", v.Reason)
	}
}

// DistinctDays is the one way a caller may fill Record.Days: the live v8 record
// spans 0.174 days and touches 2 distinct UTC days, and neither number can be
// derived from the other.
func TestDistinctDays(t *testing.T) {
	if got := DistinctDays(nil); got != 0 {
		t.Fatalf("DistinctDays(nil) = %d, want 0", got)
	}
	// Four hours of observations straddling a UTC midnight: two days, 0.17 span.
	ts := []int64{86400 - 3600, 86400 - 60, 86400 + 60, 86400 + 3600}
	if got := DistinctDays(ts); got != 2 {
		t.Fatalf("DistinctDays(straddling midnight) = %d, want 2", got)
	}
	// Repeats within a day are one day, however many rows there are.
	same := make([]int64, 500)
	for i := range same {
		same[i] = 5*day + int64(i)
	}
	if got := DistinctDays(same); got != 1 {
		t.Fatalf("DistinctDays(500 rows in one day) = %d, want 1", got)
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
