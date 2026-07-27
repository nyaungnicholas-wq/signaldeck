// Package clusterstat is the ONE cluster-robust statistics module every surface
// on this platform must call before it publishes an interval, with the UTC DAY
// as the unit of resampling.
//
// # The defect this package exists to prevent
//
// Every skill number here is measured across ~1,000 symbols on a handful of
// market days. Deduplicating to one row per (symbol, UTC-day) — which this
// platform already does — removes the intraday pseudo-replication (~60x) and
// leaves the larger problem entirely untouched: on any given day every symbol
// shares ONE market move. A binomial interval over 13,008 such rows asserts
// 13,008 independent trials when the sample contains roughly 19.
//
// Measured on the live 1d directional record (2026-07-25, 13,058 resolved
// symbol-days spanning 23 days), by the estimator in this file:
//
//	pooled directional accuracy : 48.12%
//	naive Wilson 95%            : [47.26, 48.97]   width 0.0171
//	measured design effect      : 14.7x -> effective N 887, not 13,058
//	cluster-corrected Wilson    : [44.84, 51.41]   width 0.0656
//
// The shipped interval was 3.8x too narrow. Note carefully what does NOT move:
// the point estimate, and with it the platform's conclusion that it has no
// demonstrated directional edge — 48.1% is outside both intervals and outside
// the 54.6% naive baseline by a wide margin. What was wrong was the advertised
// STRENGTH of that conclusion. "Ten independent confirmations" were ten reads of
// one correlated sample.
//
// # Why the design effect is MEASURED, never assumed
//
// The clustering strength is a property of the data, not a constant. It differs
// by horizon, by universe size and by day. Hard-coding the 14.7x measured on the
// 1d record would be the same class of error as assuming 1.0x: a number
// asserted where one can be computed. DesignEffect estimates it from the
// between-day variance of the daily proportion against the binomial expectation,
// using the survey linearization ("ultimate cluster") variance of a ratio
// estimator, which is what handles the unequal day sizes this platform has (one
// day holds 7 observations, the next holds 1,046).
//
// That choice is load-bearing. An unweighted between-day variance over the same
// live rows returns deff 2.15 — because four near-empty days dominate an
// unweighted average of per-day variances — and would have shipped an interval
// still 2.6x too narrow while appearing to have been corrected.
//
// # The second finding, which the interval alone does not express
//
// On the live record the model does not make 13,008 cross-sectional calls. The
// fraction of the universe it predicted UP on consecutive days ran 98.6%, 99.4%,
// 98.4%, then 8.5%, 6.9%, then 99.2%, 99.2%, then 2.1%. It makes roughly ONE
// MARKET-WIDE BET PER DAY and its accuracy tracks that day's up-rate. A
// cluster-corrected proportion over symbol-days is still the wrong unit when
// breadth is that high, so Result also reports the DAY-LEVEL framing — was the
// day's market-wide bet right — which on the live record is 8 of 19 days,
// 42.1%, an interval that comfortably includes a coin flip.
//
// # Refusal
//
// Below MinDistinctDays distinct days this package returns NO interval — a nil
// pointer and a stated reason, never a narrow number. A between-day variance
// estimated from three days is not a correction, it is a different way to be
// overconfident, and this platform has already shipped a false unlock by
// counting 995 clustered observations over 3 days as evidence.
package clusterstat

import (
	"math"
	"sort"
	"strconv"
)

// MinDistinctDays is the floor of distinct resampling days below which every
// interval is withheld. It matches the day floor the track record already
// enforces, so wiring a surface to this package can never LOOSEN a gate that
// surface already had.
const MinDistinctDays = 10

// z95 is the two-sided 95% normal quantile.
const z95 = 1.959963985

// Direction is the side a directional call took. The zero value is DirNone so a
// caller grading a non-directional proportion (a hit rate, a coverage fraction)
// cannot accidentally publish a breadth diagnostic computed from unset fields.
type Direction int8

const (
	// DirNone means the caller is not grading a directional call.
	DirNone Direction = 0
	// DirUp means the model called up.
	DirUp Direction = 1
	// DirDown means the model called down.
	DirDown Direction = -1
)

