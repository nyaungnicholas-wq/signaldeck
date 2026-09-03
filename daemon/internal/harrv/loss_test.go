package harrv

import (
	"math"
	"testing"
)

func TestQLIKEIsZeroOnlyWhenExact(t *testing.T) {
	if got, ok := QLIKE(0.04, 0.04); !ok || math.Abs(got) > 1e-15 {
		t.Errorf("QLIKE(a,a) = %v ok=%v, want 0", got, ok)
	}
	for _, f := range []float64{0.01, 0.02, 0.08, 0.16} {
		got, ok := QLIKE(0.04, f)
		if !ok || got <= 0 {
			t.Errorf("QLIKE(0.04, %v) = %v, want strictly positive", f, got)
		}
	}
}

// QLIKE punishes UNDER-forecasting harder than over-forecasting by the same
// ratio. That asymmetry is the whole reason the lognormal retransform in
// PredictAt is mandatory: a model biased low is penalised twice, once for being
// wrong and once for being wrong in the expensive direction.
func TestQLIKEPunishesUnderForecastingHarder(t *testing.T) {
	const actual = 0.04
	under, _ := QLIKE(actual, actual/2) // forecast half the truth
	over, _ := QLIKE(actual, actual*2)  // forecast twice the truth
	if under <= over {
		t.Errorf("under-forecast loss %.6f is not worse than over-forecast %.6f", under, over)
	}
}

func TestLossesRefuseUnusableInput(t *testing.T) {
	nan := math.NaN()
	inf := math.Inf(1)
	for _, tc := range [][2]float64{{0, 0.04}, {0.04, 0}, {-1, 0.04}, {0.04, -1}, {nan, 0.04}, {0.04, inf}} {
		if _, ok := QLIKE(tc[0], tc[1]); ok {
			t.Errorf("QLIKE(%v,%v) returned a number it cannot justify", tc[0], tc[1])
		}
	}
	if _, ok := MSE(nan, 1); ok {
		t.Error("MSE accepted NaN")
	}
	if got, ok := MSE(3, 1); !ok || math.Abs(got-4) > 1e-15 {
		t.Errorf("MSE(3,1) = %v, want 4", got)
	}
}

// Sign convention: d = L(model) - L(null), so a NEGATIVE mean means the model
// is better. Getting this backwards would invert every published verdict.
func TestDieboldMarianoSignConvention(t *testing.T) {
	better := make([]float64, 400)
	for i := range better {
		better[i] = -0.01 + 0.001*math.Sin(float64(i))
	}
	r := DieboldMariano(better, NeweyWestLag(len(better)))
	if !r.OK {
		t.Fatal("DM refused a clean series")
	}
	if r.Mean >= 0 || r.T >= 0 {
		t.Errorf("consistently lower model loss gave Mean=%.4f T=%.2f, want both negative", r.Mean, r.T)
	}

	worse := make([]float64, 400)
	for i := range worse {
		worse[i] = -better[i]
	}
	if r2 := DieboldMariano(worse, NeweyWestLag(len(worse))); r2.Mean <= 0 || r2.T <= 0 {
		t.Errorf("mirrored series gave Mean=%.4f T=%.2f, want both positive", r2.Mean, r2.T)
	}
}

// A differential centred on zero must not produce a significant statistic.
func TestDieboldMarianoFindsNothingInNoise(t *testing.T) {
	d := make([]float64, 500)
	for i := range d {
		d[i] = math.Sin(float64(i) * 2.399963) // zero-mean, no drift
	}
	r := DieboldMariano(d, NeweyWestLag(len(d)))
	if !r.OK {
		t.Fatal("DM refused")
	}
	if math.Abs(r.T) > 1.96 {
		t.Errorf("|T| = %.2f on zero-mean noise; that is a false positive", math.Abs(r.T))
	}
}

