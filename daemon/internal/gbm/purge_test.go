package gbm

import (
	"math/rand"
	"testing"
)

// ── H1 · purged/embargoed walk-forward ───────────────────────────────────────
//
// The adversarial review's highest-value predictive finding: Evaluate split
// train/test with ZERO gap, so with a label that spans H seconds and rows
// sampled far more often than H, every training row within H of the boundary
// carried a label REALIZED INSIDE the test block. Because features are
// persistent (a technical indicator moves slowly), a tree that memorizes those
// rows is memorizing the test block's answers through feature space — the
// de Prado failure mode. `Lift > 0` on such a grade is the gate that admits a
// leg to the live blend, so the leak buys real capital allocation.
//
// The fixture below is the honest test of that claim: prices are a RANDOM WALK,
// so the true predictable edge is exactly zero. Any lift the walk-forward
// reports is leakage.

// overlapSpan is the fixture's label span in Ts units (10-minute bars, 7-day
// label => 1008 bars), matching the live 1w-label / 10-minute-sampling shape the
// review measured.
const (
	overlapStep = int64(600)                // seconds between samples (10 minutes)
	overlapSpan = int64(1008) * overlapStep // 7 days of 10-minute bars
)

// makeOverlapping builds a zero-edge dataset with OVERLAPPING labels.
//
// Prices are a driftless random walk, so P(up over the next span) is 50/50 and
// independent of anything observable — the true out-of-sample lift is 0 by
// construction. Features are PERSISTENT functions of the path (level, trailing
// mean, trailing change), exactly like the technical features the live vector
// carries: neighbouring rows have neighbouring features. Each row's label
// resolves `overlapSpan` later, so ~1008 consecutive rows share a forward
// window. That combination — persistent features plus overlapping labels — is
// what lets an unpurged split leak: the training rows straddling the boundary
// hand the tree the test block's labels, indexed by a feature region the test
// rows also occupy.
func makeOverlapping(n int, seed int64) []Sample {
	rng := rand.New(rand.NewSource(seed))
	// Need enough prices that the LAST sample's label is realized on the path.
	horizon := int(overlapSpan / overlapStep)
	nSteps := n + horizon + 60
	px := make([]float64, nSteps)
	px[0] = 100
	for i := 1; i < nSteps; i++ {
		px[i] = px[i-1] * (1 + 0.0004*rng.NormFloat64()) // driftless random walk
	}
	out := make([]Sample, 0, n)
	for i := 50; i < n+50; i++ {
		var trail float64
		for k := i - 50; k < i; k++ {
			trail += px[k]
		}
		trail /= 50
		y := 0.0
		if px[i+horizon] > px[i] {
			y = 1
		}
		ts := int64(i) * overlapStep
		out = append(out, Sample{
			Ts:       ts,
			LabelEnd: ts + overlapSpan,
			Feat: []float64{
				px[i] / trail,             // level vs trailing mean (persistent)
				trail / px[i-49],          // trailing drift (persistent)
				px[i]/px[i-20] - 1,        // 20-bar change (persistent)
				rng.NormFloat64() * 0.001, // pure noise
			},
			Y: y,
		})
	}
	return out
}

// TestEvaluate_PurgeContaminationAndNoFalseEdge measures what the purge is worth
// on live-shaped data and pins the two properties that must hold.
//
// MEASURED, and worth stating plainly because it is not the expected result: on
// this fixture the leak does NOT inflate Lift. It cannot, because Lift subtracts
// the majority-class base rate and a 7-day label sampled every 10 minutes makes
// a test block nearly one single bet — labels inside it are near-constant, so
// leaked accuracy and the base rate rise together and cancel. What the leak
// actually costs is visible in the other number this test prints: on every seed,
// the overwhelming majority of each fold's training rows carried labels realized
// inside the block they were about to be graded on. The grade was computed on a
// training set that was almost entirely contaminated; that it happened to net
// out in Lift on a random walk is luck, not a defence.
//
// So the assertions are the ones the data supports: the purge must remove the
// straddling rows, and the purged grade must not claim an edge on a random walk
// on average. The per-seed before/after Lift is logged, not asserted — asserting
// a collapse that the estimator's variance cannot resolve would be tuning the
// test to a story.
func TestEvaluate_PurgeContaminationAndNoFalseEdge(t *testing.T) {
	const seeds = 6
	var sumLeaky, sumPurged float64
	graded := 0
	for s := int64(1); s <= seeds; s++ {
		samples := makeOverlapping(5000, s)
		span, ok := labelSpanOf(samples)
		if !ok {
			t.Fatal("fixture must declare a label span")
		}
		leaky, err := evaluateFolds(samples, 5, Defaults(), span, 0, false)
		if err != nil {
			t.Fatalf("seed %d unpurged: %v", s, err)
		}
		purged, err := Evaluate(samples, 5, Defaults())
		if err != nil {
			t.Fatalf("seed %d purged: %v", s, err)
		}
		if purged.PurgedTrainRows == 0 {
			t.Fatalf("seed %d: purge removed no training rows on an overlapping fixture", s)
		}
		t.Logf("seed %d: unpurged lift=%+.4f  purged lift=%+.4f  (n=%d, purged %d contaminated train rows)",
			s, leaky.Lift, purged.Lift, purged.N, purged.PurgedTrainRows)
		sumLeaky += leaky.Lift
		sumPurged += purged.Lift
		graded++
	}
	meanLeaky, meanPurged := sumLeaky/float64(graded), sumPurged/float64(graded)
	t.Logf("MEAN lift over %d zero-edge seeds: unpurged=%+.4f  purged=%+.4f", graded, meanLeaky, meanPurged)

	// The prices are a driftless random walk, so the true edge is exactly zero
	// and the purged estimator must not report one ON AVERAGE. Per seed it still
	// swings wildly in BOTH directions — a 7-day label makes each fold roughly a
	// single bet, so one grade is one observation, not 1,200. That is review
	// finding C4 and the purge does not fix it; the interval around any of these
	// numbers is far wider than the numbers themselves.
	if meanPurged > 0.01 {
		t.Fatalf("purged mean lift on a random walk should be <=0, got %+.4f", meanPurged)
	}
}

