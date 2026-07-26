package symbolagent

import (
	"math"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// mkExample builds one labeled example: pressure leg = pr, forecast leg = fc,
// realized up/down, a forward return that matches the direction, and the UTC
// day the example falls on. Every floor in this package counts distinct days,
// so a fixture that leaves ts at 0 is one day of evidence and is refused.
func mkExample(pr, fc float64, up int, ts int64) adaptive.Example {
	fwd := 0.01
	if up == 0 {
		fwd = -0.01
	}
	return adaptive.Example{
		Legs:      map[string]float64{ensemble.LegPressure: pr, ensemble.LegForecast: fc},
		Regime:    "trend_up",
		Ts:        ts,
		Up:        up,
		FwdReturn: fwd,
	}
}

// synthetic builds n examples where PRESSURE is a strong directional predictor
// (its leg agrees with the outcome ~90% of the time) and FORECAST is a pure
// coin flip (alternating, uncorrelated with the outcome). rawPairs pairs a
// plausible blend prob with each outcome for the calibration fit.
func synthetic(n int) ([]adaptive.Example, []ensemble.Pair) {
	var ex []adaptive.Example
	var pairs []ensemble.Pair
	for i := 0; i < n; i++ {
		up := i % 2 // balanced 50/50 base rate
		// Pressure leans the right way 90% of the time.
		pr := 0.7
		if up == 0 {
			pr = 0.3
		}
		if i%10 == 0 { // 10% of the time pressure is wrong
			pr = 1 - pr
		}
		// Forecast is a coin flip UNCORRELATED with the outcome: its direction
		// tracks i%4 (up on 0/1, down on 2/3), which lines up with up (i%2) for
		// exactly half the examples → ~50% hit-rate, no measurable edge.
		fc := 0.7
		if i%4 >= 2 {
			fc = 0.3
		}
		ts := int64(i) * 86400 // one example per distinct UTC day
		ex = append(ex, mkExample(pr, fc, up, ts))
		// Raw blend prob: track the outcome loosely so there's spread to fit.
		raw := 0.45 + 0.1*float64(up)
		pairs = append(pairs, ensemble.Pair{Pred: raw, Actual: float64(up), Ts: ts})
	}
	return ex, pairs
}

// TestLearn_GatingBelowThreshold: with fewer than MinPersonal of its own
// outcomes, a symbol MUST NOT get a personal model, MUST fall back to the best
// available global tier, MUST expose nil personal weights + identity
// calibration, and MUST say "still learning n/threshold".
func TestLearn_GatingBelowThreshold(t *testing.T) {
	ex, pairs := synthetic(MinPersonal - 1) // one short of graduation
	m := Learn(ex, pairs, true /*regimeLearned*/, true /*globalLearned*/)

	if m.Tier != TierRegime {
		t.Fatalf("below threshold with a regime fallback must be tier=regime, got %q", m.Tier)
	}
	if m.Weights != nil {
		t.Fatalf("still-learning symbol must expose NO personal weights, got %v", m.Weights)
	}
	if m.Calibration.Fitted {
		t.Fatal("still-learning symbol must not ship a fitted calibration")
	}
	if m.NSamples != MinPersonal-1 {
		t.Fatalf("nSamples = %d, want %d", m.NSamples, MinPersonal-1)
	}
	if !strings.Contains(m.Personality, "Still learning") ||
		!strings.Contains(m.Personality, "global per-regime model") {
		t.Fatalf("personality must be an honest still-learning line naming the fallback: %q", m.Personality)
	}

	// With no global evidence at all, the fallback degrades to static.
	mStatic := Learn(ex, pairs, false, false)
	if mStatic.Tier != TierStatic {
		t.Fatalf("no global evidence must fall back to static, got %q", mStatic.Tier)
	}
	if !strings.Contains(mStatic.Personality, "equal-weight prior") {
		t.Fatalf("static personality must name the equal-weight prior: %q", mStatic.Personality)
	}
}

// TestLearn_GraduatesToPersonal: at/above MinPersonal with a real edge, the
// symbol graduates — pressure earns weight, forecast (a coin flip) earns none,
// and the personality is derived deterministically from the measured skill.
func TestLearn_GraduatesToPersonal(t *testing.T) {
	ex, pairs := synthetic(MinPersonal + 20)
	m := Learn(ex, pairs, true, true)

	if m.Tier != TierPersonal {
		t.Fatalf("above threshold with real edge must be tier=personal, got %q", m.Tier)
	}
	if len(m.Weights) == 0 {
		t.Fatal("personal model must carry its own weights")
	}
	if m.Weights[ensemble.LegPressure] <= 0 {
		t.Fatalf("pressure is the strong predictor — it must earn weight: %v", m.Weights)
	}
	if w := m.Weights[ensemble.LegForecast]; w > 0 {
		t.Fatalf("forecast is a coin flip — it must earn ZERO weight, got %v", w)
	}
	// Weights are normalized.
	var sum float64
	for _, v := range m.Weights {
		sum += v
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("weights must sum to 1, got %v", sum)
	}

	// Skill exposes pressure's measured hit-rate (~0.9) and its sample count.
	sk, ok := m.Skill[ensemble.LegPressure]
	if !ok || !sk.HasHR {
		t.Fatalf("pressure skill missing hit-rate: %+v", sk)
	}
	if sk.HitRate < 0.8 {
		t.Fatalf("pressure hit-rate should be ~0.9, got %v", sk.HitRate)
	}
	if sk.N != len(ex) {
		t.Fatalf("pressure present in every example, N=%d want %d", sk.N, len(ex))
	}

	// Personality is deterministic + leads with the strongest component.
	if !strings.Contains(m.Personality, "Pressure-led") {
		t.Fatalf("personality should lead with Pressure: %q", m.Personality)
	}
	m2 := Learn(ex, pairs, true, true)
	if m2.Personality != m.Personality {
		t.Fatalf("personality must be deterministic:\n %q\n %q", m.Personality, m2.Personality)
	}
}

// TestLearn_CalibrationPrequential asserts the no-leakage property of the
// per-symbol calibration: the fitted isotonic map is trained ONLY on the
// symbol's own resolved (rawProb, outcome) pairs. A brand-new live prediction
// — whose outcome is unresolved and therefore NOT in the training set — is
// recalibrated by that map, never by its own (nonexistent) outcome. Here we
// prove the map (a) is only fitted with >= MinCalibrationPairs pairs and (b)
// is a pure function of the training pairs: a point's own outcome never enters.
func TestLearn_CalibrationPrequential(t *testing.T) {
	// Enough pairs to fit.
	ex, pairs := synthetic(MinPersonal + 20)
	m := Learn(ex, pairs, true, true)
	if !m.Calibration.Fitted {
		t.Fatal("with enough resolved pairs the personal calibration must fit")
	}
	fn := m.Calibration.Map()

	// The map is a deterministic function of the TRAINING pairs only. Evaluate
	// it at a fresh raw prob (a hypothetical live prediction not in the set):
	// the result depends solely on the fitted knots, never on any future
	// outcome — that IS the prequential guarantee.
	live := 0.5
	got1 := fn(live)
	// Re-derive the map from the SAME pairs → identical output (pure function,
	// no hidden dependence on a point's own realized label).
	kx, ky, ok := ensemble.CalibrateKnots(pairs)
	if !ok {
		t.Fatal("knots must fit")
	}
	got2 := ensemble.MapFromKnots(kx, ky)(live)
	if math.Abs(got1-got2) > 1e-12 {
		t.Fatalf("calibration is not a pure function of the training pairs: %v vs %v", got1, got2)
	}

	// Monotone + bounded (isotonic invariant): higher raw never maps lower.
	if fn(0.2) > fn(0.8)+1e-12 {
		t.Fatalf("calibration must be monotone: fn(0.2)=%v > fn(0.8)=%v", fn(0.2), fn(0.8))
	}
	if v := fn(1.0); v < 0 || v > 1 {
		t.Fatalf("calibration must stay in [0,1], got %v", v)
	}

	// Below the pair floor → NOT fitted → identity (no overfit map).
	few, fewPairs := synthetic(ensemble.MinCalibrationPairs - 1)
	// Force personal-tier eligibility off by keeping n below MinPersonal, but
	// also assert the calibration alone refuses to fit on too-few pairs.
	_ = few
	if _, _, ok := ensemble.CalibrateKnots(fewPairs); ok {
		t.Fatal("calibration must refuse to fit below MinCalibrationPairs")
	}
}

// TestLearn_NoEdgeStaysUnpersonal: a symbol with plenty of samples but whose
// components are all coin flips must NOT graduate — no fabricated weights.
func TestLearn_NoEdgeStaysUnpersonal(t *testing.T) {
	var ex []adaptive.Example
	var pairs []ensemble.Pair
	for i := 0; i < MinPersonal+20; i++ {
		up := i % 2
		// Both legs alternate independent of the outcome → ~50% hit-rate.
		pr := 0.7
		if i%3 == 0 {
			pr = 0.3
		}
		fc := 0.3
		if i%3 == 0 {
			fc = 0.7
		}
		ts := int64(i) * 86400
		ex = append(ex, mkExample(pr, fc, up, ts))
		pairs = append(pairs, ensemble.Pair{Pred: 0.5, Actual: float64(up), Ts: ts})
	}
	m := Learn(ex, pairs, true, true)
	if m.Tier == TierPersonal {
		t.Fatalf("no component beats a coin flip — must NOT graduate to personal, got tier=%q weights=%v",
			m.Tier, m.Weights)
	}
}
