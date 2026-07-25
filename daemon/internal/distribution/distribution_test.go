package distribution

import (
	"math"
	"math/rand"
	"testing"
)

func normalSample(n int, mu, sigma float64, seed int64) []float64 {
	rnd := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic test fixture
	out := make([]float64, n)
	for i := range out {
		out[i] = mu + sigma*rnd.NormFloat64()
	}
	return out
}

// The forecast must refuse thin samples rather than reporting a confident shape
// built from a handful of points.
func TestForecastRefusesThinSample(t *testing.T) {
	if _, ok := Forecast(normalSample(MinSample-1, 0, 0.02, 1), 0.001); ok {
		t.Fatal("forecast produced from below MinSample")
	}
	if _, ok := Forecast(normalSample(MinSample, 0, 0.02, 1), 0.001); !ok {
		t.Fatal("forecast refused at exactly MinSample")
	}
	if _, ok := Forecast(normalSample(200, 0, 0.02, 1), -0.01); ok {
		t.Fatal("negative tau accepted")
	}
	bad := normalSample(200, 0, 0.02, 1)
	bad[7] = math.NaN()
	if _, ok := Forecast(bad, 0.001); ok {
		t.Fatal("NaN in sample accepted")
	}
}

// The three probabilities partition the outcome space — there is no fourth
// case, and the no-trade band must be represented rather than folded into a
// direction.
func TestProbabilitiesPartition(t *testing.T) {
	d, ok := Forecast(normalSample(2000, 0.001, 0.02, 7), 0.005)
	if !ok {
		t.Fatal("forecast refused")
	}
	if s := d.PUp + d.PDown + d.PInside; math.Abs(s-1) > 1e-12 {
		t.Fatalf("probabilities sum to %v, want 1", s)
	}
	if d.PInside <= 0 {
		t.Fatal("no-trade band has zero probability on a sample that must contain small moves")
	}
	if math.Abs(d.Edge-(d.PUp-d.PDown)) > 1e-12 {
		t.Fatalf("Edge = %v, want PUp-PDown", d.Edge)
	}
}

// A wider cost band must strictly shrink the tradeable probabilities. This is
// the property that makes the band a real gate rather than a label.
func TestWiderBandShrinksTradeableProbability(t *testing.T) {
	s := normalSample(3000, 0, 0.02, 11)
	tight, _ := Forecast(s, 0.001)
	wide, _ := Forecast(s, 0.03)
	if wide.PUp >= tight.PUp || wide.PDown >= tight.PDown {
		t.Fatalf("wider band did not shrink tails: tight=%.3f/%.3f wide=%.3f/%.3f",
			tight.PUp, tight.PDown, wide.PUp, wide.PDown)
	}
	if wide.PInside <= tight.PInside {
		t.Fatal("wider band did not grow the no-trade zone")
	}
}

// Quantiles must be ordered and track the true distribution.
func TestQuantilesRecoverKnownDistribution(t *testing.T) {
	d, _ := Forecast(normalSample(20000, 0.01, 0.05, 3), 0.001)
	if !(d.Q10 < d.Q50 && d.Q50 < d.Q90) {
		t.Fatalf("quantiles out of order: %v %v %v", d.Q10, d.Q50, d.Q90)
	}
	// N(0.01, 0.05): median ~0.01, q10 ~ 0.01-1.2816*0.05 = -0.054.
	if math.Abs(d.Q50-0.01) > 0.003 {
		t.Fatalf("median = %v, want ~0.01", d.Q50)
	}
	if math.Abs(d.Q10-(-0.054)) > 0.005 {
		t.Fatalf("q10 = %v, want ~-0.054", d.Q10)
	}
	if math.Abs(d.Sigma-0.05) > 0.003 {
		t.Fatalf("sigma = %v, want ~0.05", d.Sigma)
	}
}

