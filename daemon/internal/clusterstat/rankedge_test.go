package clusterstat

import "testing"

// TestRankEdgeRefusesUngradedRatherThanBenching pins the distinction the whole
// gate rests on: "never measured" must be reported as ok=false so the caller
// keeps the leg on its historical lift gate, NOT as a zero edge that would
// bench a leg on evidence nobody ever collected.
func TestRankEdgeRefusesUngradedRatherThanBenching(t *testing.T) {
	for _, c := range []struct {
		name string
		auc  float64
		n    int
	}{
		{"unscored row", 0, 0},
		{"graded but n=1", 0.9, 1},
		// Under the evidence floor the bound could not clear 0.5 for ANY AUC,
		// so judging the leg there would bench it on arithmetic, not evidence.
		{"strong AUC under the evidence floor", 0.9, RankEdgeMinEval - 1},
		// Degenerate endpoints are artifacts on real data, never a real grade —
		// see RankEdge. Admitting auc=1 would be the worst failure this gate
		// has, so it is pinned here.
		{"degenerate auc=1", 1, 500},
		{"degenerate auc=0", 0, 500},
		{"auc above the unit interval", 1.5, 500},
		{"auc below the unit interval", -0.2, 500},
	} {
		if _, ok := RankEdge(c.auc, c.n); ok {
			t.Fatalf("%s: want ok=false (no usable grade), got ok=true", c.name)
		}
	}
}

func TestRankEdgeSignsAndMonotonicity(t *testing.T) {
	// A coin-flip leg must never be admitted, however many evaluations back it.
	for _, n := range []int{RankEdgeMinEval, 500, 50000} {
		if e, ok := RankEdge(0.5, n); !ok || e > 0 {
			t.Fatalf("auc=0.5 n=%d: want a non-positive edge, got %v (ok=%v)", n, e, ok)
		}
	}
	// A leg that ranks BACKWARDS is strictly negative.
	if e, ok := RankEdge(0.32, 1000); !ok || e >= 0 {
		t.Fatalf("anti-predictive leg: want negative edge, got %v (ok=%v)", e, ok)
	}
	// Nearly-backwards is the realistic shape of a bad leg, and it must bench.
	if e, ok := RankEdge(0.05, 1000); !ok || e >= 0 {
		t.Fatalf("near-inverted leg must grade and bench, got %v (ok=%v)", e, ok)
	}
	// Same AUC, more evidence -> the bound tightens toward the estimate, so a
	// real edge becomes admissible only once it has been earned.
	small, _ := RankEdge(0.56, 100)
	large, _ := RankEdge(0.56, 10000)
	if !(small < large) {
		t.Fatalf("more evaluations must raise the lower bound: n=100 gave %v, n=10000 gave %v", small, large)
	}
	if small > 0 {
		t.Fatalf("auc=0.56 on 100 evaluations should not clear the bar, got %v", small)
	}
	if large <= 0 {
		t.Fatalf("auc=0.56 on 10000 evaluations should clear the bar, got %v", large)
	}
}
