package pipeline

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/canary"
	"github.com/nyaungnicholas-wq/signaldeck/internal/modelhealth"
)

// streakShadow is the strongest shadow record a retired model could show over
// d distinct days: 40 observations a day, all correct, against a 0.5 null.
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

func retiredScore() modelhealth.Score {
	return modelhealth.Score{Verdict: modelhealth.VerdictRetired, Emitting: false}
}

// One day short of the floor leaves the stored verdict exactly as it was, and
// still returns the readmission block so the record shows the shortfall.
func TestApplyReadmissionBelowFloorLeavesRetired(t *testing.T) {
	s := retiredScore()
	ra := ApplyReadmission(&s, streakShadow(canary.ReadmitMinDistinctDays-1))
	if ra.Eligible {
		t.Fatal("eligible below the day floor")
	}
	if s.Verdict != modelhealth.VerdictRetired || s.Emitting {
		t.Fatalf("score changed below the floor: %+v", s)
	}
}

// At the floor the persisted score flips: this is the verdict ModelEmitting and
// the forecast monitor read, which the API-only path never wrote.
func TestApplyReadmissionAtFloorFlipsStoredVerdict(t *testing.T) {
	s := retiredScore()
	ra := ApplyReadmission(&s, streakShadow(canary.ReadmitMinDistinctDays))
	if !ra.Eligible {
		t.Fatalf("not eligible at the floor: %s", ra.Reason)
	}
	if s.Verdict != modelhealth.VerdictReadmitted || !s.Emitting {
		t.Fatalf("score not flipped: %+v", s)
	}
	if len(s.Reasons) != 1 || !strings.HasPrefix(s.Reasons[0], "re-admitted by the coded threshold") {
		t.Fatalf("reason not recorded: %v", s.Reasons)
	}
}

// A model that is not retired is never touched, whatever its shadow says.
func TestApplyReadmissionIgnoresNonRetired(t *testing.T) {
	s := modelhealth.Score{Verdict: modelhealth.VerdictHealthy, Emitting: true}
	ApplyReadmission(&s, streakShadow(canary.ReadmitMinDistinctDays))
	if s.Verdict != modelhealth.VerdictHealthy || !s.Emitting {
		t.Fatalf("healthy score changed: %+v", s)
	}
}
