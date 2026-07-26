// Regressions for the 2026-07-26 hostile review, finding C3: the isotonic
// recalibration map shipped NON-MONOTONE on 495 of 928 live per-symbol maps
// (53%), the one property isotonic regression exists to guarantee.
//
// Measured on the live DB before the fix (symbol 12, horizon 1d):
//
//	kx=0.361111 ky=0.637852  -> published 63.8%
//	kx=0.365217 ky=0.418225  -> published 41.8%
//
// A MORE bullish raw input published a 22-point LOWER probability, so every
// ranking keyed on cal_prob was scrambled. Root cause: poolAdjacentViolators
// ran PAV (monotone by construction) and THEN applied two weight-DEPENDENT
// per-block transforms — empirical-Bayes shrinkage toward the base rate and
// the rule-of-succession bound. Both are order-preserving only when adjacent
// blocks carry the SAME weight; block weights vary, so the transforms reorder
// the fitted values. The package comment claimed the shrinkage "is monotonic
// (a convex combination with a constant)" — true per block, false ACROSS
// blocks of different weight, which is the case that ships.
package ensemble

import (
	"errors"
	"math/rand"
	"testing"
)

// Property test: over randomized level sets with WIDELY varying block weights
// — the exact condition that broke the shipped map — the fit must always be
// monotone and always pass its own write-time assertion. A single hand-built
// case only proves the one shape; this proves the invariant.
func TestPoolAdjacentViolatorsMonotoneProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20260726))
	for trial := 0; trial < 2000; trial++ {
		n := 2 + rng.Intn(30)
		levels := make([]levelStat, 0, n)
		x := rng.Float64() * 0.3
		for i := 0; i < n; i++ {
			x += 0.0005 + rng.Float64()*0.02
			if x >= 1 {
				break
			}
			// Weights spanning three orders of magnitude: 1..1000 pairs.
			w := 1
			switch rng.Intn(3) {
			case 1:
				w = 1 + rng.Intn(50)
			case 2:
				w = 1 + rng.Intn(1000)
			}
			levels = append(levels, levelStat{x: x, mean: rng.Float64(), weight: w})
		}
		if len(levels) < 2 {
			continue
		}
		kx, ky := poolAdjacentViolators(levels)
		if err := ValidateKnots(kx, ky); err != nil {
			t.Fatalf("trial %d: %v\nlevels=%+v\nkx=%v\nky=%v", trial, err, levels, kx, ky)
		}
	}
}

// nonMonotoneLevels reproduces the live symbol-12/1d shape: a heavy low block,
// a heavy mid block whose mean sits ABOVE the base rate, and a thin top block.
// Shrinkage pulls the thin block hard toward the (low) base rate while barely
// moving the heavy block below it — inverting the pair.
func nonMonotoneLevels() []levelStat {
	return []levelStat{
		{x: 0.30, mean: 0.00, weight: 40}, // drags the base rate down
		{x: 0.36, mean: 0.65, weight: 40}, // heavy: shrinkage barely moves it
		{x: 0.37, mean: 1.00, weight: 1},  // thin: shrinkage collapses it
	}
}

// A higher prediction may never receive a lower fitted frequency, whatever the
// per-block weights are. This is the property the whole layer exists to
// provide; without it cal_prob is not a ranking key.
func TestPoolAdjacentViolatorsMonotoneUnderMixedBlockWeights(t *testing.T) {
	kx, ky := poolAdjacentViolators(nonMonotoneLevels())
	if len(kx) != 3 || len(ky) != 3 {
		t.Fatalf("knot counts kx=%d ky=%d, want 3", len(kx), len(ky))
	}
	for i := 1; i < len(ky); i++ {
		if ky[i] < ky[i-1]-1e-12 {
			t.Fatalf("non-monotone fit: kx=%v -> ky=%v; ky[%d]=%.6f < ky[%d]=%.6f",
				kx, ky, i, ky[i], i-1, ky[i-1])
		}
	}
	// The pooled value must still be dominated by the well-sampled block — a
	// single observation may not drag a 40-pair block anywhere meaningful.
	if ky[2] < 0.45 || ky[2] > 0.60 {
		t.Errorf("ky[2]=%.4f: a w=1 block moved the w=40 block too far", ky[2])
	}
}

