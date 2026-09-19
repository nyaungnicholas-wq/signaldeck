package harrv

import (
	"math"
	"math/rand"
	"testing"
)

// synthHAR builds an RV series that genuinely follows the HAR recursion, so
// OLS has a right answer to find. The features are computed with the SAME
// helpers the estimator uses, which is what makes coefficient recovery a test
// of the solver rather than of two different definitions of "weekly mean".
// h = 1 throughout this file: the coefficient-recovery generator IS a
// one-step recursion, so fitting it at any other horizon would be testing
// the solver against a process it was not generated from.
func synthHAR(n int, b0, bd, bw, bm float64, noise func(i int) float64) []float64 {
	rv := make([]float64, n)
	for i := 0; i < LagM+2; i++ {
		rv[i] = 1e-4 * (1 + 0.1*math.Sin(float64(i)))
	}
	for i := LagM + 1; i < n-1; i++ {
		f, ok := features(rv, i)
		if !ok {
			rv[i+1] = 1e-4
			continue
		}
		y := b0 + bd*f[1] + bw*f[2] + bm*f[3] + noise(i)
		rv[i+1] = math.Exp(y)
	}
	return rv
}

// OLS must recover the generating coefficients. Corsi's canonical estimates
// sit near bd 0.35 / bw 0.35 / bm 0.25, so those are the values used: if the
// solver cannot find them the whole method is untestable downstream.
//
// THE NOISE HAS TO BE REAL. A first attempt at this test used a tiny smooth
// sinusoid, and it failed with bd = -0.11 and bm = 0.91 -- not because the
// solver was wrong but because the test was. With coefficients summing to 0.95
// and almost no innovation, ln RV converges to the fixed point b0/(1-0.95) and
// the daily, weekly and monthly regressors collapse onto the same value. Once
// they are collinear, many coefficient vectors fit equally well and the
// parameters are simply not identified. Driving the process with a genuine
// innovation is what separates the three regressors and gives OLS something to
// recover. Seeded, so it is reproducible rather than flaky.
func TestFitRecoversKnownCoefficients(t *testing.T) {
	const (
		b0 = -0.5
		bd = 0.35
		bw = 0.35
		bm = 0.25
	)
	rng := rand.New(rand.NewSource(20260903))
	noise := func(int) float64 { return rng.NormFloat64() * 0.5 }
	rv := synthHAR(4000, b0, bd, bw, bm, noise)

	f, ok := FitAt(rv, 3900, 1)
	if !ok {
		t.Fatal("FitAt refused on a clean 4,000-point series")
	}
	if f.N < MinTrain {
		t.Errorf("N = %d, want >= %d", f.N, MinTrain)
	}
	// WHAT IS ACTUALLY IDENTIFIED, and what is not.
	//
	// bd recovers cleanly. bw and bm do NOT recover individually, and that is a
	// property of the design rather than a defect: a 5-day and a 22-day mean of
	// the same series are strongly correlated, so OLS can shift weight between
	// them at almost no cost in fit. Measured here, bw came back 0.428 against
	// a true 0.350 and bm 0.172 against 0.250 -- while their SUM was 0.600
	// against a true 0.600, exactly. Their sum is the identified quantity.
	//
	// Asserting the individual split tightly would be asserting something the
	// data cannot answer, and it is worth knowing before anyone reads meaning
	// into a fitted weekly-versus-monthly weight on real symbols.
	if math.Abs(f.BetaD-bd) > 0.06 {
		t.Errorf("bd = %.4f, want %.4f", f.BetaD, bd)
	}
	if got, want := f.BetaW+f.BetaM, bw+bm; math.Abs(got-want) > 0.06 {
		t.Errorf("bw+bm = %.4f, want %.4f (the identified combination)", got, want)
	}
	if got, want := f.BetaD+f.BetaW+f.BetaM, bd+bw+bm; math.Abs(got-want) > 0.06 {
		t.Errorf("total persistence = %.4f, want %.4f", got, want)
	}
	if math.Abs(f.Beta0-b0) > 0.15 {
		t.Errorf("b0 = %.4f, want %.4f", f.Beta0, b0)
	}

	// the generator's innovation variance is 0.5^2 = 0.25
	if math.Abs(f.ResidVar-0.25) > 0.05 {
		t.Errorf("ResidVar = %g, want about 0.25", f.ResidVar)
	}
}

