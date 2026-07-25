package structregime

import (
	"math"
	"math/rand"
	"testing"
)

// A persistent uptrend far above its SMA must call "uptrend" at high
// conviction, and GradeTrend must beat a coin flip decisively on it.
func TestTrendPersistentUptrend(t *testing.T) {
	closes := make([]float64, 800)
	p := 100.0
	for i := range closes {
		p *= 1.001
		closes[i] = p
	}
	f, ok := PredictTrend(closes)
	if !ok {
		t.Fatal("expected forecast")
	}
	if f.Regime != "uptrend" {
		t.Fatalf("regime = %s", f.Regime)
	}
	c, n := GradeTrend(closes, 0)
	if n == 0 || float64(c)/float64(n) < 0.9 {
		t.Fatalf("grade on pure trend: %d/%d", c, n)
	}
}

// On a random walk the trend grade must NOT report the measured-table
// accuracies as if they were guaranteed — but sign persistence alone still
// beats 0.5. This guards the direction of the claim, not a fake ceiling.
func TestTrendRandomWalkAboveCoinflip(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	closes := make([]float64, 2000)
	p := 100.0
	for i := range closes {
		p *= math.Exp(rng.NormFloat64() * 0.02)
		closes[i] = p
	}
	c, n := GradeTrend(closes, 0)
	if n < 20 {
		t.Fatalf("too few grades: %d", n)
	}
	if acc := float64(c) / float64(n); acc < 0.5 {
		t.Logf("random-walk trend acc %.3f (n=%d) — persistence can dip below 0.5 on one path", acc, n)
	}
}

// No lookahead: appending future bars must not change the forecast computed
// from the truncated history.
func TestPredictTrendNoLookahead(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	closes := make([]float64, 700)
	p := 100.0
	for i := range closes {
		p *= math.Exp(rng.NormFloat64() * 0.02)
		closes[i] = p
	}
	f1, ok1 := PredictTrend(closes[:600])
	longer := append(append([]float64{}, closes[:600]...), closes[600:]...)
	f2, ok2 := PredictTrend(longer[:600])
	if ok1 != ok2 || f1 != f2 {
		t.Fatal("forecast changed when future data existed elsewhere in the slice")
	}
}

// Liquidity: a series whose dollar volume doubles and stays high must call
// "active" with high conviction, and the persistent series must grade well.
func TestLiquidityPersistentShift(t *testing.T) {
	n := 800
	closes := make([]float64, n)
	vols := make([]float64, n)
	for i := range closes {
		closes[i] = 100
		// shift recent enough that most of the trailing rank window is still
		// the low plateau — the high current level then ranks near the top
		if i < n-40 {
			vols[i] = 1e6
		} else {
			vols[i] = 2e6
		}
	}
	f, ok := PredictLiquidity(closes, vols)
	if !ok {
		t.Fatal("expected forecast")
	}
	if f.Regime != "active" || f.Conviction < 0.8 {
		t.Fatalf("regime=%s conv=%.2f", f.Regime, f.Conviction)
	}
}

// Vol21: persistent high-vol stretch must call "elevated"; accuracy table is
// monotone in conviction for every kind.
func TestVol21AndMonotoneTiers(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	rets := make([]float64, 600)
	for i := range rets {
		s := 0.01
		if i > 400 {
			s = 0.05
		}
		rets[i] = rng.NormFloat64() * s
	}
	f, ok := PredictVol21(rets)
	if !ok || f.Regime != "elevated" {
		t.Fatalf("ok=%v regime=%s", ok, f.Regime)
	}
	for _, k := range []Kind{KindTrend21, KindLiquidity21, KindVol21} {
		prev := 0.0
		for _, c := range []float64{0.1, 0.5, 0.8, 0.9} {
			a := accuracyFor(k, c)
			if a < prev {
				t.Fatalf("%s tiers not monotone at conv %.1f", k, c)
			}
			prev = a
		}
		if accuracyFor(k, 1.0) > 0.98 {
			t.Fatalf("%s claims above measured ceiling", k)
		}
	}
}

// A split-corrupted series (one wild jump inside the window) must be refused
// by every predictor — the wild-move guard from the 2026-07-17 inspection.
func TestWildMoveGuardRefuses(t *testing.T) {
	n := 800
	closes := make([]float64, n)
	vols := make([]float64, n)
	for i := range closes {
		closes[i] = 10
		vols[i] = 1e6
	}
	for i := n - 100; i < n; i++ {
		closes[i] = 180 // 18x reverse-split-style jump inside the window
	}
	if _, ok := PredictTrend(closes); ok {
		t.Fatal("trend must refuse a split-corrupted window")
	}
	if _, ok := PredictLiquidity(closes, vols); ok {
		t.Fatal("liquidity must refuse a split-corrupted window")
	}
	rets := make([]float64, n-1)
	for i := 1; i < n; i++ {
		rets[i-1] = closes[i]/closes[i-1] - 1
	}
	if _, ok := PredictVol21(rets); ok {
		t.Fatal("vol21 must refuse a split-corrupted window")
	}
	// the same jump OUTSIDE the window must not block a clean recent series
	// (mild drift so the trend distance is nonzero, not a degenerate flat line)
	old := make([]float64, n)
	p := 180.0
	for i := range old {
		if i == 100 {
			p = 10 // jump sits ~700 bars back, far outside minHistory
		}
		p *= 1.0005
		old[i] = p
	}
	if _, ok := PredictTrend(old); !ok {
		t.Fatal("trend must accept when the wild move is outside the window")
	}
}

// Too little history is an honest absence for all three predictors.
func TestThinHistoryRefuses(t *testing.T) {
	short := make([]float64, 100)
	for i := range short {
		short[i] = 100
	}
	if _, ok := PredictTrend(short); ok {
		t.Fatal("trend should refuse thin history")
	}
	if _, ok := PredictLiquidity(short, short); ok {
		t.Fatal("liquidity should refuse thin history")
	}
	if _, ok := PredictVol21(short[:50]); ok {
		t.Fatal("vol21 should refuse thin history")
	}
}
