package clusterstat

import (
	"math"
	"math/rand"
	"testing"
)

// RankAUC had no test. It is the statistic RankEdge consumes, and RankEdge is
// the gate that decides whether a leg is allowed to move a published
// probability — so an error here is not a wrong number in a report, it is the
// wrong legs in the blend. The comment on RankEdge records what that already
// cost once: an admission gate measured on the wrong quantity benched the only
// leg that ranked.

// A leg that orders every winner above every loser scores 1; the same leg read
// backwards scores 0. Catches a sign inversion in the Mann-Whitney sum, which
// would admit anti-signal legs and bench the good ones.
func TestRankAUCIsOneWhenPerfectAndZeroWhenReversed(t *testing.T) {
	preds := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6}
	up := []float64{0, 0, 0, 1, 1, 1}
	down := []float64{1, 1, 1, 0, 0, 0}

	if got := RankAUC(preds, up); got != 1 {
		t.Errorf("perfect ordering scored %v, want 1", got)
	}
	if got := RankAUC(preds, down); got != 0 {
		t.Errorf("exactly-reversed ordering scored %v, want 0", got)
	}
}

// The textbook Mann-Whitney case, computed by hand so the U correction is
// pinned to an outside number rather than to whatever the code does.
//
// preds 0.1(neg) 0.35(pos) 0.4(neg) 0.8(pos); the four cross-class pairs are
// 0.35>0.1 win, 0.35<0.4 loss, 0.8>0.1 win, 0.8>0.4 win = 3/4.
// Catches an off-by-one in nPos*(nPos+1)/2 — which shifts every leg's grade by
// a constant and silently re-draws the admission line.
func TestRankAUCMatchesTheHandComputedMannWhitney(t *testing.T) {
	preds := []float64{0.1, 0.4, 0.35, 0.8}
	actuals := []float64{0, 0, 1, 1}
	if got := RankAUC(preds, actuals); math.Abs(got-0.75) > 1e-12 {
		t.Errorf("RankAUC = %v, want 0.75", got)
	}
}

// A leg that emits ONE number for every symbol has no ordering at all, and must
// score exactly 0.5 — at every class balance, not just a balanced one.
//
// This is the load-bearing case. RankAUC sorts with sort.Slice, which is NOT
// stable, so on an all-ties input the row order after sorting is arbitrary.
// Average ranks are the only reason that arbitrariness cannot leak into the
// answer. Drop the tie-averaging and a no-information leg scores anywhere from
// 0 to 1 depending on how the sort happened to land — and a leg that measured
// nothing gets admitted.
func TestRankAUCIsExactlyHalfForAConstantLeg(t *testing.T) {
	for _, nPos := range []int{1, 3, 5, 9} {
		preds := make([]float64, 10)
		actuals := make([]float64, 10)
		for i := range preds {
			preds[i] = 0.42
			if i < nPos {
				actuals[i] = 1
			}
		}
		if got := RankAUC(preds, actuals); got != 0.5 {
			t.Errorf("constant leg with %d/10 positives scored %v, want exactly 0.5", nPos, got)
		}
	}
}

// A tie ACROSS classes is half a win, not a whole one. Two rows, same score,
// opposite outcomes: the only defensible answer is 0.5.
func TestRankAUCSplitsACrossClassTie(t *testing.T) {
	if got := RankAUC([]float64{0.5, 0.5}, []float64{1, 0}); got != 0.5 {
		t.Errorf("one tied pair scored %v, want 0.5", got)
	}
	// And a tie must not be worth more than a clean win.
	clean := RankAUC([]float64{0.6, 0.4}, []float64{1, 0})
	tied := RankAUC([]float64{0.5, 0.5}, []float64{1, 0})
	if !(clean > tied) {
		t.Errorf("a tie (%v) scored as well as a clean win (%v)", tied, clean)
	}
}

// The answer must not depend on the order rows arrive in. sort.Slice is
// unstable, so this is the property that keeps a grade reproducible across
// runs: the same leg, the same rows, a different iteration order, one number.
func TestRankAUCIsInvariantToInputOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	preds := make([]float64, 200)
	actuals := make([]float64, 200)
	for i := range preds {
		// Heavy tie structure on purpose: 8 distinct score levels over 200 rows.
		preds[i] = float64(rng.Intn(8)) / 8
		if rng.Float64() < preds[i]+0.2 {
			actuals[i] = 1
		}
	}
	want := RankAUC(preds, actuals)

	for trial := 0; trial < 25; trial++ {
		p := append([]float64(nil), preds...)
		a := append([]float64(nil), actuals...)
		rng.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i]; a[i], a[j] = a[j], a[i] })
		if got := RankAUC(p, a); got != want {
			t.Fatalf("shuffle %d changed the grade: %v != %v — the statistic is not "+
				"reproducible and neither is any admission decision made from it", trial, got, want)
		}
	}
}

// One class only means nothing about ranking was observed. The documented
// answer is 0.5, and 0.5 is what makes RankEdge refuse to admit — asserted
// below. A silent 0 or 1 here would be a fabricated grade.
func TestRankAUCReportsNoEvidenceWhenOneClassIsEmpty(t *testing.T) {
	if got := RankAUC([]float64{0.1, 0.9, 0.5}, []float64{1, 1, 1}); got != 0.5 {
		t.Errorf("all-positive sample scored %v, want 0.5 (no ranking observable)", got)
	}
	if got := RankAUC([]float64{0.1, 0.9, 0.5}, []float64{0, 0, 0}); got != 0.5 {
		t.Errorf("all-negative sample scored %v, want 0.5 (no ranking observable)", got)
	}
}

// The reason any of the above matters: RankAUC feeds RankEdge, and RankEdge
// decides admission. This pins the composition, not the pieces — a leg with no
// ordering must not be admitted, and a leg that orders well must be, at the
// same evaluation count.
func TestRankAUCFeedsRankEdgeSoANoOrderingLegIsNotAdmitted(t *testing.T) {
	const n = 400
	rng := rand.New(rand.NewSource(9))

	flatPreds := make([]float64, n)
	flatActuals := make([]float64, n)
	sharpPreds := make([]float64, n)
	sharpActuals := make([]float64, n)
	for i := 0; i < n; i++ {
		flatPreds[i] = 0.5
		sharpPreds[i] = rng.Float64()
		if rng.Float64() < 0.5 {
			flatActuals[i] = 1
		}
		// A genuinely informative ordering: P(win) rises with the score.
		if rng.Float64() < 0.1+0.8*sharpPreds[i] {
			sharpActuals[i] = 1
		}
	}

	flatEdge, flatOK := RankEdge(RankAUC(flatPreds, flatActuals), n)
	if !flatOK {
		t.Fatalf("%d evaluations is above RankEdgeMinEval=%d; the grade must be usable", n, RankEdgeMinEval)
	}
	if flatEdge > 0 {
		t.Errorf("a leg with no ordering was credited with edge %+.4f — it would reach the blend", flatEdge)
	}

	sharpAUC := RankAUC(sharpPreds, sharpActuals)
	sharpEdge, sharpOK := RankEdge(sharpAUC, n)
	if !sharpOK {
		t.Fatalf("informative leg (AUC %.4f) produced no usable grade", sharpAUC)
	}
	if sharpEdge <= 0 {
		t.Errorf("a leg measured at AUC %.4f over %d evaluations was benched (edge %+.4f) — "+
			"this is the failure mode RankEdge was written to end", sharpAUC, n, sharpEdge)
	}
}
