package ensemble

import (
	"math"
	"math/rand"
	"testing"
)

// ptr is a small helper for building *float64 fields in table rows.
func ptr(v float64) *float64 { return &v }

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestRawProbability(t *testing.T) {
	tests := []struct {
		name      string
		comp      Components
		wantProb  float64
		wantN     int
		tolerance float64
	}{
		{
			name:      "pressure only, neutral score",
			comp:      Components{PressureScore: 0},
			wantProb:  0.5, // (0+1)/2
			wantN:     1,
			tolerance: 1e-9,
		},
		{
			name:      "pressure only, max bullish",
			comp:      Components{PressureScore: 1},
			wantProb:  1.0, // (1+1)/2
			wantN:     1,
			tolerance: 1e-9,
		},
		{
			name:      "pressure only, max bearish",
			comp:      Components{PressureScore: -1},
			wantProb:  0.0, // (-1+1)/2
			wantN:     1,
			tolerance: 1e-9,
		},
		{
			name:      "pressure + expectancy averaged",
			comp:      Components{PressureScore: 0, ExpectancyHitRate: ptr(0.8)},
			wantProb:  0.65, // mean(0.5, 0.8)
			wantN:     2,
			tolerance: 1e-9,
		},
		{
			name: "edgeless forecast is DROPPED (lift <= 0)",
			comp: Components{
				PressureScore:     0,
				ExpectancyHitRate: ptr(0.8),
				ForecastProb:      ptr(0.99), // extreme, but must be ignored
				ForecastLift:      ptr(0.0),  // no edge -> drop
			},
			wantProb:  0.65, // mean(0.5, 0.8) — forecast excluded
			wantN:     2,
			tolerance: 1e-9,
		},
		{
			name: "negative-lift forecast is DROPPED",
			comp: Components{
				PressureScore:     0,
				ExpectancyHitRate: ptr(0.8),
				ForecastProb:      ptr(0.1),
				ForecastLift:      ptr(-0.05), // negative edge -> drop
			},
			wantProb:  0.65,
			wantN:     2,
			tolerance: 1e-9,
		},
		{
			name: "forecast with positive lift is INCLUDED",
			comp: Components{
				PressureScore:     0,
				ExpectancyHitRate: ptr(0.8),
				ForecastProb:      ptr(0.9),
				ForecastLift:      ptr(0.03), // real edge -> include
			},
			wantProb:  (0.5 + 0.8 + 0.9) / 3.0,
			wantN:     3,
			tolerance: 1e-9,
		},
		{
			name: "forecast prob present but lift nil -> dropped",
			comp: Components{
				PressureScore: 0.2,
				ForecastProb:  ptr(0.95),
				ForecastLift:  nil,
			},
			wantProb:  clamp01((0.2 + 1) / 2), // 0.6, forecast dropped
			wantN:     1,
			tolerance: 1e-9,
		},
		{
			name: "forecast lift present but prob nil -> dropped",
			comp: Components{
				PressureScore: 0.2,
				ForecastProb:  nil,
				ForecastLift:  ptr(0.5),
			},
			wantProb:  0.6,
			wantN:     1,
			tolerance: 1e-9,
		},
		{
			name: "all three contribute",
			comp: Components{
				PressureScore:     0.5, // -> 0.75
				ExpectancyHitRate: ptr(0.6),
				ForecastProb:      ptr(0.7),
				ForecastLift:      ptr(0.1),
			},
			wantProb:  (0.75 + 0.6 + 0.7) / 3.0,
			wantN:     3,
			tolerance: 1e-9,
		},
		{
			name:      "out-of-range pressure clamps",
			comp:      Components{PressureScore: 5}, // (5+1)/2=3 -> clamp 1
			wantProb:  1.0,
			wantN:     1,
			tolerance: 1e-9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProb, gotN := RawProbability(tt.comp)
			if gotN != tt.wantN {
				t.Errorf("nUsed = %d, want %d", gotN, tt.wantN)
			}
			if !approx(gotProb, tt.wantProb, tt.tolerance) {
				t.Errorf("prob = %v, want %v (tol %v)", gotProb, tt.wantProb, tt.tolerance)
			}
		})
	}
}