// TestPurgedTrain_NoTrainingLabelOverlapsTestBlock is the structural guarantee,
// asserted directly rather than statistically: with a KNOWN label span and a
// split boundary that ~1000 training rows straddle, the training set Evaluate
// actually fits on must contain no row whose label window reaches into the test
// block (nor into the embargo gap in front of it).
func TestPurgedTrain_NoTrainingLabelOverlapsTestBlock(t *testing.T) {
	samples := makeOverlapping(4000, 42)
	span, ok := labelSpanOf(samples)
	if !ok {
		t.Fatal("labelSpanOf must read the declared span")
	}
	if span != overlapSpan {
		t.Fatalf("label span must come from the DATA: got %d want %d", span, overlapSpan)
	}
	embargo := embargoFor(span)
	if embargo <= 0 {
		t.Fatalf("a %d-second label span must earn a positive embargo, got %d", span, embargo)
	}

	n := len(samples)
	for f := 1; f < 5; f++ {
		trainEnd := n * f / 5
		testStart := samples[trainEnd].Ts
		train := purgedTrain(samples, trainEnd, testStart, span, embargo)

		// The defect in one assertion: every retained training label must have
		// been fully realized before the test block opens, with the embargo gap
		// on top. A single violation is a row whose outcome was decided by
		// prices the test block is about to be graded on.
		for _, s := range train {
			if labelEndOf(s, span) > testStart-embargo {
				t.Fatalf("fold %d: training row ts=%d labelEnd=%d overlaps test block starting %d (embargo %d) — LEAKAGE",
					f, s.Ts, labelEndOf(s, span), testStart, embargo)
			}
		}
		// And the purge must be doing real work: every row within one label span
		// (plus embargo) of the boundary straddles it, so the count removed is
		// that whole window — or the entire training set when the history is
		// shorter than the label horizon, which is itself the honest answer.
		removed := trainEnd - len(train)
		window := int((overlapSpan + embargo) / overlapStep)
		if want := min(window, trainEnd); removed < want {
			t.Fatalf("fold %d: purged %d rows, expected %d (the rows straddling the boundary)", f, removed, want)
		}
	}
}

// TestEvaluate_RefusesUndeclaredLabelSpan encodes the withhold-don't-guess rule:
// a purge is only correct if the label span is known, and the span must come
// from the data. Samples that do not declare when their label resolved cannot be
// purged, so the grade cannot be certified leak-free — and an uncertifiable
// grade must be WITHHELD, not published. Lift is the gate that admits a leg to
// the live blend; a silently unpurged Lift is worse than no Lift.
func TestEvaluate_RefusesUndeclaredLabelSpan(t *testing.T) {
	samples := makeLearnable(800, 7)
	for i := range samples {
		samples[i].LabelEnd = 0 // caller never declared a label horizon
	}
	if _, err := Evaluate(samples, 5, Defaults()); err != ErrNoLabelSpan {
		t.Fatalf("expected ErrNoLabelSpan for undeclared label horizons, got %v", err)
	}
	if _, _, ok := Run(samples, samples[0].Feat, 5, Defaults()); ok {
		t.Fatal("Run must refuse to emit a probability whose grade cannot be purged")
	}
}

// TestWithLabelSpan_DeclaresHorizon covers the one-line path a caller uses to
// declare its horizon (the fix the live per-symbol trainer and the research lab
// both need).
func TestWithLabelSpan_DeclaresHorizon(t *testing.T) {
	samples := makeLearnable(400, 3)
	for i := range samples {
		samples[i].LabelEnd = 0
	}
	declared := WithLabelSpan(samples, 7)
	for i, s := range declared {
		if s.LabelEnd != s.Ts+7 {
			t.Fatalf("sample %d: LabelEnd=%d want %d", i, s.LabelEnd, s.Ts+7)
		}
	}
	if samples[0].LabelEnd != 0 {
		t.Fatal("WithLabelSpan must not mutate the caller's slice")
	}
	span, ok := labelSpanOf(declared)
	if !ok || span != 7 {
		t.Fatalf("labelSpanOf(declared) = %d,%v want 7,true", span, ok)
	}
}

// TestLabelSpanOf_TakesTheWidest guards the conservative direction: with mixed
// horizons in one set (a schema slip, not a design), the purge must be sized by
// the WIDEST label present, never the median — a narrow purge would leave the
// long-horizon rows straddling the boundary.
func TestLabelSpanOf_TakesTheWidest(t *testing.T) {
	samples := []Sample{
		{Ts: 0, LabelEnd: 10, Feat: []float64{0}, Y: 0},
		{Ts: 1, LabelEnd: 101, Feat: []float64{0}, Y: 1}, // span 100
		{Ts: 2, LabelEnd: 12, Feat: []float64{0}, Y: 0},
	}
	span, ok := labelSpanOf(samples)
	if !ok || span != 100 {
		t.Fatalf("labelSpanOf = %d,%v want 100,true", span, ok)
	}
}