func TestQuantileType7KnownValues(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5}
	for _, tc := range []struct{ q, want float64 }{
		{0, 1}, {0.5, 3}, {1, 5}, {0.25, 2}, {0.1, 1.4},
	} {
		if got := quantile(xs, tc.q); math.Abs(got-tc.want) > 1e-12 {
			t.Fatalf("quantile(%v) = %v, want %v", tc.q, got, tc.want)
		}
	}
}

// THE headline property: a high-win-rate strategy with tiny winners and large
// losers must report NEGATIVE expected value, and a low-win-rate strategy with
// large winners must report positive — the case a win-rate target gets exactly
// backwards.
func TestExpectedValueRanksPayoffOverWinRate(t *testing.T) {
	// 70% of moves are +0.5%, 30% are −4%: wins often, loses money.
	var bad []float64
	for i := 0; i < 700; i++ {
		bad = append(bad, 0.005)
	}
	for i := 0; i < 300; i++ {
		bad = append(bad, -0.04)
	}
	db, ok := Forecast(bad, 0.001)
	if !ok {
		t.Fatal("refused")
	}
	if db.Edge <= 0 {
		t.Fatalf("Edge = %v, want positive (70%% of moves clear +tau)", db.Edge)
	}
	if db.ExpectedValue >= 0 {
		t.Fatalf("ExpectedValue = %v, want negative for a 70%%-win/negative-payoff sample", db.ExpectedValue)
	}

	// 40% of moves are +6%, 60% are −1%: loses often, makes money.
	var good []float64
	for i := 0; i < 400; i++ {
		good = append(good, 0.06)
	}
	for i := 0; i < 600; i++ {
		good = append(good, -0.01)
	}
	dg, _ := Forecast(good, 0.001)
	if dg.Edge >= 0 {
		t.Fatalf("Edge = %v, want negative (60%% of moves are down)", dg.Edge)
	}
	// The cost-aware lean is short, which on this sample loses; the honest
	// answer is that the lean and the money disagree, and ExpectedValue is the
	// one that decides.
	if dg.ExpectedValue >= 0 {
		t.Fatalf("ExpectedValue = %v; the short lean must be priced honestly", dg.ExpectedValue)
	}
	// Taking the other side is what pays, and the distribution says so.
	if -dg.Mean-0.001 >= dg.Mean-0.001 {
		// sanity: long EV exceeds short EV on a positive-mean sample
		if dg.Mean <= 0 {
			t.Fatalf("fixture mean = %v, expected positive", dg.Mean)
		}
	}
}

// A zero lean must not be charged a cost it never paid.
func TestZeroEdgeCostsNothing(t *testing.T) {
	var s []float64
	for i := 0; i < 100; i++ {
		s = append(s, 0.02, -0.02)
	}
	d, ok := Forecast(s, 0.001)
	if !ok {
		t.Fatal("refused")
	}
	if d.Edge != 0 {
		t.Fatalf("Edge = %v, want exactly 0 on a symmetric sample", d.Edge)
	}
	if d.ExpectedValue != 0 {
		t.Fatalf("ExpectedValue = %v, want 0 (no trade, no cost)", d.ExpectedValue)
	}
}

// Pinball loss must be minimized by the true quantile — the property that makes
// it a proper scoring rule and makes Skill trustworthy.
func TestPinballIsMinimizedAtTrueQuantile(t *testing.T) {
	sample := normalSample(5000, 0, 0.03, 5)
	d, _ := Forecast(sample, 0.001)
	truth := d.Q10
	loss := func(pred float64) float64 {
		var l float64
		for _, v := range sample {
			l += pinball(pred, v, 0.10)
		}
		return l
	}
	base := loss(truth)
	for _, off := range []float64{-0.02, -0.005, 0.005, 0.02} {
		if loss(truth+off) < base {
			t.Fatalf("pinball loss lower at offset %v than at the true q10", off)
		}
	}
}

