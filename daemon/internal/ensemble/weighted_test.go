package ensemble

import (
	"math"
	"testing"
)

// ── learning-flywheel wave: sentiment leg + weighted blend ──────────────

func fptr(v float64) *float64 { return &v }

func TestLegProbabilities(t *testing.T) {
	// Full house: every leg present and eligible.
	c := Components{
		PressureScore:     0.6, // -> 0.8
		ExpectancyHitRate: fptr(0.62),
		ForecastProb:      fptr(0.71),
		ForecastLift:      fptr(0.05),
		SentimentScore:    fptr(1.0), // -> 0.5 + 0.15 = 0.65
	}
	legs := LegProbabilities(c)
	want := map[string]float64{
		LegPressure:   0.8,
		LegExpectancy: 0.62,
		LegForecast:   0.71,
		LegSentiment:  0.5 + SentimentScale,
	}
	if len(legs) != len(want) {
		t.Fatalf("legs = %v, want %v", legs, want)
	}
	for k, v := range want {
		if math.Abs(legs[k]-v) > 1e-12 {
			t.Fatalf("leg %s = %v, want %v", k, legs[k], v)
		}
	}

	// Edgeless forecast is dropped; absent optional legs are absent.
	c = Components{PressureScore: 0, ForecastProb: fptr(0.9), ForecastLift: fptr(0)}
	legs = LegProbabilities(c)
	if len(legs) != 1 || legs[LegPressure] != 0.5 {
		t.Fatalf("edgeless forecast must be dropped: %v", legs)
	}

	// Bearish sentiment maps below 0.5, conservatively scaled.
	legs = LegProbabilities(Components{SentimentScore: fptr(-1.0)})
	if got := legs[LegSentiment]; math.Abs(got-(0.5-SentimentScale)) > 1e-12 {
		t.Fatalf("sentiment(-1) leg = %v, want %v", got, 0.5-SentimentScale)
	}
}

func TestRawProbability_SentimentLeg(t *testing.T) {
	// nil sentiment: behavior is EXACTLY the pre-sentiment blend.
	base, nBase := RawProbability(Components{PressureScore: 0.6})
	if nBase != 1 || math.Abs(base-0.8) > 1e-12 {
		t.Fatalf("baseline: (%v, %d), want (0.8, 1)", base, nBase)
	}

	// Present sentiment joins the blend as one more equal-weight leg.
	p, n := RawProbability(Components{PressureScore: 0.6, SentimentScore: fptr(1.0)})
	want := (0.8 + 0.65) / 2
	if n != 2 || math.Abs(p-want) > 1e-12 {
		t.Fatalf("with sentiment: (%v, %d), want (%v, 2)", p, n, want)
	}

	// Full-range sentiment (-1 -> +1) moves this 2-leg blend by exactly
	// SentimentScale (2*scale swing on its own leg, halved by the equal
	// blend) — the conservative mapping is the whole point.
	pNeg, _ := RawProbability(Components{PressureScore: 0.6, SentimentScore: fptr(-1.0)})
	if math.Abs((p-pNeg)-SentimentScale) > 1e-12 {
		t.Fatalf("sentiment swing = %v, want exactly %v", p-pNeg, SentimentScale)
	}
}

func TestWeightedProbability_NilAndEmptyFallBackToRaw(t *testing.T) {
	c := Components{PressureScore: 0.4, ExpectancyHitRate: fptr(0.7), SentimentScore: fptr(0.5)}
	raw, nRaw := RawProbability(c)
	for name, w := range map[string]map[string]float64{"nil": nil, "empty": {}} {
		p, n := WeightedProbability(c, w)
		if p != raw || n != nRaw {
			t.Fatalf("%s weights: (%v,%d) != RawProbability (%v,%d)", name, p, n, raw, nRaw)
		}
	}
}

func TestWeightedProbability_WeightedMean(t *testing.T) {
	c := Components{PressureScore: 0.6, ExpectancyHitRate: fptr(0.6)} // legs 0.8, 0.6
	p, n := WeightedProbability(c, map[string]float64{LegPressure: 0.75, LegExpectancy: 0.25})
	if n != 2 || math.Abs(p-0.75) > 1e-12 { // 0.8*0.75 + 0.6*0.25
		t.Fatalf("(%v, %d), want (0.75, 2)", p, n)
	}

	// Unnormalized weights give the same answer (mean is weight-sum scaled).
	p2, _ := WeightedProbability(c, map[string]float64{LegPressure: 3, LegExpectancy: 1})
	if math.Abs(p2-0.75) > 1e-12 {
		t.Fatalf("unnormalized: %v, want 0.75", p2)
	}

	// A zero-weight leg contributes nothing and is not counted.
	p3, n3 := WeightedProbability(c, map[string]float64{LegPressure: 1, LegExpectancy: 0})
	if n3 != 1 || math.Abs(p3-0.8) > 1e-12 {
		t.Fatalf("zero-weight leg: (%v, %d), want (0.8, 1)", p3, n3)
	}
}

func TestWeightedProbability_NoMassOnPresentLegsFallsBackToRaw(t *testing.T) {
	// Weights learned only for legs that are ABSENT right now (e.g. the
	// forecast lost its edge since the weights were computed) must not
	// zero out the blend — the static equal prior applies instead.
	c := Components{PressureScore: 0.6} // only the pressure leg is present
	raw, nRaw := RawProbability(c)
	p, n := WeightedProbability(c, map[string]float64{LegForecast: 0.7, LegSentiment: 0.3})
	if p != raw || n != nRaw {
		t.Fatalf("(%v,%d) != raw fallback (%v,%d)", p, n, raw, nRaw)
	}
}
