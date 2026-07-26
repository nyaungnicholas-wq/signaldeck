package forecast

import (
	"testing"
)

// ── A15 · purged/embargoed walk-forward ──────────────────────────────────────
//
// Evaluate used to split CONTIGUOUSLY with no gap: train = samples[:trainEnd],
// test = samples[trainEnd:testEnd]. At the 1w horizon (fwdBars = 5) the last
// five training samples carry labels read off bars 1..5 INSIDE the test block,
// so the fold trains on the very outcomes it is about to be graded on. Since
// Lift > 0 is the gate that admits the forecast leg to the live blend, that leak
// buys real allocation. These tests pin the purge that removes it.

// TestPurgedTrain_NoTrainingLabelOverlapsTestBlock is the structural guarantee
// and does not depend on any measured number: after the purge, no training
// sample's label window may reach the test block, nor into the embargo gap held
// in front of it. This is the property whose absence WAS finding A15.
func TestPurgedTrain_NoTrainingLabelOverlapsTestBlock(t *testing.T) {
	const folds = 5
	for _, fwd := range []int{1, 5} { // the two horizons Run maps to (1d, 1w)
		bars := barsFromCloses(noiseCloses(900, 7))
		samples := buildSamples(bars, fwd)
		span, ok := labelSpanOf(samples)
		if !ok {
			t.Fatalf("fwd=%d: buildSamples must declare a label span", fwd)
		}
		if span != fwd {
			t.Fatalf("fwd=%d: label span read off the data = %d, want %d", fwd, span, fwd)
		}
		embargo := embargoFor(span)
		if embargo <= 0 {
			t.Fatalf("fwd=%d: a %d-bar label span must earn a positive embargo, got %d", fwd, span, embargo)
		}

		n := len(samples)
		totalPurged := 0
		for f := 1; f < folds; f++ {
			trainEnd := n * f / folds
			testStart := samples[trainEnd].idx
			train := purgedTrain(samples, trainEnd, testStart, span, embargo)
			totalPurged += trainEnd - len(train)
			for _, s := range train {
				if labelEndOf(s, span) > testStart-embargo {
					t.Fatalf("fwd=%d fold %d: training row idx=%d labelEnd=%d overlaps test block starting %d (embargo %d) — LEAKAGE",
						fwd, f, s.idx, labelEndOf(s, span), testStart, embargo)
				}
			}
			// The purge must also be doing real work: samples are one per bar, so
			// the rows dropped are exactly those with idx in
			// [testStart-embargo-span+1, testStart-1] — span+embargo-1 of them.
			// (The row whose label lands exactly on the cutoff is KEPT: its
			// terminal price is data the test block's own features already
			// contain, which is contemporaneous, not future.)
			if removed, want := trainEnd-len(train), span+embargo-1; removed != want {
				t.Fatalf("fwd=%d fold %d: purged %d rows, expected %d (the rows straddling the boundary)", fwd, f, removed, want)
			}
		}
		t.Logf("fwd=%d: span=%d embargo=%d purged %d training rows across %d folds", fwd, span, embargo, totalPurged, folds-1)
	}
}

// TestEvaluate_ReportsPurgeAndMeasuresTheLeak states the consequence of the fix
// in the only currency that matters here: the Lift that gates the leg. It grades
// the same series both ways and reports both numbers. The assertion is the one
// the data supports — a purged grade must not claim an edge ON AVERAGE on a
// driftless random walk — not a demand that the purge shrink Lift on every seed
// (single-fold noise is far larger than the leak; that is finding C4, which the
// purge does not fix).
func TestEvaluate_ReportsPurgeAndMeasuresTheLeak(t *testing.T) {
	const folds = 5
	for _, fwd := range []int{1, 5} {
		var sumLeaky, sumPurged float64
		graded := 0
		for seed := int64(1); seed <= 12; seed++ {
			samples := buildSamples(barsFromCloses(noiseCloses(900, seed)), fwd)
			span, _ := labelSpanOf(samples)
			leaky, err := evaluateFolds(samples, folds, span, embargoFor(span), false)
			if err != nil {
				t.Fatalf("fwd=%d seed %d unpurged: %v", fwd, seed, err)
			}
			purged, err := evaluateSamples(samples, folds)
			if err != nil {
				t.Fatalf("fwd=%d seed %d purged: %v", fwd, seed, err)
			}
			if purged.PurgedTrainRows == 0 {
				t.Fatalf("fwd=%d seed %d: purge removed no training rows on an overlapping fixture", fwd, seed)
			}
			if purged.LabelSpan != fwd || purged.EmbargoSpan != embargoFor(span) {
				t.Fatalf("fwd=%d: grade must report the purge it applied, got span=%d embargo=%d", fwd, purged.LabelSpan, purged.EmbargoSpan)
			}
			sumLeaky += leaky.Lift
			sumPurged += purged.Lift
			graded++
		}
		meanLeaky, meanPurged := sumLeaky/float64(graded), sumPurged/float64(graded)
		t.Logf("fwd=%d MEAN lift over %d zero-edge seeds: unpurged=%+.4f  purged=%+.4f", fwd, graded, meanLeaky, meanPurged)
		if meanPurged > 0.01 {
			t.Fatalf("fwd=%d: purged mean lift on a random walk should be <=0, got %+.4f", fwd, meanPurged)
		}
	}
}

// TestEvaluate_RefusesUndeclaredLabelSpan is the refusal, and it is the point of
// the whole exercise: a purge is only correct if the label span is known, and the
// span must come off the data. When nothing declares one, no fold can be purged,
// so the grade cannot be certified leak-free — and an uncertifiable Lift is
// exactly the number that admits the forecast leg to the live blend. Withhold it.
func TestEvaluate_RefusesUndeclaredLabelSpan(t *testing.T) {
	samples := buildSamples(barsFromCloses(noiseCloses(900, 3)), 5)
	for i := range samples {
		samples[i].labelEnd = 0 // nothing declares when its label resolved
	}
	if _, err := evaluateSamples(samples, 5); err != ErrNoLabelSpan {
		t.Fatalf("expected ErrNoLabelSpan for undeclared label horizons, got %v", err)
	}
}

// TestEvaluate_PurgeCanStarveTheGradeAndThenItRefuses covers the gbm outcome: if
// the purge leaves every fold's training set below the trust floor, there is no
// gradable fold left and Evaluate must return ErrInsufficientData rather than a
// grade assembled from whatever survived. A leg with no honest grade does not
// ship a probability.
func TestEvaluate_PurgeCanStarveTheGradeAndThenItRefuses(t *testing.T) {
	// A label span wider than the entire fold geometry: every training row
	// straddles every boundary, so the purge empties all of them.
	samples := buildSamples(barsFromCloses(noiseCloses(400, 11)), 5)
	for i := range samples {
		samples[i].labelEnd = samples[i].idx + 10_000
	}
	if _, err := evaluateSamples(samples, 5); err != ErrInsufficientData {
		t.Fatalf("expected ErrInsufficientData when the purge starves every fold, got %v", err)
	}
}
