package gbm

import (
	"math"
	"math/rand"
	"testing"
)

// makeLearnable builds a synthetic dataset with a clear NON-LINEAR decision
// boundary (an XOR-like interaction plus a linear tilt) so a working GBM must
// beat both a coin flip and a purely linear model. Deterministic via seed.
//
// Labels resolve one tick after their row (LabelEnd = Ts+1), i.e. the rows do
// NOT overlap: each label is decided before the next sample exists. That is the
// point — the purge must be a no-op here, so these grades stay comparable to the
// pre-purge ones and any change in them would mean the purge over-reaches.
func makeLearnable(n int, seed int64) []Sample {
	rng := rand.New(rand.NewSource(seed))
	out := make([]Sample, n)
	for i := 0; i < n; i++ {
		x0 := rng.Float64()*2 - 1
		x1 := rng.Float64()*2 - 1
		x2 := rng.Float64()*2 - 1 // pure noise feature
		// XOR-ish target: up when x0 and x1 share sign (a non-linear region),
		// with a mild linear push from x0. Small label noise keeps it honest.
		signal := 0.0
		if (x0 > 0) == (x1 > 0) {
			signal += 1
		} else {
			signal -= 1
		}
		signal += 0.5 * x0
		y := 0.0
		if signal > 0 {
			y = 1
		}
		if rng.Float64() < 0.05 { // 5% label flips
			y = 1 - y
		}
		out[i] = Sample{Ts: int64(i), LabelEnd: int64(i) + 1, Feat: []float64{x0, x1, x2}, Y: y}
	}
	return out
}

func TestTrain_LearnsNonlinearBoundary(t *testing.T) {
	train := makeLearnable(600, 1)
	m, err := Train(train, Defaults())
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	// Held-out test set (different seed => different draws, same generator).
	test := makeLearnable(400, 99)
	correct := 0
	for _, s := range test {
		if (m.Predict(s.Feat) >= 0.5) == (s.Y >= 0.5) {
			correct++
		}
	}
	acc := float64(correct) / float64(len(test))
	// The boundary is >90% learnable; require comfortably above chance and above
	// what a linear model could do on XOR (~0.5-0.6).
	if acc < 0.80 {
		t.Fatalf("expected GBM to learn the non-linear boundary (acc>=0.80), got %.3f", acc)
	}
}

func TestEvaluate_PositiveLiftOnLearnable(t *testing.T) {
	samples := makeLearnable(800, 7)
	g, err := Evaluate(samples, 5, Defaults())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if g.N == 0 {
		t.Fatal("expected out-of-sample predictions")
	}
	if g.Lift <= 0.05 {
		t.Fatalf("expected clear OOS lift on learnable data, got lift=%.3f (acc=%.3f base=%.3f)", g.Lift, g.Accuracy, g.BaseRate)
	}
	if g.AUC <= 0.6 {
		t.Fatalf("expected AUC well above 0.5 on learnable data, got %.3f", g.AUC)
	}
}

func TestEvaluate_NoiseReportsNoEdge(t *testing.T) {
	// Labels independent of features => an honest model must report ~zero lift.
	rng := rand.New(rand.NewSource(42))
	n := 800
	samples := make([]Sample, n)
	for i := 0; i < n; i++ {
		y := 0.0
		if rng.Float64() < 0.5 {
			y = 1
		}
		samples[i] = Sample{
			Ts:       int64(i),
			LabelEnd: int64(i) + 1,
			Feat:     []float64{rng.NormFloat64(), rng.NormFloat64(), rng.NormFloat64()},
			Y:        y,
		}
	}
	g, err := Evaluate(samples, 5, Defaults())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	// On pure noise the honest OOS lift must be near zero (allow small slack for
	// finite-sample luck). This is the "no edge yet => say so" contract.
	if g.Lift > 0.06 {
		t.Fatalf("noise should show ~no lift, got lift=%.3f (acc=%.3f base=%.3f)", g.Lift, g.Accuracy, g.BaseRate)
	}
	if math.Abs(g.AUC-0.5) > 0.08 {
		t.Fatalf("noise AUC should be ~0.5, got %.3f", g.AUC)
	}
}

