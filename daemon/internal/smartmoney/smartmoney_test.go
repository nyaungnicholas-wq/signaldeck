package smartmoney

import (
	"math"
	"testing"
)

const eps = 1e-9

// factorByKey returns the factor with the given key (t.Fatal if absent).
func factorByKey(t *testing.T, r Result, key string) Factor {
	t.Helper()
	for _, f := range r.Factors {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("expected a %q factor, got factors %+v", key, r.Factors)
	return Factor{}
}

// TestAllAbsent: no positioning data at all → honest absence, not a fake score.
func TestAllAbsent(t *testing.T) {
	r := Score(Inputs{DaysToCover: -1}) // -1 = no short-interest row; everything else zero/false
	if r.Available {
		t.Fatalf("no component present must yield Available:false, got %+v", r)
	}
	if len(r.Factors) != 0 {
		t.Fatalf("absent result must carry no factors, got %+v", r.Factors)
	}
}

// TestRenormalizationSingleComponent: with only insider present, its weight
// renormalizes to 1.0 and the score equals the insider value exactly.
func TestRenormalizationSingleComponent(t *testing.T) {
	r := Score(Inputs{
		InsiderBuys: 1_000_000, InsiderSells: 0, InsiderDistinctBuyers: 1,
		DaysToCover: -1, // squeeze absent
	})
	if !r.Available {
		t.Fatal("insider-only inputs must be Available")
	}
	if len(r.Factors) != 1 {
		t.Fatalf("expected exactly 1 factor, got %+v", r.Factors)
	}
	f := factorByKey(t, r, "insider")
	if math.Abs(f.Weight-1.0) > eps {
		t.Fatalf("sole component weight must renormalize to 1.0, got %v", f.Weight)
	}
	if math.Abs(r.Score-f.Value) > eps {
		t.Fatalf("single-component score must equal that component's value: score=%v value=%v", r.Score, f.Value)
	}
	// buys only, netRatio=1 → strong accumulation.
	if r.Score <= 0.99 || r.Label != "strong_accumulation" {
		t.Fatalf("all-buys should be ~1.0 strong_accumulation, got score=%v label=%q", r.Score, r.Label)
	}
}

// TestRenormalizationMissingComponent: adding a second present component
// renormalizes both weights to sum to 1, and Σ value*weight == score.
func TestRenormalizationMissingComponent(t *testing.T) {
	// Insider (net buy, ratio 0.2) + institutional; squeeze absent.
	r := Score(Inputs{
		InsiderBuys: 60, InsiderSells: 40, InsiderDistinctBuyers: 1,
		DaysToCover:  -1,
		InstManagers: 4, InstNotional: 1.2e9,
	})
	if len(r.Factors) != 2 {
		t.Fatalf("expected 2 factors (insider+institutional), got %+v", r.Factors)
	}
	var sumW, recomputed float64
	for _, f := range r.Factors {
		sumW += f.Weight
		recomputed += f.Value * f.Weight
	}
	if math.Abs(sumW-1.0) > eps {
		t.Fatalf("present weights must renormalize to 1.0, got %v", sumW)
	}
	if math.Abs(recomputed-r.Score) > eps {
		t.Fatalf("score must equal Σ value*weight: score=%v recomputed=%v", r.Score, recomputed)
	}
	// Base weights 0.40 and 0.25 → renormalized 0.615.. and 0.384..
	ins := factorByKey(t, r, "insider")
	if math.Abs(ins.Weight-0.40/0.65) > eps {
		t.Fatalf("insider renormalized weight wrong: got %v want %v", ins.Weight, 0.40/0.65)
	}
}

// TestInsiderClusterBoostsMagnitude: more distinct buyers amplifies a net-buy
// value, and the multiplier is capped at 1.75×.
func TestInsiderClusterBoostsMagnitude(t *testing.T) {
	base := Score(Inputs{InsiderBuys: 60, InsiderSells: 40, InsiderDistinctBuyers: 1, DaysToCover: -1})
	cluster := Score(Inputs{InsiderBuys: 60, InsiderSells: 40, InsiderDistinctBuyers: 3, DaysToCover: -1})
	bv := factorByKey(t, base, "insider").Value
	cv := factorByKey(t, cluster, "insider").Value
	if !(cv > bv) {
		t.Fatalf("a cluster of distinct buyers must boost the value: single=%v cluster=%v", bv, cv)
	}
	// netRatio=0.2, 3 buyers → mult 1.5 → 0.30.
	if math.Abs(cv-0.30) > 1e-9 {
		t.Fatalf("3 distinct buyers on ratio 0.2 should give 0.30, got %v", cv)
	}
	// Cap: 10 buyers → mult would be 3.25, capped 1.75 → 0.35.
	capped := factorByKey(t, Score(Inputs{InsiderBuys: 60, InsiderSells: 40, InsiderDistinctBuyers: 10, DaysToCover: -1}), "insider").Value
	if math.Abs(capped-0.35) > 1e-9 {
		t.Fatalf("cluster multiplier must cap at 1.75× (→0.35), got %v", capped)
	}
}

// TestInsiderClusterNeverRescuesSelling: the cluster boost is buy-only, so a
// distinct-buyer count cannot lift a net-selling value.
func TestInsiderClusterNeverRescuesSelling(t *testing.T) {
	r := Score(Inputs{InsiderBuys: 40, InsiderSells: 60, InsiderDistinctBuyers: 5, DaysToCover: -1})
	f := factorByKey(t, r, "insider")
	if f.Value >= 0 {
		t.Fatalf("net selling must stay negative regardless of buyer count, got %v", f.Value)
	}
	if math.Abs(f.Value-(-0.2)) > 1e-9 {
		t.Fatalf("net-sell ratio must be the raw -0.2 (no cluster amplification), got %v", f.Value)
	}
}

// TestNetSellingIsDistribution: pure open-market selling → negative score and
// a distribution band.
func TestNetSellingIsDistribution(t *testing.T) {
	r := Score(Inputs{InsiderBuys: 0, InsiderSells: 1_000_000, InsiderSellTx: 3, DaysToCover: -1})
	if !r.Available || r.Score >= 0 {
		t.Fatalf("all-sells must be a present, negative score, got %+v", r)
	}
	if r.Label != "strong_distribution" {
		t.Fatalf("net ratio -1 should be strong_distribution, got %q (score %v)", r.Label, r.Score)
	}
}

// TestSqueezeOneDirectional: squeeze fuel is never negative — a very negative
// short-volume z (or low days-to-cover) is "no fuel", not bearish.
func TestSqueezeOneDirectional(t *testing.T) {
	// Only squeeze present, with strongly NEGATIVE short-volume z.
	r := Score(Inputs{DaysToCover: -1, ShortVolZ: -5, ShortVolZOK: true})
	f := factorByKey(t, r, "squeeze")
	if f.Value < 0 {
		t.Fatalf("squeeze fuel must never be negative, got %v", f.Value)
	}
	if f.Value != 0 {
		t.Fatalf("a very negative short-vol z is zero fuel, got %v", f.Value)
	}
	if r.Score < 0 {
		t.Fatalf("a squeeze-only symbol must not produce a negative score, got %v", r.Score)
	}

	// A high days-to-cover produces positive fuel.
	hi := Score(Inputs{DaysToCover: 10})
	if factorByKey(t, hi, "squeeze").Value <= 0 {
		t.Fatalf("high days-to-cover must give positive squeeze fuel, got %+v", hi.Factors)
	}
}

// TestSqueezeMeanOfPresentParts: the value averages ONLY the present sub-parts.
func TestSqueezeMeanOfPresentParts(t *testing.T) {
	// dtc=10 → 1.0; shortVolZ=1.5 → 0.5; funding absent. mean = 0.75.
	r := Score(Inputs{DaysToCover: 10, ShortVolZ: 1.5, ShortVolZOK: true})
	f := factorByKey(t, r, "squeeze")
	if math.Abs(f.Value-0.75) > 1e-9 {
		t.Fatalf("squeeze mean of {1.0,0.5} should be 0.75, got %v", f.Value)
	}
}

// TestSqueezeCryptoFunding: very negative perp funding (crowded shorts) is fuel.
func TestSqueezeCryptoFunding(t *testing.T) {
	// funding -0.0025 → -(-0.0025)*200 = 0.5.
	r := Score(Inputs{DaysToCover: -1, Funding: -0.0025, FundingOK: true})
	f := factorByKey(t, r, "squeeze")
	if math.Abs(f.Value-0.5) > 1e-9 {
		t.Fatalf("funding -0.0025 should give 0.5 fuel, got %v", f.Value)
	}
	// Positive funding (crowded longs) is not squeeze fuel.
	pos := Score(Inputs{DaysToCover: -1, Funding: 0.0025, FundingOK: true})
	if factorByKey(t, pos, "squeeze").Value != 0 {
		t.Fatalf("positive funding is not squeeze fuel, got %v", factorByKey(t, pos, "squeeze").Value)
	}
}

// TestInstitutionalBounded: the institutional value is bounded to ≤0.4 no
// matter how many managers hold the name.
func TestInstitutionalBounded(t *testing.T) {
	r := Score(Inputs{DaysToCover: -1, InstManagers: 1_000_000, InstNotional: 5e11})
	f := factorByKey(t, r, "institutional")
	if f.Value > 0.4+eps {
		t.Fatalf("institutional value must be capped at 0.4, got %v", f.Value)
	}
	if math.Abs(f.Value-0.4) > eps {
		t.Fatalf("a huge manager count should saturate at 0.4, got %v", f.Value)
	}
	// A single manager is small and positive.
	one := factorByKey(t, Score(Inputs{DaysToCover: -1, InstManagers: 1}), "institutional").Value
	if !(one > 0 && one < 0.2) {
		t.Fatalf("one manager should be a small positive value, got %v", one)
	}
}

// TestLabelBands checks every band boundary of label().
func TestLabelBands(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{0.8, "strong_accumulation"},
		{0.5, "strong_accumulation"},
		{0.49, "accumulation"},
		{0.2, "accumulation"},
		{0.19, "neutral"},
		{0.0, "neutral"},
		{-0.19, "neutral"},
		{-0.2, "distribution"}, // -0.2 is NOT > -0.2 → distribution
		{-0.49, "distribution"},
		{-0.5, "strong_distribution"}, // -0.5 is NOT > -0.5 → strong_distribution
		{-0.9, "strong_distribution"},
	}
	for _, c := range cases {
		if got := label(c.score); got != c.want {
			t.Errorf("label(%v) = %q, want %q", c.score, got, c.want)
		}
	}
}

// TestClamp guards the small pure helper the whole engine rests on.
func TestClamp(t *testing.T) {
	if clamp(-2, -1, 1) != -1 || clamp(2, -1, 1) != 1 || clamp(0.3, -1, 1) != 0.3 {
		t.Fatal("clamp failed at a boundary")
	}
}

// TestScoreEqualsFactorSum is the transparency invariant: for any present mix,
// Score is exactly Σ Value*Weight over the returned factors.
func TestScoreEqualsFactorSum(t *testing.T) {
	r := Score(Inputs{
		InsiderBuys: 500_000, InsiderSells: 100_000, InsiderDistinctBuyers: 2, InsiderSellTx: 1,
		DaysToCover: 6, ShortVolZ: 2.0, ShortVolZOK: true,
		InstManagers: 8, InstNotional: 3.3e9,
	})
	if len(r.Factors) != 3 {
		t.Fatalf("expected all 3 factors present, got %+v", r.Factors)
	}
	var sum float64
	for _, f := range r.Factors {
		sum += f.Value * f.Weight
	}
	if math.Abs(sum-r.Score) > eps {
		t.Fatalf("score must equal Σ value*weight exactly: score=%v sum=%v", r.Score, sum)
	}
}
