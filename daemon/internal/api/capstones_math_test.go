package api

import (
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The pure helpers behind /api/graph and /api/scenario. They decide which edges
// exist in the knowledge graph and how heavy they are, so a wrong result here
// does not error -- it silently publishes a relationship that is not there.
// None of them had a test.

// TestMeanF_EmptyIsZeroNotNaN pins the empty case. Dividing by a zero length
// would return NaN, and NaN propagates: it would reach pearsonCommon's means
// and poison every correlation computed from that series rather than failing
// anywhere a reader would notice.
func TestMeanF_EmptyIsZeroNotNaN(t *testing.T) {
	if got := meanF(nil); got != 0 {
		t.Fatalf("meanF(nil) = %v, want 0", got)
	}
	if got := meanF([]float64{}); got != 0 {
		t.Fatalf("meanF(empty) = %v, want 0", got)
	}
	if got := meanF([]float64{1, 2, 3, 4}); got != 2.5 {
		t.Fatalf("got = %v, want %v", got, 2.5)
	}
}

// TestMkEdge_OrdersEndpoints pins the endpoint ordering. An edge discovered
// from NVDA to AMD and one discovered from AMD to NVDA are the same edge; if
// the ordering were dropped the graph would carry both and every co-movement
// would be double counted.
func TestMkEdge_OrdersEndpoints(t *testing.T) {
	forward := mkEdge("NVDA", "AMD", "correlation", 0.8)
	reverse := mkEdge("AMD", "NVDA", "correlation", 0.8)
	if forward != reverse {
		t.Fatalf("got = %+v, want %+v (endpoints must be ordered)", forward, reverse)
	}
	if forward.A != "AMD" || forward.B != "NVDA" {
		t.Fatalf("got A=%v B=%v, want A=AMD B=NVDA", forward.A, forward.B)
	}
	if forward.Kind != "correlation" || forward.Weight != 0.8 {
		t.Fatalf("got Kind=%v Weight=%v, want correlation/0.8", forward.Kind, forward.Weight)
	}
}

// TestPearsonCommon_UsesOnlySharedDays pins the intersection. Day 4 exists in
// one series only; if it were carried into the calculation the correlation
// would not be 1.0 and n would not be 3, so this fails loudly if the shared-day
// filter is ever dropped.
func TestPearsonCommon_UsesOnlySharedDays(t *testing.T) {
	a := map[int64]float64{1: 1, 2: 2, 3: 3, 4: 99}
	b := map[int64]float64{1: 1, 2: 2, 3: 3}
	corr, n, ok := pearsonCommon(a, b)
	if !ok {
		t.Fatalf("got ok = %v, want true", ok)
	}
	if n != 3 {
		t.Fatalf("got n = %v, want 3", n)
	}
	if math.Abs(corr-1.0) > 1e-9 {
		t.Fatalf("got = %v, want ~1.0", corr)
	}
}

// TestPearsonCommon_RefusesThinAndFlat pins both refusals. A flat series has
// zero variance, so the denominator is zero; returning a correlation there
// would be inventing a relationship out of a constant. Thin overlap is the
// same judgement about sample size.
func TestPearsonCommon_RefusesThinAndFlat(t *testing.T) {
	tests := []struct {
		name   string
		a, b   map[int64]float64
		wantN  int
		wantOk bool
	}{
		{"two shared days", map[int64]float64{1: 1, 2: 2}, map[int64]float64{1: 1, 2: 2}, 2, false},
		{"a is flat", map[int64]float64{1: 5, 2: 5, 3: 5}, map[int64]float64{1: 1, 2: 2, 3: 3}, 3, false},
		{"b is flat", map[int64]float64{1: 1, 2: 2, 3: 3}, map[int64]float64{1: 5, 2: 5, 3: 5}, 3, false},
		{"no shared days", map[int64]float64{1: 1}, map[int64]float64{2: 2}, 0, false},
	}
	for _, tt := range tests {
		corr, n, ok := pearsonCommon(tt.a, tt.b)
		if ok != tt.wantOk {
			t.Fatalf("%s: got ok = %v, want %v", tt.name, ok, tt.wantOk)
		}
		if n != tt.wantN {
			t.Fatalf("%s: got n = %v, want %v", tt.name, n, tt.wantN)
		}
		if corr != 0 {
			t.Fatalf("%s: got corr = %v, want 0 on a refusal", tt.name, corr)
		}
	}
}

// TestPearsonCommon_NegativeCorrelation pins the sign. A sign error would
// invert every edge in the graph while still producing plausible magnitudes,
// which is the failure a magnitude-only assertion would miss.
func TestPearsonCommon_NegativeCorrelation(t *testing.T) {
	a := map[int64]float64{1: 1, 2: 2, 3: 3}
	b := map[int64]float64{1: 3, 2: 2, 3: 1}
	corr, _, ok := pearsonCommon(a, b)
	if !ok {
		t.Fatalf("got ok = %v, want true", ok)
	}
	if math.Abs(corr+1.0) > 1e-9 {
		t.Fatalf("got = %v, want ~-1.0", corr)
	}
}

// TestJaccard_OverlapAndEmpties pins the overlap ratio used for 13F
// co-ownership edges. The empty cases matter most: a symbol with no known
// holders must score 0 against everything, not divide by a zero union.
func TestJaccard_OverlapAndEmpties(t *testing.T) {
	a := map[string]bool{"a": true, "b": true}
	b := map[string]bool{"b": true, "c": true}
	if got := jaccard(a, b); math.Abs(got-1.0/3.0) > 1e-9 {
		t.Fatalf("got = %v, want ~0.3333", got)
	}
	if got := jaccard(a, map[string]bool{"a": true, "b": true}); got != 1 {
		t.Fatalf("identical sets: got = %v, want 1", got)
	}
	if got := jaccard(a, map[string]bool{"x": true}); got != 0 {
		t.Fatalf("disjoint: got = %v, want 0", got)
	}
	if got := jaccard(a, map[string]bool{}); got != 0 {
		t.Fatalf("empty b: got = %v, want 0", got)
	}
	if got := jaccard(map[string]bool{}, b); got != 0 {
		t.Fatalf("empty a: got = %v, want 0", got)
	}
}

// TestStddevReturns_SkipsZeroCloseAndNeedsTwo pins the guard against a zero
// previous close. A single bad bar in a real series would otherwise divide by
// zero and hand an Inf to the scenario endpoint, which renders it as a number.
func TestStddevReturns_SkipsZeroCloseAndNeedsTwo(t *testing.T) {
	bar := func(c float64) md.Bar { return md.Bar{Close: c} }

	if got := stddevReturns([]md.Bar{bar(100)}); got != 0 {
		t.Fatalf("one bar: got = %v, want 0", got)
	}
	if got := stddevReturns([]md.Bar{bar(100), bar(110)}); got != 0 {
		t.Fatalf("one return: got = %v, want 0", got)
	}

	// A zero close mid-series must be skipped, not divided by.
	got := stddevReturns([]md.Bar{bar(100), bar(0), bar(110), bar(121)})
	if math.IsInf(got, 0) || math.IsNaN(got) {
		t.Fatalf("zero close produced %v, want a finite number", got)
	}

	// 100 -> 110 -> 121 is +10%% twice, so the sample stddev is exactly 0.
	if got := stddevReturns([]md.Bar{bar(100), bar(110), bar(121)}); math.Abs(got) > 1e-12 {
		t.Fatalf("constant returns: got = %v, want ~0", got)
	}
}

// TestSignedLabel_LeadingSpaceBothSigns pins the leading space, which the
// caller concatenates straight onto a label -- dropping it silently joins the
// number to the preceding word. Zero counts as non-negative.
func TestSignedLabel_LeadingSpaceBothSigns(t *testing.T) {
	for _, tt := range []struct {
		in   float64
		want string
	}{
		{10, " +10"},
		{-0.25, " -0.25"},
		{0, " +0"},
	} {
		if got := signedLabel(tt.in); got != tt.want {
			t.Fatalf("signedLabel(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
