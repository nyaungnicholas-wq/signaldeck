package postmortem

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// A miss with zero recorded evidence must attribute to Unexplained — the honest
// default. This is the most important guarantee: we never invent a cause.
func TestClassify_UnexplainedWhenNoEvidence(t *testing.T) {
	c := Case{Prob: 0.62, Up: 0, FwdReturn: -0.03, Disagreement: 0.2}
	r := Classify(c, DefaultThresholds())
	if r.Primary != ReasonUnexplained {
		t.Fatalf("want unexplained, got %s", r.Primary)
	}
	if len(r.Reasons) != 1 || !approx(r.Reasons[0].Weight, 1.0) {
		t.Fatalf("unexplained should be sole reason weight 1, got %+v", r.Reasons)
	}
	if !approx(r.Conviction, 0.12) || !approx(r.Magnitude, 0.03) {
		t.Fatalf("conviction/magnitude wrong: %+v", r)
	}
}

// A regime change in-window is the strongest single cause and must rank primary.
func TestClassify_RegimeShiftPrimary(t *testing.T) {
	c := Case{
		Prob: 0.7, Up: 0, FwdReturn: -0.05,
		Disagreement: 0.2, RegimeChanged: true, RegimeNote: "uptrend→downtrend",
	}
	r := Classify(c, DefaultThresholds())
	if r.Primary != ReasonRegimeShift {
		t.Fatalf("want regime_shift primary, got %s", r.Primary)
	}
}

// Weights must be normalized to sum to 1 across all emitted reasons so
// clustering can average them meaningfully.
func TestClassify_WeightsNormalized(t *testing.T) {
	c := Case{
		Prob: 0.75, Up: 0, FwdReturn: -0.06,
		Disagreement:  0.02, // triggers model_disagreement (conviction 0.25 >= min)
		RegimeChanged: true,
		NewsAgainst:   true, NewsMag: 0.6,
		VIXRegime:  3,
		CalibKnown: true, CalibDeficit: -0.15,
		NUsed: 5, // triggers data_quality
	}
	r := Classify(c, DefaultThresholds())
	var sum float64
	for _, x := range r.Reasons {
		sum += x.Weight
	}
	if !approx(sum, 1.0) {
		t.Fatalf("weights must sum to 1, got %v (%+v)", sum, r.Reasons)
	}
	// all six evidence-backed reasons should be present (not unexplained)
	if len(r.Reasons) != 6 {
		t.Fatalf("want 6 reasons, got %d: %+v", len(r.Reasons), r.Reasons)
	}
	for _, x := range r.Reasons {
		if x.Code == ReasonUnexplained {
			t.Fatal("unexplained must not appear alongside real evidence")
		}
	}
}

// Model disagreement must require a directional call: a near-coin-flip prob
// (below MinConviction) must NOT emit disagreement even with split legs.
func TestClassify_DisagreementNeedsConviction(t *testing.T) {
	c := Case{Prob: 0.53, Up: 0, FwdReturn: -0.01, Disagreement: 0.0}
	r := Classify(c, DefaultThresholds())
	for _, x := range r.Reasons {
		if x.Code == ReasonModelDisagreement {
			t.Fatal("disagreement should not fire below MinConviction")
		}
	}
}

// Calibration deficit only counts as evidence when it was actually measured,
// never on an unknown (zero) deficit.
func TestClassify_CalibUnknownIsNotEvidence(t *testing.T) {
	c := Case{Prob: 0.7, Up: 0, FwdReturn: -0.04, Disagreement: 0.3, CalibKnown: false, CalibDeficit: 0}
	r := Classify(c, DefaultThresholds())
	if r.Primary != ReasonUnexplained {
		t.Fatalf("unknown calibration must not manufacture a cause; got %s", r.Primary)
	}
}

// Determinism: identical input yields byte-identical reason ordering.
func TestClassify_Deterministic(t *testing.T) {
	c := Case{Prob: 0.7, Up: 0, FwdReturn: -0.05, Disagreement: 0.02, RegimeChanged: true, VIXRegime: 3}
	a := Classify(c, DefaultThresholds())
	b := Classify(c, DefaultThresholds())
	if len(a.Reasons) != len(b.Reasons) {
		t.Fatal("nondeterministic length")
	}
	for i := range a.Reasons {
		if a.Reasons[i] != b.Reasons[i] {
			t.Fatalf("nondeterministic order at %d: %+v vs %+v", i, a.Reasons[i], b.Reasons[i])
		}
	}
}

// Clustering groups by primary reason, sorts by descending count, and computes
// shares that sum to 1.
func TestClusterReports(t *testing.T) {
	reps := []Report{
		{Primary: ReasonUnexplained, Reasons: []Reason{{ReasonUnexplained, 1, ""}}, Magnitude: 0.02},
		{Primary: ReasonUnexplained, Reasons: []Reason{{ReasonUnexplained, 1, ""}}, Magnitude: 0.04},
		{Primary: ReasonRegimeShift, Reasons: []Reason{{ReasonRegimeShift, 0.6, ""}}, Magnitude: 0.05},
	}
	cs := ClusterReports(reps)
	if len(cs) != 2 {
		t.Fatalf("want 2 clusters, got %d", len(cs))
	}
	if cs[0].Code != ReasonUnexplained || cs[0].Count != 2 {
		t.Fatalf("biggest cluster wrong: %+v", cs[0])
	}
	if !approx(cs[0].Share, 2.0/3.0) {
		t.Fatalf("share wrong: %v", cs[0].Share)
	}
	if !approx(cs[0].MeanMag, 0.03) {
		t.Fatalf("mean mag wrong: %v", cs[0].MeanMag)
	}
	var share float64
	for _, c := range cs {
		share += c.Share
	}
	if !approx(share, 1.0) {
		t.Fatalf("shares must sum to 1, got %v", share)
	}
}

func TestIsWrong(t *testing.T) {
	cases := []struct {
		prob float64
		up   int
		want bool
	}{
		{0.7, 1, false}, // predicted up, went up → right
		{0.7, 0, true},  // predicted up, went down → wrong
		{0.3, 0, false}, // predicted down, went down → right
		{0.3, 1, true},  // predicted down, went up → wrong
		{0.5, 0, false}, // no call → never wrong
		{0.5, 1, false},
	}
	for _, c := range cases {
		if got := IsWrong(c.prob, c.up); got != c.want {
			t.Fatalf("IsWrong(%v,%d)=%v want %v", c.prob, c.up, got, c.want)
		}
	}
}
