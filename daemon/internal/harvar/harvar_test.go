package harvar

import (
	"math"
	"testing"
)

func TestStandardizedResidualsDivideByForecastVol(t *testing.T) {
	rets := []float64{0.02, -0.01, 0.03}
	rvHat := []float64{0.0004, 0.0001, 0.0009} // sigma = 0.02, 0.01, 0.03
	z := StandardizedResiduals(rets, rvHat)
	want := []float64{1, -1, 1}
	if len(z) != len(want) {
		t.Fatalf("got %d residuals, want %d", len(z), len(want))
	}
	for i := range want {
		if math.Abs(z[i]-want[i]) > 1e-12 {
			t.Errorf("z[%d] = %v, want %v", i, z[i], want[i])
		}
	}
}

// A day that cannot be standardised is DROPPED, not defaulted to zero. A zero
// residual is a claim that the day was exactly average, which would quietly
// thin the tail and flatter every VaR built from it.
func TestStandardizedResidualsDropUnusableDays(t *testing.T) {
	nan := math.NaN()
	rets := []float64{0.02, nan, 0.01, 0.01, 0.02}
	rvHat := []float64{0.0004, 0.0004, 0, nan, 0.0004}
	z := StandardizedResiduals(rets, rvHat)
	if len(z) != 2 {
		t.Fatalf("kept %d residuals, want 2 (a NaN return, a zero variance and a NaN variance are all unusable)", len(z))
	}
	for _, v := range z {
		if v == 0 {
			t.Error("an unusable day was defaulted to 0 instead of dropped")
		}
	}
	if len(StandardizedResiduals(nil, nil)) != 0 {
		t.Error("nil input should produce no residuals")
	}
}

// z drawn from a known shape gives a known quantile. With 1,000 residuals
// uniform on [-1,1], the 5% quantile is -0.9, so VaR = 0.9*sigma.
func TestVaRESUsesTheEmpiricalQuantile(t *testing.T) {
	const n = 1000
	z := make([]float64, n)
	for i := range z {
		z[i] = -1 + 2*float64(i)/float64(n-1)
	}
	const sigma2 = 0.0004 // sigma = 0.02
	v, es, ok := VaRES(sigma2, z, 0.05)
	if !ok {
		t.Fatal("VaRES refused 1,000 residuals")
	}
	if want := 0.9 * 0.02; math.Abs(v-want) > 5e-4 {
		t.Errorf("VaR = %.6f, want about %.6f", v, want)
	}
	// ES is the mean of the worst 5%, i.e. about the midpoint of [-1,-0.9]
	if want := 0.95 * 0.02; math.Abs(es-want) > 5e-4 {
		t.Errorf("ES = %.6f, want about %.6f", es, want)
	}
}

// ES is the mean loss GIVEN a breach, so it can never be smaller than the
// breach threshold. This ordering is the cheapest available check that the
// tail arithmetic is the right way round.
func TestExpectedShortfallNeverBelowVaR(t *testing.T) {
	z := make([]float64, 2000)
	for i := range z {
		// a fat-tailed-ish deterministic shape
		x := float64(i)/1000 - 1
		z[i] = x * x * x * 3
	}
	for _, level := range []float64{Level5, Level1, 0.10, 0.025} {
		v, es, ok := VaRES(0.0004, z, level)
		if !ok {
			continue
		}
		if es < v {
			t.Errorf("level %v: ES %.6f < VaR %.6f", level, es, v)
		}
		if v <= 0 || es <= 0 {
			t.Errorf("level %v: VaR/ES must be positive loss fractions, got %v/%v", level, v, es)
		}
	}
}

