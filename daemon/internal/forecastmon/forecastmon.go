// Package forecastmon watches the directional forecaster for the two SILENT
// deaths this system has actually suffered, and makes each one visible within a
// day instead of within an audit.
//
// Neither failure below announced itself. Both were found by someone re-running
// the numbers by hand, weeks later, and in both cases every dashboard kept
// reporting a healthy fleet the whole time. That is the defect this package
// exists to close: not the forecasting error, the SILENCE around it.
//
//  1. CROSS-SECTION COLLAPSE. Measured 2026-07-27..2026-08-04: the model emitted
//     6-13 DISTINCT probabilities across ~328 symbols per day. Every symbol got
//     essentially the same forecast, so the record was one market-wide call
//     repeated 328 times a day while n looked like 328 independent trials. The
//     day-clustered design effect on that window measured 27.9 — 2,626 rows
//     worth 94 observations. Every published statistic covering it graded a dead
//     configuration, including the per-bucket table that made the <30% bucket
//     look informative. Post-collapse that bucket holds 11 rows and the sign
//     reverses.
//
//  2. CALIBRATION INVERSION. On that same window the >=70% bucket said 80.9% and
//     delivered 42.5% — below the base rate. A forecaster claiming high
//     confidence while realizing worse than a constant guess is not noisy, it is
//     actively misleading, and nothing anywhere checked the claim against the
//     outcome.
//
// HOW IT SURFACES. Run returns a non-nil error when a check trips. The daemon's
// health endpoint reports any worker whose latest run did not succeed, so a
// collapse turns /api/health degraded within one cadence — no alert transport
// required. That matters: alert transports are operator-configured and this
// machine has none, so a monitor that could only alert would itself be silent.
//
// WHAT IT DELIBERATELY DOES NOT DO. It never adjusts, clamps, flips or suppresses
// a forecast. It measures and it reports. Inversion in particular is presumed to
// be a BUG in the data path until a human traces it; a monitor that "corrected"
// an inverted sign would bury the very evidence needed to find the cause.
package forecastmon

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// MinDistinctRatio is the collapse threshold: distinct probability values as a
// fraction of the symbols forecast that day.
//
// Calibrated from the real record rather than chosen. Healthy days measured
// 0.42-0.53 (e.g. 174 distinct across 322 symbols); collapsed days measured
// 0.018-0.040 (6 distinct across 328). 0.15 sits an order of magnitude below
// every healthy day and four times above every collapsed one, so it separates
// the two populations without sitting near either.
const MinDistinctRatio = 0.15

// MinSymbolsForCollapse is the universe size below which the distinct-ratio test
// is not meaningful — a handful of symbols legitimately share a forecast.
const MinSymbolsForCollapse = 30

// MinDaysForInversion is the number of distinct trading days required before the
// inversion check will return a verdict at all.
//
// Ten matches the accuracy registry's own floor, and it is here for the same
// reason: a bucket read on two days is one market move with a large n printed
// next to it. Below this the check reports "insufficient" and does NOT trip, so
// the monitor cannot manufacture an alert out of a thin window.
const MinDaysForInversion = 10

// MinBucketN is the per-bucket row floor for the inversion check.
const MinBucketN = 100

// DayStat is one trading day's forecast cross-section.
type DayStat struct {
	Day           string
	Symbols       int // symbols forecast that day (after dedup)
	DistinctProbs int // distinct probability values emitted that day
}

// DistinctRatio reports how much cross-sectional variety the day carried.
func (d DayStat) DistinctRatio() float64 {
	if d.Symbols == 0 {
		return 0
	}
	return float64(d.DistinctProbs) / float64(d.Symbols)
}

// Collapsed reports whether this day's forecasts had no meaningful spread.
func (d DayStat) Collapsed() bool {
	return d.Symbols >= MinSymbolsForCollapse && d.DistinctRatio() < MinDistinctRatio
}

// Bucket is one confidence band's claim measured against its outcome.
type Bucket struct {
	Label  string
	N      int
	Said   float64 // mean claimed P(up)
	Actual float64 // realized up-rate
}

// Inverted reports whether this bucket's realized rate sits on the wrong side of
// the base rate given what it claimed.
//
// The test is deliberately against the BASE RATE, not against Said. A bucket
// claiming 80% and delivering 55% is merely overconfident and stays useful for
// ranking. A bucket claiming 80% and delivering BELOW what you would get by
// guessing the majority class every time has negative information: acting on it
// is worse than ignoring it. Only the second is an inversion.
func (b Bucket) Inverted(baseRate float64) bool {
	if b.N < MinBucketN {
		return false
	}
	switch {
	case b.Said > 0.5 && b.Actual < baseRate:
		return true // claimed up, realized rarer than the base rate
	case b.Said < 0.5 && b.Actual > baseRate:
		return true // claimed down, realized MORE up than the base rate
	}
	return false
}