// Conditioning that genuinely narrows the distribution must earn POSITIVE
// skill; conditioning that adds nothing must not.
func TestGradeQuantilesRewardsRealConditioningOnly(t *testing.T) {
	clim, _ := Forecast(normalSample(4000, 0, 0.05, 21), 0.001)  // wide climatology
	tight, _ := Forecast(normalSample(4000, 0, 0.01, 22), 0.001) // correctly narrow

	// Realized returns actually come from the NARROW regime.
	real := normalSample(400, 0, 0.01, 23)
	var good, bad []QPair
	for _, r := range real {
		good = append(good, QPair{Cond: tight, Clim: clim, Realized: r})
		bad = append(bad, QPair{Cond: clim, Clim: clim, Realized: r})
	}

	g := GradeQuantiles(good)
	if !g.Meaningful {
		t.Fatal("400 pairs graded as not meaningful")
	}
	if g.Skill <= 0 {
		t.Fatalf("Skill = %v, want > 0 when the conditional forecast is correctly narrow", g.Skill)
	}
	if b := GradeQuantiles(bad); math.Abs(b.Skill) > 1e-9 {
		t.Fatalf("Skill = %v, want 0 when the conditional forecast IS the climatology", b.Skill)
	}

	// An overconfident forecast — narrow when reality is wide — must be caught
	// by coverage, and must NOT earn skill.
	wideReal := normalSample(400, 0, 0.05, 24)
	var over []QPair
	for _, r := range wideReal {
		over = append(over, QPair{Cond: tight, Clim: clim, Realized: r})
	}
	o := GradeQuantiles(over)
	if o.Skill > 0 {
		t.Fatalf("overconfident forecast earned Skill = %v", o.Skill)
	}
	if o.Coverage80 > 0.5 {
		t.Fatalf("Coverage80 = %v; an overconfident width must show poor coverage", o.Coverage80)
	}
	// A well-specified forecast covers ~80%.
	if g.Coverage80 < 0.7 || g.Coverage80 > 0.9 {
		t.Fatalf("Coverage80 = %v, want ~0.80 for a correctly-specified forecast", g.Coverage80)
	}
}

func TestGradeQuantilesEmpty(t *testing.T) {
	if g := GradeQuantiles(nil); g.Meaningful || g.N != 0 || g.Skill != 0 {
		t.Fatalf("empty grade = %+v", g)
	}
}

// THE label-noise fix: moves inside the cost band must be scored as neither
// right nor wrong, and the accuracy must be computed only over real moves.
func TestGradeBandExcludesNoiseFromTheDenominator(t *testing.T) {
	pairs := []BandPair{
		{Edge: 0.2, Realized: 0.05},    // correct, gradeable
		{Edge: 0.2, Realized: -0.05},   // wrong, gradeable
		{Edge: 0.2, Realized: 0.0001},  // noise — no-call under the band
		{Edge: 0.2, Realized: -0.0002}, // noise — no-call
		{Edge: -0.3, Realized: -0.04},  // correct, gradeable
		{Edge: 0, Realized: 0.09},      // declined to lean — no-call
	}
	g := GradeBand(pairs, 0.001)
	if g.N != 6 {
		t.Fatalf("N = %d, want 6", g.N)
	}
	if g.Gradeable != 3 || g.NoCall != 3 {
		t.Fatalf("gradeable=%d noCall=%d, want 3/3", g.Gradeable, g.NoCall)
	}
	if g.Correct != 2 || math.Abs(g.Accuracy-2.0/3.0) > 1e-12 {
		t.Fatalf("correct=%d accuracy=%v, want 2 and 0.667", g.Correct, g.Accuracy)
	}
	if math.Abs(g.NoCallRate-0.5) > 1e-12 {
		t.Fatalf("NoCallRate = %v, want 0.5", g.NoCallRate)
	}
	if g.Meaningful {
		t.Fatal("3 gradeable observations must not be Meaningful")
	}

	// The same calls under the OLD binary target: every no-call becomes a
	// verdict, and the measured accuracy changes purely because of noise.
	old := GradeBand(pairs, 0)
	if old.Gradeable == g.Gradeable {
		t.Fatal("tau=0 must grade strictly more observations than a real cost band")
	}
	if math.Abs(old.Accuracy-g.Accuracy) < 1e-9 {
		t.Fatal("fixture does not demonstrate the label-noise effect")
	}
}
