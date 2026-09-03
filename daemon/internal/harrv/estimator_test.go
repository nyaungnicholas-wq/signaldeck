package harrv

import (
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func bar(ts int64, o, h, l, c float64) md.Bar {
	return md.Bar{Ts: ts, Open: o, High: h, Low: l, Close: c, Volume: 1}
}

const tol = 1e-12

// When the close equals the open the Garman-Klass close-open term vanishes and
// GK collapses to 0.5*ln(H/L)^2. Asserted against a hand-chosen ratio rather
// than by re-running the implementation's own expression, which would only
// prove the code equals itself.
//
// H/L = e exactly, so ln(H/L) = 1 and the intraday variance must be 0.5.
func TestGarmanKlassIdentityAtFlatClose(t *testing.T) {
	lo := 100.0
	hi := lo * math.E
	// open == close == the geometric mid, so ln(C/O) == 0 and both sit inside [L,H]
	mid := lo * math.Sqrt(math.E)
	bars := []md.Bar{
		bar(0, mid, hi, lo, mid), // seed: supplies Cprev
		bar(1, mid, hi, lo, mid), // open == Cprev, so the overnight term is 0 too
	}
	rv, _, x := RVSeries(bars)
	if got, want := rv[1], 0.5; math.Abs(got-want) > 1e-9 {
		t.Errorf("RV = %.12f, want %.12f (overnight 0 + GK 0.5)", got, want)
	}
	if x.GKNegative != 0 || x.FlatRange != 0 || x.Overnight != 0 {
		t.Errorf("unexpected exclusions on a clean bar: %+v", x)
	}
}

// The overnight gap is genuinely added, not dropped. With a flat intraday bar
// the only variance left is the gap, so RV must equal ln(O/Cprev)^2 exactly --
// except a flat intraday bar is H==L, which is refused. So use a real range and
// check the DIFFERENCE between a gapped and an ungapped copy of the same bar.
func TestOvernightTermIsAdded(t *testing.T) {
	lo, hi := 100.0, 110.0
	c := 105.0
	flat := []md.Bar{bar(0, c, hi, lo, c), bar(1, c, hi, lo, c)}
	// same bar, but the previous close is 5% lower, so O/Cprev = 1/0.95
	prev := c * 0.95
	gapped := []md.Bar{bar(0, prev, hi, lo, prev), bar(1, c, hi, lo, c)}

	flatRV, _, _ := RVSeries(flat)
	gapRV, _, _ := RVSeries(gapped)

	gap := math.Log(c / prev)
	if got, want := gapRV[1]-flatRV[1], gap*gap; math.Abs(got-want) > 1e-12 {
		t.Errorf("gap contribution = %.12f, want ln(O/Cprev)^2 = %.12f", got, want)
	}
}

// A hole must be NaN, never zero. Zero variance asserts that nothing moved,
// which is a different and false claim, and it would also make ln(RV) explode
// downstream instead of being skipped.
func TestGuardsProduceNaNAndAreCounted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bars  []md.Bar
		field func(Exclusions) int
	}{
		{
			"flat range (H == L) is unestimable, not zero variance",
			[]md.Bar{bar(0, 100, 101, 99, 100), bar(1, 100, 100, 100, 100)},
			func(x Exclusions) int { return x.FlatRange },
		},
		{
			"overnight jump beyond the split guard",
			// ln(O/Cprev) = ln(2) = 0.693 > 0.65: an unadjusted 2:1 split
			[]md.Bar{bar(0, 100, 101, 99, 100), bar(1, 200, 210, 195, 205)},
			func(x Exclusions) int { return x.Overnight },
		},
		{
			"non-positive price",
			[]md.Bar{bar(0, 100, 101, 99, 100), bar(1, 0, 101, 99, 100)},
			func(x Exclusions) int { return x.NonPos },
		},
		{
			"inverted range (H < L) is corrupt input",
			[]md.Bar{bar(0, 100, 101, 99, 100), bar(1, 100, 99, 101, 100)},
			func(x Exclusions) int { return x.NonPos },
		},
	} {
		rv, _, x := RVSeries(tc.bars)
		if !math.IsNaN(rv[1]) {
			t.Errorf("%s: rv = %v, want NaN", tc.name, rv[1])
		}
		if tc.field(x) != 1 {
			t.Errorf("%s: exclusion not counted (%+v)", tc.name, x)
		}
	}
}

// Just inside the guard must still be estimated. A guard that also rejects the
// legitimate extreme is an outage, not a guard.
func TestOvernightGuardBoundary(t *testing.T) {
	prev := 100.0
	just := prev * math.Exp(OvernightGuard*0.999) // inside
	over := prev * math.Exp(OvernightGuard*1.001) // outside

	in := []md.Bar{bar(0, prev, prev*1.01, prev*0.99, prev), bar(1, just, just*1.01, just*0.99, just)}
	out := []md.Bar{bar(0, prev, prev*1.01, prev*0.99, prev), bar(1, over, over*1.01, over*0.99, over)}

	if rv, _, _ := RVSeries(in); math.IsNaN(rv[1]) {
		t.Error("a move just inside OvernightGuard was refused")
	}
	if rv, _, _ := RVSeries(out); !math.IsNaN(rv[1]) {
		t.Error("a move just outside OvernightGuard was accepted")
	}
}