// Source supplies the measured record. An interface so the checks are testable
// without a database — the arithmetic is the part that must be right.
type Source interface {
	// DayStats returns one entry per trading day in [since, now], newest last.
	DayStats(ctx context.Context, horizon string, since time.Time) ([]DayStat, error)
	// Buckets returns the per-confidence-band record over the same window,
	// along with the window's realized base rate and its distinct day count.
	Buckets(ctx context.Context, horizon string, since time.Time) (buckets []Bucket, baseRate float64, days int, err error)
}

// Monitor is the worker.
type Monitor struct {
	Src     Source
	Horizon string
	// Window is how far back each run looks. Zero means 14 days.
	Window time.Duration
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

// Name implements workers.Worker.
func (m *Monitor) Name() string { return "forecast-monitor" }

// Interval implements workers.Worker. Daily: the failures it watches for persist
// for days once they start, and a tighter cadence would only add noise.
func (m *Monitor) Interval() time.Duration { return 24 * time.Hour }

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Run performs one check. The returned detail is written to the run log whether
// or not the check trips, so a healthy history is as legible as a failure.
func (m *Monitor) Run(ctx context.Context) (string, error) {
	window := m.Window
	if window == 0 {
		window = 14 * 24 * time.Hour
	}
	horizon := m.Horizon
	if horizon == "" {
		horizon = "1d"
	}
	since := m.now().Add(-window)

	days, err := m.Src.DayStats(ctx, horizon, since)
	if err != nil {
		return "", fmt.Errorf("day stats: %w", err)
	}
	buckets, baseRate, nDays, err := m.Src.Buckets(ctx, horizon, since)
	if err != nil {
		return "", fmt.Errorf("buckets: %w", err)
	}

	var problems []string

	// 1. Collapse.
	var collapsed []DayStat
	for _, d := range days {
		if d.Collapsed() {
			collapsed = append(collapsed, d)
		}
	}
	if len(collapsed) > 0 {
		sort.Slice(collapsed, func(i, j int) bool { return collapsed[i].Day < collapsed[j].Day })
		worst := collapsed[0]
		for _, d := range collapsed {
			if d.DistinctRatio() < worst.DistinctRatio() {
				worst = d
			}
		}
		problems = append(problems, fmt.Sprintf(
			"CROSS-SECTION COLLAPSE on %d/%d day(s): worst %s emitted %d distinct probabilities "+
				"across %d symbols (ratio %.3f, floor %.2f). Every statistic covering these days "+
				"grades one market-wide call repeated per symbol, not %d independent trials",
			len(collapsed), len(days), worst.Day, worst.DistinctProbs, worst.Symbols,
			worst.DistinctRatio(), MinDistinctRatio, worst.Symbols))
	}

	// 2. Inversion — only once the window can support a verdict.
	switch {
	case nDays < MinDaysForInversion:
		problems = append(problems, fmt.Sprintf(
			"inversion check WITHHELD: %d/%d distinct days. Not a pass — there is not enough "+
				"record to say either way", nDays, MinDaysForInversion))
	default:
		var inverted []Bucket
		for _, b := range buckets {
			if b.Inverted(baseRate) {
				inverted = append(inverted, b)
			}
		}
		for _, b := range inverted {
			problems = append(problems, fmt.Sprintf(
				"CALIBRATION INVERSION in bucket %s: claimed %.1f%% up, realized %.1f%% "+
					"against a %.1f%% base rate (n=%d). Acting on this bucket is worse than ignoring it",
				b.Label, b.Said*100, b.Actual*100, baseRate*100, b.N))
		}
	}

	detail := fmt.Sprintf("%d day(s) checked, %d bucket(s), base rate %.1f%%",
		len(days), len(buckets), baseRate*100)
	if len(problems) == 0 {
		return detail + "; no collapse, no inversion", nil
	}
	// Returned as an ERROR so the daemon's health endpoint reports this worker as
	// not delivering. That is the whole delivery mechanism: it needs no alert
	// transport, and this machine has none configured.
	return detail, fmt.Errorf("%s", strings.Join(problems, " | "))
}

// DesignEffect measures how much of a row count is one market move counted many
// times: the ratio of the observed between-day variance in the hit rate to the
// variance a single binomial would predict.
//
// Exported because every consumer of a day-clustered record needs it and the
// alternative is each one re-deriving it. Returns 1 when it cannot be measured,
// which is the conservative answer only in the sense that it never INVENTS
// overdispersion — a caller with two days must treat 1 as unmeasured, not as
// evidence of independence.
func DesignEffect(days []DayStat, rates []float64) float64 {
	if len(rates) < 2 {
		return 1
	}
	var mean, meanN float64
	for _, r := range rates {
		mean += r
	}
	mean /= float64(len(rates))
	for _, d := range days {
		meanN += float64(d.Symbols)
	}
	if len(days) > 0 {
		meanN /= float64(len(days))
	}
	if meanN <= 0 || mean <= 0 || mean >= 1 {
		return 1
	}
	expected := mean * (1 - mean) / meanN
	if expected <= 0 {
		return 1
	}
	var observed float64
	for _, r := range rates {
		observed += (r - mean) * (r - mean)
	}
	observed /= float64(len(rates) - 1)
	if deff := observed / expected; deff > 1 && !math.IsInf(deff, 0) {
		return deff
	}
	return 1
}
