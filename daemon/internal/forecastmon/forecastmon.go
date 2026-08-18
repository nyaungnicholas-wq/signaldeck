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

	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
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

// MinCoverageRatio is the starvation threshold: the share of the day's
// cross-section that actually received a forecast rather than an abstention.
//
// Calibrated from the real record the same way MinDistinctRatio was. Healthy
// days measured 0.927-0.988 (e.g. 325 forecast of 329 symbols); starved days
// measured 0.058-0.094 (19-31 of 329, once leg admission tightened on
// 2026-08-06). 0.50 sits five times above every starved day and roughly half
// of every healthy one, so it separates the populations without sitting near
// either.
//
// Starvation is NOT presumed to be a bug. Withholding when no leg clears its
// admission bar is the honest answer, and this check does not argue otherwise.
// It exists because the honest answer was previously INVISIBLE: the
// cross-section went from 322 forecasts to 20 overnight and the only thing that
// noticed described it as a different failure entirely.
const MinCoverageRatio = 0.5

// DayStat is one trading day's forecast cross-section.
type DayStat struct {
	Day string
	// Symbols is the whole cross-section that day (after dedup): symbols that
	// received a forecast PLUS those the ensemble declined to forecast.
	Symbols int
	// DistinctProbs counts distinct probability values among the rows that
	// carry a forecast. Abstentions are excluded — every one emits the same
	// 0.5, so counting them made this number fall as the ensemble abstained
	// more. See store.ForecastDayStatsRaw.
	DistinctProbs int
	// Withheld is how many of Symbols carried no forecast (zero admitted legs).
	// Zero on the resolved-outcome side, so Forecast == Symbols there and every
	// ratio below is unchanged for that caller.
	Withheld int
}

// Forecast is how many symbols actually received a forecast that day.
func (d DayStat) Forecast() int { return d.Symbols - d.Withheld }

// DistinctRatio reports how much cross-sectional variety the day carried,
// measured over the symbols that were actually forecast.
func (d DayStat) DistinctRatio() float64 {
	if d.Forecast() <= 0 {
		return 0
	}
	return float64(d.DistinctProbs) / float64(d.Forecast())
}

// CoverageRatio reports how much of the cross-section was forecast at all.
func (d DayStat) CoverageRatio() float64 {
	if d.Symbols == 0 {
		return 0
	}
	return float64(d.Forecast()) / float64(d.Symbols)
}

// Collapsed reports whether this day's forecasts had no meaningful spread.
//
// The floor is applied to Forecast, not Symbols: a day on which four symbols
// were forecast and 325 were withheld is a coverage failure, and calling it a
// collapse would put the wrong name on it. Starved covers that case.
func (d DayStat) Collapsed() bool {
	return d.Forecast() >= MinSymbolsForCollapse && d.DistinctRatio() < MinDistinctRatio
}

// Starved reports whether the ensemble declined most of the cross-section.
func (d DayStat) Starved() bool {
	return d.Symbols >= MinSymbolsForCollapse && d.CoverageRatio() < MinCoverageRatio
}

// Bucket is one confidence band's claim measured against its outcome.
type Bucket struct {
	Label string
	N     int
	// Days is how many DISTINCT trading days contributed rows to this bucket.
	//
	// N alone cannot support an inversion verdict and never could: every symbol
	// on one day shares one market move, so rows cluster by day exactly as
	// directionalrecord.go and store/forecastmon.go already say. MinDaysForInversion
	// was being applied to the WINDOW's day count, which the window passes easily,
	// and never to the bucket's own. Measured 2026-08-17: the 55-70% and >=70%
	// buckets carried 189 and 198 rows drawn from TWO trading days each, and both
	// were published as hard inversions reading "acting on this bucket is worse
	// than ignoring it" — a claim on ~2 independent observations.
	Days   int
	Said   float64 // mean claimed P(up)
	Actual float64 // realized up-rate, POOLED over rows
	// DayRates is the realized up-rate of each contributing day, one entry per
	// day. Actual answers "what happened"; this answers "could it have been
	// chance", and only the second can carry a verdict. Pooling hides the
	// clustering: 790 rows over 11 days has a day-clustered SE ~2.8x the naive
	// binomial one, measured 2026-08-17.
	DayRates []float64
}

// tCrit975 is Student's t at 97.5% for df=9.
//
// MinDaysForInversion already requires >=10 days, so df >= 9 always, and 2.262
// is the WIDEST critical value in that admissible range (it falls to 1.96 as df
// grows). Using it for every bucket is therefore conservative in the only
// direction that matters here: the interval can be too wide, never too narrow,
// so an inversion is never asserted on less evidence than the data supports.
// A per-df table would buy tighter intervals and more assertions — the opposite
// of what this check needs.
const tCrit975 = 2.262

