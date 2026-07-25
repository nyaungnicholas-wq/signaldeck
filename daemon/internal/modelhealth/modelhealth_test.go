package modelhealth

import "testing"

// The single most important test in this package: the real directional
// ensemble, with its real measured numbers, must be RETIRED. If this ever goes
// green while the model keeps emitting, the gate is decorative.
func TestRealDirectionalEnsembleIsRetired(t *testing.T) {
	// Measured 2026-07-24 on the live DB: 12,696 independent symbol-days,
	// 48.0% accuracy against a 54.4% majority-class baseline.
	s := Grade(Inputs{
		Observations: 12696,
		Accuracy:     0.480,
		BaselineAcc:  0.544,
		RecentAcc:    0.483,
		RecentN:      10485,
		BrierSkill:   -0.252,
		CalibrationErr: 0.08,
		AgeDays:      5,
	})
	if s.Verdict != VerdictRetired || s.Emitting {
		t.Fatalf("negative-edge model must be retired and stop emitting, got %s emitting=%v (score %.3f)",
			s.Verdict, s.Emitting, s.Overall)
	}
}

// A model with genuine edge and good calibration keeps running.
func TestHealthyModelKeepsEmitting(t *testing.T) {
	s := Grade(Inputs{
		Observations: 500, Accuracy: 0.72, BaselineAcc: 0.55,
		RecentAcc: 0.73, RecentN: 200, BrierSkill: 0.18,
		CalibrationErr: 0.02, AgeDays: 5, FeatureDriftPct: 0.03,
	})
	if s.Verdict != VerdictHealthy || !s.Emitting {
		t.Fatalf("healthy model graded %s (score %.3f, comps %+v)", s.Verdict, s.Overall, s.Components)
	}
}

// Thin evidence must not condemn a model any more than it may promote one.
func TestThinEvidenceIsProvisional(t *testing.T) {
	s := Grade(Inputs{Observations: 12, Accuracy: 0.30, BaselineAcc: 0.55})
	if s.Verdict != VerdictProvisional {
		t.Fatalf("verdict = %s, want provisional", s.Verdict)
	}
	if !s.Emitting {
		t.Fatal("provisional models keep emitting, labeled experimental")
	}
}

// Edge is measured against the naive baseline, not 50% — otherwise an
// imbalanced up-rate manufactures skill that is not there.
func TestEdgeIsMeasuredAgainstBaselineNotCoinFlip(t *testing.T) {
	// 53% accuracy looks like a win against 50%, but the majority class alone
	// scores 58% — this model is worse than guessing "up" every day.
	s := Grade(Inputs{
		Observations: 1000, Accuracy: 0.53, BaselineAcc: 0.58,
		RecentAcc: 0.53, RecentN: 500, BrierSkill: 0.01,
		CalibrationErr: 0.02, AgeDays: 1,
	})
	if s.Verdict != VerdictRetired {
		t.Fatalf("beating 50%% but losing to the baseline must retire, got %s", s.Verdict)
	}
}

// Good calibration and freshness must not rescue a model with no edge.
func TestStrongComponentsCannotRescueNegativeEdge(t *testing.T) {
	s := Grade(Inputs{
		Observations: 5000, Accuracy: 0.50, BaselineAcc: 0.51,
		RecentAcc: 0.50, RecentN: 1000, BrierSkill: 0.30,
		CalibrationErr: 0.0, AgeDays: 0, FeatureDriftPct: 0.0,
	})
	if s.Emitting {
		t.Fatalf("negative edge must stop emission regardless of other components: %+v", s)
	}
}

func TestDecayingModelIsFlagged(t *testing.T) {
	s := Grade(Inputs{
		Observations: 2000, Accuracy: 0.62, BaselineAcc: 0.55,
		RecentAcc: 0.52, RecentN: 400, // recent decay of 10pp
		BrierSkill: 0.05, CalibrationErr: 0.05, AgeDays: 20,
	})
	var found bool
	for _, r := range s.Reasons {
		if r == "recent accuracy has decayed materially vs its own record" {
			found = true
		}
	}
	if !found {
		t.Fatalf("decay not reported: %+v", s.Reasons)
	}
	if s.Components["drift"] >= 0.5 {
		t.Fatalf("drift component should be penalised, got %.3f", s.Components["drift"])
	}
}

func TestStaleAndDriftedInputsPenalised(t *testing.T) {
	s := Grade(Inputs{
		Observations: 1000, Accuracy: 0.60, BaselineAcc: 0.55,
		RecentAcc: 0.60, RecentN: 500, BrierSkill: 0.05, CalibrationErr: 0.03,
		AgeDays: 200, MaxAgeDays: 90, FeatureDriftPct: 0.5,
	})
	if s.Components["freshness"] != 0 {
		t.Fatalf("a model 200d past a 90d horizon should score 0 freshness, got %.3f",
			s.Components["freshness"])
	}
	if s.Components["stability"] > 0.51 {
		t.Fatalf("50%% feature drift should halve stability, got %.3f", s.Components["stability"])
	}
	if len(s.Reasons) < 2 {
		t.Fatalf("both staleness and drift should be reported: %+v", s.Reasons)
	}
}

func TestNegativeBrierSkillHalvesCalibration(t *testing.T) {
	good := Grade(Inputs{Observations: 500, Accuracy: 0.60, BaselineAcc: 0.55,
		RecentN: 100, RecentAcc: 0.60, BrierSkill: 0.10, CalibrationErr: 0.02, AgeDays: 1})
	bad := Grade(Inputs{Observations: 500, Accuracy: 0.60, BaselineAcc: 0.55,
		RecentN: 100, RecentAcc: 0.60, BrierSkill: -0.10, CalibrationErr: 0.02, AgeDays: 1})
	if bad.Components["calibration"] >= good.Components["calibration"] {
		t.Fatalf("negative Brier skill must reduce calibration: %.3f vs %.3f",
			bad.Components["calibration"], good.Components["calibration"])
	}
}

func TestScoreAndComponentsStayInRange(t *testing.T) {
	extremes := []Inputs{
		{Observations: 100, Accuracy: 1, BaselineAcc: 0, BrierSkill: 5, CalibrationErr: 0, RecentN: 100, RecentAcc: 1},
		{Observations: 100, Accuracy: 0, BaselineAcc: 1, BrierSkill: -5, CalibrationErr: 10,
			AgeDays: 10000, FeatureDriftPct: 5, RecentN: 100},
	}
	for i, in := range extremes {
		s := Grade(in)
		if s.Overall < 0 || s.Overall > 1 {
			t.Fatalf("case %d overall out of range: %.3f", i, s.Overall)
		}
		for k, v := range s.Components {
			if v < 0 || v > 1 {
				t.Fatalf("case %d component %s out of range: %.3f", i, k, v)
			}
		}
	}
}

func TestNoRecentWindowScoresDriftNeutral(t *testing.T) {
	// Inventing a trend from a handful of recent points is how noise becomes a
	// retirement decision — absent evidence, drift must be neutral.
	s := Grade(Inputs{Observations: 200, Accuracy: 0.60, BaselineAcc: 0.55,
		RecentAcc: 0.20, RecentN: 3, BrierSkill: 0.05, CalibrationErr: 0.03, AgeDays: 1})
	if s.Components["drift"] != 0.5 {
		t.Fatalf("drift with a too-thin recent window should be neutral, got %.3f",
			s.Components["drift"])
	}
}