// The retransform must actually be applied. exp(yhat) is the MEDIAN of a
// lognormal; the mean is exp(yhat + s^2/2). Omitting it biases every level
// forecast low, which costs nothing in a log-scale loss -- exactly why it is
// easy to miss -- and is then punished by QLIKE (asymmetric against
// under-forecasting) and by VaR coverage (too tight, breaches too often).
func TestPredictAppliesJensenCorrection(t *testing.T) {
	rv := synthHAR(1200, -0.5, 0.35, 0.35, 0.25,
		func(i int) float64 { return 0.15 * math.Sin(float64(i)*1.7) })
	f, ok := FitAt(rv, 1100, 1)
	if !ok {
		t.Fatal("FitAt refused")
	}
	got, ok := PredictAt(rv, 1100, f)
	if !ok {
		t.Fatal("PredictAt refused")
	}

	x, _ := features(rv, 1100)
	yhat := f.Beta0*x[0] + f.BetaD*x[1] + f.BetaW*x[2] + f.BetaM*x[3]
	median := math.Exp(yhat)
	want := math.Exp(yhat + f.ResidVar/2)

	if math.Abs(got-want)/want > 1e-12 {
		t.Errorf("PredictAt = %g, want exp(yhat + s^2/2) = %g", got, want)
	}
	if got <= median {
		t.Errorf("forecast %g is not above the median %g -- the correction is missing", got, median)
	}
}

// FitAt and PredictAt must read nothing after t. Rigging the future to be
// enormous must not move either one by a single bit.
func TestFitAndPredictDoNotReadTheFuture(t *testing.T) {
	rv := synthHAR(1200, -0.5, 0.35, 0.35, 0.25,
		func(i int) float64 { return 0.02 * math.Sin(float64(i)*1.7) })
	const at = 900

	fFull, okA := FitAt(rv, at, 1)
	pFull, okP := PredictAt(rv, at, fFull)
	if !okA || !okP {
		t.Fatal("baseline fit/predict refused")
	}

	poisoned := append([]float64(nil), rv...)
	for i := at + 1; i < len(poisoned); i++ {
		poisoned[i] = 9.0 // ~300% daily vol; would wreck any fit that saw it
	}
	fPois, okB := FitAt(poisoned, at, 1)
	pPois, okQ := PredictAt(poisoned, at, fPois)
	if !okB || !okQ {
		t.Fatal("fit/predict refused on the poisoned series")
	}

	if fFull != fPois {
		t.Errorf("FitAt read the future:\n clean    %+v\n poisoned %+v", fFull, fPois)
	}
	if pFull != pPois {
		t.Errorf("PredictAt read the future: %v vs %v", pFull, pPois)
	}

	// And truncating at t must give the identical fit.
	fTrunc, ok := FitAt(rv[:at+1], at, 1)
	if !ok || fTrunc != fFull {
		t.Errorf("FitAt on a truncated series differs:\n full  %+v\n trunc %+v", fFull, fTrunc)
	}
}

func TestFitRefusesThinHistory(t *testing.T) {
	rv := synthHAR(600, -0.5, 0.35, 0.35, 0.25, func(int) float64 { return 0 })
	if _, ok := FitAt(rv, MinHistory-1, 1); ok {
		t.Error("fitted below MinHistory")
	}
	if _, ok := FitAt(rv, 10_000, 1); ok {
		t.Error("fitted past the end of the series")
	}
	short := make([]float64, 100)
	for i := range short {
		short[i] = 1e-4
	}
	if _, ok := FitAt(short, 90, 1); ok {
		t.Error("fitted a 100-point series")
	}
}

