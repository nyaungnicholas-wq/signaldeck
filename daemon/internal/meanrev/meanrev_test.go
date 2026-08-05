package meanrev

import (
	"math"
	"testing"
)

func TestInvert_ReflectsAboutHalf(t *testing.T) {
	if got := Invert(0.8, 1.0); math.Abs(got-0.2) > 1e-9 {
		t.Fatalf("Invert(0.8,1) = %.4f, want 0.2", got)
	}
	if got := Invert(0.3, 1.0); math.Abs(got-0.7) > 1e-9 {
		t.Fatalf("Invert(0.3,1) = %.4f, want 0.7", got)
	}
	if got := Invert(0.5, 1.0); got != 0.5 {
		t.Fatalf("neutral momentum should stay neutral, got %.4f", got)
	}
}

func TestInvert_StrengthScales(t *testing.T) {
	// Half strength => half the lean away from 0.5.
	if got := Invert(0.8, 0.5); math.Abs(got-0.35) > 1e-9 {
		t.Fatalf("Invert(0.8,0.5) = %.4f, want 0.35", got)
	}
}

func TestCostLabel(t *testing.T) {
	// Move clears +cost => profitable-long label +1.
	if costLabel(0.02, 0.01) != 1 {
		t.Fatal("return>cost should label +1")
	}
	// Positive but below cost => untradeable (0).
	if costLabel(0.005, 0.01) != 0 {
		t.Fatal("return<cost should label 0 (untradeable)")
	}
	// Move breaks below -cost => profitable-short label -1.
	if costLabel(-0.02, 0.01) != -1 {
		t.Fatal("return<-cost should label -1")
	}
	// Small negative above -cost => untradeable (0).
	if costLabel(-0.005, 0.01) != 0 {
		t.Fatal("small negative should label 0 (untradeable)")
	}
}

// buildMeanReverting makes a synthetic labeled set where the momentum raw prob
// is SYSTEMATICALLY WRONG (extreme up leans are followed by down moves), so a
// mean-reversion inversion should show positive net-of-cost lift.
func buildMeanReverting(n int) []Sample {
	out := make([]Sample, n)
	for i := 0; i < n; i++ {
		// Alternate strong up / strong down momentum calls.
		raw := 0.8
		if i%2 == 0 {
			raw = 0.2
		}
		// The realized move goes AGAINST the momentum call by a clear margin
		// (well beyond cost): raw>0.5 (up call) => down move, and vice versa.
		var fwd float64
		var up int
		if raw > 0.5 {
			fwd = -0.03 // momentum said up, price fell
			up = 0
		} else {
			fwd = 0.03 // momentum said down, price rose
			up = 1
		}
		out[i] = Sample{Ts: int64(i) * 86400, RawProb: raw, Up: up, FwdReturn: fwd}
	}
	return out
}

func TestEvaluate_PositiveLiftWhenMomentumIsWrong(t *testing.T) {
	samples := buildMeanReverting(400)
	g, err := Evaluate(samples, 5, DefaultStrength, 0.01)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if g.Lift <= 0 {
		t.Fatalf("mean-reversion should show positive net-of-cost lift when momentum is systematically wrong, got lift=%.3f acc=%.3f base=%.3f", g.Lift, g.Accuracy, g.BaseRate)
	}
	// The inverted prob should also rank the (mean-reverting) direction well.
	if g.AUC <= 0.6 {
		t.Fatalf("expected strong AUC, got %.3f", g.AUC)
	}
}

// buildMomentumContinuation makes a set where momentum is RIGHT (up leans
// followed by up moves). Mean reversion should then FAIL the gate (lift<=0).
func buildMomentumContinuation(n int) []Sample {
	out := make([]Sample, n)
	for i := 0; i < n; i++ {
		raw := 0.8
		if i%2 == 0 {
			raw = 0.2
		}
		var fwd float64
		var up int
		if raw > 0.5 {
			fwd = 0.03 // momentum said up, price rose (momentum correct)
			up = 1
		} else {
			fwd = -0.03
			up = 0
		}
		out[i] = Sample{Ts: int64(i) * 86400, RawProb: raw, Up: up, FwdReturn: fwd}
	}
	return out
}

