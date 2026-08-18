package forecastmon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// stubSource replays a measured record without a database.
type stubSource struct {
	days    []DayStat
	rawDays []DayStat
	buckets []Bucket
	base    float64
	nDays   int
	// retired, not emitting, so the zero value is a LIVE model. Every case
	// written before this field keeps its original meaning: for a live model a
	// starvation is a real error, and none of them silently became degraded.
	retired bool
}

func (s stubSource) ModelEmitting(context.Context, string) (bool, string, error) {
	if s.retired {
		return false, "retired", nil
	}
	return true, "live", nil
}

func (s stubSource) DayStats(context.Context, string, time.Time) ([]DayStat, error) {
	return s.days, nil
}
func (s stubSource) Buckets(context.Context, string, time.Time) ([]Bucket, float64, int, error) {
	return s.buckets, s.base, s.nDays, nil
}
func (s stubSource) RawDayStats(context.Context, string, time.Time) ([]DayStat, error) {
	return s.rawDays, nil
}

func run(t *testing.T, src Source) (string, error) {
	t.Helper()
	m := &Monitor{Src: src, Horizon: "1d", Now: func() time.Time {
		return time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	}}
	return m.Run(context.Background())
}

// THE OTHER REGRESSION THAT MATTERS, and the one that fired for four days
// while every dashboard stayed green.
//
// These are the real measured raw cross-sections from 2026-08-05..2026-08-10.
// On 2026-08-06 leg admission tightened (906310c) and the ensemble began
// declining most of the universe; a withheld prediction is still persisted, with
// raw_prob = 0.5 exactly. Counting those identical 0.5s as forecasts dragged the
// distinct-ratio to 0.058-0.097 and tripped RAW MODEL COLLAPSE every single run,
// asserting "the ensemble itself has stopped discriminating" — while among the
// rows that CARRIED a forecast the ratio those same days was 0.947-1.000.
//
// The statistic moved in the opposite direction to the thing it measured: the
// more honestly the ensemble abstained, the more collapsed it was reported to
// be. So the two failures are asserted apart here. If a withheld row is ever
// folded back into the discrimination count, the first loop fails; if the
// coverage cliff is ever left unreported, the second does.
// A RETIRED model that declines the cross-section is doing what retirement
// means. Reporting that as a failed run every hour is how a monitor becomes
// wallpaper: measured 2026-08-17, this fired on 11 of 15 days while both
// horizons carried verdict=retired, emitting=false, so the surface that would
// have shown a REAL coverage loss had been red for eleven days already.
//
// Degraded, not failed — and the message has to say which, or the operator
// cannot tell the two apart either.
func TestStarvationUnderARetiredModelIsDegradedNotFailed(t *testing.T) {
	starved := []DayStat{
		{Day: "2026-08-16", Symbols: 329, DistinctProbs: 18, Withheld: 310},
		{Day: "2026-08-17", Symbols: 329, DistinctProbs: 34, Withheld: 293},
	}
	_, err := run(t, stubSource{rawDays: starved, base: 0.5, nDays: 2, retired: true})
	if err == nil {
		t.Fatal("an expected starvation must still be REPORTED, not swallowed")
	}
	if !errors.Is(err, workers.ErrDegraded) {
		t.Errorf("retired-model starvation filed as a failure, not degraded: %v", err)
	}
	if !strings.Contains(err.Error(), "FORECAST COVERAGE STARVED") {
		t.Errorf("degraded run stopped naming the condition: %v", err)
	}
	if !strings.Contains(err.Error(), "EXPECTED, NOT A FAULT") {
		t.Errorf("message does not tell the reader this is expected: %v", err)
	}
}

// The other half, and the one that must never be downgraded: the same coverage
// cliff while the model is LIVE is the failure this check was built for.
func TestStarvationWhileEmittingStaysAnError(t *testing.T) {
	starved := []DayStat{
		{Day: "2026-08-16", Symbols: 329, DistinctProbs: 18, Withheld: 310},
		{Day: "2026-08-17", Symbols: 329, DistinctProbs: 34, Withheld: 293},
	}
	_, err := run(t, stubSource{rawDays: starved, base: 0.5, nDays: 2})
	if err == nil {
		t.Fatal("a live model starving the cross-section returned no error")
	}
	if errors.Is(err, workers.ErrDegraded) {
		t.Errorf("live-model starvation was downgraded to degraded: %v", err)
	}
	if strings.Contains(err.Error(), "EXPECTED") {
		t.Errorf("live starvation was described as expected: %v", err)
	}
}