// clusteredBounds is the 95% interval for the realized rate with each DAY as one
// observation, not each symbol-day row. ok is false when the record cannot
// support an interval at all.
func (b Bucket) clusteredBounds() (lo, hi float64, ok bool) {
	k := len(b.DayRates)
	if k < 2 {
		return 0, 0, false
	}
	var sum float64
	for _, r := range b.DayRates {
		sum += r
	}
	mean := sum / float64(k)
	var ss float64
	for _, r := range b.DayRates {
		d := r - mean
		ss += d * d
	}
	se := math.Sqrt(ss/float64(k-1)) / math.Sqrt(float64(k))
	return mean - tCrit975*se, mean + tCrit975*se, true
}

// invertedSign is the direction test alone, with no sufficiency floor. Split out
// so the caller can tell "inverted and provable" from "inverted-looking on a
// sample that cannot carry the claim" — the second must be reported as withheld,
// never silently dropped.
func invertedSign(b Bucket, baseRate float64) bool {
	return (b.Said > 0.5 && b.Actual < baseRate) || (b.Said < 0.5 && b.Actual > baseRate)
}

// Inverted reports whether this bucket's realized rate sits on the wrong side of
// the base rate given what it claimed.
//
// The test is deliberately against the BASE RATE, not against Said. A bucket
// claiming 80% and delivering 55% is merely overconfident and stays useful for
// ranking. A bucket claiming 80% and delivering BELOW what you would get by
// guessing the majority class every time has negative information: acting on it
// is worse than ignoring it. Only the second is an inversion.
// It must also survive noise. "Acting on this bucket is worse than ignoring it"
// is a directional claim about live money, and a point estimate cannot make it:
// the surviving 45-55% assertion on 2026-08-17 was a 1.9pp gap whose
// day-clustered z was -0.73. The interval must exclude the base rate on the side
// the claim needs — an upper bound below it for a bucket claiming UP, a lower
// bound above it for one claiming DOWN.
func (b Bucket) Inverted(baseRate float64) bool {
	if !b.Judgeable() || !invertedSign(b, baseRate) {
		return false
	}
	lo, hi, ok := b.clusteredBounds()
	if !ok {
		return false
	}
	if b.Said > 0.5 {
		return hi < baseRate
	}
	return lo > baseRate
}

// Judgeable reports whether the bucket has enough INDEPENDENT record to carry a
// verdict either way — rows AND distinct days. A bucket that fails this is not
// evidence of calibration health; it is an absence of evidence, and the caller
// says so out loud.
func (b Bucket) Judgeable() bool { return b.N >= MinBucketN && b.Days >= MinDaysForInversion }

