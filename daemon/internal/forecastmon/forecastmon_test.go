package forecastmon

import (
	"context"
	"strings"
	"testing"
	"time"
)

// stubSource replays a measured record without a database.
type stubSource struct {
	days    []DayStat
	buckets []Bucket
	base    float64
	nDays   int
}

func (s stubSource) DayStats(context.Context, string, time.Time) ([]DayStat, error) {
	return s.days, nil
}
func (s stubSource) Buckets(context.Context, string, time.Time) ([]Bucket, float64, int, error) {
	return s.buckets, s.base, s.nDays, nil
}

func run(t *testing.T, src Source) (string, error) {
	t.Helper()
	m := &Monitor{Src: src, Horizon: "1d", Now: func() time.Time {
		return time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	}}
	return m.Run(context.Background())
}

// THE REGRESSION THAT MATTERS. These are the real measured cross-sections from
// 2026-07-27..2026-08-04, when the model emitted 6-13 distinct probabilities
// across ~328 symbols for eight consecutive days and every dashboard reported a
// healthy fleet. If this test ever passes silently, the monitor is dead.
func TestCatchesTheRealCollapse(t *testing.T) {
	measured := []DayStat{
		{Day: "2026-07-27", Symbols: 330, DistinctProbs: 6},
		{Day: "2026-07-28", Symbols: 330, DistinctProbs: 8},
		{Day: "2026-07-29", Symbols: 328, DistinctProbs: 13},
		{Day: "2026-07-31", Symbols: 328, DistinctProbs: 6},
		{Day: "2026-08-01", Symbols: 328, DistinctProbs: 6},
		{Day: "2026-08-02", Symbols: 328, DistinctProbs: 8},
		{Day: "2026-08-03", Symbols: 328, DistinctProbs: 6},
		{Day: "2026-08-04", Symbols: 326, DistinctProbs: 12},
	}
	for _, d := range measured {
		if !d.Collapsed() {
			t.Errorf("%s: %d distinct across %d symbols (ratio %.3f) did NOT trip the collapse test",
				d.Day, d.DistinctProbs, d.Symbols, d.DistinctRatio())
		}
	}

	detail, err := run(t, stubSource{days: measured, base: 0.583, nDays: 8})
	if err == nil {
		t.Fatalf("eight collapsed days returned no error; detail=%q", detail)
	}
	if !strings.Contains(err.Error(), "CROSS-SECTION COLLAPSE") {
		t.Errorf("error does not name the failure: %v", err)
	}
	// It must name the worst day so an operator knows where to look.
	if !strings.Contains(err.Error(), "8/8 day(s)") {
		t.Errorf("error does not report how many days collapsed: %v", err)
	}
}

// The mirror image: real HEALTHY cross-sections must not trip it, or the monitor
// is noise and will be ignored — which is the same as being absent.
func TestHealthyDaysDoNotTrip(t *testing.T) {
	measured := []DayStat{
		{Day: "2026-08-05", Symbols: 327, DistinctProbs: 179},
		{Day: "2026-08-06", Symbols: 322, DistinctProbs: 174},
		{Day: "2026-07-26", Symbols: 330, DistinctProbs: 139},
		{Day: "2026-07-25", Symbols: 276, DistinctProbs: 140},
		{Day: "2026-07-22", Symbols: 322, DistinctProbs: 159},
		{Day: "2026-07-21", Symbols: 322, DistinctProbs: 133},
	}
	for _, d := range measured {
		if d.Collapsed() {
			t.Errorf("%s: healthy day (ratio %.3f) tripped the collapse test", d.Day, d.DistinctRatio())
		}
	}
	// Healthy days, and enough of them for a verdict, with well-behaved buckets.
	detail, err := run(t, stubSource{
		days:  measured,
		base:  0.50,
		nDays: 12,
		buckets: []Bucket{
			{Label: "<30%", N: 400, Said: 0.25, Actual: 0.40},
			{Label: ">=70%", N: 400, Said: 0.80, Actual: 0.62},
		},
	})
	if err != nil {
		t.Fatalf("healthy record reported a problem: %v", err)
	}
	if !strings.Contains(detail, "no collapse, no inversion") {
		t.Errorf("detail should state the all-clear plainly, got %q", detail)
	}
}