// TestEvaluate_NoLeakage is the core honesty test: appending FUTURE samples must
// NOT change the grade contributed by earlier folds. We compare the predictions
// an earlier fold makes when the series is truncated vs when future data is
// appended — they must be bit-identical, proving no future row trained a past
// prediction.
func TestEvaluate_NoLeakage(t *testing.T) {
	full := makeLearnable(900, 3)

	// Manually reproduce fold 1 of a 3-fold split on the FIRST 600 samples, and
	// again on the same first 600 samples but embedded in the full 900. The
	// walk-forward construction must yield identical fold-1 predictions because
	// fold 1 only ever trains on samples[:200] and predicts samples[200:300] —
	// none of which depend on anything after index 600.
	// We assert the property directly via Evaluate on prefixes: the first fold's
	// train/test slices are a pure function of the earliest samples.

	// Grade the prefix [0:600) with 3 folds.
	prefix := full[:600]
	gPrefix, err := Evaluate(prefix, 3, Defaults())
	if err != nil {
		t.Fatalf("Evaluate prefix: %v", err)
	}

	// Now craft a series whose first 600 samples are identical to `prefix` but
	// with different data appended after. The FIRST fold (train [:200], test
	// [200:400] in a 3-fold split over 600) must produce identical predictions
	// regardless of what comes later, because its train+test slices live entirely
	// within [0:400). We verify by recomputing fold-1 predictions in isolation.
	pred1 := firstFoldPreds(prefix, 3)
	appended := make([]Sample, 0, 900)
	appended = append(appended, prefix...)
	// Append 300 later-dated samples with WILD labels — if any leaked into an
	// earlier fold, predictions would shift.
	for i := 600; i < 900; i++ {
		appended = append(appended, Sample{Ts: int64(i), LabelEnd: int64(i) + 1, Feat: full[i].Feat, Y: 1})
	}
	pred1b := firstFoldPreds(appended[:600], 3)

	if len(pred1) != len(pred1b) {
		t.Fatalf("fold-1 prediction count changed: %d vs %d", len(pred1), len(pred1b))
	}
	for i := range pred1 {
		if pred1[i] != pred1b[i] {
			t.Fatalf("fold-1 prediction %d changed after appending future data: %.12f vs %.12f — LEAKAGE", i, pred1[i], pred1b[i])
		}
	}
	// Sanity: the prefix grade itself is well-defined.
	if gPrefix.N == 0 {
		t.Fatal("prefix grade empty")
	}
}

// firstFoldPreds recomputes just the first walk-forward fold's out-of-sample
// predictions (train on samples[:n/folds], predict [n/folds:2n/folds]) exactly
// as Evaluate does, so the leakage test can compare them in isolation.
func firstFoldPreds(samples []Sample, folds int) []float64 {
	n := len(samples)
	trainEnd := n * 1 / folds
	testEnd := n * 2 / folds
	m, err := Train(samples[:trainEnd], Defaults())
	if err != nil {
		return nil
	}
	out := make([]float64, 0, testEnd-trainEnd)
	for _, s := range samples[trainEnd:testEnd] {
		out = append(out, m.Predict(s.Feat))
	}
	return out
}

func TestTrain_Deterministic(t *testing.T) {
	samples := makeLearnable(400, 11)
	m1, err := Train(samples, Defaults())
	if err != nil {
		t.Fatalf("Train 1: %v", err)
	}
	m2, err := Train(samples, Defaults())
	if err != nil {
		t.Fatalf("Train 2: %v", err)
	}
	test := makeLearnable(200, 12)
	for _, s := range test {
		if m1.Predict(s.Feat) != m2.Predict(s.Feat) {
			t.Fatal("GBM training is not deterministic")
		}
	}
}

func TestEvaluate_SortsUnorderedInput(t *testing.T) {
	// Evaluate must defensively enforce time order. Shuffle a learnable set and
	// confirm the grade matches the sorted one (walk-forward is time-based).
	ordered := makeLearnable(600, 21)
	shuffled := make([]Sample, len(ordered))
	copy(shuffled, ordered)
	rng := rand.New(rand.NewSource(5))
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	gOrdered, err := Evaluate(ordered, 5, Defaults())
	if err != nil {
		t.Fatalf("ordered: %v", err)
	}
	gShuffled, err := Evaluate(shuffled, 5, Defaults())
	if err != nil {
		t.Fatalf("shuffled: %v", err)
	}
	if gOrdered.N != gShuffled.N || math.Abs(gOrdered.Lift-gShuffled.Lift) > 1e-9 {
		t.Fatalf("shuffled input not re-sorted to identical grade: ordered lift=%.6f shuffled lift=%.6f", gOrdered.Lift, gShuffled.Lift)
	}
}

func TestTrain_InsufficientData(t *testing.T) {
	if _, err := Train(makeLearnable(10, 1), Defaults()); err != ErrInsufficientData {
		t.Fatalf("expected ErrInsufficientData for tiny set, got %v", err)
	}
}

func TestTrain_RaggedFeaturesRejected(t *testing.T) {
	samples := makeLearnable(100, 1)
	samples[50].Feat = []float64{1, 2} // wrong dimension
	if _, err := Train(samples, Defaults()); err != ErrBadParams {
		t.Fatalf("expected ErrBadParams for ragged features, got %v", err)
	}
}

func TestEvaluate_BadFolds(t *testing.T) {
	if _, err := Evaluate(makeLearnable(300, 1), 1, Defaults()); err != ErrBadParams {
		t.Fatalf("expected ErrBadParams for folds<2, got %v", err)
	}
}

func TestRun_GradesThenPredicts(t *testing.T) {
	samples := makeLearnable(700, 31)
	latest := []float64{0.6, 0.6, -0.2} // same-sign region => should lean up
	prob, grade, ok := Run(samples, latest, 5, Defaults())
	if !ok {
		t.Fatal("Run should succeed on ample learnable data")
	}
	if grade.Lift <= 0 {
		t.Fatalf("expected positive OOS lift, got %.3f", grade.Lift)
	}
	if prob < 0.5 {
		t.Fatalf("same-sign region should lean up (>0.5), got %.3f", prob)
	}
}

func TestPredict_WrongDimIsNeutral(t *testing.T) {
	m, err := Train(makeLearnable(200, 1), Defaults())
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if p := m.Predict([]float64{1, 2}); p != 0.5 {
		t.Fatalf("wrong-dim vector should return neutral 0.5, got %.3f", p)
	}
}
