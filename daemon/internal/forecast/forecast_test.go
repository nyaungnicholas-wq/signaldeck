package forecast

import (
	"math"
	"math/rand"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// barsFromCloses builds a bar series from a close-price slice. Open/High/Low
// track close (fine for this model, which only reads Close and Volume), and
// volume is constant unless overridden by the caller via barsFromClosesVol.
func barsFromCloses(closes []float64) []marketdata.Bar {
	return barsFromClosesVol(closes, nil)
}

// barsFromClosesVol builds bars with explicit volumes (vols may be nil for a
// constant volume of 1000).
func barsFromClosesVol(closes, vols []float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, len(closes))
	for i, c := range closes {
		v := 1000.0
		if vols != nil {
			v = vols[i]
		}
		bars[i] = marketdata.Bar{
			Ts:     int64(i) * 86400,
			Open:   c,
			High:   c,
			Low:    c,
			Close:  c,
			Volume: v,
		}
	}
	return bars
}

// separableCloses builds a price series with a learnable directional signal:
// when the last-bar return is up, the next fwd move is up with high
// probability, and vice versa (momentum). A real linear classifier should
// beat the base rate on this out-of-sample.
func separableCloses(n, fwd int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	closes := make([]float64, n)
	closes[0] = 100
	// Build with a persistent momentum regime: the sign of the *previous*
	// return strongly predicts the next return.
	prevUp := true
	for i := 1; i < n; i++ {
		var drift float64
		if prevUp {
			drift = 0.004 // +0.4% when momentum up
		} else {
			drift = -0.004
		}
		noise := rng.NormFloat64() * 0.002
		r := drift + noise
		closes[i] = closes[i-1] * (1 + r)
		// Flip regime occasionally so both classes appear.
		if rng.Float64() < 0.03 {
			prevUp = !prevUp
		} else {
			prevUp = r > 0
		}
	}
	return closes
}

// noiseCloses builds a pure random-walk series with zero drift: direction is
// unpredictable, so an honest model must grade near the base rate.
func noiseCloses(n int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	closes := make([]float64, n)
	closes[0] = 100
	for i := 1; i < n; i++ {
		r := rng.NormFloat64() * 0.01 // zero-mean, no structure
		closes[i] = closes[i-1] * (1 + r)
	}
	return closes
}

func TestFeaturesNoLookahead(t *testing.T) {
	// Building features at index i must not depend on any bar > i. We verify
	// by computing features on a prefix and on the full series and checking
	// they are identical for the same i.
	closes := separableCloses(300, 1, 1)
	full := barsFromCloses(closes)

	tests := []struct {
		name string
		i    int
	}{
		{"first computable", warmup},
		{"mid series", 150},
		{"near end of prefix", 199},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefix := full[:tc.i+1] // bars[..i] only
			fPrefix, okP := features(prefix, tc.i)
			fFull, okF := features(full, tc.i)
			if !okP || !okF {
				t.Fatalf("features not computable at i=%d (okP=%v okF=%v)", tc.i, okP, okF)
			}
			if len(fPrefix) != numFeatures || len(fFull) != numFeatures {
				t.Fatalf("wrong feature length: %d vs %d", len(fPrefix), len(fFull))
			}
			for k := range fFull {
				if fPrefix[k] != fFull[k] {
					t.Errorf("feature[%d] differs prefix=%v full=%v — lookahead leak",
						k, fPrefix[k], fFull[k])
				}
			}
		})
	}
}

func TestFeaturesWarmupGuard(t *testing.T) {
	closes := noiseCloses(100, 7)
	bars := barsFromCloses(closes)
	tests := []struct {
		i      int
		wantOK bool
	}{
		{0, false},
		{warmup - 1, false},
		{warmup, true},
		{99, true},
		{100, false}, // out of range
	}
	for _, tc := range tests {
		_, ok := features(bars, tc.i)
		if ok != tc.wantOK {
			t.Errorf("features ok at i=%d = %v, want %v", tc.i, ok, tc.wantOK)
		}
	}
}

