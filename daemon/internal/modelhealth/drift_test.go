package modelhealth

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

func sample(n int, f func(*rand.Rand) float64, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = f(r)
	}
	return out
}

func TestIdenticalDistributionsDoNotDrift(t *testing.T) {
	a := sample(500, func(r *rand.Rand) float64 { return r.NormFloat64() }, 1)
	b := sample(500, func(r *rand.Rand) float64 { return r.NormFloat64() }, 2)
	d := DriftFor("x", a, b)
	if d.Drifted {
		t.Fatalf("same distribution flagged as drift: KS %.3f > crit %.3f", d.KS, d.Critical)
	}
}

func TestShiftedMeanIsDetected(t *testing.T) {
	a := sample(500, func(r *rand.Rand) float64 { return r.NormFloat64() }, 1)
	b := sample(500, func(r *rand.Rand) float64 { return r.NormFloat64() + 2 }, 2)
	if d := DriftFor("x", a, b); !d.Drifted {
		t.Fatalf("a 2-sigma shift must be detected: KS %.3f crit %.3f", d.KS, d.Critical)
	}
}

// The failure a mean comparison sleeps through, and the reason KS was chosen.
func TestVarianceCollapseWithUnchangedMeanIsDetected(t *testing.T) {
	a := sample(600, func(r *rand.Rand) float64 { return r.NormFloat64() * 3 }, 1)
	b := sample(600, func(r *rand.Rand) float64 { return r.NormFloat64() * 0.2 }, 2)
	meanA, meanB := mean(a), mean(b)
	if math.Abs(meanA-meanB) > 0.5 {
		t.Fatalf("test setup: means should be close, got %.3f vs %.3f", meanA, meanB)
	}
	if d := DriftFor("x", a, b); !d.Drifted {
		t.Fatal("a variance collapse at constant mean must be detected — this is " +
			"precisely what a mean-based check misses")
	}
}

func TestBimodalShiftIsDetected(t *testing.T) {
	a := sample(600, func(r *rand.Rand) float64 { return r.NormFloat64() }, 1)
	b := sample(600, func(r *rand.Rand) float64 {
		if r.Intn(2) == 0 {
			return r.NormFloat64() - 4
		}
		return r.NormFloat64() + 4
	}, 2)
	if d := DriftFor("x", a, b); !d.Drifted {
		t.Fatal("a distribution going bimodal at a similar mean must be detected")
	}
}

func TestThinSamplesClaimNoVerdict(t *testing.T) {
	a := sample(10, func(r *rand.Rand) float64 { return r.NormFloat64() }, 1)
	b := sample(10, func(r *rand.Rand) float64 { return r.NormFloat64() + 50 }, 2)
	d := DriftFor("x", a, b)
	if d.Drifted {
		t.Fatal("10 points per side must not produce a drift verdict, however " +
			"different they look")
	}
}

func TestCriticalValueScalesWithSampleSize(t *testing.T) {
	// A fixed cutoff would flag noise on small samples and miss real drift on
	// large ones — the threshold must tighten as evidence grows.
	small := ksCritical(60, 60)
	large := ksCritical(5000, 5000)
	if !(large < small) {
		t.Fatalf("critical value should shrink with n: %.4f (small) vs %.4f (large)",
			small, large)
	}
}

func TestKSBounds(t *testing.T) {
	if got := KS([]float64{1, 2, 3}, []float64{1, 2, 3}); got != 0 {
		t.Fatalf("identical samples should give KS 0, got %.3f", got)
	}
	if got := KS([]float64{1, 1, 1}, []float64{9, 9, 9}); got != 1 {
		t.Fatalf("disjoint samples should give KS 1, got %.3f", got)
	}
	if got := KS(nil, []float64{1}); got != 0 {
		t.Fatalf("empty input should give 0, got %.3f", got)
	}
}

func TestDriftFractionExcludesUnjudgeableFeatures(t *testing.T) {
	// A feature store full of thin features must not dilute a real drift
	// signal by counting the thin ones as clean.
	rs := []DriftResult{
		{Feature: "a", Drifted: true, RefN: 100, LiveN: 100},
		{Feature: "b", Drifted: false, RefN: 100, LiveN: 100},
		{Feature: "thin1", Drifted: false, RefN: 5, LiveN: 5},
		{Feature: "thin2", Drifted: false, RefN: 5, LiveN: 5},
	}
	if got := DriftFraction(rs); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("drift fraction should be 1 of 2 JUDGED features = 0.5, got %.3f", got)
	}
}

