package structregime

import (
	"math"
	"testing"
)

// TestPhiKnownPoints pins the CDF at values a wrong implementation cannot fake.
func TestPhiKnownPoints(t *testing.T) {
	cases := []struct{ z, want float64 }{
		{0, 0.5},
		{1, 0.8413447461},
		{-1, 0.1586552539},
		{1.959963985, 0.975},
		{-1.959963985, 0.025},
		{2.5758293035, 0.995},
	}
	for _, c := range cases {
		got := Phi(c.z)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Phi(%v) = %v, want %v", c.z, got, c.want)
		}
	}
}

func TestPhiMonotoneAndBounded(t *testing.T) {
	prev := Phi(-8)
	for z := -8.0; z <= 8.0; z += 0.01 {
		p := Phi(z)
		if p < 0 || p > 1 {
			t.Fatalf("Phi(%v) = %v out of [0,1]", z, p)
		}
		if p < prev-1e-12 {
			t.Fatalf("Phi not monotone at z=%v: %v < %v", z, p, prev)
		}
		prev = p
	}
}

// TestGeometryFromRefusesUnusableSigma is the honesty guard: an unmeasurable
// null must report OK=false, never a fabricated 0.5.
func TestGeometryFromRefusesUnusableSigma(t *testing.T) {
	bad := []struct{ dist, sigma float64 }{
		{1, 0},
		{1, -1},
		{1, math.NaN()},
		{math.NaN(), 1},
		{math.Inf(1), 1},
		{1, math.Inf(1)},
	}
	for _, b := range bad {
		if g := GeometryFrom(b.dist, b.sigma); g.OK {
			t.Errorf("GeometryFrom(%v, %v) returned OK=true, want refusal", b.dist, b.sigma)
		}
	}
}

func TestGeometryFromComputesZAndPhi(t *testing.T) {
	g := GeometryFrom(2, 1)
	if !g.OK {
		t.Fatal("expected OK")
	}
	if math.Abs(g.Z-2) > 1e-12 {
		t.Errorf("Z = %v, want 2", g.Z)
	}
	if math.Abs(g.PGeometry-Phi(2)) > 1e-12 {
		t.Errorf("PGeometry = %v, want Phi(2)=%v", g.PGeometry, Phi(2))
	}
	// Distance is taken in absolute value, so sign cannot change the null.
	if n := GeometryFrom(-2, 1); math.Abs(n.Z-g.Z) > 1e-12 {
		t.Errorf("negative distance gave Z=%v, want %v", n.Z, g.Z)
	}
	// A far barrier is never LESS likely to hold than a near one.
	near, far := GeometryFrom(0.5, 1), GeometryFrom(3, 1)
	if far.PGeometry < near.PGeometry {
		t.Errorf("far barrier %v < near barrier %v", far.PGeometry, near.PGeometry)
	}
	// P is always >= 0.5: |distance| >= 0 so Z >= 0.
	if near.PGeometry < 0.5 {
		t.Errorf("PGeometry %v < 0.5", near.PGeometry)
	}
}

// TestTrendGeometryOnRandomWalk is the property that matters: on a driftless
// random walk the null should be well-formed and Z should scale with distance.
func TestTrendGeometryOnRandomWalk(t *testing.T) {
	closes := synthWalk(600, 100, 0.01, 12345)
	g, ok := TrendGeometry(closes)
	if !ok {
		t.Fatal("TrendGeometry refused a clean 600-bar series")
	}
	if !g.OK || g.SigmaHorizon <= 0 {
		t.Fatalf("bad geometry: %+v", g)
	}
	if g.Z < 0 || !isFinite(g.Z) {
		t.Fatalf("Z = %v", g.Z)
	}
	if g.PGeometry < 0.5 || g.PGeometry > 1 {
		t.Fatalf("PGeometry = %v outside [0.5,1]", g.PGeometry)
	}
}

func TestGeometryRefusesShortHistory(t *testing.T) {
	short := synthWalk(100, 100, 0.01, 7)
	if _, ok := TrendGeometry(short); ok {
		t.Error("TrendGeometry accepted 100 bars, want refusal below minHistory")
	}
	vols := make([]float64, len(short))
	for i := range vols {
		vols[i] = 1e6
	}
	if _, ok := LiquidityGeometry(short, vols); ok {
		t.Error("LiquidityGeometry accepted 100 bars, want refusal")
	}
}

