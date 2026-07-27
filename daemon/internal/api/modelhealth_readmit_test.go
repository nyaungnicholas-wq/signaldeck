package api

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/canary"
)

// streakShadow builds the strongest shadow record a retired model could show
// over d distinct UTC days: 40 observations a day, every one correct, against
// a 0.5 prequential null. If any record could argue its way past a day floor,
// this one could — which is what makes it the right fixture for proving the
// floor is a floor.
func streakShadow(d int) canary.Record {
	tallies := make([]canary.DayTally, d)
	for i := range tallies {
		tallies[i] = canary.DayTally{Day: int64(i), N: 40, Hits: 40}
	}
	return canary.Record{
		Version: "shadow-1d", N: 40 * d, Correct: 40 * d, Days: d,
		FirstTs: 0, LastTs: int64(d-1) * 86400, DayTallies: tallies,
		BaselineAccuracy: 0.5,
	}
}

func retiredMeta() map[string]any {
	return map[string]any{
		"model": "directional-ensemble-1d", "verdict": "retired", "emitting": false,
	}
}

// A 19-day streak — even a PERFECT one — cannot re-admit. The threshold is
// canary.ReadmitMinDistinctDays (2x the platform's 10-day interval floor),
// and a coded threshold that bends for a good-looking streak one day short is
// a judgment call wearing a constant.
func TestNineteenDayStreakCannotReadmit(t *testing.T) {
	v := applyReadmission(retiredMeta(), streakShadow(canary.ReadmitMinDistinctDays-1))
	if emitting, _ := v["emitting"].(bool); emitting {
		t.Fatal("a 19-day streak flipped emitting back to true")
	}
	if v["verdict"] != "retired" {
		t.Fatalf("verdict = %v, want retired to stand", v["verdict"])
	}
	if _, readmitted := v["readmitted"]; readmitted {
		t.Fatal("readmitted was recorded below the day floor")
	}
	ra, ok := v["readmission"].(canary.Readmission)
	if !ok {
		t.Fatal("the readmission block must be published even when the record falls short")
	}
	if ra.Eligible {
		t.Fatal("readmission reported eligible on 19 days")
	}
	if !strings.Contains(ra.Reason, "19 distinct days") || !strings.Contains(ra.Reason, "20") {
		t.Fatalf("reason does not state the shortfall against the threshold: %q", ra.Reason)
	}
}

// At the threshold the same record re-admits, and the payload carries the
// numbers the decision was computed from — designEffect and effectiveN, not
// just the verdict.
func TestTwentyDayRecordReadmitsWithPublishedInputs(t *testing.T) {
	v := applyReadmission(retiredMeta(), streakShadow(canary.ReadmitMinDistinctDays))
	if emitting, _ := v["emitting"].(bool); !emitting {
		t.Fatal("a record clearing the coded threshold must flip emitting back to true")
	}
	if readmitted, _ := v["readmitted"].(bool); !readmitted {
		t.Fatal("readmitted=true must be recorded — it is what guardDerivedVariant honours")
	}
	if v["verdict"] != "readmitted" {
		t.Fatalf("verdict = %v, want readmitted", v["verdict"])
	}
	ra := v["readmission"].(canary.Readmission)
	if !ra.Eligible {
		t.Fatalf("block says ineligible after re-admission: %q", ra.Reason)
	}
	if ra.DesignEffect <= 0 || ra.EffectiveN <= 0 {
		t.Fatalf("designEffect=%v effectiveN=%v — the interval's inputs must be published",
			ra.DesignEffect, ra.EffectiveN)
	}
	if ra.Lower <= ra.Null {
		t.Fatalf("re-admitted with lower bound %v not clearing the null %v", ra.Lower, ra.Null)
	}
}

// A shadow record whose lower bound sits on the null cannot re-admit however
// many days it runs: the day floor is necessary, never sufficient.
func TestNullHuggingRecordCannotReadmitOnDaysAlone(t *testing.T) {
	d := 3 * canary.ReadmitMinDistinctDays
	tallies := make([]canary.DayTally, d)
	for i := range tallies {
		tallies[i] = canary.DayTally{Day: int64(i), N: 40, Hits: 20}
	}
	shadow := canary.Record{
		Version: "shadow-1d", N: 40 * d, Correct: 20 * d, Days: d,
		FirstTs: 0, LastTs: int64(d-1) * 86400, DayTallies: tallies,
		BaselineAccuracy: 0.5,
	}
	v := applyReadmission(retiredMeta(), shadow)
	if emitting, _ := v["emitting"].(bool); emitting {
		t.Fatal("a 50% record re-admitted on day count alone")
	}
	ra := v["readmission"].(canary.Readmission)
	if !strings.Contains(ra.Reason, "prequential null") {
		t.Fatalf("reason does not name the null as the blocker: %q", ra.Reason)
	}
}

// Models the worker has not retired pass through untouched: the door back in
// exists only for records that went out through the door marked retired.
func TestReadmissionLeavesHealthyModelsAlone(t *testing.T) {
	v := applyReadmission(map[string]any{
		"model": "directional-ensemble-1d", "verdict": "healthy", "emitting": true,
	}, streakShadow(canary.ReadmitMinDistinctDays))
	if _, has := v["readmission"]; has {
		t.Fatal("a healthy model must not carry a readmission block")
	}
	if emitting, _ := v["emitting"].(bool); !emitting || v["verdict"] != "healthy" {
		t.Fatalf("healthy model rewritten: %+v", v)
	}
}