func TestTrainInsufficientData(t *testing.T) {
	tests := []struct {
		name    string
		nCloses int
		fwd     int
		wantErr error
	}{
		{"too few bars", 60, 1, ErrInsufficientData},
		{"just under threshold", warmup + minLabeledSamples - 5, 1, ErrInsufficientData},
		{"bad fwdBars zero", 300, 0, ErrBadParams},
		{"bad fwdBars negative", 300, -1, ErrBadParams},
		{"enough data", warmup + minLabeledSamples + 20, 1, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bars := barsFromCloses(noiseCloses(tc.nCloses, 3))
			_, err := Train(bars, tc.fwd)
			if err != tc.wantErr {
				t.Errorf("Train err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestEvaluateSeparableBeatsBaseRate(t *testing.T) {
	// On a learnable momentum series, out-of-sample accuracy should exceed the
	// base rate (positive lift) and AUC should exceed 0.5.
	closes := separableCloses(1200, 1, 42)
	bars := barsFromCloses(closes)
	g, err := Evaluate(bars, 1, 5)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	t.Logf("separable grade: N=%d acc=%.3f base=%.3f lift=%.3f auc=%.3f brier=%.3f",
		g.N, g.Accuracy, g.BaseRate, g.Lift, g.AUC, g.BrierScore)
	if g.N == 0 {
		t.Fatal("no out-of-sample predictions")
	}
	if g.Lift <= 0 {
		t.Errorf("expected positive lift on separable data, got %.3f (acc=%.3f base=%.3f)",
			g.Lift, g.Accuracy, g.BaseRate)
	}
	if g.AUC <= 0.5 {
		t.Errorf("expected AUC > 0.5 on separable data, got %.3f", g.AUC)
	}
	if g.Accuracy <= g.BaseRate {
		t.Errorf("accuracy %.3f should exceed base rate %.3f", g.Accuracy, g.BaseRate)
	}
}

func TestEvaluateNoiseNoEdge(t *testing.T) {
	// On pure noise the model must be honest: accuracy ~ base rate and lift ~ 0.
	// Average across seeds to avoid a single lucky/unlucky draw.
	const seeds = 8
	var sumLift, sumAUCdev float64
	for s := int64(0); s < seeds; s++ {
		closes := noiseCloses(1000, 100+s)
		bars := barsFromCloses(closes)
		g, err := Evaluate(bars, 1, 5)
		if err != nil {
			t.Fatalf("Evaluate error (seed %d): %v", s, err)
		}
		t.Logf("noise seed=%d: acc=%.3f base=%.3f lift=%+.3f auc=%.3f",
			s, g.Accuracy, g.BaseRate, g.Lift, g.AUC)
		sumLift += g.Lift
		sumAUCdev += math.Abs(g.AUC - 0.5)
	}
	meanLift := sumLift / seeds
	meanAUCdev := sumAUCdev / seeds
	t.Logf("noise mean lift=%+.4f, mean |AUC-0.5|=%.4f", meanLift, meanAUCdev)
	// Honesty: on noise the average lift must be essentially zero (no edge
	// claimed). Allow a small band for finite-sample wobble.
	if math.Abs(meanLift) > 0.03 {
		t.Errorf("noise mean lift = %+.4f, want ~0 (model should claim no edge)", meanLift)
	}
	if meanAUCdev > 0.06 {
		t.Errorf("noise mean |AUC-0.5| = %.4f, want ~0 (no ranking skill on noise)", meanAUCdev)
	}
}

func TestEvaluateNoLookaheadAppendingFuture(t *testing.T) {
	// Core honesty check: an earlier fold's predictions must not change when
	// we append future bars to the series. We reproduce the first evaluable
	// fold's fit/predict on a prefix and on the extended series and assert the
	// out-of-sample predictions for that fold are identical.
	base := separableCloses(1000, 1, 9)
	future := separableCloses(400, 1, 999) // arbitrary "future" continuation
	extended := append(append([]float64{}, base...), future...)

	barsBase := barsFromCloses(base)
	barsExt := barsFromCloses(extended)

	// Recompute fold f=1 boundaries for the BASE series and capture its
	// out-of-sample predictions, then do the same computation but with the
	// extended series available — using the SAME sample indices from base —
	// and confirm predictions are byte-identical. Since fold 1 trains only on
	// base samples[:trainEnd] and predicts base samples[trainEnd:testEnd],
	// appending future bars cannot touch it.
	predsBase := foldOnePredictions(t, barsBase)
	predsExtSameFold := foldOnePredictionsUsingBaseFold(t, barsBase, barsExt)

	if len(predsBase) == 0 {
		t.Fatal("no fold-1 predictions to compare")
	}
	if len(predsBase) != len(predsExtSameFold) {
		t.Fatalf("prediction count changed: %d vs %d", len(predsBase), len(predsExtSameFold))
	}
	for i := range predsBase {
		if predsBase[i] != predsExtSameFold[i] {
			t.Errorf("fold-1 prediction[%d] changed after appending future bars: %v vs %v",
				i, predsBase[i], predsExtSameFold[i])
		}
	}
}

// foldOnePredictions reproduces Evaluate's first evaluable fold (f=1, folds=5)
// on the given bars and returns its out-of-sample predictions.
func foldOnePredictions(t *testing.T, bars []marketdata.Bar) []float64 {
	t.Helper()
	samples := buildSamples(bars, 1)
	n := len(samples)
	folds := 5
	trainEnd := n * 1 / folds
	testEnd := n * 2 / folds
	train := samples[:trainEnd]
	test := samples[trainEnd:testEnd]
	std := fitStandardizer(train)
	z := make([][]float64, len(train))
	yv := make([]float64, len(train))
	for r, s := range train {
		z[r] = std.apply(s.feat)
		yv[r] = s.y
	}
	w, b := fitLogit(z, yv, defaultIters)
	out := make([]float64, 0, len(test))
	for _, s := range test {
		out = append(out, predictLogit(w, b, std.apply(s.feat)))
	}
	return out
}

// foldOnePredictionsUsingBaseFold fits the SAME fold as foldOnePredictions
// (boundaries taken from the base series) but sources its samples from the
// extended series. Because the fold only references sample indices that exist
// in the base range and the extended series shares that prefix exactly, the
// predictions must match — proving no future leakage.
func foldOnePredictionsUsingBaseFold(t *testing.T, barsBase, barsExt []marketdata.Bar) []float64 {
	t.Helper()
	baseSamples := buildSamples(barsBase, 1)
	extSamples := buildSamples(barsExt, 1)
	n := len(baseSamples)
	folds := 5
	trainEnd := n * 1 / folds
	testEnd := n * 2 / folds
	// Use extended samples but the same index range (they share the prefix).
	train := extSamples[:trainEnd]
	test := extSamples[trainEnd:testEnd]
	std := fitStandardizer(train)
	z := make([][]float64, len(train))
	yv := make([]float64, len(train))
	for r, s := range train {
		z[r] = std.apply(s.feat)
		yv[r] = s.y
	}
	w, b := fitLogit(z, yv, defaultIters)
	out := make([]float64, 0, len(test))
	for _, s := range test {
		out = append(out, predictLogit(w, b, std.apply(s.feat)))
	}
	return out
}

func TestEvaluateBadParams(t *testing.T) {
	bars := barsFromCloses(noiseCloses(1000, 5))
	tests := []struct {
		name  string
		fwd   int
		folds int
	}{
		{"fwd zero", 0, 5},
		{"fwd negative", -2, 5},
		{"folds one", 1, 1},
		{"folds zero", 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Evaluate(bars, tc.fwd, tc.folds); err != ErrBadParams {
				t.Errorf("Evaluate(%d,%d) err = %v, want ErrBadParams", tc.fwd, tc.folds, err)
			}
		})
	}
}

func TestEvaluateInsufficientData(t *testing.T) {
	bars := barsFromCloses(noiseCloses(warmup+40, 5)) // < minLabeledSamples labeled
	if _, err := Evaluate(bars, 1, 5); err != ErrInsufficientData {
		t.Errorf("Evaluate err = %v, want ErrInsufficientData", err)
	}
}

func TestPredictLatest(t *testing.T) {
	closes := separableCloses(400, 1, 11)
	bars := barsFromCloses(closes)
	m, err := Train(bars, 1)
	if err != nil {
		t.Fatalf("Train error: %v", err)
	}
	p, ok := m.PredictLatest(bars)
	if !ok {
		t.Fatal("PredictLatest not ok on sufficient data")
	}
	if p < 0 || p > 1 {
		t.Errorf("probability out of [0,1]: %v", p)
	}

	// Determinism: predicting twice yields the identical probability.
	p2, _ := m.PredictLatest(bars)
	if p != p2 {
		t.Errorf("PredictLatest not deterministic: %v vs %v", p, p2)
	}

	// Too little history → not ok.
	if _, ok := m.PredictLatest(bars[:warmup-1]); ok {
		t.Error("PredictLatest should be not-ok with < warmup bars")
	}
	if _, ok := m.PredictLatest(nil); ok {
		t.Error("PredictLatest should be not-ok on empty bars")
	}
}

func TestPredictLatestUsesOnlyLatestBar(t *testing.T) {
	// PredictLatest on bars[..last] must equal the prediction after appending
	// arbitrary FUTURE bars, because features for the latest existing bar don't
	// depend on later bars. We check that predicting at index `last` is stable
	// whether or not future bars follow it (using the same trained model).
	closes := separableCloses(500, 1, 21)
	bars := barsFromCloses(closes)
	m, err := Train(bars, 1)
	if err != nil {
		t.Fatalf("Train error: %v", err)
	}
	// Prediction for the last bar of the original series.
	pOrig, _ := m.PredictLatest(bars)

	// The feature vector at that same index computed on an extended series
	// must be identical, so a prediction anchored there is unchanged.
	extended := barsFromCloses(append(append([]float64{}, closes...), noiseCloses(50, 5)...))
	fOrig, _ := features(bars, len(bars)-1)
	fExt, _ := features(extended, len(bars)-1)
	for k := range fOrig {
		if fOrig[k] != fExt[k] {
			t.Fatalf("feature[%d] at fixed index changed after append: %v vs %v",
				k, fOrig[k], fExt[k])
		}
	}
	// And the resulting probability at that index is identical.
	pExtSameIdx := m.predictRaw(fExt)
	if pOrig != pExtSameIdx {
		t.Errorf("prediction at fixed index changed after append: %v vs %v", pOrig, pExtSameIdx)
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		horizon marketdata.Horizon
		nCloses int
		wantOK  bool
	}{
		{"1d sufficient", marketdata.H1d, 1000, true},
		{"1w sufficient", marketdata.H1w, 1200, true},
		{"1h unsupported", marketdata.H1h, 1000, false},
		{"1d too little data", marketdata.H1d, 100, false},
		{"empty", marketdata.H1d, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var bars []marketdata.Bar
			if tc.nCloses > 0 {
				bars = barsFromCloses(separableCloses(tc.nCloses, 1, 55))
			}
			f, ok := Run(bars, tc.horizon)
			if ok != tc.wantOK {
				t.Fatalf("Run ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if f.Prob < 0 || f.Prob > 1 {
				t.Errorf("prob out of range: %v", f.Prob)
			}
			if f.Grade.N == 0 {
				t.Error("Run returned a forecast with an empty grade (dishonest)")
			}
			if f.NTrain <= 0 {
				t.Error("Run returned NTrain <= 0")
			}
			t.Logf("%s: prob=%.3f grade{N=%d acc=%.3f lift=%+.3f auc=%.3f} nTrain=%d",
				tc.name, f.Prob, f.Grade.N, f.Grade.Accuracy, f.Grade.Lift, f.Grade.AUC, f.NTrain)
		})
	}
}

func TestRunAlwaysCarriesGrade(t *testing.T) {
	// The brand promise: Run never returns a probability without a grade.
	for _, h := range []marketdata.Horizon{marketdata.H1d, marketdata.H1w} {
		bars := barsFromCloses(separableCloses(1000, 1, int64(77)))
		f, ok := Run(bars, h)
		if !ok {
			t.Fatalf("Run(%s) unexpectedly not ok", h)
		}
		if f.Grade.N == 0 {
			t.Errorf("Run(%s) returned prob=%.3f with N=0 grade", h, f.Prob)
		}
	}
}

func TestGradeFromMetrics(t *testing.T) {
	tests := []struct {
		name     string
		preds    []float64
		actuals  []float64
		wantAcc  float64
		wantBase float64
		wantAUC  float64
	}{
		{
			// Prequential baseline over [1,1,0,0], one cluster per row. Row 0
			// has no prior, so the null is credited the model's own hit there
			// (a day it cannot call contributes zero lift); row 1 follows "up"
			// and wins; rows 2-3 still follow "up" and lose. (1+1+0+0)/4 = 0.5.
			name:     "perfect",
			preds:    []float64{0.9, 0.8, 0.2, 0.1},
			actuals:  []float64{1, 1, 0, 0},
			wantAcc:  1.0,
			wantBase: 0.5,
			wantAUC:  1.0,
		},
		{
			name:     "inverted",
			preds:    []float64{0.1, 0.2, 0.8, 0.9},
			actuals:  []float64{1, 1, 0, 0},
			wantAcc:  0.0,
			wantBase: 0.25,
			wantAUC:  0.0,
		},
		{
			// An all-up window with an always-up model has NO edge, and the
			// grade must say so: baseline 1.0, lift exactly 0. The hindsight
			// floor got this case right by accident. What it got wrong was every
			// PARTIALLY imbalanced window, where it charged the model for
			// imbalance the model could not have known about in advance.
			name:     "all one class base rate",
			preds:    []float64{0.6, 0.7, 0.9, 0.55},
			actuals:  []float64{1, 1, 1, 1},
			wantAcc:  1.0,
			wantBase: 1.0,
			wantAUC:  0.5, // no negatives → undefined ranking → 0.5
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// One cluster per row, matching how evaluateFolds passes bar indices.
			idx := make([]int64, len(tc.preds))
			for i := range idx {
				idx[i] = int64(i)
			}
			g := gradeFrom(idx, tc.preds, tc.actuals)
			if math.Abs(g.Accuracy-tc.wantAcc) > 1e-9 {
				t.Errorf("Accuracy = %v, want %v", g.Accuracy, tc.wantAcc)
			}
			if math.Abs(g.BaseRate-tc.wantBase) > 1e-9 {
				t.Errorf("BaseRate = %v, want %v", g.BaseRate, tc.wantBase)
			}
			if math.Abs(g.AUC-tc.wantAUC) > 1e-9 {
				t.Errorf("AUC = %v, want %v", g.AUC, tc.wantAUC)
			}
			if math.Abs(g.Lift-(tc.wantAcc-tc.wantBase)) > 1e-9 {
				t.Errorf("Lift = %v, want %v", g.Lift, tc.wantAcc-tc.wantBase)
			}
		})
	}
}

func TestAUCTiesAverageRank(t *testing.T) {
	// All-equal scores must give AUC exactly 0.5 (no ranking information),
	// exercising the tie-averaging branch.
	preds := []float64{0.5, 0.5, 0.5, 0.5}
	actuals := []float64{1, 0, 1, 0}
	if got := aucRank(preds, actuals); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("AUC on tied preds = %v, want 0.5", got)
	}
}

func TestTrainDeterministic(t *testing.T) {
	// Identical input must yield identical weights (no RNG in the fit).
	bars := barsFromCloses(separableCloses(500, 1, 33))
	m1, err1 := Train(bars, 1)
	m2, err2 := Train(bars, 1)
	if err1 != nil || err2 != nil {
		t.Fatalf("Train errors: %v %v", err1, err2)
	}
	if m1.Bias != m2.Bias {
		t.Errorf("bias differs: %v vs %v", m1.Bias, m2.Bias)
	}
	for k := range m1.Weights {
		if m1.Weights[k] != m2.Weights[k] {
			t.Errorf("weight[%d] differs: %v vs %v", k, m1.Weights[k], m2.Weights[k])
		}
	}
}

func TestVolumeRatioFeature(t *testing.T) {
	// Volume ratio should reflect a volume spike at the latest bar.
	closes := noiseCloses(120, 4)
	vols := make([]float64, len(closes))
	for i := range vols {
		vols[i] = 1000
	}
	vols[len(vols)-1] = 3000 // 3x spike at the last bar
	bars := barsFromClosesVol(closes, vols)
	f, ok := features(bars, len(bars)-1)
	if !ok {
		t.Fatal("features not computable")
	}
	volRatio := f[7] // last feature is volume ratio
	if volRatio < 2.5 {
		t.Errorf("expected volume ratio ~3 on a 3x spike, got %v", volRatio)
	}
}