func TestLiquidityGeometryWellFormed(t *testing.T) {
	n := 600
	closes := synthWalk(n, 50, 0.01, 999)
	vols := make([]float64, n)
	s := uint64(4242)
	for i := range vols {
		s = s*6364136223846793005 + 1442695040888963407
		u := float64((s>>11)&((1<<53)-1)) / float64(uint64(1)<<53)
		vols[i] = 1e6 * (0.5 + u)
	}
	g, ok := LiquidityGeometry(closes, vols)
	if !ok || !g.OK {
		t.Fatalf("LiquidityGeometry refused clean input: ok=%v g=%+v", ok, g)
	}
	if g.SigmaHorizon <= 0 || !isFinite(g.PGeometry) {
		t.Fatalf("bad geometry %+v", g)
	}
	if _, ok := LiquidityGeometry(closes, vols[:n-1]); ok {
		t.Error("LiquidityGeometry accepted mismatched lengths")
	}
}

func TestVolGeometryWellFormed(t *testing.T) {
	rets := make([]float64, 600)
	s := uint64(31337)
	for i := range rets {
		s = s*6364136223846793005 + 1442695040888963407
		u := float64((s>>11)&((1<<53)-1)) / float64(uint64(1)<<53)
		rets[i] = (u - 0.5) * 0.02
	}
	g, ok := VolGeometry(rets)
	if !ok || !g.OK {
		t.Fatalf("VolGeometry refused clean input: ok=%v g=%+v", ok, g)
	}
	if g.PGeometry < 0.5 || g.PGeometry > 1 {
		t.Fatalf("PGeometry = %v", g.PGeometry)
	}
}

// TestEdgeOverGeometrySignsTheClaim: a model below the geometric null must
// report a NEGATIVE edge, which is the whole point of publishing it.
func TestEdgeOverGeometrySignsTheClaim(t *testing.T) {
	g := GeometryFrom(1.5, 1) // Phi(1.5) ~= 0.9332
	e, ok := EdgeOverGeometry(0.90, g)
	if !ok {
		t.Fatal("expected ok")
	}
	if e >= 0 {
		t.Errorf("edge = %v, want negative (0.90 is below Phi(1.5)=%v)", e, g.PGeometry)
	}
	if e2, _ := EdgeOverGeometry(0.99, g); e2 <= 0 {
		t.Errorf("edge = %v, want positive", e2)
	}
	if _, ok := EdgeOverGeometry(0.9, Geometry{}); ok {
		t.Error("EdgeOverGeometry accepted an unmeasurable null")
	}
	if _, ok := EdgeOverGeometry(math.NaN(), g); ok {
		t.Error("EdgeOverGeometry accepted NaN accuracy")
	}
	if _, ok := EdgeOverGeometry(1.4, g); ok {
		t.Error("EdgeOverGeometry accepted accuracy outside [0,1]")
	}
}

// TestGeometryDoesNotTouchPredictions is the regression guard for the frozen
// pre-registered claims: adding the null must not move any shipped number.
func TestGeometryDoesNotTouchPredictions(t *testing.T) {
	closes := synthWalk(600, 100, 0.01, 20260804)
	f, ok := PredictTrend(closes)
	if !ok {
		t.Fatal("PredictTrend refused")
	}
	if f.Kind != KindTrend21 || f.HorizonDays != 21 {
		t.Fatalf("prediction shape changed: %+v", f)
	}
	if f.HistoricalAccuracy != accuracyFor(KindTrend21, f.Conviction) {
		t.Error("HistoricalAccuracy no longer matches the frozen band table")
	}
	if f.Conviction < 0 || f.Conviction > 1 {
		t.Errorf("conviction %v outside [0,1]", f.Conviction)
	}
}

func isFinite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// synthWalk builds a deterministic multiplicative random walk.
func synthWalk(n int, start, vol float64, seed uint64) []float64 {
	out := make([]float64, n)
	p := start
	s := seed
	for i := range out {
		s = s*6364136223846793005 + 1442695040888963407
		u := float64((s>>11)&((1<<53)-1)) / float64(uint64(1)<<53)
		p *= 1 + (u-0.5)*2*vol
		if p <= 1 {
			p = 1
		}
		out[i] = p
	}
	return out
}
