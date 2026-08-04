package ensemble

import "testing"

// f64 is declared in attribution_test.go in this same package.

// TestRequireMeasuredLegsBenchesUnmeasuredPressure closes the "unknown means
// allowed" loophole: the fail-safe that keeps an UNMEASURED pressure leg is
// correct for cold start, but must not survive into a production emission.
func TestRequireMeasuredLegsBenchesUnmeasuredPressure(t *testing.T) {
	unmeasured := Components{PressureScore: 0.8} // PressureLift nil = never graded

	// Default (research / cold-start) behaviour is unchanged: the leg is kept.
	if legs := LegProbabilities(unmeasured); len(legs) != 1 {
		t.Fatalf("default mode dropped the unmeasured pressure leg: %v", legs)
	}

	strict := unmeasured
	strict.RequireMeasuredLegs = true
	if legs := LegProbabilities(strict); len(legs) != 0 {
		t.Errorf("strict mode kept an unmeasured leg: %v", legs)
	}

	// A MEASURED positive leg survives strict mode.
	proven := strict
	proven.PressureLift = f64(0.02)
	if legs := LegProbabilities(proven); len(legs) != 1 {
		t.Errorf("strict mode dropped a measured positive leg: %v", legs)
	}

	// A MEASURED negative leg is dropped in both modes.
	bad := unmeasured
	bad.PressureLift = f64(-0.01)
	if legs := LegProbabilities(bad); len(legs) != 0 {
		t.Errorf("default mode kept a measured anti-predictive leg: %v", legs)
	}
}

// TestAdmittedProbabilityRefusesInsteadOfInventing is the no-forecast fallback.
// A 0.5 with nUsed=0 reads on a wire exactly like a real coin-flip forecast;
// ok=false cannot be mistaken for one.
func TestAdmittedProbabilityRefusesInsteadOfInventing(t *testing.T) {
	empty := Components{PressureScore: 0.9, RequireMeasuredLegs: true}
	p, n, ok := AdmittedProbability(empty, nil)
	if ok {
		t.Errorf("AdmittedProbability admitted a forecast with no legs (p=%v n=%v)", p, n)
	}
	if n != 0 {
		t.Errorf("nUsed = %d with no admitted legs, want 0", n)
	}

	// With one admitted leg it agrees with WeightedProbability exactly.
	live := Components{PressureScore: 0.5, PressureLift: f64(0.03), RequireMeasuredLegs: true}
	gotP, gotN, gotOK := AdmittedProbability(live, nil)
	if !gotOK {
		t.Fatal("AdmittedProbability refused a legitimate single-leg blend")
	}
	wantP, wantN := WeightedProbability(live, nil)
	if gotP != wantP || gotN != wantN {
		t.Errorf("AdmittedProbability = (%v,%v), WeightedProbability = (%v,%v)", gotP, gotN, wantP, wantN)
	}
}

// TestStrictModeAppliesToEveryLeg: the opt-in model legs already required a
// positive lift; strict mode must not accidentally loosen them.
func TestStrictModeAppliesToEveryLeg(t *testing.T) {
	c := Components{
		PressureScore:       0.4,
		PressureLift:        f64(0.01),
		RequireMeasuredLegs: true,
		GBMProb:             f64(0.7), // no GBMLift -> unproven
		MeanRevProb:         f64(0.3), MeanRevLift: f64(-0.001), // measured negative
		AlphaXProb: f64(0.6), AlphaXLift: f64(0.02), // proven
	}
	legs := LegProbabilities(c)
	if _, ok := legs[LegGBM]; ok {
		t.Error("unproven GBM leg admitted")
	}
	if _, ok := legs[LegMeanRev]; ok {
		t.Error("anti-predictive meanrev leg admitted")
	}
	if _, ok := legs[LegAlphaX]; !ok {
		t.Error("proven alphax leg dropped")
	}
	if _, ok := legs[LegPressure]; !ok {
		t.Error("proven pressure leg dropped")
	}
}

// TestExpectancyAndSentimentGatedInStrictMode: both legs enter today on a
// freshness/count gate with no measured out-of-sample skill gate at all. In
// strict mode they need a measured positive lift like every other leg.
func TestExpectancyAndSentimentGatedInStrictMode(t *testing.T) {
	c := Components{
		ExpectancyHitRate:   f64(0.61),
		SentimentScore:      f64(0.8),
		RequireMeasuredLegs: true,
	}
	legs := LegProbabilities(c)
	if _, ok := legs[LegExpectancy]; ok {
		t.Error("expectancy admitted in strict mode without a measured lift")
	}
	if _, ok := legs[LegSentiment]; ok {
		t.Error("sentiment admitted in strict mode without a measured lift")
	}

	withLift := c
	withLift.ExpectancyLift = f64(0.02)
	withLift.SentimentLift = f64(0.015)
	legs = LegProbabilities(withLift)
	if _, ok := legs[LegExpectancy]; !ok {
		t.Error("measured expectancy leg dropped")
	}
	if _, ok := legs[LegSentiment]; !ok {
		t.Error("measured sentiment leg dropped")
	}

	// Default mode must be unchanged for these two: no lift required. (Pressure
	// is present too — an unmeasured PressureLift is kept in cold-start mode —
	// so assert membership, not a count.)
	loose := Components{ExpectancyHitRate: f64(0.61), SentimentScore: f64(0.8)}
	legs = LegProbabilities(loose)
	if _, ok := legs[LegExpectancy]; !ok {
		t.Error("default mode dropped expectancy")
	}
	if _, ok := legs[LegSentiment]; !ok {
		t.Error("default mode dropped sentiment")
	}
}