// A deeper level must produce a larger loss estimate. If the 1% VaR were
// smaller than the 5%, the quantile indexing would be inverted -- a mistake
// that produces plausible-looking numbers and would only surface as a strange
// breach rate months later.
func TestDeeperLevelMeansLargerLoss(t *testing.T) {
	z := make([]float64, 5000)
	for i := range z {
		z[i] = -3 + 6*float64(i)/float64(len(z)-1)
	}
	v5, es5, ok5 := VaRES(0.0004, z, Level5)
	v1, es1, ok1 := VaRES(0.0004, z, Level1)
	if !ok5 || !ok1 {
		t.Fatal("VaRES refused")
	}
	if v1 <= v5 {
		t.Errorf("1%% VaR %.6f is not deeper than 5%% VaR %.6f", v1, v5)
	}
	if es1 <= es5 {
		t.Errorf("1%% ES %.6f is not deeper than 5%% ES %.6f", es1, es5)
	}
}

// A thin tail is refused rather than rounded up. Returning 0 would publish
// "this cannot lose money", which is the worst thing a risk surface can say.
func TestVaRESRefusesAThinTail(t *testing.T) {
	// 100 residuals at 1% leaves a tail of 1, far below MinTailObs
	z := make([]float64, 100)
	for i := range z {
		z[i] = -1 + 2*float64(i)/99
	}
	if _, _, ok := VaRES(0.0004, z, 0.01); ok {
		t.Errorf("published a 1%% VaR from a tail of %d, floor is %d", int(0.01*100), MinTailObs)
	}
	// the same series at 5% has a tail of 5, still below the floor
	if _, _, ok := VaRES(0.0004, z, 0.05); ok {
		t.Error("published a 5% VaR from a tail of 5")
	}
	// and 500 residuals at 1% gives a tail of 5 -- the case the plan calls out,
	// where a two-year window still cannot support a 1% figure
	z500 := make([]float64, 500)
	for i := range z500 {
		z500[i] = -1 + 2*float64(i)/499
	}
	if _, _, ok := VaRES(0.0004, z500, 0.01); ok {
		t.Error("published a 1% VaR from a 500-day window (tail of 5)")
	}
}

func TestVaRESRefusesUnusableInput(t *testing.T) {
	z := make([]float64, 1000)
	for i := range z {
		z[i] = -1 + 2*float64(i)/999
	}
	for _, s := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, _, ok := VaRES(s, z, 0.05); ok {
			t.Errorf("accepted sigma2 = %v", s)
		}
	}
	for _, l := range []float64{0, 1, -0.1, 1.5} {
		if _, _, ok := VaRES(0.0004, z, l); ok {
			t.Errorf("accepted level = %v", l)
		}
	}
	if _, _, ok := VaRES(0.0004, nil, 0.05); ok {
		t.Error("accepted an empty residual series")
	}
}

// A day with no usable forecast is EXCLUDED, never scored as a pass. Counting
// a missing forecast as "no breach" is how a model with poor coverage gets
// flattered by its own gaps.
func TestBreachesExcludeDaysWithoutAForecast(t *testing.T) {
	nan := math.NaN()
	rets := []float64{-0.05, -0.01, nan, -0.06, -0.02}
	varPct := []float64{0.03, 0.03, 0.03, nan, 0.03}
	b, idx := Breaches(rets, varPct)

	if len(b) != 3 || len(idx) != 3 {
		t.Fatalf("kept %d days, want 3 (a NaN return and a NaN forecast are both dropped)", len(b))
	}
	if !b[0] || b[1] || b[2] {
		t.Errorf("breach flags = %v, want [true false false]", b)
	}
	// the surviving days must be identifiable
	want := []int{0, 1, 4}
	for i := range want {
		if idx[i] != want[i] {
			t.Errorf("idx = %v, want %v", idx, want)
			break
		}
	}
}

// The threshold is strict: a loss exactly equal to the VaR is not a breach.
func TestBreachBoundaryIsStrict(t *testing.T) {
	b, _ := Breaches([]float64{-0.03, -0.030001}, []float64{0.03, 0.03})
	if len(b) != 2 {
		t.Fatalf("got %d days, want 2", len(b))
	}
	if b[0] {
		t.Error("a loss exactly equal to the VaR was counted as a breach")
	}
	if !b[1] {
		t.Error("a loss just beyond the VaR was not counted as a breach")
	}
}