func TestWithheldRowsDoNotReadAsCollapse(t *testing.T) {
	// Symbols = whole cross-section, Withheld = declined. Measured.
	measured := []DayStat{
		{Day: "2026-08-05", Symbols: 329, DistinctProbs: 179, Withheld: 4},
		{Day: "2026-08-06", Symbols: 329, DistinctProbs: 152, Withheld: 24},
		{Day: "2026-08-07", Symbols: 329, DistinctProbs: 31, Withheld: 298},
		{Day: "2026-08-09", Symbols: 329, DistinctProbs: 18, Withheld: 310},
		{Day: "2026-08-10", Symbols: 329, DistinctProbs: 20, Withheld: 308},
	}
	for _, d := range measured {
		if d.Collapsed() {
			t.Errorf("%s: %d distinct across %d FORECAST symbols (ratio %.3f) was called a "+
				"collapse; %d withheld rows are not evidence the model stopped discriminating",
				d.Day, d.DistinctProbs, d.Forecast(), d.DistinctRatio(), d.Withheld)
		}
	}

	// The starvation IS real and must be reported — as itself.
	starved := measured[2:] // 08-07 onward: coverage 0.094, 0.058, 0.064
	for _, d := range starved {
		if !d.Starved() {
			t.Errorf("%s: only %d of %d symbols forecast (coverage %.3f) did NOT trip the "+
				"starvation test", d.Day, d.Forecast(), d.Symbols, d.CoverageRatio())
		}
	}
	for _, d := range measured[:2] { // 08-05, 08-06: coverage 0.988, 0.927
		if d.Starved() {
			t.Errorf("%s: coverage %.3f is healthy but tripped the starvation test",
				d.Day, d.CoverageRatio())
		}
	}

	detail, err := run(t, stubSource{rawDays: measured, base: 0.5, nDays: 5})
	if err == nil {
		t.Fatalf("three starved days returned no error; detail=%q", detail)
	}
	if strings.Contains(err.Error(), "RAW MODEL COLLAPSE") {
		t.Errorf("starvation was reported as a collapse — the wrong diagnosis is the bug: %v", err)
	}
	if !strings.Contains(err.Error(), "FORECAST COVERAGE STARVED") {
		t.Errorf("error does not name the failure: %v", err)
	}
	// It must name the newest day and the real coverage so an operator can act.
	if !strings.Contains(err.Error(), "2026-08-10") || !strings.Contains(err.Error(), "3/5 day(s)") {
		t.Errorf("error does not locate the failure in time: %v", err)
	}
}