// Obs is ONE already-deduplicated observation — one (symbol, UTC-day) row. This
// package does not deduplicate: collapsing intraday repeats is the caller's
// job and is a different defect from the one fixed here. Handing in raw
// minute-cadence rows will produce a design effect that absorbs both, which is
// not wrong but is much harder to interpret.
type Obs struct {
	// Day is the UTC day index (unix seconds / 86400). It is the resampling
	// unit: every interval in this package resamples days, never rows.
	Day int64
	// Hit is the binary outcome being proportioned (call was correct, band
	// resolved as claimed, and so on).
	Hit bool
	// Dir is the side the call took, for the market-breadth diagnostic. Leave
	// it DirNone when the statistic is not a directional call.
	Dir Direction
}

// Day is one cluster already tallied, for callers that hold per-day counts
// rather than rows.
type Day struct {
	Day  int64
	N    int
	Hits int
}

// Interval is a two-sided confidence interval. It is always returned by pointer
// so that "we cannot say" is representable as nil and can never be mistaken for
// a narrow interval around the point estimate.
type Interval struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// Width is the interval's span.
func (i Interval) Width() float64 { return i.Hi - i.Lo }

// Config controls the grade. Defaults are the platform's house settings.
type Config struct {
	// MinDistinctDays is the refusal floor; below it no interval is returned.
	MinDistinctDays int
	// BootstrapIters is how many day-resamples the bootstrap draws. The
	// bootstrap runs over per-day tallies, not rows, so cost is O(iters*days)
	// and is independent of sample size.
	BootstrapIters int
	// Alpha is the two-sided error rate (0.05 = a 95% interval).
	Alpha float64
}

// Defaults returns the house configuration.
func Defaults() Config {
	return Config{MinDistinctDays: MinDistinctDays, BootstrapIters: 2000, Alpha: 0.05}
}

// DayBet is the day-as-the-unit framing: on each day, did the model's
// market-wide majority call match the market's realized majority direction. It
// is the honest statistic when daily breadth is high, because a day on which
// 99% of the universe is called up is one bet, not a thousand.
type DayBet struct {
	Days     int       `json:"days"`
	Correct  int       `json:"correct"`
	Accuracy float64   `json:"accuracy"`
	CI       *Interval `json:"ci"`
	Note     string    `json:"note"`
}

// Result is the honest report on a day-clustered proportion. Every interval is
// nil when the sample cannot support it.
type Result struct {
	// N is the raw observation count. It is never the sample size for
	// inference; EffectiveN is.
	N int `json:"n"`
	// DistinctDays is the count of distinct resampling units, and is reported
	// beside every N on every surface by house rule.
	DistinctDays    int     `json:"distinctDays"`
	MinDistinctDays int     `json:"minDistinctDays"`
	P               float64 `json:"p"`

	// DesignEffect is the MEASURED variance inflation from day clustering. 1.0
	// means the observations behaved independently; 14.7 means each day's
	// ~1,000 symbols carried the information of ~68.
	DesignEffect float64 `json:"designEffect"`
	// EffectiveN is N/DesignEffect — the independent-observation count the
	// sample actually holds.
	EffectiveN float64 `json:"effectiveN"`

	// CI is the headline interval: Wilson evaluated at EffectiveN. nil when
	// refused.
	CI *Interval `json:"ci"`
	// NaiveCIDiscredited is the interval an independence assumption would have
	// claimed. It is published ONLY so a reader can see the size of the
	// correction, and its field name says so, because this platform's standing
	// failure mode is disclosure substituting for correction — the wrong number
	// shipping beside the caveat explaining why it is wrong.
	NaiveCIDiscredited *Interval `json:"naiveCIDiscredited"`
	// BootstrapCI resamples whole days with replacement — a second, assumption-
	// light route to the same interval. Close agreement with CI is the check
	// that the design effect was estimated sanely; a large disagreement means
	// the day sizes are so unequal that neither should be trusted.
	BootstrapCI *Interval `json:"bootstrapCI"`
	// WidthRatio is CI width / naive width: how many times too narrow the
	// uncorrected interval was.
	WidthRatio float64 `json:"widthRatio"`

	// DailyAgreement is the mean fraction of a day's calls that pointed the
	// same way. At 0.5 the model made genuinely cross-sectional calls; near 1.0
	// it made one market-wide bet and N is a counting artifact.
	DailyAgreement float64 `json:"dailyAgreement"`
	// DayBet is the day-as-the-unit framing, present only when the caller
	// supplied directions and enough directional days exist.
	DayBet *DayBet `json:"dayBet"`

	// Refused is true when no interval could honestly be produced.
	Refused bool `json:"refused"`
	// Reason is never empty when Refused, and states what was missing.
	Reason string `json:"reason"`
	// Method describes what was computed, for the payload.
	Method string `json:"method"`
}

