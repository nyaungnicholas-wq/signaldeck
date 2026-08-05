package researchx

import (
	"fmt"
	"math"
	"testing"
)

// The grid emits every condition set twice, once per direction in
// discoverCalls. follow_pressure and inverse_pressure are exact negations of
// each other (researchx.go: +sign vs -sign of the same pressure_score, over the
// same firing mask), so the two arms of a pair are perfectly anti-correlated:
// the 48x48 correlation matrix of their weekly returns has rank 24 with 24 zero
// eigenvalues, measured 2026-08-03.
//
// That pairing is NOT a double-counted Bonferroni divisor, and this file exists
// to stop anyone "reclaiming" it. Each arm is judged with a ONE-SIDED Wilson
// lower bound, so the pair spends MaxAlpha/divisor in each tail. Testing N
// condition sets two-sided at MaxAlpha/N is identical to testing 2N one-sided
// arms at MaxAlpha/2N. Halving the divisor while leaving the gate one-sided
// would double the per-tail alpha and inflate false positives.
//
// TestMirrorPairEquivalence pins that equality numerically.

func condKey(r Rule) string {
	s := ""
	for _, c := range r.Conds {
		s += fmt.Sprintf("%s|%s|%g|%t;", c.Key, c.Op, c.Val, c.Pct)
	}
	return s
}

// Every rule in the grid has exactly one twin: same conditions, opposite call.
func TestGridIsExactMirrorPairs(t *testing.T) {
	grid := discoverGrid(DiscoverConfig{}.withDefaults().MaxCandidates)
	if len(grid)%2 != 0 {
		t.Fatalf("grid size %d is odd; every condition set must appear in both directions", len(grid))
	}

	calls := map[string]map[string]int{}
	for _, r := range grid {
		k := condKey(r)
		if calls[k] == nil {
			calls[k] = map[string]int{}
		}
		calls[k][r.Call]++
	}

	for k, byCall := range calls {
		if len(byCall) != 2 {
			t.Errorf("condition set %q appears with %d direction(s), want both", k, len(byCall))
		}
		for _, call := range discoverCalls {
			if byCall[call] != 1 {
				t.Errorf("condition set %q has %d x %s, want exactly 1", k, byCall[call], call)
			}
		}
	}

	if got, want := len(calls), len(grid)/2; got != want {
		t.Errorf("distinct condition sets = %d, want %d (grid %d / 2)", got, want, len(grid))
	}
}

// The divisor's factor of 2 from discoverCalls is the two-tail factor. Testing
// n condition sets two-sided at MaxAlpha/n must give the same per-tail level as
// testing 2n one-sided arms at MaxAlpha/2n. If someone drops one direction from
// discoverCalls without also halving the per-tail alpha, this fails.
func TestMirrorPairEquivalence(t *testing.T) {
	for _, prior := range []int{0, 1, 7} {
		cfg := DiscoverConfig{PriorSearches: prior}
		grid := discoverGrid(cfg.withDefaults().MaxCandidates)
		sets := len(grid) / 2

		perTailOneSided := cfg.CorrectedAlpha()               // what the gate applies today
		twoSided := MaxAlpha / float64(sets*(1+prior))        // same family, two-sided framing
		perTailTwoSided := twoSided / 2

		if math.Abs(perTailOneSided-perTailTwoSided) > 1e-15 {
			t.Errorf("prior=%d: one-sided per-tail alpha %g != two-sided per-tail alpha %g",
				prior, perTailOneSided, perTailTwoSided)
		}
		if got, want := cfg.Divisor(), 2*sets*(1+prior); got != want {
			t.Errorf("prior=%d: Divisor()=%d, want 2*%d*%d=%d", prior, got, sets, 1+prior, want)
		}
	}
}
