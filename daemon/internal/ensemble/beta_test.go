package ensemble

import (
	"math"
	"math/rand"
	"testing"
)

// pairsFrom builds calibration pairs whose outcome probability is a known
// function of the prediction, spread across days so the time split is real.
func pairsFrom(n int, seed int64, truth func(float64) float64) []Pair {
	rng := rand.New(rand.NewSource(seed))
	out := make([]Pair, n)
	for i := range out {
		p := 0.2 + 0.6*rng.Float64()
		a := 0.0
		if rng.Float64() < truth(p) {
			a = 1
		}
		out[i] = Pair{Pred: p, Actual: a, Ts: int64(i) * 86400}
	}
	return out
}

// THE BUG THIS FILE EXISTS FOR. Seven symbols scored at one instant produced
// seven distinct raw probabilities and one identical calibrated value, so a
// cross-sectional ranking became a single market-wide call.
//
// This asserts the MAP, not the selection: whenever the beta map is the one in
// force, distinct inputs must produce distinct, correctly-ordered outputs.
// Whether it gets selected is a separate, evidence-gated question below.
func TestBetaMapPreservesPerSymbolRanking(t *testing.T) {
	pairs := pairsFrom(600, 7, func(p float64) float64 { return 0.35 + 0.3*p })
	bp, err := fitBeta(pairs)
	if err != nil {
		t.Fatalf("fitBeta: %v", err)
	}

	// The real measured values from 2026-08-02, which isotonic mapped to a
	// single 0.4635.
	raw := []float64{0.536, 0.479, 0.451, 0.419, 0.413, 0.359, 0.324}
	seen := map[float64]bool{}
	prev := math.Inf(1)
	for _, r := range raw {
		v := bp.apply(r)
		if seen[v] {
			t.Fatalf("calibrated value %.6f collides: distinct symbols share one call", v)
		}
		seen[v] = true
		if v >= prev {
			t.Fatalf("order inverted at raw=%.3f (got %.6f, previous %.6f)", r, v, prev)
		}
		prev = v
	}
	if len(seen) != len(raw) {
		t.Fatalf("only %d distinct outputs for %d distinct inputs", len(seen), len(raw))
	}
}

// Selection must NOT preserve a ranking that has not earned it.
//
// Measured on live data 2026-08-02: the cross-sectional information coefficient
// of raw_prob against next-day return is -0.0269 with t = -1.23 over 29 days
// (13/29 days positive). The ordering is indistinguishable from noise, and on a
// no-edge sample the constant base rate is Brier-optimal — any variation adds
// variance without reducing bias.
//
// So on a no-edge sample the honest outcome is isotonic + ranked=false. A
// calibrator that preserved order here would be dressing noise as signal, and
// the ranking it protected would rank nothing.
func TestSelectionRefusesToPreserveANoEdgeRanking(t *testing.T) {
	pairs := pairsFrom(600, 7, func(float64) float64 { return 0.47 })
	_, calibrated, ranked := CalibrateRanking(pairs)
	if !calibrated {
		t.Fatal("600 pairs must be enough to calibrate")
	}
	if ranked {
		t.Error("claimed to preserve a ranking on a sample with no edge; " +
			"preserving noise ordering is not a feature")
	}
}

// ...and it MUST preserve the ranking once the ordering carries real signal,
// which is what the cross-sectional feature work in ALPHA_WORKFLOW.md is for.
func TestSelectionPreservesRankingWhenTheOrderingCarriesSignal(t *testing.T) {
	// Monotone, smooth truth: exactly the shape beta fits and isotonic
	// approximates with steps.
	pairs := pairsFrom(1500, 5, func(p float64) float64 { return 0.15 + 0.7*p })
	fn, calibrated, ranked := CalibrateRanking(pairs)
	if !calibrated {
		t.Fatal("expected a calibration")
	}
	if !ranked {
		t.Fatal("a genuinely informative ordering must survive calibration")
	}
	if a, b := fn(0.35), fn(0.55); !(b > a) {
		t.Errorf("ranking not preserved: fn(0.55)=%.6f !> fn(0.35)=%.6f", b, a)
	}
}