// End-to-end through the public Calibrate: the returned map must be
// non-decreasing everywhere, not just at the knots.
func TestCalibrateMapIsMonotoneEverywhere(t *testing.T) {
	var pairs []Pair
	for i := 0; i < 40; i++ {
		pairs = append(pairs, Pair{Pred: 0.30, Actual: 0})
	}
	for i := 0; i < 40; i++ {
		a := 0.0
		if i < 26 { // mean 0.65
			a = 1
		}
		pairs = append(pairs, Pair{Pred: 0.36, Actual: a})
	}
	pairs = append(pairs, Pair{Pred: 0.37, Actual: 1})

	fn, ok := Calibrate(pairs)
	if !ok {
		t.Fatal("expected calibrated=true with 81 pairs and real spread")
	}
	prev := -1.0
	for i := 0; i <= 1000; i++ {
		x := float64(i) / 1000
		y := fn(x)
		if y < prev-1e-12 {
			t.Fatalf("calibration map inverted at x=%.3f: fn=%.6f < previous %.6f", x, y, prev)
		}
		prev = y
	}
}

// ValidateKnots is the write-time assertion: a non-monotone map must FAIL
// rather than ship. Without it a future weight-dependent transform silently
// reintroduces C3.
func TestValidateKnots(t *testing.T) {
	if err := ValidateKnots([]float64{0.1, 0.2, 0.3}, []float64{0.2, 0.4, 0.4}); err != nil {
		t.Fatalf("valid knots rejected: %v", err)
	}
	// ky decreasing — the live defect.
	if err := ValidateKnots([]float64{0.361, 0.365}, []float64{0.6379, 0.4182}); !errors.Is(err, ErrNonMonotone) {
		t.Errorf("non-monotone ky accepted, got err=%v", err)
	}
	// kx not strictly increasing — interpolate assumes it and would divide by 0.
	if err := ValidateKnots([]float64{0.3, 0.3}, []float64{0.4, 0.5}); err == nil {
		t.Error("duplicate kx accepted")
	}
	if err := ValidateKnots([]float64{0.3, 0.4}, []float64{0.4}); err == nil {
		t.Error("mismatched knot lengths accepted")
	}
	if err := ValidateKnots(nil, nil); err == nil {
		t.Error("empty knots accepted")
	}
	// A probability outside [0,1] is not a frequency.
	if err := ValidateKnots([]float64{0.3, 0.4}, []float64{0.4, 1.5}); err == nil {
		t.Error("out-of-range ky accepted")
	}
}

// CalibrateKnots persists what it returns, so it must never hand back a map
// ValidateKnots would reject.
func TestCalibrateKnotsOutputPassesItsOwnAssertion(t *testing.T) {
	// One pair per UTC day: CalibrateKnots also enforces MinCalibrationDays, so
	// an unstamped fixture would be refused for spanning a single day and this
	// test would stop exercising the monotonicity assertion at all.
	var pairs []Pair
	for i := 0; i < 40; i++ {
		pairs = append(pairs, Pair{Pred: 0.30, Actual: 0, Ts: int64(i) * 86400})
	}
	for i := 0; i < 40; i++ {
		a := 0.0
		if i < 26 {
			a = 1
		}
		pairs = append(pairs, Pair{Pred: 0.36, Actual: a, Ts: int64(i) * 86400})
	}
	pairs = append(pairs, Pair{Pred: 0.37, Actual: 1, Ts: 0})

	kx, ky, ok := CalibrateKnots(pairs)
	if !ok {
		t.Fatal("expected calibrated=true")
	}
	if err := ValidateKnots(kx, ky); err != nil {
		t.Fatalf("CalibrateKnots returned knots it would itself reject: %v (kx=%v ky=%v)", err, kx, ky)
	}
}