func TestDriftFractionIsZeroWhenNothingIsJudgeable(t *testing.T) {
	rs := []DriftResult{{Feature: "a", RefN: 3, LiveN: 3}}
	if got := DriftFraction(rs); got != 0 {
		t.Fatalf("no judgeable features should yield 0, got %.3f", got)
	}
}

// The whole point: drift must actually move the health score now.
func TestDriftNowAffectsTheHealthScore(t *testing.T) {
	base := Inputs{Observations: 1000, Accuracy: 0.62, BaselineAcc: 0.55,
		RecentAcc: 0.62, RecentN: 300, BrierSkill: 0.05, CalibrationErr: 0.03,
		AgeDays: 5}
	// Ptr(0) explicitly: MEASURED at zero drift, which is what this comparison
	// means. Leaving the field unset used to give the same answer only because
	// "not measured" and "no drift" were the same value — the conflation this
	// package exists to remove. See TestUnmeasuredDriftIsWithheldNotPerfect.
	base.FeatureDriftPct = Ptr(0)
	clean := Grade(base)
	base.FeatureDriftPct = Ptr(0.6)
	drifted := Grade(base)
	if !(drifted.Overall < clean.Overall) {
		t.Fatalf("60%% feature drift must lower the score: %.4f vs %.4f",
			drifted.Overall, clean.Overall)
	}
	if drifted.Components["stability"] >= clean.Components["stability"] {
		t.Fatal("stability component must fall with drift")
	}
}

// THE REGRESSION THAT MATTERS. Every failure path in the caller used to return
// 0 for unmeasurable drift, and 0 scores stability at a PERFECT 1.0 — which is
// verbatim the defect the header of drift.go says this package was written to
// close ("nothing ever computed it, so it was always zero and stability always
// scored a perfect 1.0"), reinstated through the fix's own error path.
//
// Unmeasured must therefore score differently from measured-clean, and must not
// be the best possible answer.
func TestUnmeasuredDriftIsWithheldNotPerfect(t *testing.T) {
	base := Inputs{Observations: 1000, Accuracy: 0.62, BaselineAcc: 0.55,
		RecentAcc: 0.62, RecentN: 300, BrierSkill: 0.05, CalibrationErr: 0.03,
		AgeDays: 5}

	base.FeatureDriftPct = nil
	unmeasured := Grade(base)
	if _, ok := unmeasured.Components["stability"]; ok {
		t.Error("unmeasured drift must WITHHOLD the stability component, not score it")
	}
	var named bool
	for _, r := range unmeasured.Reasons {
		if strings.Contains(r, "feature drift could not be measured") {
			named = true
		}
	}
	if !named {
		t.Errorf("a withheld component must say so in Reasons: %v", unmeasured.Reasons)
	}

	// And it must not silently become the worst answer either: dropping the
	// component renormalises over what WAS measured, so an unmeasurable drift
	// neither promotes nor condemns.
	base.FeatureDriftPct = Ptr(0)
	clean := Grade(base)
	base.FeatureDriftPct = Ptr(1)
	allDrifted := Grade(base)
	if unmeasured.Overall > clean.Overall {
		t.Errorf("unmeasured (%.4f) must not outscore measured-clean (%.4f)",
			unmeasured.Overall, clean.Overall)
	}
	if unmeasured.Overall < allDrifted.Overall {
		t.Errorf("unmeasured (%.4f) must not score below total drift (%.4f)",
			unmeasured.Overall, allDrifted.Overall)
	}
}

// DriftFractionJudged separates "nothing drifted" from "nothing could be
// judged", which DriftFraction alone reports identically as 0.
func TestDriftFractionJudgedSeparatesEmptyFromClean(t *testing.T) {
	tooThin := []DriftResult{{Feature: "a", RefN: 3, LiveN: 3}}
	if frac, judged := DriftFractionJudged(tooThin); frac != 0 || judged != 0 {
		t.Errorf("unjudgeable features: got frac=%.3f judged=%d; want 0,0", frac, judged)
	}
	clean := []DriftResult{{Feature: "a", RefN: 500, LiveN: 500, Drifted: false}}
	if frac, judged := DriftFractionJudged(clean); frac != 0 || judged != 1 {
		t.Errorf("measured-clean feature: got frac=%.3f judged=%d; want 0,1", frac, judged)
	}
}

func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}