// GK is non-negative for any CONSISTENT bar, because O and C both lie in [L,H]
// so |ln(C/O)| <= ln(H/L) and 0.5*x^2 - 0.386*y^2 >= 0.114*x^2 when y^2 <= x^2.
// The Parkinson fallback therefore only fires on INCONSISTENT input -- a close
// outside the day's range -- which is a data-integrity case, not a market one.
// Worth pinning: if this ever fires on real bars, the bars are wrong.
func TestParkinsonFallbackOnlyOnInconsistentBars(t *testing.T) {
	// close far outside [L,H] drives the close-open term above the range term
	bars := []md.Bar{
		bar(0, 100, 100.5, 99.5, 100),
		bar(1, 100, 100.001, 99.999, 130), // C way above H
	}
	rv, _, x := RVSeries(bars)
	if x.GKNegative != 1 {
		t.Fatalf("expected the Parkinson fallback to fire, got %+v", x)
	}
	if math.IsNaN(rv[1]) || rv[1] <= 0 {
		t.Errorf("fallback produced %v, want a positive variance", rv[1])
	}
}

func TestFloorKeepsLogDefined(t *testing.T) {
	// a range so tiny the variance lands under MinVariance
	bars := []md.Bar{
		bar(0, 100, 100.0000001, 99.9999999, 100),
		bar(1, 100, 100.0000001, 99.9999999, 100),
	}
	rv, _, x := RVSeries(bars)
	if rv[1] != MinVariance || x.Floored != 1 {
		t.Errorf("rv = %v floored = %d, want %v and 1", rv[1], x.Floored, MinVariance)
	}
	if math.IsInf(math.Log(rv[1]), -1) {
		t.Error("ln(RV) is -Inf after flooring, which is the thing the floor prevents")
	}
}

func TestDegenerateInputsDoNotPanic(t *testing.T) {
	if rv, ts, _ := RVSeries(nil); len(rv) != 0 || len(ts) != 0 {
		t.Error("nil input should produce empty slices")
	}
	rv, _, x := RVSeries([]md.Bar{bar(0, 1, 2, 1, 1)})
	if len(rv) != 1 || !math.IsNaN(rv[0]) {
		t.Error("a single bar has no previous close and must be NaN")
	}
	if x.NoPrev != 1 {
		t.Errorf("NoPrev not counted: %+v", x)
	}
}

// THE LOOKAHEAD TEST. The value at index i must be identical whether the series
// is truncated at i+1 or runs to the end, and it must not move when everything
// after i is replaced with values chosen to flip the answer.
//
// This is the shape of internal/structregime/naivebaseline_test.go's truncation
// invariance check, and its reasoning applies verbatim: a baseline that peeks at
// the future fails silently -- it makes the model look better or worse with no
// symptom anywhere.
func TestTruncationInvariance(t *testing.T) {
	base := make([]md.Bar, 60)
	px := 100.0
	for i := range base {
		px *= 1 + float64(i%7-3)/200
		base[i] = bar(int64(i), px, px*1.02, px*0.98, px*1.005)
	}
	full, _, _ := RVSeries(base)

	for _, cut := range []int{25, 40, 59} {
		trunc, _, _ := RVSeries(base[:cut+1])
		for i := 0; i <= cut; i++ {
			a, b := full[i], trunc[i]
			if math.IsNaN(a) != math.IsNaN(b) || (!math.IsNaN(a) && a != b) {
				t.Fatalf("rv[%d] changed when truncated at %d: %v vs %v", i, cut, a, b)
			}
		}
		// Rolling aggregates must be invariant too, including against a future
		// deliberately rigged to be enormous.
		poisoned := append([]md.Bar(nil), base...)
		for i := cut + 1; i < len(poisoned); i++ {
			poisoned[i] = bar(int64(i), 1000, 5000, 10, 20)
		}
		prv, _, _ := RVSeries(poisoned)
		for _, w := range []int{5, 22} {
			want, okA := RollingMean(full, cut, w, 1)
			got, okB := RollingMean(prv, cut, w, 1)
			if okA != okB || (okA && math.Abs(want-got) > tol) {
				t.Errorf("RollingMean(w=%d) at %d moved when the FUTURE changed: %v vs %v",
					w, cut, want, got)
			}
		}
	}
}

func TestRollingMeanSkipsNaNAndEnforcesMinCount(t *testing.T) {
	nan := math.NaN()
	s := []float64{1, nan, 3, nan, 5}

	if got, ok := RollingMean(s, 4, 5, 3); !ok || math.Abs(got-3) > tol {
		t.Errorf("mean of 1,3,5 = %v ok=%v, want 3", got, ok)
	}
	if _, ok := RollingMean(s, 4, 5, 4); ok {
		t.Error("minCount=4 with only 3 real values should refuse")
	}
	// a window running off the start is not a short window, it is no window
	if _, ok := RollingMean(s, 1, 5, 1); ok {
		t.Error("a window extending before index 0 should refuse")
	}
	for _, bad := range [][3]int{{-1, 5, 1}, {99, 5, 1}, {4, 0, 1}, {4, -3, 1}} {
		if _, ok := RollingMean(s, bad[0], bad[1], bad[2]); ok {
			t.Errorf("RollingMean(i=%d,w=%d) should refuse", bad[0], bad[1])
		}
	}
}

func TestCount(t *testing.T) {
	nan := math.NaN()
	if got := Count([]float64{1, nan, 3, nan}); got != 2 {
		t.Errorf("Count = %d, want 2", got)
	}
	if got := Count(nil); got != 0 {
		t.Errorf("Count(nil) = %d, want 0", got)
	}
}