// 495 non-monotone maps are already PERSISTED. Refitting is hourly; until then
// the read path must refuse them. A withheld correction (identity) beats a
// published inversion — serving them scrambles every cal_prob ranking.
func TestMapFromKnotsRefusesPersistedNonMonotoneMap(t *testing.T) {
	// The live symbol-12/1d knots, verbatim.
	kx := []float64{0.3611111111111111, 0.3652173913043478}
	ky := []float64{0.6378522467631379, 0.41822515584891823}

	if _, err := MapFromKnotsChecked(kx, ky); !errors.Is(err, ErrNonMonotone) {
		t.Fatalf("MapFromKnotsChecked accepted an inverted persisted map: err=%v", err)
	}
	fn := MapFromKnots(kx, ky)
	if got := fn(0.3611); !approx(got, 0.3611, 1e-9) {
		t.Errorf("refused map must fall back to identity; fn(0.3611)=%v", got)
	}
	if got := fn(0.3652); !approx(got, 0.3652, 1e-9) {
		t.Errorf("refused map must fall back to identity; fn(0.3652)=%v", got)
	}
	// A valid persisted map still works (the refusal is targeted, not blanket).
	good := MapFromKnots([]float64{0.3, 0.7}, []float64{0.4, 0.6})
	if good(0.3) >= good(0.7) {
		t.Errorf("valid persisted map broke: %v %v", good(0.3), good(0.7))
	}
}

// A bare Brier score is not interpretable without its benchmark: 0.302 sounds
// small, but against a 56% base rate the constant forecast scores 0.246, so the
// model is 23% WORSE than saying "up" every day. Publishing Brier without the
// skill score reads as selective — the same codebase computes skill correctly
// on /api/trackrecord.
func TestBrierSkill(t *testing.T) {
	// Live 1d numbers, reproduced from data/signaldeck.db on 2026-07-25:
	// n=10000, brier=0.3020, baseRate=0.5598 -> skill = -0.2257.
	pairs := make([]Pair, 0, 10000)
	for i := 0; i < 10000; i++ {
		// 55.98% up, with a deliberately anti-predictive forecast so Brier
		// lands above the base-rate reference.
		up := 0.0
		if i%10000 < 5598 {
			up = 1
		}
		pairs = append(pairs, Pair{Pred: 1 - up, Actual: up})
	}
	skill, base, ok := BrierSkill(pairs)
	if !ok {
		t.Fatal("expected a gradable skill score")
	}
	if !approx(base, 0.5598, 1e-9) {
		t.Errorf("baseRate=%v want 0.5598", base)
	}
	// Brier of a perfectly-inverted forecast is 1.0; reference is p(1-p).
	wantSkill := 1 - 1.0/(0.5598*(1-0.5598))
	if !approx(skill, wantSkill, 1e-9) {
		t.Errorf("skill=%v want %v", skill, wantSkill)
	}
	if skill >= 0 {
		t.Errorf("an inverted forecast must score negative skill, got %v", skill)
	}

	// A perfect forecast scores skill 1.
	perfect := make([]Pair, 0, 100)
	for i := 0; i < 100; i++ {
		up := float64(i % 2)
		perfect = append(perfect, Pair{Pred: up, Actual: up})
	}
	if s, _, ok := BrierSkill(perfect); !ok || !approx(s, 1, 1e-12) {
		t.Errorf("perfect forecast skill=%v ok=%v want 1", s, ok)
	}

	// Degenerate outcomes (all up) give a zero-variance reference: the skill
	// score is undefined and must be WITHHELD, never reported as 0.
	allUp := make([]Pair, 0, 50)
	for i := 0; i < 50; i++ {
		allUp = append(allUp, Pair{Pred: 0.6, Actual: 1})
	}
	if _, _, ok := BrierSkill(allUp); ok {
		t.Error("skill must be withheld when the base-rate reference is degenerate")
	}
	if _, _, ok := BrierSkill(nil); ok {
		t.Error("skill must be withheld on an empty history")
	}
}
