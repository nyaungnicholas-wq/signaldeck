package ensemble

import "testing"

// The pressure leg's OOS gate: it contributes when unmeasured (nil, fail-safe)
// or measured with positive lift, and is benched only on a MEASURED lift <= 0.

func TestPressureLegKeptWhenUnmeasured(t *testing.T) {
	legs := LegProbabilities(Components{PressureScore: 0.6}) // PressureLift nil
	if _, ok := legs[LegPressure]; !ok {
		t.Error("unmeasured pressure leg dropped; want kept (fail-safe)")
	}
}

func TestPressureLegKeptWhenPositiveLift(t *testing.T) {
	legs := LegProbabilities(Components{PressureScore: 0.6, PressureLift: fptr(0.03)})
	if _, ok := legs[LegPressure]; !ok {
		t.Error("positive-lift pressure leg dropped; want kept")
	}
}

func TestPressureLegBenchedWhenNonPositiveLift(t *testing.T) {
	for _, lift := range []float64{0, -0.03, -0.15} {
		legs := LegProbabilities(Components{PressureScore: 0.6, PressureLift: fptr(lift)})
		if _, ok := legs[LegPressure]; ok {
			t.Errorf("pressure leg with measured lift %.2f kept; want benched", lift)
		}
	}
}

// When pressure is the only leg and it is benched, the blend is a neutral,
// information-free 0.5 with nUsed=0 (the honest "no measured signal" state).
func TestBenchedPressureAloneYieldsNeutral(t *testing.T) {
	prob, n := RawProbability(Components{PressureScore: 0.9, PressureLift: fptr(-0.05)})
	if n != 0 {
		t.Errorf("nUsed = %d, want 0 (pressure benched, nothing else)", n)
	}
	if prob != 0.5 {
		t.Errorf("prob = %.3f, want 0.5 (neutral prior)", prob)
	}
}

// A benched pressure leg leaves the OTHER legs untouched: expectancy still drives
// the blend, so the ensemble degrades gracefully rather than blanking.
func TestBenchedPressureLeavesOtherLegs(t *testing.T) {
	c := Components{PressureScore: 0.9, PressureLift: fptr(-0.05), ExpectancyHitRate: fptr(0.7)}
	legs := LegProbabilities(c)
	if _, ok := legs[LegPressure]; ok {
		t.Error("pressure leg kept despite negative lift")
	}
	prob, n := RawProbability(c)
	if n != 1 || prob != 0.7 {
		t.Errorf("blend = (prob %.3f, n %d), want (0.700, 1) from expectancy alone", prob, n)
	}
}