// A small universe legitimately shares a forecast; that is not a collapse.
func TestSmallUniverseIsNotACollapse(t *testing.T) {
	d := DayStat{Day: "2026-07-24", Symbols: 7, DistinctProbs: 2}
	if d.Collapsed() {
		t.Error("a 7-symbol day tripped the collapse test; the ratio is meaningless there")
	}
}

// Inversion is measured against the BASE RATE, not against the claim.
// Overconfidence still ranks; negative information does not.
func TestInversionIsAgainstTheBaseRate(t *testing.T) {
	const base = 0.583

	// The real >=70% bucket from the collapsed window: claimed 80.9%, delivered
	// 42.5% against a 58.3% base rate. Acting on it beat ignoring it — backwards.
	real := Bucket{Label: ">=70%", N: 3802, Said: 0.809, Actual: 0.425}
	if !real.Inverted(base) {
		t.Error("the measured >=70% bucket (said 80.9%, delivered 42.5%, base 58.3%) was not called inverted")
	}

	// Merely overconfident: claims 80%, delivers 65%, still above the base rate.
	// Useful for ranking, so it must NOT be flagged.
	if (Bucket{Label: ">=70%", N: 3802, Said: 0.80, Actual: 0.65}).Inverted(base) {
		t.Error("an overconfident-but-informative bucket was flagged as inverted")
	}

	// A down-call that realizes MORE up than the base rate is equally inverted.
	if !(Bucket{Label: "<30%", N: 3020, Said: 0.234, Actual: 0.70}).Inverted(base) {
		t.Error("a down-call realizing above the base rate was not called inverted")
	}

	// Thin buckets never trip: post-collapse the <30% bucket holds 11 rows, and
	// an alert built on that is exactly the overfitting this session forbids.
	if (Bucket{Label: "<30%", N: 11, Said: 0.244, Actual: 0.545}).Inverted(0.3914) {
		t.Error("an 11-row bucket tripped the inversion test")
	}
}

// A thin window must WITHHOLD rather than pass. Reporting "no inversion" on two
// days is the same false all-clear that let the last failure run for weeks.
func TestThinWindowWithholdsRatherThanPasses(t *testing.T) {
	_, err := run(t, stubSource{
		days:    []DayStat{{Day: "2026-08-05", Symbols: 327, DistinctProbs: 179}},
		base:    0.3914,
		nDays:   2, // the real post-collapse holdout
		buckets: []Bucket{{Label: "<30%", N: 11, Said: 0.244, Actual: 0.545}},
	})
	if err == nil {
		t.Fatal("a 2-day window returned a clean pass; it must withhold instead")
	}
	if !strings.Contains(err.Error(), "WITHHELD") {
		t.Errorf("error should say the verdict was withheld, got: %v", err)
	}
	if strings.Contains(err.Error(), "INVERSION in bucket") {
		t.Errorf("a withheld window must not also assert an inversion: %v", err)
	}
}

// DesignEffect must expose pseudo-replication, and must never invent it.
func TestDesignEffect(t *testing.T) {
	days := []DayStat{{Symbols: 100}, {Symbols: 100}, {Symbols: 100}, {Symbols: 100}}
	if got := DesignEffect(days, []float64{0.5, 0.5, 0.5, 0.5}); got != 1 {
		t.Errorf("identical days gave deff %.2f, want 1", got)
	}
	if got := DesignEffect(days, []float64{1, 0, 1, 0}); got <= 2 {
		t.Errorf("maximally clustered days gave deff %.2f, want >> 1", got)
	}
	if got := DesignEffect(days[:1], []float64{0.5}); got != 1 {
		t.Errorf("a single day gave deff %.2f, want 1 (unmeasurable)", got)
	}
}
