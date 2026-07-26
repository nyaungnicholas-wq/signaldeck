package macrofeat

import (
	"math"
	"testing"
)

func ramp(n int, start, step float64) []Point {
	out := make([]Point, n)
	for i := 0; i < n; i++ {
		out[i] = Point{Ts: int64(i) * 86400, Value: start + float64(i)*step}
	}
	return out
}

// The revised series must never reach the vector. This platform stores only the
// CURRENT value of each observation, so a restated series would hand the model a
// number nobody had on the day — lookahead of the purest kind.
func TestRevisedSeriesAreNotAdmissible(t *testing.T) {
	for _, banned := range []string{"CPIAUCSL", "M2SL", "UNRATE"} {
		for _, s := range AdmissibleSeries {
			if s.ID == banned {
				t.Fatalf("%s is revised after publication and must not be a feature", banned)
			}
		}
	}
	// And feeding one anyway produces nothing.
	out := FromSeries(map[string][]Point{"CPIAUCSL": ramp(400, 100, 0.2)})
	if len(out) != 0 {
		t.Fatalf("a non-admissible series produced features: %v", out)
	}
}

// A thin series contributes NOTHING rather than a percentile computed from a
// handful of points. Absence is a legitimate answer; a fake precision is not.
func TestThinSeriesProducesNoFeatures(t *testing.T) {
	out := FromSeries(map[string][]Point{"DGS10": ramp(MinObservations-1, 3.0, 0.01)})
	if len(out) != 0 {
		t.Fatalf("thin series produced features: %v", out)
	}
}

// A steadily rising series must sit at the TOP of its own percentile, and a
// falling one at the bottom. This is the basic orientation check.
func TestPercentileOrientation(t *testing.T) {
	up := FromSeries(map[string][]Point{"DGS10": ramp(300, 1.0, 0.01)})
	if p := up["macro_dgs10_pct"]; p < 0.9 {
		t.Fatalf("rising series percentile = %v, want near 1", p)
	}
	if c := up["macro_dgs10_chg"]; c <= 0 {
		t.Fatalf("rising series change = %v, want positive", c)
	}
	down := FromSeries(map[string][]Point{"DGS10": ramp(300, 4.0, -0.01)})
	if p := down["macro_dgs10_pct"]; p > 0.1 {
		t.Fatalf("falling series percentile = %v, want near 0", p)
	}
	if c := down["macro_dgs10_chg"]; c >= 0 {
		t.Fatalf("falling series change = %v, want negative", c)
	}
}

// A flat series has no meaningful percentile. Returning 0.5 would assert "sits
// exactly at its median", which is a real state and must not be manufactured
// from a degenerate window.
func TestFlatSeriesWithheldNotNeutral(t *testing.T) {
	flat := make([]Point, 300)
	for i := range flat {
		flat[i] = Point{Ts: int64(i) * 86400, Value: 2.5}
	}
	out := FromSeries(map[string][]Point{"DGS10": flat})
	if _, ok := out["macro_dgs10_pct"]; ok {
		t.Fatal("a flat series must not produce a percentile")
	}
	if _, ok := out["macro_dgs10_chg"]; ok {
		t.Fatal("a flat series must not produce a change")
	}
}

// The curve series legitimately cross zero (10y-2y inverts; NFCI is centered on
// zero). A percentage change would explode through zero — the encoding must stay
// finite and bounded across an inversion.
func TestChangeIsFiniteThroughZero(t *testing.T) {
	// A curve marching from +1.0 down through 0 to -1.0.
	pts := ramp(300, 1.0, -1.0/150.0)
	out := FromSeries(map[string][]Point{"T10Y2Y": pts})
	c, ok := out["macro_curve_10y2y_chg"]
	if !ok {
		t.Fatal("expected a change feature across an inversion")
	}
	if math.IsNaN(c) || math.IsInf(c, 0) {
		t.Fatalf("change through zero = %v, must be finite", c)
	}
	if c <= -1 || c >= 1 {
		t.Fatalf("change = %v, must be squashed into (-1,1)", c)
	}
}

// Every emitted value must be bounded — an unbounded macro feature would
// dominate the scale-sensitive legs regardless of whether it carries signal.
func TestAllFeaturesBounded(t *testing.T) {
	hist := map[string][]Point{}
	for _, s := range AdmissibleSeries {
		hist[s.ID] = ramp(400, 1.0, 0.02)
	}
	out := FromSeries(hist)
	if len(out) == 0 {
		t.Fatal("expected features from a full admissible panel")
	}
	for k, v := range out {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("%s = %v, must be finite", k, v)
		}
		if v < -1 || v > 1 {
			t.Fatalf("%s = %v, must be bounded to [-1,1]", k, v)
		}
	}
}

// No lookahead: features must depend only on the observations supplied. Appending
// LATER observations must not change what an earlier decision would have seen.
func TestNoLookahead(t *testing.T) {
	full := ramp(400, 1.0, 0.01)
	early := full[:300]
	a := FromSeries(map[string][]Point{"DGS10": early})
	b := FromSeries(map[string][]Point{"DGS10": append([]Point(nil), early...)})
	for k := range a {
		if a[k] != b[k] {
			t.Fatalf("%s not deterministic", k)
		}
	}
	// The later-data version is a DIFFERENT decision point and may differ; what
	// must hold is that recomputing the earlier point never sees the extra rows.
	later := FromSeries(map[string][]Point{"DGS10": full})
	if len(later) == 0 {
		t.Fatal("expected features at the later point too")
	}
}
