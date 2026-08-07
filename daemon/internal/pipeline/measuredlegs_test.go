package pipeline

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// TestPredictionRunnerDefaultsToMeasuredLegs pins the inverted default. The
// historical default was cold start (keep ungraded legs); this asserts the
// runner now ships production mode, so a leg no trainer has graded is benched
// rather than admitted on absence of evidence.
func TestPredictionRunnerDefaultsToMeasuredLegs(t *testing.T) {
	if !requireMeasuredLegs() {
		t.Fatal("requireMeasuredLegs() = false, want true (production mode is the default)")
	}
}

// TestColdStartEscapeHatchRestoresFailSafe pins the ROLLBACK path, which is the
// reason the flag is an environment variable rather than a constant: a trainer
// outage must be recoverable with a restart, not a rebuild.
func TestColdStartEscapeHatchRestoresFailSafe(t *testing.T) {
	t.Setenv("SIGNALDECK_COLD_START_LEGS", "1")
	if requireMeasuredLegs() {
		t.Fatal("SIGNALDECK_COLD_START_LEGS=1 did not restore cold-start mode")
	}
}

// TestStrictModeBenchesTheUngradedSentimentLeg states the CONSEQUENCE rather
// than the flag, so the test still means something if the plumbing moves.
//
// Sentiment is the leg the flag actually reaches on the live record: nothing
// assigns SentimentLift, it has no RankEdge entry (too few graded symbols to
// clear the fleet evidence floor), and its measured within-day AUC is 0.3805 —
// it ranks backwards. Cold start admits it anyway; production mode does not.
func TestStrictModeBenchesTheUngradedSentimentLeg(t *testing.T) {
	score := 0.9
	base := ensemble.Components{
		PressureScore: 0.1,
		// Ungraded: no SentimentLift, no RankEdge entry.
		SentimentScore: &score,
	}

	cold := base
	cold.RequireMeasuredLegs = false
	if _, ok := ensemble.LegProbabilities(cold)[ensemble.LegSentiment]; !ok {
		t.Fatal("cold start dropped the sentiment leg; the historical contract kept it")
	}

	strict := base
	strict.RequireMeasuredLegs = true
	if _, ok := ensemble.LegProbabilities(strict)[ensemble.LegSentiment]; ok {
		t.Fatal("production mode admitted an ungraded sentiment leg")
	}
}

// TestStrictModeLeavesGradedLegsAlone bounds the blast radius. A graded leg is
// judged on its grade in BOTH modes — if flipping the flag could also bench a
// leg that carries measured evidence, the change would be a coverage cut rather
// than an honesty fix.
func TestStrictModeLeavesGradedLegsAlone(t *testing.T) {
	for _, strict := range []bool{false, true} {
		c := ensemble.Components{
			PressureScore:       0.1,
			RequireMeasuredLegs: strict,
			RankEdge:            map[string]float64{ensemble.LegPressure: 0.02},
		}
		if _, ok := ensemble.LegProbabilities(c)[ensemble.LegPressure]; !ok {
			t.Fatalf("strict=%v benched a leg with a positive measured ranking edge", strict)
		}
	}
}
