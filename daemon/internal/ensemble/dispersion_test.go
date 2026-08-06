package ensemble

import (
	"math"
	"testing"
)

// band builds n probabilities spread evenly over [lo,hi] using `distinct`
// separate values — the two knobs the gate actually reads, set independently so
// a fixture can be wide-but-collapsed or many-valued-but-flat.
func band(n, distinct int, lo, hi float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		step := i % distinct
		if distinct == 1 {
			out[i] = lo
			continue
		}
		out[i] = lo + (hi-lo)*float64(step)/float64(distinct-1)
	}
	return out
}

// Every fixture below is a real pass off the live record, measured 2026-08-06
// from the predictions table. If the gate ever stops classifying these the way
// the day actually behaved, the constants moved and the reason is in the diff.
func TestCrossSectionGateOnLiveDays(t *testing.T) {
	cases := []struct {
		day      string
		n        int
		distinct int
		lo, hi   float64
		want     bool
		why      string
	}{
		// ---- collapsed regime: calibration map had degenerated ----
		{"2026-07-27 1d", 331, 8, 0.511, 0.514, false,
			"8 values across 331 symbols, 0.003 wide, called 100% UP"},
		{"2026-07-31 1d", 329, 8, 0.417, 0.425, false, "1.5% UP, spread 0.008"},
		{"2026-08-01 1d", 329, 6, 0.423, 0.443, false, "6 distinct values"},
		{"2026-08-02 1d", 329, 8, 0.446, 0.463, false, "8 distinct values"},

		// THE CASE A SPREAD TEST ALONE MISSES. 0.066 clears MinCrossSectionSpread
		// yet 5 values across 329 symbols is not a per-symbol forecast, and this
		// day called 99.1% of the universe UP.
		{"2026-08-03 1d", 329, 5, 0.515, 0.581, false,
			"spread 0.066 passes, but 5 distinct values must not"},

		// THE CASE A DISTINCT-COUNT TEST ALONE MISSES. 137 values is plenty; they
		// all sit inside a band 0.010 wide and called 98.6% of the universe DOWN.
		{"2026-07-15 1d", 1048, 137, 0.474, 0.484, false,
			"137 distinct values passes, but a 0.010 band must not"},

		// ---- repaired regime: 2026-08-05 calibration fix ----
		{"2026-08-05 1d", 329, 326, 0.360, 0.656, true, "326 values, spread 0.296"},
		{"2026-08-06 1d", 325, 325, 0.372, 0.614, true, "325 values, spread 0.242"},
		{"2026-08-05 1w", 329, 328, 0.386, 0.608, true, "328 values, spread 0.222"},

		// A genuine bearish VIEW: differentiated, narrow, mostly one-sided. Must
		// publish — agreement is the grader's problem, not the gate's.
		{"2026-08-06 1w", 329, 326, 0.456, 0.530, true,
			"92% called down, but 326 distinct values over 0.074 is a view"},
	}
	for _, c := range cases {
		cs := MeasureCrossSection(band(c.n, c.distinct, c.lo, c.hi))
		got, reason := cs.Usable()
		if got != c.want {
			t.Errorf("%s: Usable()=%v want %v (%s)\n  measured n=%d distinct=%d spread=%.4f agreement=%.3f reason=%q",
				c.day, got, c.want, c.why, cs.N, cs.Distinct, cs.Spread, cs.Agreement, reason)
		}
		if !got && reason == "" {
			t.Errorf("%s: refused with no reason", c.day)
		}
	}
}

// The gate must never fire on agreement alone. A perfectly differentiated
// cross-section that happens to point one way is a view, and censoring it would
// make the model hide from markets it has an opinion about.
func TestUnanimousButDifferentiatedStillPublishes(t *testing.T) {
	// 300 distinct values, all above 0.5 — 100% agreement, real dispersion.
	cs := MeasureCrossSection(band(300, 300, 0.55, 0.95))
	if ok, reason := cs.Usable(); !ok {
		t.Fatalf("a differentiated unanimous view must publish, refused: %s", reason)
	}
	if cs.Agreement != 1.0 {
		t.Fatalf("agreement should be reported as 1.0, got %.3f", cs.Agreement)
	}
}

func TestEmptyAndDegenerateInputs(t *testing.T) {
	if ok, _ := MeasureCrossSection(nil).Usable(); ok {
		t.Error("an empty cross-section must not be usable")
	}
	// Every symbol on one number: the pathology in its purest form.
	cs := MeasureCrossSection(band(400, 1, 0.47, 0.47))
	if ok, _ := cs.Usable(); ok {
		t.Error("a constant cross-section must not be usable")
	}
	if cs.Spread != 0 || cs.Distinct != 1 {
		t.Errorf("constant: spread=%.4f distinct=%d, want 0 and 1", cs.Spread, cs.Distinct)
	}
}

// The small-universe floor: 12 symbols cannot be asked for n/20 distinct values
// (that would be 0) or the gate becomes a no-op exactly where it is cheapest to
// collapse.
func TestSmallUniverseUsesAbsoluteFloor(t *testing.T) {
	cs := MeasureCrossSection(band(12, 3, 0.2, 0.8))
	if ok, _ := cs.Usable(); ok {
		t.Error("3 distinct values must fail the absolute floor of 10")
	}
}

// A fleet SMALLER than the distinct floor must not be judged by it. Demanding 10
// distinct values from 5 symbols is a condition nothing can ever satisfy, and a
// gate in that state stops the predictor permanently rather than protecting it.
// Caught by three existing pipeline tests that run a one-symbol universe.
func TestUniverseBelowFloorIsNotJudged(t *testing.T) {
	for _, n := range []int{1, 2, 5, 9} {
		cs := MeasureCrossSection(band(n, 1, 0.47, 0.47))
		if ok, reason := cs.Usable(); !ok {
			t.Errorf("n=%d: gate must not apply below the floor, refused: %s", n, reason)
		}
	}
	// At the floor the rule engages again, and a constant fleet fails it.
	cs := MeasureCrossSection(band(10, 1, 0.47, 0.47))
	if ok, _ := cs.Usable(); ok {
		t.Error("n=10 constant must be refused — the rule applies from the floor up")
	}
}

func TestQuantileInterpolates(t *testing.T) {
	s := []float64{0, 0.25, 0.5, 0.75, 1}
	if got := quantile(s, 0.5); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("median = %v, want 0.5", got)
	}
	if got := quantile(s, 0); got != 0 {
		t.Errorf("q0 = %v, want 0", got)
	}
	if got := quantile(s, 1); got != 1 {
		t.Errorf("q1 = %v, want 1", got)
	}
}
