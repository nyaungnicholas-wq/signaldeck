package ensemble

import "testing"

// f64 is declared in attribution_test.go.

func TestRankGateAdmitsOnPositiveRankEdgeDespiteNegativeLift(t *testing.T) {
	c := Components{
		ForecastProb: f64(0.9),
		ForecastLift: f64(-0.2),
		RankEdge:     map[string]float64{LegForecast: 0.03},
	}
	probs := LegProbabilities(c)
	if got, ok := probs[LegForecast]; !ok || got != 0.9 {
		t.Fatalf("LegForecast: expected 0.9 in map, got %v", probs)
	}
}

func TestRankGateBenchesOnNegativeRankEdgeDespitePositiveLift(t *testing.T) {
	c := Components{
		PressureScore: 0.8,
		PressureLift:  f64(0.4),
		RankEdge:      map[string]float64{LegPressure: -0.02},
	}
	probs := LegProbabilities(c)
	if _, ok := probs[LegPressure]; ok {
		t.Fatalf("LegPressure: expected absent from map, got %v", probs)
	}
}

func TestRankGateZeroEdgeBenches(t *testing.T) {
	c := Components{
		ForecastProb: f64(0.9),
		ForecastLift: f64(0.5),
		RankEdge:     map[string]float64{LegForecast: 0},
	}
	probs := LegProbabilities(c)
	if _, ok := probs[LegForecast]; ok {
		t.Fatalf("LegForecast: expected absent from map, got %v", probs)
	}
}

func TestRankGateNoEntryFallsBackToLift(t *testing.T) {
	t.Run("positive lift admits", func(t *testing.T) {
		c := Components{
			ForecastProb: f64(0.9),
			ForecastLift: f64(0.1),
		}
		probs := LegProbabilities(c)
		if got, ok := probs[LegForecast]; !ok || got != 0.9 {
			t.Fatalf("LegForecast: expected 0.9 in map, got %v", probs)
		}
	})

	t.Run("negative lift drops", func(t *testing.T) {
		c := Components{
			ForecastProb: f64(0.9),
			ForecastLift: f64(-0.1),
		}
		probs := LegProbabilities(c)
		if _, ok := probs[LegForecast]; ok {
			t.Fatalf("LegForecast: expected absent from map, got %v", probs)
		}
	})
}

func TestRankGateNoEntryPreservesColdStartPressure(t *testing.T) {
	c := Components{
		PressureScore:       0.4,
		PressureLift:        nil,
		RequireMeasuredLegs: false,
	}
	probs := LegProbabilities(c)
	if _, ok := probs[LegPressure]; !ok {
		t.Fatalf("LegPressure: expected present in map, got %v", probs)
	}

	c.RequireMeasuredLegs = true
	probs = LegProbabilities(c)
	if _, ok := probs[LegPressure]; ok {
		t.Fatalf("LegPressure: expected absent from map, got %v", probs)
	}
}
