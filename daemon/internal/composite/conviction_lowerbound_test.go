package composite

import "testing"

// TestPointEstimateAloneCannotBuyHigh is the fix for the defect the live record
// exposed: the 1d high-conviction tier posted 55.2% accuracy on 297 rows across
// FIVE distinct days and was in fact 5.1pp BELOW its own majority-class null. A
// win rate with no interval behind it must not license HIGH conviction.
func TestPointEstimateAloneCannotBuyHigh(t *testing.T) {
	clean := ConvictionInputs{Edge: 0.06, NUsed: 4, EdgeProvenLive: true, WinRate: 0.62}
	got := Assess(clean)
	if got.Band == BandHigh {
		t.Fatalf("a bare 62%% win rate bought HIGH conviction with no interval: %v", got.Drivers)
	}
	if got.Band != BandModerate {
		t.Fatalf("want moderate cap, got %q", got.Band)
	}
	// The reason must be stated, not silently applied.
	found := false
	for _, d := range got.Drivers {
		if contains(d, "interval") {
			found = true
		}
	}
	if !found {
		t.Errorf("the missing-interval cap was applied without saying so: %v", got.Drivers)
	}
}

// TestLowerBoundGovernsTheCeiling: the interval decides, not the point estimate.
func TestLowerBoundGovernsTheCeiling(t *testing.T) {
	// Same 62% point estimate, three different intervals, three different bands.
	base := ConvictionInputs{Edge: 0.06, NUsed: 4, EdgeProvenLive: true, WinRate: 0.62}

	tight := base
	tight.WinRateLB = 0.59 // whole interval above strongWinRate
	if b := Assess(tight).Band; b != BandHigh {
		t.Errorf("lower bound 0.59 (>= %.2f) should give high, got %q", strongWinRate, b)
	}

	loose := base
	loose.WinRateLB = 0.54 // clears modest only
	if b := Assess(loose).Band; b != BandModerate {
		t.Errorf("lower bound 0.54 should give moderate, got %q", b)
	}

	wide := base
	wide.WinRateLB = 0.48 // cannot rule out a coin flip
	if b := Assess(wide).Band; b != BandLow {
		t.Errorf("lower bound 0.48 should give low, got %q", b)
	}
}

// TestWideIntervalBeatsFlatteringPointEstimate is the live 1d high-conviction
// tier in miniature: a good-looking rate on a sample too thin to support it.
func TestWideIntervalBeatsFlatteringPointEstimate(t *testing.T) {
	thin := ConvictionInputs{
		Edge: 0.06, NUsed: 4, EdgeProvenLive: true,
		WinRate:   0.552, // looks like an edge
		WinRateLB: 0.44,  // 297 rows over 5 days buys nothing
	}
	if b := Assess(thin).Band; b != BandLow {
		t.Fatalf("thin-sample 55.2%% should be LOW on its lower bound, got %q", b)
	}
}

// TestCeilingIsMonotoneInTheLowerBound: raising the lower bound may never lower
// the band. A ceiling that is not monotone is a ranking bug.
func TestCeilingIsMonotoneInTheLowerBound(t *testing.T) {
	prev := -1
	for lb := 0.40; lb <= 0.70; lb += 0.01 {
		in := ConvictionInputs{Edge: 0.06, NUsed: 4, EdgeProvenLive: true, WinRate: 0.65, WinRateLB: lb}
		r := rank(Assess(in).Band)
		if r < prev {
			t.Fatalf("band fell from rank %d to %d as the lower bound rose to %.2f", prev, r, lb)
		}
		prev = r
	}
}

// TestUnprovenStillBeatsEverything: an unproven model is LOW no matter how good
// its interval looks — the existing gate must not be bypassed by the new field.
func TestUnprovenStillBeatsEverything(t *testing.T) {
	in := ConvictionInputs{Edge: 0.06, NUsed: 4, EdgeProvenLive: false, WinRate: 0.70, WinRateLB: 0.68}
	if b := Assess(in).Band; b != BandLow {
		t.Fatalf("unproven with a great interval → want low, got %q", b)
	}
}