// TestRawProbabilityNeutralWhenEmpty exercises the defensive n==0 branch. It is
// unreachable via the normal struct path (pressure always counts), so we call
// the internal averaging contract directly by asserting the documented neutral
// prior behaviour holds for a zeroed component (pressure 0 -> 0.5).
func TestRawProbabilityNeutralPrior(t *testing.T) {
	// A fully-zeroed Components still contributes pressure (0 -> 0.5).
	prob, n := RawProbability(Components{})
	if n != 1 {
		t.Fatalf("nUsed = %d, want 1 (pressure always present)", n)
	}
	if !approx(prob, 0.5, 1e-9) {
		t.Fatalf("prob = %v, want 0.5 neutral", prob)
	}
}

func TestCalibrationCurve(t *testing.T) {
	tests := []struct {
		name     string
		pairs    []Pair
		bins     int
		wantLen  int
		checkBin func(t *testing.T, bins []Bin)
	}{
		{
			name:    "empty history returns empty bins of full width",
			pairs:   nil,
			bins:    5,
			wantLen: 5,
			checkBin: func(t *testing.T, bins []Bin) {
				for i, b := range bins {
					if b.N != 0 {
						t.Errorf("bin %d N=%d, want 0", i, b.N)
					}
				}
				if bins[0].Lo != 0 || !approx(bins[len(bins)-1].Hi, 1.0, 1e-9) {
					t.Errorf("edges span wrong: lo=%v hi=%v", bins[0].Lo, bins[len(bins)-1].Hi)
				}
			},
		},
		{
			name: "predictions land in correct bins",
			pairs: []Pair{
				{Pred: 0.05, Actual: 0}, // bin 0
				{Pred: 0.15, Actual: 1}, // bin 1
				{Pred: 0.95, Actual: 1}, // bin 9
				{Pred: 1.0, Actual: 1},  // top edge -> bin 9
			},
			bins:    10,
			wantLen: 10,
			checkBin: func(t *testing.T, bins []Bin) {
				if bins[0].N != 1 || bins[1].N != 1 || bins[9].N != 2 {
					t.Errorf("bin counts wrong: b0=%d b1=%d b9=%d", bins[0].N, bins[1].N, bins[9].N)
				}
				if !approx(bins[9].MeanActual, 1.0, 1e-9) {
					t.Errorf("bin9 meanActual=%v want 1", bins[9].MeanActual)
				}
			},
		},
		{
			name: "mean pred and actual computed per bin",
			pairs: []Pair{
				{Pred: 0.72, Actual: 1},
				{Pred: 0.78, Actual: 0},
			},
			bins:    10,
			wantLen: 10,
			checkBin: func(t *testing.T, bins []Bin) {
				// bin 7 covers [0.7,0.8)
				if bins[7].N != 2 {
					t.Fatalf("bin7 N=%d want 2", bins[7].N)
				}
				if !approx(bins[7].MeanPred, 0.75, 1e-9) {
					t.Errorf("bin7 meanPred=%v want 0.75", bins[7].MeanPred)
				}
				if !approx(bins[7].MeanActual, 0.5, 1e-9) {
					t.Errorf("bin7 meanActual=%v want 0.5", bins[7].MeanActual)
				}
			},
		},
		{
			name:    "bins < 1 coerced to default",
			pairs:   []Pair{{Pred: 0.5, Actual: 1}},
			bins:    0,
			wantLen: defaultBins,
			checkBin: func(t *testing.T, bins []Bin) {
				if len(bins) != defaultBins {
					t.Errorf("len=%d want %d", len(bins), defaultBins)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalibrationCurve(tt.pairs, tt.bins)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			tt.checkBin(t, got)
		})
	}
}

func TestBrierScore(t *testing.T) {
	tests := []struct {
		name  string
		pairs []Pair
		want  float64
	}{
		{
			name:  "empty -> 0",
			pairs: nil,
			want:  0,
		},
		{
			name:  "perfect predictions -> 0",
			pairs: []Pair{{Pred: 1, Actual: 1}, {Pred: 0, Actual: 0}},
			want:  0,
		},
		{
			name:  "constant 0.5 -> 0.25",
			pairs: []Pair{{Pred: 0.5, Actual: 1}, {Pred: 0.5, Actual: 0}},
			want:  0.25,
		},
		{
			// Hand-check: (0.9-1)^2=0.01, (0.2-0)^2=0.04, (0.6-1)^2=0.16
			// mean = (0.01+0.04+0.16)/3 = 0.21/3 = 0.07
			name: "hand-checked tiny set",
			pairs: []Pair{
				{Pred: 0.9, Actual: 1},
				{Pred: 0.2, Actual: 0},
				{Pred: 0.6, Actual: 1},
			},
			want: 0.07,
		},
		{
			name:  "worst case -> 1",
			pairs: []Pair{{Pred: 1, Actual: 0}, {Pred: 0, Actual: 1}},
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BrierScore(tt.pairs)
			if !approx(got, tt.want, 1e-9) {
				t.Errorf("BrierScore = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalibrateTooFewPairs(t *testing.T) {
	// Fewer than MinCalibrationPairs -> identity, calibrated=false.
	var pairs []Pair
	for i := 0; i < MinCalibrationPairs-1; i++ {
		pairs = append(pairs, Pair{Pred: 0.7, Actual: 1})
	}
	fn, calibrated := Calibrate(pairs)
	if calibrated {
		t.Fatalf("calibrated=true with %d pairs, want false", len(pairs))
	}
	for _, v := range []float64{0.0, 0.3, 0.7, 1.0} {
		if !approx(fn(v), v, 1e-9) {
			t.Errorf("identity map fn(%v)=%v, want %v", v, fn(v), v)
		}
	}
}

func TestCalibrateNoSpread(t *testing.T) {
	// Enough pairs but all identical predictions -> cannot fit -> identity.
	pairs := make([]Pair, MinCalibrationPairs+10)
	for i := range pairs {
		pairs[i] = Pair{Pred: 0.5, Actual: float64(i % 2)}
	}
	fn, calibrated := Calibrate(pairs)
	if calibrated {
		t.Fatalf("calibrated=true with zero prediction spread, want false")
	}
	if !approx(fn(0.5), 0.5, 1e-9) {
		t.Errorf("identity fn(0.5)=%v want 0.5", fn(0.5))
	}
}

// buildPerfectlyCalibrated makes a synthetic set where, within each prediction
// level p, exactly a fraction p of outcomes are 1 — i.e. predictions equal the
// realized frequency. Such a set should score ReliabilityScore ~1 and calibrate
// to ~identity.
func buildPerfectlyCalibrated() []Pair {
	var pairs []Pair
	levels := []float64{0.05, 0.15, 0.25, 0.35, 0.45, 0.55, 0.65, 0.75, 0.85, 0.95}
	const perLevel = 200
	for _, p := range levels {
		ups := int(math.Round(p * perLevel))
		for i := 0; i < perLevel; i++ {
			actual := 0.0
			if i < ups {
				actual = 1.0
			}
			pairs = append(pairs, Pair{Pred: p, Actual: actual})
		}
	}
	return pairs
}

func TestReliabilityScorePerfect(t *testing.T) {
	pairs := buildPerfectlyCalibrated()
	got := ReliabilityScore(pairs)
	if got < 0.98 {
		t.Errorf("ReliabilityScore = %v, want >= 0.98 for perfectly calibrated set", got)
	}
}

func TestReliabilityScoreEmpty(t *testing.T) {
	if got := ReliabilityScore(nil); !approx(got, 1.0, 1e-9) {
		t.Errorf("ReliabilityScore(nil) = %v, want 1.0 by convention", got)
	}
}

func TestReliabilityScoreMiscalibrated(t *testing.T) {
	// A set where predictions are systematically wrong: predict 0.9 but only
	// 10% actually go up, predict 0.1 but 90% go up. Reliability should be low.
	var pairs []Pair
	for i := 0; i < 100; i++ {
		up := 0.0
		if i < 10 {
			up = 1.0
		}
		pairs = append(pairs, Pair{Pred: 0.9, Actual: up}) // pred .9, actual .1
	}
	for i := 0; i < 100; i++ {
		up := 0.0
		if i < 90 {
			up = 1.0
		}
		pairs = append(pairs, Pair{Pred: 0.1, Actual: up}) // pred .1, actual .9
	}
	got := ReliabilityScore(pairs)
	// mean|pred-actual| per bin = (|.9-.1| + |.1-.9|)/2 = 0.8 -> score ~0.2
	if got > 0.3 {
		t.Errorf("ReliabilityScore = %v, want <= 0.3 for badly miscalibrated set", got)
	}
}

func TestCalibratePerfectIsIdentityish(t *testing.T) {
	pairs := buildPerfectlyCalibrated()
	fn, calibrated := Calibrate(pairs)
	if !calibrated {
		t.Fatalf("calibrated=false on a large well-formed set, want true")
	}
	// A perfectly-calibrated set should map each level near itself.
	for _, p := range []float64{0.15, 0.45, 0.75, 0.95} {
		got := fn(p)
		if !approx(got, p, 0.05) {
			t.Errorf("fn(%v)=%v, want ~%v (identity-ish)", p, got, p)
		}
	}
	// Monotonicity of the fitted map.
	prev := -1.0
	for x := 0.0; x <= 1.0; x += 0.05 {
		v := fn(x)
		if v < prev-1e-9 {
			t.Errorf("map not monotone: fn(%v)=%v < prev %v", x, v, prev)
		}
		prev = v
	}
}

func TestCalibrateOverconfidentPullsTowardBaseRate(t *testing.T) {
	// Overconfident model: it emits extreme probabilities (near 0 / near 1),
	// but realized frequencies are much closer to the 0.5 base rate. The fitted
	// calibration map must pull extremes toward the middle.
	rng := rand.New(rand.NewSource(42))
	var pairs []Pair
	const n = 4000
	for i := 0; i < n; i++ {
		// Raw prediction is extreme: either ~0.05 or ~0.95.
		var pred, trueP float64
		if i%2 == 0 {
			pred = 0.95
			trueP = 0.65 // reality much less certain than claimed
		} else {
			pred = 0.05
			trueP = 0.35
		}
		actual := 0.0
		if rng.Float64() < trueP {
			actual = 1.0
		}
		pairs = append(pairs, Pair{Pred: pred, Actual: actual})
	}

	fn, calibrated := Calibrate(pairs)
	if !calibrated {
		t.Fatalf("calibrated=false, want true for %d pairs", len(pairs))
	}

	// The high extreme 0.95 should map DOWN toward its realized ~0.65,
	// and the low extreme 0.05 should map UP toward ~0.35 — both pulled
	// toward the base rate relative to the raw prediction.
	high := fn(0.95)
	low := fn(0.05)
	if high >= 0.95 {
		t.Errorf("fn(0.95)=%v, expected pulled DOWN below 0.95", high)
	}
	if !approx(high, 0.65, 0.06) {
		t.Errorf("fn(0.95)=%v, expected ~0.65 (realized freq)", high)
	}
	if low <= 0.05 {
		t.Errorf("fn(0.05)=%v, expected pulled UP above 0.05", low)
	}
	if !approx(low, 0.35, 0.06) {
		t.Errorf("fn(0.05)=%v, expected ~0.35 (realized freq)", low)
	}
	// The calibrated Brier score must be no worse than the raw one — remapping
	// to realized frequencies cannot hurt on the fitting set.
	rawBrier := BrierScore(pairs)
	calPairs := make([]Pair, len(pairs))
	for i, p := range pairs {
		calPairs[i] = Pair{Pred: fn(p.Pred), Actual: p.Actual}
	}
	calBrier := BrierScore(calPairs)
	if calBrier > rawBrier+1e-9 {
		t.Errorf("calibrated Brier %v worse than raw %v", calBrier, rawBrier)
	}
}

func TestCalibrateMonotoneMapClampsOutsideRange(t *testing.T) {
	// Build a set whose predictions only span [0.3, 0.7]; the map should clamp
	// queries outside that observed range to the endpoint values.
	rng := rand.New(rand.NewSource(7))
	var pairs []Pair
	for i := 0; i < 400; i++ {
		pred := 0.3 + 0.4*rng.Float64()
		actual := 0.0
		if rng.Float64() < pred {
			actual = 1.0
		}
		pairs = append(pairs, Pair{Pred: pred, Actual: actual})
	}
	fn, calibrated := Calibrate(pairs)
	if !calibrated {
		t.Fatalf("calibrated=false, want true")
	}
	// Outside the observed range, the map holds the endpoint (no extrapolation).
	loEnd := fn(0.30)
	if !approx(fn(0.0), loEnd, 1e-9) {
		t.Errorf("fn(0.0)=%v should clamp to low endpoint %v", fn(0.0), loEnd)
	}
	hiEnd := fn(0.70)
	if !approx(fn(1.0), hiEnd, 1e-9) {
		t.Errorf("fn(1.0)=%v should clamp to high endpoint %v", fn(1.0), hiEnd)
	}
}

func TestInterpolate(t *testing.T) {
	kx := []float64{0.2, 0.5, 0.8}
	ky := []float64{0.1, 0.4, 0.9}
	tests := []struct {
		x, want float64
	}{
		{0.0, 0.1},   // clamp low
		{0.2, 0.1},   // at knot
		{0.35, 0.25}, // midpoint of first segment
		{0.5, 0.4},   // at knot
		{0.65, 0.65}, // midpoint of second segment
		{0.8, 0.9},   // at knot
		{1.0, 0.9},   // clamp high
	}
	for _, tt := range tests {
		got := interpolate(kx, ky, tt.x)
		if !approx(got, tt.want, 1e-9) {
			t.Errorf("interpolate(%v) = %v, want %v", tt.x, got, tt.want)
		}
	}
	// Single knot degenerate case.
	if got := interpolate([]float64{0.5}, []float64{0.3}, 0.9); !approx(got, 0.3, 1e-9) {
		t.Errorf("single-knot interpolate = %v, want 0.3", got)
	}
}

func TestPoolAdjacentViolatorsMonotone(t *testing.T) {
	// Non-monotone per-level realized means must become non-decreasing after
	// weighted PAV. One level per distinct prediction, weight = pair count.
	levels := []levelStat{
		{x: 0.1, mean: 1, weight: 1},
		{x: 0.2, mean: 0, weight: 1},
		{x: 0.3, mean: 0, weight: 1},
		{x: 0.4, mean: 1, weight: 1},
		{x: 0.5, mean: 1, weight: 1},
	}
	kx, ky := poolAdjacentViolators(levels)
	if len(kx) != len(levels) || len(ky) != len(levels) {
		t.Fatalf("knot counts: kx=%d ky=%d, want %d", len(kx), len(ky), len(levels))
	}
	prev := -1.0
	for i, y := range ky {
		if y < prev-1e-9 {
			t.Errorf("PAV output not monotone at %d: %v < %v", i, y, prev)
		}
		prev = y
	}
	// The leading violation [1,0,0] pools to mean 1/3 over the first 3 levels.
	for i := 0; i < 3; i++ {
		if !approx(ky[i], 1.0/3.0, 1e-9) {
			t.Errorf("ky[%d]=%v, want 1/3 after pooling", i, ky[i])
		}
	}
	// The last two levels (means 1,1) stay at 1.
	if !approx(ky[3], 1.0, 1e-9) || !approx(ky[4], 1.0, 1e-9) {
		t.Errorf("ky[3:5]=%v,%v want 1,1", ky[3], ky[4])
	}
}

func TestAggregateByPred(t *testing.T) {
	// Pairs sorted by Pred; distinct levels collapse with correct means/weights.
	sorted := []Pair{
		{Pred: 0.2, Actual: 1},
		{Pred: 0.2, Actual: 0},
		{Pred: 0.2, Actual: 1},
		{Pred: 0.8, Actual: 0},
	}
	levels := aggregateByPred(sorted)
	if len(levels) != 2 {
		t.Fatalf("levels=%d want 2", len(levels))
	}
	if levels[0].x != 0.2 || levels[0].weight != 3 || !approx(levels[0].mean, 2.0/3.0, 1e-9) {
		t.Errorf("level0=%+v want x=0.2 w=3 mean=2/3", levels[0])
	}
	if levels[1].x != 0.8 || levels[1].weight != 1 || !approx(levels[1].mean, 0, 1e-9) {
		t.Errorf("level1=%+v want x=0.8 w=1 mean=0", levels[1])
	}
}

func TestClamp01(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {2, 1},
	}
	for _, c := range cases {
		if got := clamp01(c.in); got != c.want {
			t.Errorf("clamp01(%v)=%v want %v", c.in, got, c.want)
		}
	}
}