// A hole at t means no regressor row, and the honest output is no forecast.
func TestPredictRefusesOnAHole(t *testing.T) {
	rv := synthHAR(1200, -0.5, 0.35, 0.35, 0.25, func(int) float64 { return 0 })
	f, ok := FitAt(rv, 1100, 1)
	if !ok {
		t.Fatal("FitAt refused")
	}
	holed := append([]float64(nil), rv...)
	holed[1100] = math.NaN()
	if _, ok := PredictAt(holed, 1100, f); ok {
		t.Error("produced a forecast from a NaN regressor")
	}
}

func TestSolve4RefusesSingular(t *testing.T) {
	var a [4][4]float64 // all zeros
	if _, ok := solve4(a, [4]float64{1, 2, 3, 4}); ok {
		t.Error("solved a singular system instead of refusing")
	}
}

// TargetAt is the OUTCOME, so unlike everything else in this package it
// deliberately reads past t. What it must not do is average over a partial
// window: a 5-day target computed from the 2 days that happened to be
// estimable is a silently different quantity on exactly the symbols with
// patchy data.
func TestTargetAtRequiresAWholeWindow(t *testing.T) {
	rv := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	if got, ok := TargetAt(rv, 2, 1); !ok || got != 4 {
		t.Errorf("h=1 at t=2: got %v ok=%v, want 4", got, ok)
	}
	if got, ok := TargetAt(rv, 2, 4); !ok || got != (4+5+6+7)/4.0 {
		t.Errorf("h=4 at t=2: got %v ok=%v, want 5.5", got, ok)
	}
	// a hole anywhere in the window refuses the whole target
	holed := append([]float64(nil), rv...)
	holed[5] = math.NaN()
	if _, ok := TargetAt(holed, 2, 4); ok {
		t.Error("averaged over a window containing a hole")
	}
	// running off the end refuses rather than shortening
	if _, ok := TargetAt(rv, 6, 4); ok {
		t.Error("produced a target from a window past the end of the series")
	}
	for _, h := range []Horizon{0, -1} {
		if _, ok := TargetAt(rv, 2, h); ok {
			t.Errorf("accepted horizon %d", h)
		}
	}
}

// The no-lookahead property must survive h > 1, where it is easier to break:
// the last training row has to stop h-1 days earlier so its own target does
// not run past t.
func TestFitDoesNotReadTheFutureAtLongerHorizons(t *testing.T) {
	rv := synthHAR(2000, -0.5, 0.35, 0.35, 0.25,
		func(i int) float64 { return 0.02 * math.Sin(float64(i)*1.7) })
	const at = 1500

	for _, h := range []Horizon{1, 5, 22} {
		clean, ok := FitAt(rv, at, h)
		if !ok {
			t.Fatalf("h=%d: FitAt refused", h)
		}
		if clean.H != h {
			t.Errorf("h=%d: fit reports horizon %d", h, clean.H)
		}
		poisoned := append([]float64(nil), rv...)
		for i := at + 1; i < len(poisoned); i++ {
			poisoned[i] = 9.0
		}
		dirty, ok := FitAt(poisoned, at, h)
		if !ok {
			t.Fatalf("h=%d: FitAt refused the poisoned series", h)
		}
		if clean != dirty {
			t.Errorf("h=%d: the fit moved when only the FUTURE changed:\n clean %+v\n dirty %+v",
				h, clean, dirty)
		}
	}
}

// A longer horizon must consume more history, because each training row now
// needs h forward days before its target is observable.
func TestLongerHorizonUsesFewerTrainingRows(t *testing.T) {
	rv := synthHAR(2000, -0.5, 0.35, 0.35, 0.25,
		func(i int) float64 { return 0.02 * math.Sin(float64(i)*1.7) })
	f1, ok1 := FitAt(rv, 1500, 1)
	f22, ok22 := FitAt(rv, 1500, 22)
	if !ok1 || !ok22 {
		t.Fatal("FitAt refused")
	}
	if f22.N >= f1.N {
		t.Errorf("h=22 used %d rows, h=1 used %d; the longer horizon must use fewer", f22.N, f1.N)
	}
	if f1.N-f22.N != 21 {
		t.Errorf("row-count difference is %d, want exactly 21 (h-1)", f1.N-f22.N)
	}
}