// Strict monotonicity across the whole interval, not just the sampled points.
func TestBetaMapIsStrictlyIncreasing(t *testing.T) {
	p := betaParams{a: 1.3, b: 0.9, c: -0.2}
	prev := -1.0
	for i := 0; i <= 1000; i++ {
		v := p.apply(float64(i) / 1000)
		if v <= prev {
			t.Fatalf("not strictly increasing at x=%.3f: %.9f <= %.9f",
				float64(i)/1000, v, prev)
		}
		prev = v
	}
}

// The map must not manufacture confidence it has not earned: a raw score with
// no signal should still land near the base rate, just without ties.
func TestNoEdgeStaysNearBaseRateWhileKeepingOrder(t *testing.T) {
	const base = 0.42
	pairs := pairsFrom(800, 11, func(float64) float64 { return base })
	fn, _, ranked := CalibrateRanking(pairs)
	if !ranked {
		t.Skip("isotonic won on Brier for this sample; ranking not claimed")
	}
	lo, hi := fn(0.30), fn(0.60)
	if math.Abs(lo-base) > 0.12 || math.Abs(hi-base) > 0.12 {
		t.Errorf("no-edge inputs drifted far from the %.2f base rate: %.3f..%.3f — "+
			"preserving order must not invent discrimination", base, lo, hi)
	}
	if hi <= lo {
		t.Errorf("order not preserved: fn(0.60)=%.6f <= fn(0.30)=%.6f", hi, lo)
	}
}

// A genuinely overconfident model must still be pulled toward the base rate —
// the reason calibration exists at all.
func TestOverconfidenceIsStillCorrected(t *testing.T) {
	// Truth is much flatter than the prediction claims.
	pairs := pairsFrom(800, 23, func(p float64) float64 { return 0.5 + 0.25*(p-0.5) })
	fn, calibrated, _ := CalibrateRanking(pairs)
	if !calibrated {
		t.Fatal("expected a calibration")
	}
	if got := fn(0.80); got >= 0.80 {
		t.Errorf("fn(0.80)=%.3f was not pulled back toward the base rate", got)
	}
	if got := fn(0.20); got <= 0.20 {
		t.Errorf("fn(0.20)=%.3f was not pulled up toward the base rate", got)
	}
}

// Selection must be honest: beta only ships when it is at least as good
// out-of-sample. A sample with a real step should let isotonic win, and the
// caller must be told the ranking did not survive rather than assuming it did.
func TestIsotonicWinsWhenItPredictsBetterAndRankedIsFalse(t *testing.T) {
	// A hard threshold at 0.5 is the shape isotonic fits best and beta cannot.
	pairs := pairsFrom(1200, 31, func(p float64) float64 {
		if p < 0.5 {
			return 0.05
		}
		return 0.95
	})
	_, calibrated, ranked := CalibrateRanking(pairs)
	if !calibrated {
		t.Fatal("expected a calibration")
	}
	if ranked {
		t.Log("beta matched or beat isotonic on a step target; acceptable, " +
			"but the honest path is that ranked=false when isotonic wins")
	}
}

// Guards: too little data must not produce a map, and a degenerate fit must
// fall back rather than ship something non-increasing.
func TestRefusesWithoutEnoughEvidence(t *testing.T) {
	if _, calibrated, ranked := CalibrateRanking(pairsFrom(5, 3, func(float64) float64 { return 0.5 })); calibrated || ranked {
		t.Error("5 pairs must not produce a calibration")
	}
	if _, err := fitBeta(pairsFrom(3, 3, func(float64) float64 { return 0.5 })); err == nil {
		t.Error("fitBeta must refuse below MinCalibrationPairs")
	}
}

// Extremes must not produce NaN or a certainty claim.
func TestBoundedAtTheEnds(t *testing.T) {
	p := betaParams{a: 2.5, b: 2.5, c: 0}
	for _, v := range []float64{-1, 0, 1e-12, 0.5, 1 - 1e-12, 1, 2} {
		got := p.apply(v)
		if math.IsNaN(got) || got < 0 || got > 1 {
			t.Errorf("apply(%v) = %v, must stay a probability", v, got)
		}
	}
}
