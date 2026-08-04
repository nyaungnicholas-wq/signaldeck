package composite

import "testing"

// HIGH requires a PROVEN, STRONG measured edge — and a non-extreme, fresh,
// multi-leg, agreeing read. The measured accuracy is the ceiling.
func TestConvictionHighNeedsStrongProvenAccuracy(t *testing.T) {
	got := Assess(ConvictionInputs{Edge: 0.06, NUsed: 4, EdgeProvenLive: true, WinRate: 0.60, WinRateLB: 0.585})
	if got.Band != BandHigh {
		t.Fatalf("strong proven accuracy + clean read → want high, got %q (%v)", got.Band, got.Drivers)
	}
	if got.RiskNote == "" {
		t.Fatal("risk note must always be present")
	}
}

// A modest proven accuracy (like the live ~54%) caps conviction below high — a
// real but modest edge, no matter how big the (inflated) calProb looks.
func TestConvictionModestAccuracyCapsBelowHigh(t *testing.T) {
	got := Assess(ConvictionInputs{Edge: 0.06, NUsed: 4, EdgeProvenLive: true, WinRate: 0.54})
	if got.Band != BandModerate {
		t.Fatalf("modest proven accuracy → want moderate (capped), got %q", got.Band)
	}
}

// Not proven live → conviction is capped LOW regardless of edge size.
func TestConvictionUnprovenIsLow(t *testing.T) {
	got := Assess(ConvictionInputs{Edge: 0.30, NUsed: 4, EdgeProvenLive: false})
	if got.Band != BandLow {
		t.Fatalf("unproven → want low, got %q", got.Band)
	}
}

// THE KEY CASE (real AAPL): proven but only ~54% accurate, yet the calibrated
// probability is extreme (edge +46pp ⇒ ~96%). That overconfidence must LOWER
// conviction to LOW — a big inflated edge never buys conviction.
func TestConvictionOverconfidentExtremeEdgeIsLow(t *testing.T) {
	got := Assess(ConvictionInputs{Edge: 0.46, NUsed: 2, EdgeProvenLive: true, WinRate: 0.542})
	if got.Band != BandLow {
		t.Fatalf("extreme edge + modest accuracy → want low (overconfident), got %q (%v)", got.Band, got.Drivers)
	}
	// The overconfidence must be spelled out in the drivers.
	found := false
	for _, d := range got.Drivers {
		if len(d) > 0 && (contains(d, "overconfident")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an overconfidence driver, got %v", got.Drivers)
	}
}

// A coin-flip-sized OWN edge lowers conviction even when the model is decent.
func TestConvictionCoinFlipEdgeLowered(t *testing.T) {
	strong := Assess(ConvictionInputs{Edge: 0.06, NUsed: 4, EdgeProvenLive: true, WinRate: 0.60, WinRateLB: 0.585})
	flat := Assess(ConvictionInputs{Edge: 0.005, NUsed: 4, EdgeProvenLive: true, WinRate: 0.60, WinRateLB: 0.585})
	if rank(flat.Band) >= rank(strong.Band) {
		t.Fatalf("coin-flip edge should lower conviction: flat=%q strong=%q", flat.Band, strong.Band)
	}
}

// Each per-symbol discount lowers the band from a MODERATE ceiling toward LOW.
func TestConvictionDiscountsLowerTheBand(t *testing.T) {
	base := ConvictionInputs{Edge: 0.05, NUsed: 4, EdgeProvenLive: true, WinRate: 0.54} // → moderate
	if b := Assess(base).Band; b != BandModerate {
		t.Fatalf("modest-accuracy clean read → want moderate, got %q", b)
	}
	stale := base
	stale.PredAgeSec = 40 * 3600
	if b := Assess(stale).Band; b != BandLow {
		t.Fatalf("stale → want low, got %q", b)
	}
	thin := base
	thin.NUsed = 1
	if b := Assess(thin).Band; b != BandLow {
		t.Fatalf("thin blend → want low, got %q", b)
	}
	conflict := base
	conflict.Bull, conflict.Bear = 3, 3
	if b := Assess(conflict).Band; b != BandLow {
		t.Fatalf("conflicting factors → want low, got %q", b)
	}
}

// A dominant factor majority is NOT treated as disagreement.
func TestConvictionDominantMajorityNotPenalized(t *testing.T) {
	in := ConvictionInputs{Edge: 0.06, NUsed: 4, EdgeProvenLive: true, WinRate: 0.60, WinRateLB: 0.585, Bull: 5, Bear: 1}
	if b := Assess(in).Band; b != BandHigh {
		t.Fatalf("5-1 bullish majority should not penalize → want high, got %q", b)
	}
}

// Conviction never drops below low (floor), even with every discount stacked.
func TestConvictionFlooredAtLow(t *testing.T) {
	in := ConvictionInputs{Edge: 0.30, NUsed: 1, PredAgeSec: 99 * 3600, Bull: 2, Bear: 2, EdgeProvenLive: true, WinRate: 0.54}
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

func rank(b Band) int {
	switch b {
	case BandHigh:
		return 2
	case BandModerate:
		return 1
	default:
		return 0
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
