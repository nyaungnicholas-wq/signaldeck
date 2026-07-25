// Tests for the MEASURED forward-return table (2026-07-24 re-validation) and
// the Tradeability sentence built from it. The point of these is not that the
// arithmetic works — it is that the app cannot quietly drop the finding that
// the most ACCURATE trend band is the LEAST profitable one.
package structregime

import (
	"math"
	"strings"
	"testing"
)

// forwardReturnFor must serve the measured band at every cutoff, share
// accuracyFor's boundaries exactly, and refuse (ok=false) every kind or band
// the re-validation did not measure.
func TestForwardReturnForBands(t *testing.T) {
	tests := []struct {
		name    string
		kind    Kind
		conv    float64
		wantPct float64
		wantOK  bool
	}{
		{"trend21 bottom", KindTrend21, 0.0, 0.41, true},
		{"trend21 just under 0.5", KindTrend21, 0.4999, 0.41, true},
		{"trend21 at 0.5", KindTrend21, 0.5, 0.58, true},
		{"trend21 just under 0.8", KindTrend21, 0.7999, 0.58, true},
		{"trend21 at 0.8", KindTrend21, 0.8, 0.79, true},
		{"trend21 just under 0.9", KindTrend21, 0.8999, 0.79, true},
		{"trend21 at 0.9 inverts", KindTrend21, 0.9, -0.39, true},
		{"trend21 top", KindTrend21, 1.0, -0.39, true},
		{"trend63 at 0.9", KindTrend63, 0.9, -1.30, true},
		{"trend63 top", KindTrend63, 1.0, -1.30, true},
		{"trend63 below 0.9 not measured", KindTrend63, 0.8999, 0, false},
		{"trend63 bottom not measured", KindTrend63, 0.1, 0, false},
		{"liquidity21 not measured", KindLiquidity21, 0.95, 0, false},
		{"vol21 not measured", KindVol21, 0.95, 0, false},
		{"trend21-crypto not measured", KindTrendCrypto21, 0.95, 0, false},
		{"liquidity21-crypto not measured", KindLiquidityCrypto21, 0.95, 0, false},
		{"unknown kind not measured", Kind("nope"), 0.95, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pct, ok := forwardReturnFor(tc.kind, tc.conv)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if math.Abs(pct-tc.wantPct) > 1e-9 {
				t.Fatalf("pct = %.4f, want %.4f", pct, tc.wantPct)
			}
		})
	}
}

// The band cutoffs must stay welded to accuracyFor's: if one function's
// boundaries drift, a forecast would pair one band's accuracy with another
// band's return — exactly the misstatement this table exists to prevent.
func TestForwardReturnSharesAccuracyCutoffs(t *testing.T) {
	for _, c := range []float64{0.4999, 0.5, 0.7999, 0.8, 0.8999, 0.9} {
		acc := accuracyFor(KindTrend21, c)
		pct, ok := forwardReturnFor(KindTrend21, c)
		if !ok {
			t.Fatalf("trend21 return unexpectedly absent at conv %.4f", c)
		}
		// The measured pairing: 97.2% accuracy is the band that LOSES money.
		if acc == 0.972 && pct >= 0 {
			t.Fatalf("top accuracy band paired with non-negative return %.2f%%", pct)
		}
		if acc != 0.972 && pct <= 0 {
			t.Fatalf("band acc %.3f paired with non-positive return %.2f%%", acc, pct)
		}
	}
}

// The inversion itself, asserted as a fact of record: the top band must be the
// most accurate AND the worst-returning at both horizons.
func TestTopBandIsMostAccurateAndWorstReturning(t *testing.T) {
	for _, k := range []Kind{KindTrend21, KindTrend63} {
		top, ok := forwardReturnFor(k, 0.95)
		if !ok {
			t.Fatalf("%s: top band return missing", k)
		}
		if top > 0 {
			t.Fatalf("%s: top band return %.2f%% is positive — the measurement said otherwise", k, top)
		}
		if accuracyFor(k, 0.95) < accuracyFor(k, 0.1) {
			t.Fatalf("%s: top band is not the most accurate", k)
		}
	}
	lower, _ := forwardReturnFor(KindTrend21, 0.85)
	top, _ := forwardReturnFor(KindTrend21, 0.95)
	if !(lower > top) {
		t.Fatalf("trend21 0.8-0.9 (%.2f%%) must beat >=0.9 (%.2f%%)", lower, top)
	}
}

