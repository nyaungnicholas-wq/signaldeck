package structregime

import (
	"math"
	"testing"
)

// The crypto tier tables must be monotone non-decreasing in conviction and
// ceilinged at the measured top tier — a low-conviction call must never quote
// a high-tier number and no tier may exceed its own measurement.
func TestCryptoAccuracyTiersMonotoneAndCeilinged(t *testing.T) {
	kinds := []struct {
		kind    Kind
		ceiling float64
		low     float64 // the all-decisions number reported below conv 0.5
	}{
		{KindTrendCrypto21, 0.985, 0.934},
		{KindLiquidityCrypto21, 0.964, 0.795},
	}
	for _, k := range kinds {
		prev := 0.0
		for _, conv := range []float64{0, 0.1, 0.49, 0.5, 0.79, 0.8, 0.89, 0.9, 1} {
			acc := cryptoAccuracyFor(k.kind, conv)
			if acc < prev {
				t.Fatalf("%s: accuracy not monotone at conv %.2f (%.3f < %.3f)", k.kind, conv, acc, prev)
			}
			if acc > k.ceiling {
				t.Fatalf("%s: accuracy %.3f at conv %.2f exceeds the measured ceiling %.3f", k.kind, acc, conv, k.ceiling)
			}
			prev = acc
		}
		if got := cryptoAccuracyFor(k.kind, 0.2); got != k.low {
			t.Fatalf("%s: conv<0.5 must report the all-decisions number %.3f, got %.3f", k.kind, k.low, got)
		}
		if got := cryptoAccuracyFor(k.kind, 0.95); got != k.ceiling {
			t.Fatalf("%s: top tier must report %.3f, got %.3f", k.kind, k.ceiling, got)
		}
	}
	if got := cryptoAccuracyFor(Kind("nope"), 0.9); got != 0.5 {
		t.Fatalf("unknown kind must fall back to 0.5, got %.3f", got)
	}
}

// The crypto predictors must reuse the exact stock arithmetic (same regime,
// conviction and refusals) while stamping the crypto kind + crypto table.
func TestCryptoPredictorsMirrorStockArithmetic(t *testing.T) {
	closes := make([]float64, 300)
	vols := make([]float64, 300)
	for i := range closes {
		closes[i] = 100 + float64(i)*0.5 + 3*math.Sin(float64(i)/7)
		vols[i] = 1e6 + 1e4*float64(i%50)
	}
	base, ok := PredictTrend(closes)
	if !ok {
		t.Fatal("stock trend predictor refused the fixture")
	}
	cf, ok := PredictTrendCrypto(closes)
	if !ok {
		t.Fatal("crypto trend predictor refused the fixture")
	}
	if cf.Kind != KindTrendCrypto21 {
		t.Fatalf("kind = %s, want %s", cf.Kind, KindTrendCrypto21)
	}
	if cf.Regime != base.Regime || cf.Conviction != base.Conviction || cf.HorizonDays != base.HorizonDays {
		t.Fatalf("crypto trend diverged from the shared arithmetic: %+v vs %+v", cf, base)
	}
	if cf.HistoricalAccuracy != cryptoAccuracyFor(KindTrendCrypto21, cf.Conviction) {
		t.Fatalf("crypto trend must use the crypto table, got %.3f", cf.HistoricalAccuracy)
	}

	lbase, ok := PredictLiquidity(closes, vols)
	if !ok {
		t.Fatal("stock liquidity predictor refused the fixture")
	}
	lc, ok := PredictLiquidityCrypto(closes, vols)
	if !ok {
		t.Fatal("crypto liquidity predictor refused the fixture")
	}
	if lc.Kind != KindLiquidityCrypto21 {
		t.Fatalf("kind = %s, want %s", lc.Kind, KindLiquidityCrypto21)
	}
	if lc.Regime != lbase.Regime || lc.Rank != lbase.Rank {
		t.Fatalf("crypto liquidity diverged from the shared arithmetic")
	}
	if lc.HistoricalAccuracy != cryptoAccuracyFor(KindLiquidityCrypto21, lc.Conviction) {
		t.Fatalf("crypto liquidity must use the crypto table, got %.3f", lc.HistoricalAccuracy)
	}

	// Refusals carry over: thin history yields no forecast, never a guess.
	if _, ok := PredictTrendCrypto(closes[:100]); ok {
		t.Fatal("thin history must refuse")
	}
	if _, ok := PredictLiquidityCrypto(closes[:100], vols[:100]); ok {
		t.Fatal("thin history must refuse")
	}
}
