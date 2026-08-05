package ensemble

import (
	"math"
	"testing"
)

// noisyMonotonePairs builds the shape the live fleet actually produces.
//
// TWO INGREDIENTS, BOTH LOad-BEARING:
//
//  1. A MASS of rows at exactly 0.0 and exactly 1.0 whose outcomes mostly agree
//     with the score. This is what makes the likelihood near-unbounded, and it
//     is what the live record looks like (raw range measured [0.0000, 1.0000]).
//     A fixture that merely TOUCHES 0 and 1 once does not reproduce the bug —
//     verified: the undamped fit converges fine on a single boundary row and
//     only runs away when the boundary carries weight.
//
//  2. A NOISY monotone middle. A perfectly separable fixture is also unbounded,
//     but its correct fit IS a near-step function, so it cannot distinguish
//     "the map collapsed" from "the map is right". With noise there is a
//     genuinely graded answer, so a collapsed output is a real failure.
//
// The 7919 stride and the every-5th flip are fixed, so this is deterministic —
// no seeding, no flake.
func noisyMonotonePairs(mid int, ts func(i int) int64) []Pair {
	const boundaryPairs = 100
	ps := make([]Pair, 0, 2*boundaryPairs+mid+1)
	k := 0
	for i := 0; i < boundaryPairs; i++ {
		lo, hi := 0.0, 1.0 // outcome for raw=0.0 and raw=1.0
		if i%5 == 0 {
			lo, hi = 1, 0 // 20% contradict, so the separation is not perfect
		}
		ps = append(ps, Pair{Pred: 0.0, Actual: lo, Ts: ts(k)})
		k++
		ps = append(ps, Pair{Pred: 1.0, Actual: hi, Ts: ts(k)})
		k++
	}
	for i := 0; i <= mid; i++ {
		p := float64(i) / float64(mid)
		actual := 0.0
		if float64((i*7919)%100) < p*100 {
			actual = 1
		}
		ps = append(ps, Pair{Pred: p, Actual: actual, Ts: ts(k)})
		k++
	}
	return ps
}

// REGRESSION: fitBeta must not run to infinity on near-separable data.
//
// The fleet's raw scores reach exactly 0 and 1, so the logistic MLE for the
// beta design sits at infinity and an UNDAMPED Newton step walks toward it.
// Measured on the live record 2026-08-05, the loop converged to
// a=6.5e8, b=-1.5e8, c=-2.1e9; b<0 tripped the not-strictly-increasing guard,
// so fitBeta returned an error on EVERY fleet-wide fit. CalibrateRanking then
// fell back to isotonic with ranked=false and the pipeline refused to publish
// any calibration at all — for a month, while logging a message that implied a
// held-out comparison had chosen isotonic. It never ran.
//
// The guard is the step-halving in fitBeta. This test fails without it.
func TestFitBetaSurvivesNearSeparableData(t *testing.T) {
	pairs := noisyMonotonePairs(300, func(i int) int64 { return int64(i) })

	bp, err := fitBeta(pairs)
	if err != nil {
		t.Fatalf("fitBeta refused near-separable data: %v", err)
	}
	if !(bp.a > 0) || !(bp.b > 0) {
		t.Fatalf("map is not strictly increasing: a=%g b=%g", bp.a, bp.b)
	}
	// The real defect was magnitude, not sign. Anything in the millions is the
	// iteration escaping, even if the signs happen to come out positive.
	const sane = 1e4
	if math.Abs(bp.a) > sane || math.Abs(bp.b) > sane || math.Abs(bp.c) > sane {
		t.Fatalf("parameters escaped to infinity: a=%g b=%g c=%g", bp.a, bp.b, bp.c)
	}
	// And the fitted map must actually order its inputs.
	prev := -1.0
	distinct := map[float64]bool{}
	for i := 0; i <= 100; i++ {
		v := bp.apply(float64(i) / 100)
		if v < prev {
			t.Fatalf("map inverted at %.2f: %.6f < %.6f", float64(i)/100, v, prev)
		}
		prev = v
		distinct[math.Round(v*10000)/10000] = true
	}
	if len(distinct) < 20 {
		t.Fatalf("map emitted only %d distinct values — it collapsed the ranking it exists to preserve", len(distinct))
	}
}

// The paired beta-vs-isotonic comparison must treat one Ts as ONE observation.
//
// Callers stamp Ts with a real period (the fleet-wide fit stamps the UTC day),
// so every row sharing a Ts also shares one market move. Scoring them as
// independent understates the SE by ~sqrt(rows/periods) and manufactures
// significance: measured on the live 1d holdout (3,942 rows / 14 days) the
// per-row statistic read t=5.32 against a day-clustered t=2.45.
//
// Here every row inside a period is IDENTICAL, so the period carries exactly
// one bit of information no matter how many rows it holds. A per-row SE shrinks
// as the duplication grows; a clustered one does not. Duplicating each period's
// rows must therefore not change the verdict.
func TestCalibrateRankingClustersByTs(t *testing.T) {
	build := func(rowsPerDay int) []Pair {
		var ps []Pair
		for day := 0; day < 40; day++ {
			// A weak, noisy relationship: strong enough to fit, not so strong
			// that either map is obviously right.
			for k := 0; k < rowsPerDay; k++ {
				p := 0.2 + 0.6*float64(day%7)/6
				actual := 0.0
				if (day+k)%2 == 0 {
					actual = 1
				}
				ps = append(ps, Pair{Pred: p, Actual: actual, Ts: int64(day)})
			}
		}
		return ps
	}

	_, cal1, ranked1 := CalibrateRanking(build(1))
	_, cal40, ranked40 := CalibrateRanking(build(40))

	if !cal1 || !cal40 {
		t.Fatalf("expected both to calibrate; got %v and %v", cal1, cal40)
	}
	if ranked1 != ranked40 {
		t.Errorf("verdict changed with row duplication: ranked=%v at 1 row/day but %v at 40 rows/day — "+
			"the SE is being computed per row, so duplicating one market move buys significance",
			ranked1, ranked40)
	}
}

// With only one period in the holdout there is no spread to test, so the
// comparison is not possible and the ranking must be kept rather than collapsed
// on the strength of a single market move.
func TestCalibrateRankingKeepsRankingOnSingleCluster(t *testing.T) {
	// FOUR periods of 100 rows. The 3/4 cut lands exactly on the period-3
	// boundary, so the holdout is period 3 alone — one cluster, no spread.
	ps := noisyMonotonePairs(299, func(i int) int64 { return int64(i / 125) })

	fn, calibrated, ranked := CalibrateRanking(ps)
	if !calibrated {
		t.Fatal("fixture failed to calibrate at all — it is supposed to carry signal")
	}
	if !ranked {
		t.Error("collapsed the ranking with a single-period holdout — one market move cannot justify it")
	}
	prev := -1.0
	for i := 0; i <= 100; i++ {
		if v := fn(float64(i) / 100); v < prev {
			t.Fatalf("returned map inverts at %.2f", float64(i)/100)
		} else {
			prev = v
		}
	}
}