// A genuine raw collapse — many symbols forecast, almost no variety among them —
// must still trip, and must NOT be renamed to starvation.
func TestRealRawCollapseStillTrips(t *testing.T) {
	measured := []DayStat{{Day: "2026-07-27", Symbols: 330, DistinctProbs: 6, Withheld: 2}}
	if !measured[0].Collapsed() {
		t.Fatalf("328 forecast symbols sharing 6 values did not trip the collapse test")
	}
	if measured[0].Starved() {
		t.Errorf("coverage %.3f is healthy; this is a collapse, not starvation",
			measured[0].CoverageRatio())
	}
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
	if !strings.Contains(err.Error(), "PUBLISHED CROSS-SECTION COLLAPSE") {
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
	real := Bucket{Label: ">=70%", N: 3802, Days: 31, Said: 0.809, Actual: 0.425}
	if !real.Inverted(base) {
		t.Error("the measured >=70% bucket (said 80.9%, delivered 42.5%, base 58.3%) was not called inverted")
	}

	// Merely overconfident: claims 80%, delivers 65%, still above the base rate.
	// Useful for ranking, so it must NOT be flagged.
	if (Bucket{Label: ">=70%", N: 3802, Days: 31, Said: 0.80, Actual: 0.65}).Inverted(base) {
		t.Error("an overconfident-but-informative bucket was flagged as inverted")
	}

	// A down-call that realizes MORE up than the base rate is equally inverted.
	if !(Bucket{Label: "<30%", N: 3020, Days: 28, Said: 0.234, Actual: 0.70}).Inverted(base) {
		t.Error("a down-call realizing above the base rate was not called inverted")
	}

	// Thin buckets never trip: post-collapse the <30% bucket holds 11 rows, and
	// an alert built on that is exactly the overfitting this session forbids.
	if (Bucket{Label: "<30%", N: 11, Days: 9, Said: 0.244, Actual: 0.545}).Inverted(0.3914) {
		t.Error("an 11-row bucket tripped the inversion test")
	}

	// THE DEFECT THIS FIELD EXISTS FOR. A three-figure n drawn from two trading
	// days is ~2 independent observations, because every symbol on a day shares
	// one market move. MinDaysForInversion was applied to the WINDOW's day count,
	// which any 14-day window passes, and never to the bucket's own — so on
	// 2026-08-17 the 55-70% and >=70% buckets (189 and 198 rows, 2 days each)
	// both published "acting on this bucket is worse than ignoring it".
	twoDays := Bucket{Label: ">=70%", N: 198, Days: 2, Said: 0.862, Actual: 0.434}
	if twoDays.Inverted(0.478) {
		t.Error("198 rows from TWO days carried an inversion verdict — pseudo-replication")
	}
	if twoDays.Judgeable() {
		t.Error("a 2-day bucket reported itself judgeable")
	}
	// It must still be recognised as pointing the wrong way, so the caller can
	// report it as WITHHELD rather than drop it into silence.
	if !invertedSign(twoDays, 0.478) {
		t.Error("the sign test stopped seeing a wrong-way bucket; it would vanish entirely")
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
	// WITHHELD must file as DEGRADED, not as a failure. Post-collapse there are
	// two clean days and ten are needed, so erroring would hold the daemon red
	// for a fortnight on a condition that resolves by itself — and a monitor
	// that cries wolf for two weeks has trained everyone to dismiss it by the
	// time it means something.
	if !errors.Is(err, workers.ErrDegraded) {
		t.Errorf("a withheld verdict must wrap workers.ErrDegraded, got: %v", err)
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

// A real failure must NOT be softened to degraded just because the window is
// also too thin to judge inversion. A collapse is actionable today.
func TestCollapseStaysAHardFailureEvenWhenInversionIsWithheld(t *testing.T) {
	_, err := run(t, stubSource{
		days:  []DayStat{{Day: "2026-08-03", Symbols: 328, DistinctProbs: 6}},
		base:  0.583,
		nDays: 1,
	})
	if err == nil {
		t.Fatal("a collapsed day returned no error")
	}
	if errors.Is(err, workers.ErrDegraded) {
		t.Errorf("a collapse was filed as merely degraded: %v", err)
	}
	if !strings.Contains(err.Error(), "PUBLISHED CROSS-SECTION COLLAPSE") {
		t.Errorf("error does not name the collapse: %v", err)
	}
}

// THE DAY-LATE GAP. prediction_outcomes only carries RESOLVED rows, so at a 1d
// horizon a collapse starting today is invisible there until tomorrow. Measured:
// on 2026-08-07 raw_prob fell from 1,475 distinct values across 329 symbols to
// 78, while the resolved-side view still showed two healthy days — the monitor
// would have reported all-clear on a fleet that had already stopped
// discriminating. The raw side closes that gap.
func TestCatchesTheLiveRawCollapseTheResolvedSideCannotSee(t *testing.T) {
	_, err := run(t, stubSource{
		// Resolved side: the last two days that HAVE resolved, both healthy.
		days: []DayStat{
			{Day: "2026-08-05", Symbols: 327, DistinctProbs: 179},
			{Day: "2026-08-06", Symbols: 322, DistinctProbs: 174},
		},
		// Raw side: what the model emitted since, which has not resolved yet.
		rawDays: []DayStat{
			{Day: "2026-08-06", Symbols: 329, DistinctProbs: 1475},
			{Day: "2026-08-07", Symbols: 329, DistinctProbs: 78},
			{Day: "2026-08-08", Symbols: 329, DistinctProbs: 31},
		},
		base: 0.3914, nDays: 2,
	})
	if err == nil {
		t.Fatal("a live raw collapse went unreported because nothing had resolved yet")
	}
	if !strings.Contains(err.Error(), "RAW MODEL COLLAPSE") {
		t.Errorf("error does not name the raw collapse: %v", err)
	}
	// It must point at the NEWEST collapsed day: that is what is happening now.
	if !strings.Contains(err.Error(), "2026-08-08") {
		t.Errorf("error should name the most recent collapsed day, got: %v", err)
	}
	// And a raw collapse is a hard failure, never softened to degraded by the
	// thin-window withholding that applies to the inversion check.
	if errors.Is(err, workers.ErrDegraded) {
		t.Errorf("a raw model collapse was filed as merely degraded: %v", err)
	}
}