const methodNote = "cluster-robust: day is the unit of resampling. The design effect is MEASURED " +
	"from the between-day variance of the daily proportion against the binomial expectation " +
	"(survey linearization for a ratio estimator, which weights days by size), and the reported " +
	"interval is Wilson evaluated at effective N = N / design effect. Raw N is not a sample size."

// Grade computes the cluster-robust report with house defaults.
func Grade(obs []Obs) Result { return GradeWith(obs, Defaults()) }

// GradeWith computes the cluster-robust report. It never returns a narrow
// interval in place of an honest refusal: below cfg.MinDistinctDays every
// interval pointer is nil and Reason says why.
func GradeWith(obs []Obs, cfg Config) Result {
	if cfg.MinDistinctDays <= 0 {
		cfg.MinDistinctDays = MinDistinctDays
	}
	if cfg.Alpha <= 0 || cfg.Alpha >= 1 {
		cfg.Alpha = 0.05
	}

	tallies := tally(obs)
	res := Result{
		N:               len(obs),
		DistinctDays:    len(tallies),
		MinDistinctDays: cfg.MinDistinctDays,
		Method:          methodNote,
	}
	if res.N == 0 {
		res.Refused, res.Reason = true, "no observations"
		return res
	}

	var totalHits int
	for _, t := range tallies {
		totalHits += t.hits
	}
	res.P = float64(totalHits) / float64(res.N)
	res.DailyAgreement = meanDailyAgreement(tallies)

	if res.DistinctDays < cfg.MinDistinctDays {
		// Refuse outright rather than widen. A between-day variance estimated
		// from a handful of days is not a correction — it is a differently
		// overconfident number, and this platform has already shipped a false
		// unlock by treating 995 clustered observations over 3 days as evidence.
		res.Refused = true
		res.Reason = "no interval: " + strconv.Itoa(res.DistinctDays) + "/" +
			strconv.Itoa(cfg.MinDistinctDays) + " distinct days. Observations on one day share " +
			"one market move, so the design effect cannot be measured from this few clusters " +
			"and no honest interval exists yet — a narrow one would be worse than none."
		return res
	}

	days := make([]Day, len(tallies))
	for i, t := range tallies {
		days[i] = Day{Day: t.day, N: t.n, Hits: t.hits}
	}
	deff, ok := DesignEffect(days)
	if !ok {
		res.Refused = true
		res.Reason = "no interval: the design effect could not be measured from these clusters"
		return res
	}
	res.DesignEffect = deff
	res.EffectiveN = float64(res.N) / deff

	corrected := WilsonEff(res.P, res.EffectiveN)
	naive := WilsonEff(res.P, float64(res.N))
	res.CI, res.NaiveCIDiscredited = &corrected, &naive
	if naive.Width() > 0 {
		res.WidthRatio = corrected.Width() / naive.Width()
	}
	if bs, bok := bootstrapDays(days, cfg.BootstrapIters, cfg.Alpha); bok {
		res.BootstrapCI = &bs
	}
	res.DayBet = dayBet(tallies, cfg)
	return res
}

// DesignEffect measures how far the day clustering inflates the variance of the
// pooled proportion, as the ratio of the cluster-robust variance to the
// binomial variance the platform used to assume.
//
// The numerator is the survey linearization ("ultimate cluster") variance of the
// ratio estimator p = sum(hits) / sum(n), treating each day as a primary
// sampling unit. It is used in preference to an unweighted variance of daily
// proportions because this platform's days are wildly unequal in size — 7
// observations on one, 1,046 on another — and an unweighted average lets the
// near-empty days dominate. On the live 1d record the unweighted route returns
// 2.15 where this one returns 14.7, and shipping 2.15 would have looked like a
// correction while leaving the interval 2.6x too narrow.
//
// The result is floored at 1.0. A measured value below 1 is sampling noise in a
// small number of clusters, and using it would make the interval NARROWER than
// the independence assumption it was brought in to correct — reintroducing the
// exact overstatement this package exists to remove.
func DesignEffect(days []Day) (deff float64, ok bool) {
	k := len(days)
	if k < 2 {
		return 0, false
	}
	var n, hits int
	for _, d := range days {
		if d.N <= 0 {
			continue
		}
		n += d.N
		hits += d.Hits
	}
	if n == 0 {
		return 0, false
	}
	p := float64(hits) / float64(n)

	// A degenerate proportion (every call right, or every call wrong) carries no
	// between-day variance to measure, but it is also the most perfectly
	// clustered sample possible: every day is internally uniform. The honest
	// reading is therefore the worst case — one independent observation per day
	// — not the flattering deff of 1 the arithmetic would otherwise produce.
	if p <= 0 || p >= 1 {
		return float64(n) / float64(k), true
	}

	var s float64
	for _, d := range days {
		if d.N <= 0 {
			continue
		}
		r := float64(d.Hits) - float64(d.N)*p
		s += r * r
	}
	fn := float64(n)
	clusterVar := float64(k) / (float64(k-1) * fn * fn) * s
	binomVar := p * (1 - p) / fn
	if binomVar <= 0 || clusterVar <= 0 {
		return 1, true
	}
	deff = clusterVar / binomVar
	if deff < 1 {
		return 1, true
	}
	return deff, true
}

