package composite

import "testing"

// A clear edge with a proven live model is the only way to reach HIGH.
func TestConvictionHighNeedsProvenClearEdge(t *testing.T) {
	got := Assess(ConvictionInputs{Edge: 0.08, NUsed: 4, EdgeProvenLive: true})
	if got.Band != BandHigh {
		t.Fatalf("clear edge + proven → want high, got %q (%v)", got.Band, got.Drivers)
	}
	if got.RiskNote == "" {
		t.Fatal("risk note must always be present")
	}
}

// The same clear edge, but the model is NOT proven live, is capped below high —
// the Seeking-Alpha-style disqualification. This is the current live state.
func TestConvictionCapsWhenModelUnproven(t *testing.T) {
	got := Assess(ConvictionInputs{Edge: 0.08, NUsed: 4, EdgeProvenLive: false})
	if got.Band != BandModerate {
		t.Fatalf("clear edge but unproven → want moderate (capped), got %q", got.Band)
	}
}

// A coin-flip-sized edge is LOW no matter how good everything else is — the
// direct answer to "a 10 with a 52%% probability is not a sure thing".
func TestConvictionCoinFlipEdgeIsLow(t *testing.T) {
	got := Assess(ConvictionInputs{Edge: 0.005, NUsed: 6, EdgeProvenLive: true})
	if got.Band != BandLow {
		t.Fatalf("coin-flip edge → want low, got %q", got.Band)
	}
}

// A slight lean is MODERATE, and each discount drops it a band toward LOW.
func TestConvictionDiscountsLowerTheBand(t *testing.T) {
	base := ConvictionInputs{Edge: 0.03, NUsed: 4, EdgeProvenLive: true} // slight → moderate
	if b := Assess(base).Band; b != BandModerate {
		t.Fatalf("slight edge baseline → want moderate, got %q", b)
	}
	// Stale prediction discounts to low.
	stale := base
	stale.PredAgeSec = 40 * 3600
	if b := Assess(stale).Band; b != BandLow {
		t.Fatalf("stale slight edge → want low, got %q", b)
	}
	// Thin blend discounts to low.
	thin := base
	thin.NUsed = 1
	if b := Assess(thin).Band; b != BandLow {
		t.Fatalf("thin-blend slight edge → want low, got %q", b)
	}
	// Contradictory factors discount to low.
	conflict := base
	conflict.Bull, conflict.Bear = 3, 3
	if b := Assess(conflict).Band; b != BandLow {
		t.Fatalf("conflicting factors → want low, got %q", b)
	}
}

// A dominant factor majority is NOT treated as disagreement.
func TestConvictionDominantMajorityNotPenalized(t *testing.T) {
	in := ConvictionInputs{Edge: 0.08, NUsed: 4, EdgeProvenLive: true, Bull: 5, Bear: 1}
	if b := Assess(in).Band; b != BandHigh {
		t.Fatalf("5-1 bullish majority should not penalize → want high, got %q", b)
	}
}

// Conviction never drops below low (floor), even with every discount stacked.
func TestConvictionFlooredAtLow(t *testing.T) {
	in := ConvictionInputs{Edge: 0.001, NUsed: 1, PredAgeSec: 99 * 3600, Bull: 2, Bear: 2}
	if b := Assess(in).Band; b != BandLow {
		t.Fatalf("everything-bad → want low (floored), got %q", b)
	}
}

func TestFactorAgreementCountsOnlyDirectional(t *testing.T) {
	fs := []Factor{
		{Verdict: 1}, {Verdict: 1}, {Verdict: -1},
		{Verdict: 0}, {Verdict: 0, Gated: true}, // context/gated excluded
	}
	bull, bear := FactorAgreement(fs)
	if bull != 2 || bear != 1 {
		t.Fatalf("want bull=2 bear=1, got bull=%d bear=%d", bull, bear)
	}
}