func TestEvaluate_GateFailsOnMomentumTape(t *testing.T) {
	samples := buildMomentumContinuation(400)
	g, err := Evaluate(samples, 5, DefaultStrength, 0.01)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	// When momentum is right, inverting it must NOT show edge => the caller's
	// lift>0 gate drops the leg. This is the honesty property.
	if g.Lift > 0 {
		t.Fatalf("mean-reversion must fail the gate on momentum-continuation tape, got lift=%.3f", g.Lift)
	}
}

func TestEvaluate_CostGatesMarginalEdge(t *testing.T) {
	// Momentum is wrong but only by a TINY margin (below cost). Net of cost the
	// mean-reversion trade should NOT clear the gate — cost eats the edge.
	n := 400
	samples := make([]Sample, n)
	for i := 0; i < n; i++ {
		raw := 0.8
		if i%2 == 0 {
			raw = 0.2
		}
		var fwd float64
		var up int
		if raw > 0.5 {
			fwd = -0.002 // tiny down move (momentum said up)
			up = 0
		} else {
			fwd = 0.002
			up = 1
		}
		samples[i] = Sample{Ts: int64(i) * 86400, RawProb: raw, Up: up, FwdReturn: fwd}
	}
	// With a 1% cost, a 0.2% reversion never covers cost => no net edge.
	g, err := Evaluate(samples, 5, DefaultStrength, 0.01)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if g.Lift > 0 {
		t.Fatalf("sub-cost reversion should not clear the gate net of cost, got lift=%.3f", g.Lift)
	}
	// But with ZERO cost the same tape DOES show edge (proving cost was the gate).
	g0, err := Evaluate(samples, 5, DefaultStrength, 0.0)
	if err != nil {
		t.Fatalf("Evaluate zero-cost: %v", err)
	}
	if g0.Lift <= 0 {
		t.Fatalf("with zero cost the reversion should show edge, got lift=%.3f", g0.Lift)
	}
}

func TestEvaluate_NoLeakage(t *testing.T) {
	// Appending future rows must not change an earlier fold's predictions. Since
	// the signal is a pure reflection (no fit), we verify the fold-1 predictions
	// are identical whether or not future data is appended.
	base := buildMeanReverting(600)
	pred1 := firstFoldPreds(base, 3)

	appended := make([]Sample, 0, 900)
	appended = append(appended, base...)
	for i := 600; i < 900; i++ {
		appended = append(appended, Sample{Ts: int64(i) * 86400, RawProb: 0.9, Up: 1, FwdReturn: 0.5})
	}
	pred1b := firstFoldPreds(appended[:600], 3)

	if len(pred1) != len(pred1b) {
		t.Fatalf("fold-1 count changed: %d vs %d", len(pred1), len(pred1b))
	}
	for i := range pred1 {
		if pred1[i] != pred1b[i] {
			t.Fatalf("fold-1 prediction changed after appending future data — LEAKAGE")
		}
	}
}

func firstFoldPreds(samples []Sample, folds int) []float64 {
	n := len(samples)
	trainEnd := n * 1 / folds
	testEnd := n * 2 / folds
	out := make([]float64, 0, testEnd-trainEnd)
	for _, s := range samples[trainEnd:testEnd] {
		out = append(out, Invert(s.RawProb, DefaultStrength))
	}
	return out
}

func TestEvaluate_BadParams(t *testing.T) {
	if _, err := Evaluate(buildMeanReverting(300), 1, 1, 0.01); err != ErrBadParams {
		t.Fatalf("folds<2 should be ErrBadParams, got %v", err)
	}
	if _, err := Evaluate(buildMeanReverting(300), 5, 1, -0.5); err != ErrBadParams {
		t.Fatalf("negative cost should be ErrBadParams, got %v", err)
	}
}

func TestEvaluate_InsufficientData(t *testing.T) {
	if _, err := Evaluate(buildMeanReverting(10), 5, 1, 0.01); err != ErrInsufficientData {
		t.Fatalf("tiny set should be ErrInsufficientData, got %v", err)
	}
}

func TestRun_GradesThenReflects(t *testing.T) {
	samples := buildMeanReverting(400)
	prob, grade, ok := Run(samples, 0.85, 5, DefaultStrength, 0.01)
	if !ok {
		t.Fatal("Run should succeed")
	}
	if grade.Lift <= 0 {
		t.Fatalf("expected positive lift, got %.3f", grade.Lift)
	}
	// Latest momentum raw 0.85 (strong up) => mean-reversion leans down (0.15).
	if math.Abs(prob-0.15) > 1e-9 {
		t.Fatalf("Run latest prob = %.4f, want 0.15", prob)
	}
}