// WilsonEff is the Wilson score interval evaluated at an EFFECTIVE sample size,
// which may be fractional. Passing the raw row count here is the bug this whole
// package exists to prevent; pass N/DesignEffect.
//
// Wilson rather than the normal approximation because effective N is often small
// after correction (887 on the live record, and far less on thinner surfaces)
// and the normal interval misbehaves there and near 0/1.
func WilsonEff(p, effN float64) Interval {
	return WilsonEffAt(p, effN, z95)
}

// WilsonEffAt is WilsonEff at a caller-supplied z — for gates that run at a
// Bonferroni-corrected level rather than the house 95% (internal/researchlab's
// corrected-alpha lower bound, internal/researchx's weekly discovery gate).
//
// This is the ONE Wilson implementation in the tree, by enforced invariant
// (gates_test.go): every copy of this formula that lived in a decision gate was
// a chance for that gate to be fed raw N, which is exactly how A1 shipped.
// Callers that legitimately want a raw-count interval (a per-symbol record
// whose design effect is 1 by construction, the registry-parity reference)
// still route through here with effN = N so the arithmetic exists once.
func WilsonEffAt(p, effN, z float64) Interval {
	if effN <= 0 || z <= 0 || math.IsNaN(p) || math.IsNaN(z) {
		return Interval{}
	}
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	denom := 1 + z*z/effN
	center := (p + z*z/(2*effN)) / denom
	half := (z * math.Sqrt(p*(1-p)/effN+z*z/(4*effN*effN))) / denom
	lo, hi := center-half, center+half
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return Interval{Lo: lo, Hi: hi}
}

// BootstrapDays resamples WHOLE DAYS with replacement and returns the percentile
// interval of the pooled proportion. Resampling rows instead would assume away
// exactly the dependence that makes this data thinner than its row count
// suggests, and would reproduce the too-narrow interval by a second route.
//
// ok is false below MinDistinctDays clusters — the same refusal as everywhere
// else in this package.
func BootstrapDays(days []Day, iters int, alpha float64) (Interval, bool) {
	if len(days) < MinDistinctDays {
		return Interval{}, false
	}
	return bootstrapDays(days, iters, alpha)
}

func bootstrapDays(days []Day, iters int, alpha float64) (Interval, bool) {
	return BootstrapStat(len(days), iters, alpha, func(idx []int) (float64, bool) {
		var n, hits int
		for _, i := range idx {
			n += days[i].N
			hits += days[i].Hits
		}
		if n == 0 {
			return 0, false
		}
		return float64(hits) / float64(n), true
	})
}

// BootstrapStat resamples WHOLE DAYS with replacement and applies stat to each
// resample, returning the percentile interval of the statistic.
//
// It exists so that surfaces measuring something other than a proportion — a
// correlation, a mean return, an expectancy — resample the same unit as
// everything else in this package. A statistic bootstrapped over ROWS reproduces
// the too-narrow interval by a different route, which is how an endpoint can
// look corrected while still asserting independence the data does not have.
//
// stat receives the day indices drawn for one resample, repeats included, and
// returns ok=false to discard a degenerate draw (for example a resample with no
// variance left to correlate).
func BootstrapStat(numDays, iters int, alpha float64, stat func(dayIdx []int) (float64, bool)) (Interval, bool) {
	if numDays < 2 || iters < 100 || alpha <= 0 || alpha >= 1 || stat == nil {
		return Interval{}, false
	}
	// Fixed seed, deliberately. The same data must produce the same interval on
	// every run, or a borderline result can be re-rolled until it clears a gate.
	rng := newLCG(0xC1057E4)
	idx := make([]int, numDays)
	stats := make([]float64, 0, iters)
	for it := 0; it < iters; it++ {
		for j := range idx {
			idx[j] = rng.next(numDays)
		}
		if v, ok := stat(idx); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
			stats = append(stats, v)
		}
	}
	if len(stats) < 100 {
		return Interval{}, false
	}
	sort.Float64s(stats)
	return Interval{Lo: quantile(stats, alpha/2), Hi: quantile(stats, 1-alpha/2)}, true
}