func TestDieboldMarianoRefusesUnusableInput(t *testing.T) {
	if r := DieboldMariano([]float64{1, 2, 3}, 1); r.OK {
		t.Error("DM accepted a 3-point series")
	}
	d := make([]float64, 100)
	d[7] = math.NaN()
	if r := DieboldMariano(d, 4); r.OK {
		t.Error("DM accepted a series containing NaN")
	}
	// identical forecasts give an exactly zero differential and no variance,
	// which must refuse rather than divide by zero
	if r := DieboldMariano(make([]float64, 100), 4); r.OK {
		t.Error("DM produced a statistic from an all-zero differential")
	}
}

func TestNeweyWestLagIsTheStandardRule(t *testing.T) {
	// floor(4*(n/100)^(2/9))
	for _, tc := range [][2]int{{100, 4}, {2000, 7}, {500, 5}} {
		if got := NeweyWestLag(tc[0]); got != tc[1] {
			t.Errorf("NeweyWestLag(%d) = %d, want %d", tc[0], got, tc[1])
		}
	}
	if NeweyWestLag(0) != 0 || NeweyWestLag(-5) != 0 {
		t.Error("NeweyWestLag should be 0 for non-positive n")
	}
}

// The nulls must be causal too. EWMA in particular walks the series, so a bug
// there would be invisible in the forecast but would silently hand the null
// information the model never had.
func TestNullsDoNotReadTheFuture(t *testing.T) {
	rv := make([]float64, 300)
	for i := range rv {
		rv[i] = 1e-4 * (1 + 0.5*math.Sin(float64(i)/3))
	}
	const at = 200

	ewFull, okA := EWMAAt(rv, at, RiskMetricsLambda)
	rwFull, okB := RWAt(rv, at)
	flFull, okC := FlatAt(rv, at, 22)
	if !okA || !okB || !okC {
		t.Fatal("a null refused clean input")
	}

	poisoned := append([]float64(nil), rv...)
	for i := at + 1; i < len(poisoned); i++ {
		poisoned[i] = 9.0
	}
	if got, _ := EWMAAt(poisoned, at, RiskMetricsLambda); got != ewFull {
		t.Errorf("EWMA read the future: %v vs %v", got, ewFull)
	}
	if got, _ := RWAt(poisoned, at); got != rwFull {
		t.Errorf("RW read the future: %v vs %v", got, rwFull)
	}
	if got, _ := FlatAt(poisoned, at, 22); got != flFull {
		t.Errorf("Flat read the future: %v vs %v", got, flFull)
	}
}

// A hole updates nothing and does not reset the state. If a NaN reset the EWMA
// the null would jump to the next observation, which would make it look worse
// on exactly the symbols with patchy data.
func TestEWMASkipsHolesWithoutResetting(t *testing.T) {
	clean := []float64{1e-4, 2e-4, 3e-4, 4e-4}
	holed := []float64{1e-4, 2e-4, math.NaN(), 3e-4, 4e-4}

	a, okA := EWMAAt(clean, len(clean)-1, RiskMetricsLambda)
	b, okB := EWMAAt(holed, len(holed)-1, RiskMetricsLambda)
	if !okA || !okB {
		t.Fatal("EWMA refused")
	}
	if math.Abs(a-b) > 1e-18 {
		t.Errorf("a hole changed the EWMA: %v vs %v", a, b)
	}
	if _, ok := EWMAAt([]float64{math.NaN(), math.NaN()}, 1, RiskMetricsLambda); ok {
		t.Error("EWMA produced a value from an all-NaN series")
	}
}

func TestNullsRefuseUnusableInput(t *testing.T) {
	rv := []float64{1e-4, math.NaN(), 2e-4}
	if _, ok := RWAt(rv, 1); ok {
		t.Error("RW returned a value for a NaN day")
	}
	for _, i := range []int{-1, 99} {
		if _, ok := RWAt(rv, i); ok {
			t.Errorf("RW accepted index %d", i)
		}
	}
	for _, l := range []float64{0, 1, -0.5, 1.5} {
		if _, ok := EWMAAt(rv, 2, l); ok {
			t.Errorf("EWMA accepted lambda %v", l)
		}
	}
}
