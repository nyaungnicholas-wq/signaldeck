package canary

import (
	"math"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
)

// The threshold is pinned to the platform's interval floor, not free-floating:
// 2x clusterstat.MinDistinctDays. If the floor moves, re-admission moves with
// it, and this test is the tripwire that makes that coupling deliberate.
func TestReadmitThresholdIsTwiceTheIntervalFloor(t *testing.T) {
	if ReadmitMinDistinctDays != 2*clusterstat.MinDistinctDays {
		t.Fatalf("ReadmitMinDistinctDays = %d, want 2x clusterstat.MinDistinctDays (%d)",
			ReadmitMinDistinctDays, 2*clusterstat.MinDistinctDays)
	}
}

// One day short of the threshold cannot re-admit, however perfect the streak.
func TestReadmitRefusesBelowDayFloor(t *testing.T) {
	shadow := rec("shadow", 600, 540, int64(ReadmitMinDistinctDays-1), 0.5) // 90% on 19 days
	ra := Readmit(shadow)
	if ra.Eligible {
		t.Fatalf("re-admitted on %d days: %q", ReadmitMinDistinctDays-1, ra.Reason)
	}
	if !strings.Contains(ra.Reason, "distinct days") {
		t.Fatalf("reason does not name the day floor: %q", ra.Reason)
	}
}

// At the floor, a record whose day-clustered lower bound clears the
// prequential null re-admits — and publishes the inputs it was decided on.
func TestReadmitAcceptsAtDayFloorWithClearingBound(t *testing.T) {
	shadow := rec("shadow", 600, 540, int64(ReadmitMinDistinctDays), 0.5)
	ra := Readmit(shadow)
	if !ra.Eligible {
		t.Fatalf("refused a clearing record at the day floor: %q", ra.Reason)
	}
	if ra.DesignEffect <= 0 || ra.EffectiveN <= 0 {
		t.Fatalf("designEffect=%v effectiveN=%v, want both published", ra.DesignEffect, ra.EffectiveN)
	}
	if ra.Lower <= ra.Null || ra.IntervalMethod != "day-clustered-wilson" {
		t.Fatalf("eligible with lower=%v null=%v method=%q", ra.Lower, ra.Null, ra.IntervalMethod)
	}
}

// A record that withholds its per-day tallies gets no interval and therefore
// no re-admission: a row-count interval cannot re-admit what a day-clustered
// one retired.
func TestReadmitRefusesWithheldInterval(t *testing.T) {
	shadow := Record{Version: "shadow", N: 600, Correct: 540, Days: 40,
		FirstTs: 0, LastTs: 40 * day, BaselineAccuracy: 0.5}
	ra := Readmit(shadow)
	if ra.Eligible {
		t.Fatal("re-admitted without a day-clustered interval")
	}
	if ra.IntervalMethod != "withheld" {
		t.Fatalf("method = %q, want withheld", ra.IntervalMethod)
	}
}

// The promotion bar IS the re-admission rule: a successor that clears the
// incumbent on 19 days is held, not promoted, and the reason says which bar
// blocked it. The two gates deciding the same record must never disagree.
func TestPromotionBarIsTheReadmissionThreshold(t *testing.T) {
	inc := rec("v1", 2000, 960, 400, 0.5)                              // 48%
	ch := rec("v2", 600, 390, int64(ReadmitMinDistinctDays-1), 0.5)    // 65% on 19 days
	v := Evaluate(inc, ch)
	if v.Decision == DecisionPromote {
		t.Fatalf("promoted below the re-admission day floor: %+v", v)
	}
	if v.Decision != DecisionHold || v.Serving != "v1" {
		t.Fatalf("decision=%s serving=%q, want hold with the incumbent serving", v.Decision, v.Serving)
	}
	if !strings.Contains(v.Reason, "re-admission threshold") {
		t.Fatalf("reason does not name the re-admission threshold: %q", v.Reason)
	}
	// One more day and the same record promotes: the hold above was the day
	// floor and nothing else.
	ch20 := rec("v2", 600, 390, int64(ReadmitMinDistinctDays), 0.5)
	if v := Evaluate(inc, ch20); v.Decision != DecisionPromote {
		t.Fatalf("decision = %s (%s), want promote at the day floor", v.Decision, v.Reason)
	}
}

// PrequentialBaseline replays the hindsight-free constant guess. Day one is a
// coin flip; after an all-up day the guess is "up"; a mid-window class flip is
// credited only from the day the prior majority actually flips.
func TestPrequentialBaseline(t *testing.T) {
	days := []DayTally{{Day: 0, N: 10}, {Day: 1, N: 10}, {Day: 2, N: 10}}
	ups := map[int64]int{0: 10, 1: 10, 2: 0}
	// Day 0: coin flip = 5. Day 1: guess up, 10 ups = 10. Day 2: guess up, 0 ups = 0.
	got := PrequentialBaseline(days, ups)
	if want := 15.0 / 30.0; math.Abs(got-want) > 1e-12 {
		t.Fatalf("baseline = %v, want %v", got, want)
	}
	if got := PrequentialBaseline(nil, nil); got != 0.5 {
		t.Fatalf("empty baseline = %v, want 0.5", got)
	}
}