// dayTally is one day's aggregate. directional is true only when EVERY
// observation that day carried a side, so the breadth diagnostic is never
// computed from unset zero-value fields.
type dayTally struct {
	day         int64
	n, hits     int
	predUp      int
	actualUp    int
	directional bool
}

// tally folds observations into per-day clusters, ascending by day.
func tally(obs []Obs) []dayTally {
	idx := map[int64]int{}
	var out []dayTally
	dirCount := map[int64]int{}
	for _, o := range obs {
		i, seen := idx[o.Day]
		if !seen {
			i = len(out)
			idx[o.Day] = i
			out = append(out, dayTally{day: o.Day})
		}
		t := &out[i]
		t.n++
		if o.Hit {
			t.hits++
		}
		if o.Dir != DirNone {
			dirCount[o.Day]++
			if o.Dir == DirUp {
				t.predUp++
			}
			// The realized direction is recoverable from the call and its
			// outcome: a call of up that hit means the market went up.
			if (o.Dir == DirUp) == o.Hit {
				t.actualUp++
			}
		}
	}
	for i := range out {
		out[i].directional = dirCount[out[i].day] == out[i].n
	}
	sort.Slice(out, func(a, b int) bool { return out[a].day < out[b].day })
	return out
}

// meanDailyAgreement is the mean over days of the fraction of that day's calls
// pointing the same way — the breadth number that decides whether N is a real
// cross-sectional sample or one bet counted many times. Days with no directions
// are skipped; with none at all it is 0.
func meanDailyAgreement(tallies []dayTally) float64 {
	var sum float64
	var k int
	for _, t := range tallies {
		if !t.directional || t.n == 0 {
			continue
		}
		up := float64(t.predUp) / float64(t.n)
		sum += math.Max(up, 1-up)
		k++
	}
	if k == 0 {
		return 0
	}
	return sum / float64(k)
}

// dayBet grades the model one bet per day: the day's majority call against the
// day's majority realized direction. It exists because a cluster-corrected
// proportion over symbol-days still overstates the evidence when a day's calls
// are near-unanimous — 1,000 symbols called up on one day is one prediction
// about one market, and the only sample size that framing supports is the day
// count.
func dayBet(tallies []dayTally, cfg Config) *DayBet {
	var days, correct int
	for _, t := range tallies {
		if !t.directional || t.n == 0 {
			continue
		}
		days++
		predUp := float64(t.predUp)/float64(t.n) > 0.5
		actualUp := float64(t.actualUp)/float64(t.n) > 0.5
		if predUp == actualUp {
			correct++
		}
	}
	if days < cfg.MinDistinctDays {
		return nil
	}
	b := &DayBet{
		Days:     days,
		Correct:  correct,
		Accuracy: float64(correct) / float64(days),
		Note: "day as the unit: the model's market-wide majority call graded once per day. " +
			"This is the framing that survives when daily breadth is high — a day on which " +
			"nearly every symbol is called the same way is ONE bet, and the day count is the " +
			"only sample size it supports.",
	}
	ci := WilsonEff(b.Accuracy, float64(days))
	b.CI = &ci
	return b
}

// quantile reads a percentile off a sorted slice by linear interpolation.
func quantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[n-1]
	}
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// lcg is a fixed-seed linear congruential generator. Deterministic on purpose:
// the same sample must yield the same interval on every run, so a borderline
// result cannot be re-rolled until it looks significant.
type lcg struct{ s uint64 }

func newLCG(seed uint64) *lcg { return &lcg{s: seed} }

func (l *lcg) next(n int) int {
	l.s = l.s*6364136223846793005 + 1442695040888963407
	if n <= 0 {
		return 0
	}
	return int((l.s >> 33) % uint64(n))
}
