package attribution

import (
	"math"
	"strings"
	"testing"
)

// With thin live evidence, the historical prior must dominate the blend, and
// the report must say attribution is underpowered — NOT that there is no edge.
func TestThinLivePriorLeads(t *testing.T) {
	prior := Evidence{HitRate: 0.62, N: 400} // rich historical analog
	live := Evidence{HitRate: 0.30, N: 3}    // 3 noisy live outcomes
	r := Assess("NVDA", "1d", "risk_on", prior, live, true)

	if r.LiveWeight >= 0.5 {
		t.Fatalf("thin live (n=3) must not carry the blend, liveWeight=%.2f", r.LiveWeight)
	}
	if r.Driver != "historical-prior" || r.AttributionSupported {
		t.Fatalf("thin live → prior-driven + unsupported attribution, got driver=%q supported=%v", r.Driver, r.AttributionSupported)
	}
	// Posterior must sit close to the prior, not the 30% live noise.
	if math.Abs(r.CalibratedProb-0.62) > 0.05 {
		t.Fatalf("posterior %.3f drifted from the 62%% prior toward live noise", r.CalibratedProb)
	}
	if !strings.Contains(r.Explanation, "underpowered") && !strings.Contains(r.Explanation, "insufficient") {
		t.Fatalf("explanation must flag underpowered live, got: %q", r.Explanation)
	}
}

// Ample live evidence earns real weight and flips attribution to supported.
func TestAmpleLiveEarnsWeight(t *testing.T) {
	prior := Evidence{HitRate: 0.55, N: 300}
	live := Evidence{HitRate: 0.58, N: 120}
	r := Assess("SPY", "1w", "neutral", prior, live, true)
	if !r.AttributionSupported || r.Driver == "historical-prior" {
		t.Fatalf("ample live (n=120) → supported + blended/live, got driver=%q supported=%v", r.Driver, r.AttributionSupported)
	}
	// live weight = 120/(20+120) ≈ 0.857
	if r.LiveWeight < 0.8 {
		t.Fatalf("live weight should be high with n=120 vs priorEff=20, got %.2f", r.LiveWeight)
	}
}

// Both sources empty → honest "insufficient", NOT a fabricated no-edge verdict.
func TestNeitherSource(t *testing.T) {
	r := Assess("XYZ", "1d", "", Evidence{}, Evidence{}, false)
	if r.Driver != "insufficient" || r.RegimeMatch != MatchNone {
		t.Fatalf("empty → insufficient/none, got driver=%q match=%q", r.Driver, r.RegimeMatch)
	}
	if strings.Contains(strings.ToLower(r.Explanation), "no edge detected") {
		t.Fatalf("must NOT claim 'no edge' from absence of data: %q", r.Explanation)
	}
	if !strings.Contains(r.Explanation, "not a measured lack of edge") {
		t.Fatalf("must distinguish lack of evidence from lack of edge: %q", r.Explanation)
	}
}

// A thin prior can't dominate either — priorEff is capped at PriorStrength.
func TestThinPriorCannotDominate(t *testing.T) {
	prior := Evidence{HitRate: 0.90, N: 4} // extreme but thin
	live := Evidence{HitRate: 0.50, N: 40}
	_, liveW := Blend(prior, live)
	// priorEff = min(4, 20) = 4; liveW = 40/44 ≈ 0.91
	if liveW < 0.85 {
		t.Fatalf("a 4-sample prior must not dominate 40 live obs, liveW=%.2f", liveW)
	}
}

func TestWilsonSane(t *testing.T) {
	lo, hi := Wilson(50, 100)
	if !(lo > 0.39 && lo < 0.41 && hi > 0.59 && hi < 0.61) {
		t.Fatalf("Wilson(50,100) band off: [%.3f, %.3f]", lo, hi)
	}
	if lo, hi := Wilson(0, 0); lo != 0 || hi != 1 {
		t.Fatalf("Wilson(0,0) must be [0,1], got [%.3f,%.3f]", lo, hi)
	}
}
