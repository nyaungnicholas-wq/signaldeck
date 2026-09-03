package harvar

import (
	"math"
	"math/rand"
	"testing"
)

// breachesWith builds a series of n days with exactly k breaches, spread
// evenly. Use it where only the COUNT matters (Kupiec).
//
// Do NOT use it as the "independent" fixture for the independence test. A
// perfectly periodic series is not independent, it is strongly NEGATIVELY
// autocorrelated: a breach every 10th day means a breach never follows a
// breach, n11 is 0, and Christoffersen correctly rejects it. That is the test
// working, not failing -- the first version of this file asserted otherwise
// and was wrong.
func breachesWith(n, k int) []bool {
	b := make([]bool, n)
	if k <= 0 {
		return b
	}
	step := float64(n) / float64(k)
	for i := 0; i < k; i++ {
		idx := int(float64(i)*step) + int(step/2)
		if idx >= n {
			idx = n - 1
		}
		b[idx] = true
	}
	return b
}

// breachesRandom marks each day a breach with probability p, from a fixed
// seed. This is what "independent" actually looks like.
func breachesRandom(n int, p float64, seed int64) []bool {
	rng := rand.New(rand.NewSource(seed))
	b := make([]bool, n)
	for i := range b {
		b[i] = rng.Float64() < p
	}
	return b
}

// ChiSqSF must match the closed forms exactly. Everything downstream is a
// p-value from this function, so an error here silently rescales every verdict.
func TestChiSqSFClosedForms(t *testing.T) {
	// df=2 is exp(-x/2): at x=0 -> 1, and the 5% critical value is 5.991.
	if got := ChiSqSF(0, 2); got != 1 {
		t.Errorf("ChiSqSF(0,2) = %v, want 1", got)
	}
	if got, want := ChiSqSF(5.99146, 2), 0.05; math.Abs(got-want) > 1e-4 {
		t.Errorf("ChiSqSF(5.99146,2) = %.6f, want %.4f", got, want)
	}
	// df=1: the 5% critical value is 3.841, the 1% is 6.635.
	if got, want := ChiSqSF(3.84146, 1), 0.05; math.Abs(got-want) > 1e-4 {
		t.Errorf("ChiSqSF(3.84146,1) = %.6f, want %.4f", got, want)
	}
	if got, want := ChiSqSF(6.63490, 1), 0.01; math.Abs(got-want) > 1e-4 {
		t.Errorf("ChiSqSF(6.63490,1) = %.6f, want %.4f", got, want)
	}
	// a survival function is monotone decreasing
	prev := 1.0
	for x := 0.5; x < 20; x += 0.5 {
		got := ChiSqSF(x, 1)
		if got > prev {
			t.Fatalf("ChiSqSF is not monotone at x=%v", x)
		}
		prev = got
	}
	// an unsupported df must be LOUD, not quietly wrong
	if !math.IsNaN(ChiSqSF(1, 3)) {
		t.Error("ChiSqSF returned a number for df=3")
	}
}

// A model breaching at exactly its nominal rate must be indistinguishable from
// correct: LR near zero, p near 1.
func TestKupiecAcceptsAPerfectlyCalibratedModel(t *testing.T) {
	r := Kupiec(breachesWith(1000, 50), 0.05)
	if !r.OK {
		t.Fatal("Kupiec refused a 1,000-day series")
	}
	if math.Abs(r.Rate-0.05) > 1e-9 {
		t.Errorf("rate = %v, want 0.05", r.Rate)
	}
	if r.LR > 1e-9 {
		t.Errorf("LR = %v on an exactly calibrated model, want ~0", r.LR)
	}
	if r.P < 0.99 {
		t.Errorf("p = %v, want ~1", r.P)
	}
}

// A model breaching far too often must be rejected. This is the direction that
// matters for a risk tool: too many breaches means the VaR is too tight.
func TestKupiecRejectsAnUnderstatedVaR(t *testing.T) {
	r := Kupiec(breachesWith(1000, 150), 0.05) // 15% observed against 5% claimed
	if !r.OK {
		t.Fatal("Kupiec refused")
	}
	if r.P > 0.01 {
		t.Errorf("p = %v for 15%% breaches against a 5%% claim, want a clear rejection", r.P)
	}
	// and the opposite direction is also a failure of calibration
	r2 := Kupiec(breachesWith(1000, 2), 0.05)
	if r2.OK && r2.P > 0.01 {
		t.Errorf("p = %v for 0.2%% breaches against a 5%% claim, want a rejection", r2.P)
	}
}

// The boundary cases are where a naive implementation returns NaN: with no
// breaches at all, ln(p) is -Inf.
func TestKupiecHandlesBoundaryCounts(t *testing.T) {
	for _, k := range []int{0, 100} {
		r := Kupiec(breachesWith(100, k), 0.05)
		if !r.OK {
			t.Errorf("k=%d refused", k)
			continue
		}
		if math.IsNaN(r.LR) || math.IsInf(r.LR, 0) || math.IsNaN(r.P) {
			t.Errorf("k=%d gave LR=%v P=%v", k, r.LR, r.P)
		}
		if r.P < 0 || r.P > 1 {
			t.Errorf("k=%d gave p outside [0,1]: %v", k, r.P)
		}
	}
}