// Source supplies the measured record. An interface so the checks are testable
// without a database — the arithmetic is the part that must be right.
type Source interface {
	// DayStats returns one entry per trading day in [since, now], newest last.
	DayStats(ctx context.Context, horizon string, since time.Time) ([]DayStat, error)
	// Buckets returns the per-confidence-band record over the same window,
	// along with the window's realized base rate and its distinct day count.
	Buckets(ctx context.Context, horizon string, since time.Time) (buckets []Bucket, baseRate float64, days int, err error)
	// ModelEmitting reports whether the directional model for this horizon is
	// still published, and its verdict. Withholding means different things either
	// side of that line: a RETIRED model that declines the cross-section is doing
	// what retirement means, while a LIVE one doing the same is the coverage
	// failure this monitor exists to catch. Implementations must return true when
	// the state cannot be read — see the store implementation for why.
	ModelEmitting(ctx context.Context, horizon string) (emitting bool, verdict string, err error)
	// RawDayStats is DayStats measured on what the model EMITTED rather than on
	// what has RESOLVED. At a 1d horizon the resolved view is a full day late,
	// so a collapse starting today is invisible to DayStats until tomorrow —
	// which is how the 2026-08-07 collapse was still running unreported. This
	// is also upstream of calibration, so it separates "the model stopped
	// discriminating" from "the calibrator flattened a good score".
	RawDayStats(ctx context.Context, horizon string, since time.Time) ([]DayStat, error)
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
	// starvedNote carries a starvation that is EXPECTED, so it can be reported
	// without failing the run. See the coverage branch below.
	var starvedNote string
	emitting, verdict, err := m.Src.ModelEmitting(ctx, horizon)
	if err != nil {
		emitting, verdict = true, "unreadable"
	}

	// 1a. Collapse on the RAW side, checked FIRST because it is both the earlier
	// signal and the more fundamental failure: if the model itself stopped
	// discriminating, no calibration fix can help and the published probability
	// is a single market-wide call however it is post-processed.
	rawDays, err := m.Src.RawDayStats(ctx, horizon, since)
	if err != nil {
		return "", fmt.Errorf("raw day stats: %w", err)
	}
	var rawCollapsed []DayStat
	for _, d := range rawDays {
		if d.Collapsed() {
			rawCollapsed = append(rawCollapsed, d)
		}
	}
	if len(rawCollapsed) > 0 {
		sort.Slice(rawCollapsed, func(i, j int) bool { return rawCollapsed[i].Day < rawCollapsed[j].Day })
		w := rawCollapsed[len(rawCollapsed)-1] // newest: what is happening NOW
		problems = append(problems, fmt.Sprintf(
			"RAW MODEL COLLAPSE on %d/%d day(s), most recently %s: %d distinct raw scores "+
				"across %d forecast symbols (ratio %.3f, floor %.2f). This is UPSTREAM of "+
				"calibration — the ensemble itself has stopped discriminating on the names it "+
				"did call, which is a different failure from declining to call them",
			len(rawCollapsed), len(rawDays), w.Day, w.DistinctProbs, w.Forecast(),
			w.DistinctRatio(), MinDistinctRatio))
	}

	// 1a-ii. COVERAGE STARVATION, the failure the collapse test used to be
	// mistaken for. An ensemble that withholds most of the cross-section is not
	// discriminating badly — it is not answering. Both are worth knowing and
	// they have different fixes, so they are reported as different problems.
	var starved []DayStat
	for _, d := range rawDays {
		if d.Starved() {
			starved = append(starved, d)
		}
	}
	if len(starved) > 0 {
		sort.Slice(starved, func(i, j int) bool { return starved[i].Day < starved[j].Day })
		w := starved[len(starved)-1] // newest: what is happening NOW
		msg := fmt.Sprintf(
			"FORECAST COVERAGE STARVED on %d/%d day(s), most recently %s: only %d of %d "+
				"symbols received a forecast (coverage %.3f, floor %.2f); the other %d were "+
				"withheld with zero admitted legs. The published record thins out to that "+
				"many names a day — check leg admission before reading any accuracy number "+
				"over this window",
			len(starved), len(rawDays), w.Day, w.Forecast(), w.Symbols,
			w.CoverageRatio(), MinCoverageRatio, w.Withheld)
		// Same argument the withheld branch at the bottom already makes, applied
		// to the cause rather than the sample size. A RETIRED model that declines
		// the cross-section is doing what retirement means; erroring on it every
		// run holds the daemon red indefinitely on a condition that is not a
		// fault, and a monitor that is always red is one nobody reads — which
		// would cost us the day it means something. Measured 2026-08-17: this
		// fired on 11 of 15 days with both horizons retired and emitting=false.
		// A model that is still EMITTING and starving is the coverage failure
		// this check was built for, and that stays an error.
		if emitting {
			problems = append(problems, msg)
		} else {
			starvedNote = msg + fmt.Sprintf(" — EXPECTED, NOT A FAULT: the %s model is"+
				" %q and not emitting, so declining is the honest answer. This becomes"+
				" an ERROR again the moment it emits.", horizon, verdict)
		}
	}

	// 1b. Collapse on the published (calibrated, resolved) side.
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
		// The worst day alone cannot separate a collapse running NOW from one
		// that ended a week ago; the starvation branch reports its newest day for
		// exactly this reason. thin counts days the ratio cannot judge at all.
		newest := collapsed[len(collapsed)-1]
		var judgeable, cleanSince, thin int
		for _, d := range days {
			if d.Forecast() < MinSymbolsForCollapse {
				thin++
				continue
			}
			judgeable++
			if d.Day > newest.Day && !d.Collapsed() {
				cleanSince++
			}
		}
		problems = append(problems, fmt.Sprintf(
			"PUBLISHED CROSS-SECTION COLLAPSE on %d of %d judgeable day(s): worst %s emitted %d "+
				"distinct probabilities across %d symbols (ratio %.3f, floor %.2f). Every statistic "+
				"covering these days grades one market-wide call repeated per symbol, not %d "+
				"independent trials. MOST RECENT was %s, with %d clean judgeable day(s) since; "+
				"%d day(s) in the window carried fewer than %d forecasts and could not be judged "+
				"at all, so quiet days there are not evidence it ended",
			len(collapsed), judgeable, worst.Day, worst.DistinctProbs, worst.Symbols,
			worst.DistinctRatio(), MinDistinctRatio, worst.Symbols,
			newest.Day, cleanSince, thin, MinSymbolsForCollapse))
	}

	// 2. Inversion — only once the window can support a verdict.
	withheld := ""
	switch {
	case nDays < MinDaysForInversion:
		withheld = fmt.Sprintf(
			"inversion check WITHHELD: %d/%d distinct days. Not a pass — there is not enough "+
				"record to say either way", nDays, MinDaysForInversion)
	default:
		// A bucket is judged on ITS OWN record, not the window's. nDays above is
		// the whole window and passes easily; a single bucket can still be two
		// days of rows wearing a three-figure n.
		var unproven []Bucket
		for _, b := range buckets {
			switch {
			case b.Inverted(baseRate):
				lo, hi, _ := b.clusteredBounds()
				problems = append(problems, fmt.Sprintf(
					"CALIBRATION INVERSION in bucket %s: claimed %.1f%% up, realized %.1f%% "+
						"against a %.1f%% base rate (n=%d over %d day(s); day-clustered 95%% CI "+
						"[%.3f, %.3f] excludes it). Acting on this bucket is worse than ignoring it",
					b.Label, b.Said*100, b.Actual*100, baseRate*100, b.N, b.Days, lo, hi))
			case invertedSign(b, baseRate):
				unproven = append(unproven, b)
			}
		}
		// Say what was NOT judged. Dropping these silently would turn "we cannot
		// tell" into "nothing found", which is the failure this file keeps naming.
		for _, b := range unproven {
			var why string
			lo, hi, ok := b.clusteredBounds()
			switch {
			case !b.Judgeable():
				why = fmt.Sprintf("it rests on n=%d across only %d day(s) (floors n>=%d, days>=%d) "+
					"and every symbol on a day shares one market move",
					b.N, b.Days, MinBucketN, MinDaysForInversion)
			case ok:
				why = fmt.Sprintf("its day-clustered 95%% CI [%.3f, %.3f] over %d day(s) still "+
					"straddles the %.3f base rate, so the gap is inside noise",
					lo, hi, b.Days, baseRate)
			default:
				why = "no per-day record was available to put an interval on it"
			}
			note := fmt.Sprintf("inversion WITHHELD for bucket %s: it points the wrong way "+
				"(claimed %.1f%%, realized %.1f%% vs %.1f%% base) but %s — not enough to say "+
				"either way", b.Label, b.Said*100, b.Actual*100, baseRate*100, why)
			if withheld == "" {
				withheld = note
			} else {
				withheld += " | " + note
			}
		}
	}

	detail := fmt.Sprintf("%d day(s) checked, %d bucket(s), base rate %.1f%%",
		len(days), len(buckets), baseRate*100)

	// A tripped check is an ERROR: the daemon's health endpoint reports any
	// worker whose latest run did not deliver, so a collapse turns the daemon
	// degraded within a day. That is the whole delivery mechanism — it needs no
	// alert transport, and this machine has none configured.
	if len(problems) > 0 {
		if withheld != "" {
			problems = append(problems, withheld)
		}
		// Still say it, even when something else is failing: an expected
		// starvation is context for whatever else tripped, not noise to drop.
		if starvedNote != "" {
			problems = append(problems, starvedNote)
		}
		return detail, fmt.Errorf("%s", strings.Join(problems, " | "))
	}
	// Expected starvation files the run as degraded, not failed, for exactly the
	// reason spelled out below for a withheld verdict.
	if starvedNote != "" {
		if withheld != "" {
			starvedNote += " | " + withheld
		}
		return detail, fmt.Errorf("%s: %w", starvedNote, workers.ErrDegraded)
	}

	// A WITHHELD verdict is degraded, NOT failed, and the distinction is the
	// difference between a monitor people read and one they learn to ignore.
	// Post-collapse there are two clean days; ten are needed. Erroring here
	// would hold the daemon red for a fortnight on a condition that resolves by
	// itself, and a monitor that cries wolf for two weeks has taught everyone to
	// dismiss it by the time it means something. ErrDegraded files the run as
	// "degraded" — visible on the Agents page and in the run log, and it still
	// keeps health honest, without claiming a failure that has not happened.
	if withheld != "" {
		return detail, fmt.Errorf("%s: %w", withheld, workers.ErrDegraded)
	}
	return detail + "; no collapse, no inversion", nil
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
