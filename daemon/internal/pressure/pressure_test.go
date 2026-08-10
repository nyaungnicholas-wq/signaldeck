package pressure

import (
	"errors"
	"testing"
)

// predictiveSamples builds n rows where the pressure sign correctly predicts the
// realized direction (pressure>0 => up), balanced 50/50 so the naive base rate is
// 0.5 and a perfect signal shows lift +0.5.
func predictiveSamples(n int, anti bool) []Sample {
	out := make([]Sample, n)
	for i := 0; i < n; i++ {
		up := i % 2 // alternate 1,0,1,0 -> 50/50
		p := 0.5
		if up == 0 {
			p = -0.5
		}
		if anti {
			p = -p // pressure sign now points the WRONG way
		}
		out[i] = Sample{Ts: int64(i), Pressure: p, Up: up}
	}
	return out
}

func TestEvaluatePredictiveHasPositiveLift(t *testing.T) {
	g, err := Evaluate(predictiveSamples(200, false), 5)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if g.Accuracy < 0.99 {
		t.Errorf("perfect signal accuracy = %.3f, want ~1.0", g.Accuracy)
	}
	if g.BaseRate < 0.49 || g.BaseRate > 0.51 {
		t.Errorf("baseRate = %.3f, want ~0.5 (balanced)", g.BaseRate)
	}
	if g.Lift < 0.45 {
		t.Errorf("lift = %.3f, want ~+0.5", g.Lift)
	}
}

func TestEvaluateAntiPredictiveHasNegativeLift(t *testing.T) {
	// This is the live-fleet case: the fixed-weight pressure sign points the
	// wrong way, so the leg must grade a negative lift and get benched.
	g, err := Evaluate(predictiveSamples(200, true), 5)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if g.Lift > 0 {
		t.Errorf("anti-predictive lift = %.3f, want <= 0 (leg should bench)", g.Lift)
	}
	if g.Accuracy > 0.01 {
		t.Errorf("anti-predictive accuracy = %.3f, want ~0", g.Accuracy)
	}
}

func TestEvaluateInsufficientData(t *testing.T) {
	// Expressed in terms of the CONSTANTS, not a magic number. This asserted 50
	// samples against a 60-row floor; when 7afccf6 deduplicated the feature
	// store to one row per trading day the floor's unit changed from rows to
	// days underneath it, and a literal here would have to be found and edited
	// by hand every time — which is precisely how the unit change went unnoticed
	// for three days while every leg starved.
	if _, err := Evaluate(predictiveSamples(minSamples-1, false), 3); !errors.Is(err, ErrInsufficientData) {
		t.Errorf("%d samples (one below minSamples): err = %v, want ErrInsufficientData",
			minSamples-1, err)
	}
	// The per-fold gate must refuse independently of the sample floor: enough
	// samples overall, too few to fill each fold.
	if _, err := Evaluate(predictiveSamples(minSamples, false), minSamples); !errors.Is(err, ErrInsufficientData) {
		t.Errorf("%d samples across %d folds: err = %v, want ErrInsufficientData",
			minSamples, minSamples, err)
	}
}

func TestEvaluateBadParams(t *testing.T) {
	if _, err := Evaluate(predictiveSamples(200, false), 1); !errors.Is(err, ErrBadParams) {
		t.Errorf("folds<2: err = %v, want ErrBadParams", err)
	}
}

// TestNoLookahead proves the walk-forward scores only strictly-later blocks: the
// earliest 1/folds of the data is never in a test block, so corrupting its labels
// cannot change the grade.
func TestNoLookahead(t *testing.T) {
	base := predictiveSamples(200, false)
	g1, err := Evaluate(base, 5)
	if err != nil {
		t.Fatalf("Evaluate base: %v", err)
	}
	// Flip the labels of the first fold-block (indices [0:40) at folds=5, n=200).
	corrupt := make([]Sample, len(base))
	copy(corrupt, base)
	for i := 0; i < 40; i++ {
		corrupt[i].Up = 1 - corrupt[i].Up
		corrupt[i].Pressure = -corrupt[i].Pressure
	}
	g2, err := Evaluate(corrupt, 5)
	if err != nil {
		t.Fatalf("Evaluate corrupt: %v", err)
	}
	if g1 != g2 {
		t.Errorf("grade changed after corrupting the earliest (never-scored) block:\n base=%+v\n corr=%+v", g1, g2)
	}
}

func TestRunReturnsLatestLegProb(t *testing.T) {
	prob, _, ok := Run(predictiveSamples(200, false), 0.4, 5)
	if !ok {
		t.Fatal("Run ok=false on sufficient data")
	}
	if want := (0.4 + 1) / 2; prob != want {
		t.Errorf("latest leg prob = %.3f, want %.3f = (0.4+1)/2", prob, want)
	}
}

func TestRunRefusesThinData(t *testing.T) {
	if _, _, ok := Run(predictiveSamples(30, false), 0.4, 5); ok {
		t.Error("Run ok=true on thin data; want ok=false so no ungraded lift surfaces")
	}
}