// A live trend21 forecast in the top band must SAY the negative return — an
// empty or euphemistic string here is the failure mode the app shipped before.
func TestTradeabilityTopTrend21StatesNegativeReturn(t *testing.T) {
	f, ok := PredictTrend(topBandCloses())
	if !ok {
		t.Fatal("expected forecast")
	}
	if f.Conviction < 0.9 {
		t.Fatalf("test series did not reach the top band: conv %.3f", f.Conviction)
	}
	got := f.Tradeability
	if got == "" {
		t.Fatal("top-band trend21 forecast shipped an EMPTY tradeability string")
	}
	for _, want := range []string{"-0.39%", "NOT A TRADE", "does NOT mean higher return", "mean-revert"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tradeability missing %q\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, "+") && !strings.Contains(got, "-0.39%") {
		t.Fatalf("tradeability reads as a positive return: %s", got)
	}
}

// trend63's top band must carry its own (worse) number, never trend21's.
func TestTradeabilityTopTrend63UsesItsOwnNumber(t *testing.T) {
	f, ok := PredictTrend63(topBandCloses())
	if !ok {
		t.Fatal("expected forecast")
	}
	if !strings.Contains(f.Tradeability, "-1.30%") {
		t.Fatalf("trend63 tradeability = %q", f.Tradeability)
	}
	if strings.Contains(f.Tradeability, "-0.39%") {
		t.Fatalf("trend63 quoted the 21d number: %q", f.Tradeability)
	}
	if !strings.Contains(f.Tradeability, "63d") {
		t.Fatalf("trend63 tradeability does not name its horizon: %q", f.Tradeability)
	}
}

// TradeabilityFor is the read-surface contract: kinds whose forward return was
// never measured return the empty string, so no surface can borrow another
// kind's number. (Crypto trend forecasts are built by copying a trend21
// Forecast, so any surface that shows them must derive the string this way.)
func TestTradeabilityForUnmeasuredKindsIsEmpty(t *testing.T) {
	for _, k := range []Kind{KindLiquidity21, KindVol21, KindTrendCrypto21, KindLiquidityCrypto21} {
		for _, c := range []float64{0.1, 0.5, 0.85, 0.95} {
			if s := TradeabilityFor(k, c); s != "" {
				t.Fatalf("%s at conv %.2f invented a return: %q", k, c, s)
			}
		}
	}
	if s := TradeabilityFor(KindTrend63, 0.5); s != "" {
		t.Fatalf("trend63 moderate band invented a return: %q", s)
	}
}

// The positive bands must read as measurements, not recommendations.
func TestTradeabilityPositiveBandStaysHonest(t *testing.T) {
	s := TradeabilityFor(KindTrend21, 0.85)
	if !strings.Contains(s, "+0.79%") {
		t.Fatalf("missing the measured +0.79%%: %q", s)
	}
	if !strings.Contains(s, "not advice") {
		t.Fatalf("positive band reads as advice: %q", s)
	}
	if !strings.Contains(s, "NEGATIVE") {
		t.Fatalf("positive band hides the top-band inversion: %q", s)
	}
}

// topBandCloses is a series whose LATEST distance from its 200-day average is
// the most extreme in its own trailing window — a quiet drift for three years
// then a sharp run — which is what conviction >=0.9 physically means.
func topBandCloses() []float64 {
	closes := make([]float64, 740)
	p := 100.0
	for i := range closes {
		if i >= 700 {
			p *= 1.010 // the recent extension that lifts the distance percentile
		} else {
			p *= 1.0005
		}
		closes[i] = p
	}
	return closes
}