// Log-space arithmetic is not a style choice. Computing the ratio of products
// directly underflows to zero for N in the hundreds and yields +Inf.
func TestKupiecDoesNotUnderflowOnLongSeries(t *testing.T) {
	r := Kupiec(breachesWith(5000, 250), 0.05)
	if !r.OK || math.IsInf(r.LR, 0) || math.IsNaN(r.LR) {
		t.Errorf("5,000-day series gave LR=%v OK=%v", r.LR, r.OK)
	}
}

// THE POINT OF THE INDEPENDENCE TEST. Both series below have exactly the same
// breach COUNT and so identical Kupiec results; only the TIMING differs. A
// model can have the right number of bad days and still be wrong about when.
func TestChristoffersenSeparatesTimingFromRate(t *testing.T) {
	const n, k = 600, 60

	// genuinely unpredictable, seeded so it is reproducible rather than flaky
	spread := breachesRandom(n, float64(k)/float64(n), 20260903)

	// The clustered series carries EXACTLY the same number of breaches, placed
	// consecutively. Same count by construction, so Kupiec cannot tell them
	// apart and any difference below is timing alone -- which is the whole
	// claim this test is making.
	count := 0
	for _, b := range spread {
		if b {
			count++
		}
	}
	clustered := make([]bool, n)
	for i := 100; i < 100+count; i++ {
		clustered[i] = true
	}

	ucA, ucB := Kupiec(spread, 0.1), Kupiec(clustered, 0.1)
	if !ucA.OK || !ucB.OK {
		t.Fatal("Kupiec refused")
	}
	if ucA.Br != ucB.Br || math.Abs(ucA.LR-ucB.LR) > 1e-9 {
		t.Fatalf("premise broken: the RATE test distinguishes the two series (%d/%v vs %d/%v)",
			ucA.Br, ucA.LR, ucB.Br, ucB.LR)
	}
	if ucA.P < 0.05 {
		t.Fatalf("premise broken: the spread series already fails the RATE test (p = %v)", ucA.P)
	}

	indA, indB := ChristoffersenInd(spread), ChristoffersenInd(clustered)
	if !indA.OK || !indB.OK {
		t.Fatal("independence test refused a series with both transition rows populated")
	}
	if indA.P < 0.05 {
		t.Errorf("independently placed breaches were called dependent (p = %v)", indA.P)
	}
	if indB.P > 1e-6 {
		t.Errorf("60 consecutive breaches were not called dependent (p = %v)", indB.P)
	}

	// conditional coverage must inherit the failure
	cc := ChristoffersenCC(clustered, 0.1)
	if !cc.OK || cc.P > 1e-6 || cc.DF != 2 {
		t.Errorf("CC on clustered breaches: p=%v df=%d, want a rejection at df 2", cc.P, cc.DF)
	}
}

// With rare breaches there is no evidence about what follows a breach, and the
// honest answer is to refuse. This is the common case at the 1% level, not an
// exotic one.
func TestChristoffersenRefusesWhenUnidentified(t *testing.T) {
	none := make([]bool, 250) // no breaches at all: the n1x row is empty
	if r := ChristoffersenInd(none); r.OK {
		t.Error("independence test produced a statistic with zero breaches")
	}
	all := make([]bool, 250)
	for i := range all {
		all[i] = true // the n0x row is empty
	}
	if r := ChristoffersenInd(all); r.OK {
		t.Error("independence test produced a statistic with no non-breach days")
	}
	// and CC must refuse whenever a component refuses
	if r := ChristoffersenCC(none, 0.05); r.OK {
		t.Error("CC produced a statistic when the independence component refused")
	}
}

// Br and Rate must agree between the two tests on the same input. Counting
// only transition pairs would silently drop a breach on day one.
func TestBreachCountsAgreeAcrossTests(t *testing.T) {
	b := breachesWith(500, 25)
	b[0] = true // a breach with no predecessor
	uc := Kupiec(b, 0.05)
	ind := ChristoffersenInd(b)
	if !uc.OK || !ind.OK {
		t.Fatal("a test refused")
	}
	if uc.Br != ind.Br || math.Abs(uc.Rate-ind.Rate) > 1e-12 {
		t.Errorf("counts disagree: Kupiec %d/%v vs Christoffersen %d/%v",
			uc.Br, uc.Rate, ind.Br, ind.Rate)
	}
}

func TestCoverageRefusesUnusableInput(t *testing.T) {
	short := breachesWith(MinObs-1, 2)
	if r := Kupiec(short, 0.05); r.OK {
		t.Errorf("accepted %d observations, below MinObs=%d", MinObs-1, MinObs)
	}
	if r := ChristoffersenInd(short); r.OK {
		t.Error("independence test accepted a short series")
	}
	for _, a := range []float64{0, 1, -0.1, 1.5, math.NaN()} {
		if r := Kupiec(breachesWith(200, 10), a); r.OK {
			t.Errorf("Kupiec accepted alpha = %v", a)
		}
	}
}
