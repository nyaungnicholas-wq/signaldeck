// Package confidence assembles the ONE object a consumer should read about a
// prediction: how likely, how much, how badly it tends to hurt on the way, how
// much of that is knowable, and why.
//
// WHY THIS EXISTS. Every piece already existed and none of them lived together.
// A calibrated probability came from internal/forecast, its out-of-sample quality
// from forecast.Grade, a conditional expected return from internal/expectancy, a
// conviction band from internal/structregime, and a human-readable reason from
// half a dozen places. A consumer wanting "should I believe this, and what does
// believing it imply" had to join four sources and invent the arithmetic between
// them — so each consumer invented it slightly differently.
//
// The genuinely MISSING piece was downside. Drawdown existed only at the
// portfolio level (papertrade.Summary, internal/risklens), so nothing could
// answer "if I take this trade, how far against me does it usually go before it
// resolves?". That question is answered here by measuring the ADVERSE EXCURSION
// of the historical episodes that looked like this one — see AdverseExcursion.
//
// THE HONESTY RULE, which is the whole design. Every derived number is a POINTER
// and is nil unless it was actually measurable. A consumer that sees null asks;
// a consumer that sees 0 quotes it. Nothing here substitutes a neutral-looking
// default for a missing measurement, and Withheld names every field that came
// back nil together with the reason, so an empty assessment explains itself
// rather than looking like a confident zero.
package confidence

import (
	"fmt"
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
)

// MinEpisodes is the number of comparable historical episodes below which the
// adverse-excursion statistics are withheld.
//
// Thirty matches modelhealth.MinObservations so one number governs "is this
// evidence" across the platform. It is a floor on being ALLOWED to speak, not a
// claim that thirty episodes make a reliable tail estimate — which is exactly
// why the tail is reported as a measured percentile of the sample rather than as
// a fitted distribution that would imply more precision than the data holds.
const MinEpisodes = 30

// Episode is one historical instance of the trade being assessed: where it was
// entered and the path it took while the position was held.
//
// Lows is the sequence of the worst price reached in each period of the holding
// window, in order. Using the LOW rather than the close is deliberate: a position
// that closed flat after trading 8% down did hurt by 8%, and a reader deciding
// whether they could sit through it needs the number they would actually have
// watched. Callers with only closes may pass those, and the field name says what
// was measured.
type Episode struct {
	EntryPx float64
	Lows    []float64
	// Fwd is the realized forward return of the episode, used for the expected
	// return. It is separate from Lows because "where it ended" and "how far it
	// went against you first" are different questions.
	Fwd float64
}

// Excursion is the measured downside shape of a set of comparable episodes. Every
// value is a POSITIVE fraction of the entry price.
type Excursion struct {
	N int `json:"n"`
	// Mean is the average worst adverse excursion across episodes.
	Mean float64 `json:"mean"`
	// Median is the typical one — reported alongside the mean because a single
	// catastrophic episode moves one and not the other.
	Median float64 `json:"median"`
	// P90 is the 90th-percentile excursion: the "bad but not unprecedented"
	// case, and the number a position-sizing decision should actually respect.
	P90 float64 `json:"p90"`
	// Worst is the deepest excursion observed. It is the sample maximum, NOT a
	// bound — the next episode is free to exceed it.
	Worst float64 `json:"worst"`

	Valid bool   `json:"valid"`
	Note  string `json:"note"`
}

// AdverseExcursion measures how far the historical episodes went against the
// entry before they resolved.
//
// An episode whose entry price or path is unusable is SKIPPED rather than
// treated as a zero excursion, because a zero would be the most flattering
// possible reading of missing data and would drag every statistic here toward
// "this trade never hurts".
func AdverseExcursion(eps []Episode) Excursion {
	var maes []float64
	skipped := 0
	for _, e := range eps {
		mae, ok := episodeMAE(e)
		if !ok {
			skipped++
			continue
		}
		maes = append(maes, mae)
	}

	x := Excursion{N: len(maes)}
	if len(maes) < MinEpisodes {
		x.Note = fmt.Sprintf(
			"only %d usable episodes (need %d) — the downside shape of this setup is withheld rather than estimated from too few",
			len(maes), MinEpisodes)
		return x
	}

	sort.Float64s(maes)
	var sum float64
	for _, v := range maes {
		sum += v
	}
	x.Mean = sum / float64(len(maes))
	x.Median = percentileSorted(maes, 0.50)
	x.P90 = percentileSorted(maes, 0.90)
	x.Worst = maes[len(maes)-1]
	x.Valid = true
	x.Note = fmt.Sprintf(
		"worst adverse excursion from entry across %d comparable historical episodes; Worst is the sample maximum, not a bound", len(maes))
	if skipped > 0 {
		x.Note += fmt.Sprintf(" (%d episodes skipped for an unusable price path)", skipped)
	}
	return x
}

// episodeMAE is one episode's maximum adverse excursion as a positive fraction of
// entry. An episode that never traded below its entry has an excursion of zero,
// which is a real measurement and not a missing one.
func episodeMAE(e Episode) (float64, bool) {
	if e.EntryPx <= 0 || math.IsNaN(e.EntryPx) || math.IsInf(e.EntryPx, 0) {
		return 0, false
	}
	if len(e.Lows) == 0 {
		return 0, false
	}
	worst := 0.0
	seen := false
	for _, low := range e.Lows {
		if low <= 0 || math.IsNaN(low) || math.IsInf(low, 0) {
			continue
		}
		seen = true
		if d := 1 - low/e.EntryPx; d > worst {
			worst = d
		}
	}
	if !seen {
		return 0, false
	}
	return worst, true
}

// percentileSorted returns the p-quantile of an ascending slice by nearest-rank.
// Nearest-rank rather than interpolation because the result is reported as "an
// excursion that actually happened", and an interpolated value between two
// observations did not.
func percentileSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Evidence is the out-of-sample record behind a probability — forecast.Grade's
// fields passed as plain numbers so this package imports no model.
type Evidence struct {
	N        int     // independent out-of-sample predictions scored
	Accuracy float64 // realized directional accuracy
	BaseRate float64 // majority-class accuracy floor
	Brier    float64 // mean squared error of the probability
	// EffectiveN is the day-clustered effective sample size behind N — N over
	// the design effect the CALLER measured from the record's per-day tallies
	// (clusterstat.DesignEffect). Zero means it was not measured, and the
	// uncertainty is WITHHELD rather than computed at the raw row count: rows
	// on one day share one market move, and an interval over raw rows was
	// measured ~3.8x too narrow on the live record (A1).
	EffectiveN float64
	// CalibrationErr is mean |predicted - realized| across probability bins. Zero
	// is only meaningful if it was measured; pass CalibrationKnown accordingly.
	CalibrationErr   float64
	CalibrationKnown bool
}

// Lift is Accuracy - BaseRate: the honest edge. At or below zero the model did
// no better than always guessing the majority class.
func (e Evidence) Lift() float64 { return e.Accuracy - e.BaseRate }

// Assessment is the unified read on one prediction.
//
// Probability is the only unconditional field, because it is what the model
// emitted and is always available. Everything derived from EVIDENCE about that
// probability is a pointer and is nil when it could not be measured.
type Assessment struct {
	Probability float64 `json:"probability"`

	// ExpectedReturn is the mean realized forward return of comparable episodes.
	ExpectedReturn *float64 `json:"expectedReturn"`
	// ExpectedDrawdown is the MEAN adverse excursion of those episodes, and
	// DrawdownTail the 90th percentile. Both positive fractions.
	ExpectedDrawdown *float64 `json:"expectedDrawdown"`
	DrawdownTail     *float64 `json:"drawdownTail"`

	// Confidence in [0,1] is how much the RECORD supports believing the
	// probability at all — not a restatement of the probability. A 0.95
	// probability from a model with no demonstrated edge is a confident number
	// with no confidence behind it, and this field is what says so.
	Confidence *float64 `json:"confidence"`
	// Uncertainty is the half-width of the 95% Wilson interval on the realized
	// accuracy, evaluated at the day-clustered EFFECTIVE sample size: how much
	// of the apparent skill could be luck at the sample size the record
	// actually holds, not at its raw row count.
	Uncertainty *float64 `json:"uncertainty"`

	// Reasons explains the assessment in plain language, always populated.
	Reasons []string `json:"reasons"`
	// Withheld names every nil field with the reason it is nil, so an empty
	// assessment reads as "not measurable" rather than as a neutral result.
	Withheld []string `json:"withheld,omitempty"`
}

// Assess assembles the unified object from a probability, the out-of-sample
// record behind it, and the measured behaviour of comparable episodes.
//
// expected is the conditional forward-return statistic (internal/expectancy's
// mean, with the sample size that produced it); ex is the adverse-excursion
// measurement from AdverseExcursion. Either may be absent, and the assessment
// says so rather than filling in.
func Assess(prob float64, ev Evidence, expected ConditionalReturn, ex Excursion) Assessment {
	a := Assessment{Probability: prob}

	// Expected return.
	if expected.Valid {
		v := expected.Mean
		a.ExpectedReturn = &v
		a.Reasons = append(a.Reasons, fmt.Sprintf(
			"comparable setups averaged %+.2f%% forward over %d observations", v*100, expected.N))
	} else {
		a.Withheld = append(a.Withheld, "expectedReturn: "+orDefault(expected.Note,
			"no conditional forward-return statistic for this state"))
	}

	// Downside.
	if ex.Valid {
		mean, tail := ex.Mean, ex.P90
		a.ExpectedDrawdown = &mean
		a.DrawdownTail = &tail
		a.Reasons = append(a.Reasons, fmt.Sprintf(
			"typically went %.2f%% against the entry before resolving, %.2f%% in the worst tenth (deepest seen %.2f%%)",
			mean*100, tail*100, ex.Worst*100))
	} else {
		a.Withheld = append(a.Withheld, "expectedDrawdown/drawdownTail: "+orDefault(ex.Note,
			"no comparable episodes to measure downside from"))
	}

	// Confidence + uncertainty, both gated on the same evidence floor: below it
	// there is no record to be confident ABOUT.
	//
	// The floor counts EFFECTIVE observations, never rows. Rows on one day share
	// one market move, so 600 rows carrying the information of 12 are twelve
	// observations however the row counter reads, and a floor that accepts them
	// is not a floor. When the effective size was never measured there is no
	// certified sample size at all, and both fields are withheld together —
	// scoring the model on a sample size the platform has just refused to vouch
	// for is the same defect wearing a different number.
	measured := ev.EffectiveN > 0 && ev.Accuracy >= 0 && ev.Accuracy <= 1 && !math.IsNaN(ev.Accuracy)
	switch {
	case !measured:
		reason := "no day-clustered effective sample size was measured for this record, " +
			"and a judgement over raw rows would assert independence the observations do not have"
		a.Withheld = append(a.Withheld, "confidence: "+reason, "uncertainty: "+reason)
	case ev.N < MinEpisodes || ev.EffectiveN < float64(MinEpisodes):
		reason := fmt.Sprintf("only %.0f effective out-of-sample observations behind %d rows (need %d)",
			ev.EffectiveN, ev.N, MinEpisodes)
		a.Withheld = append(a.Withheld, "confidence: "+reason, "uncertainty: "+reason)
	default:
		// clusterstat.WilsonEff at the caller-measured effective N — the ONE
		// interval implementation the platform's gates read, so this published
		// uncertainty cannot disagree with the decision gates about what a
		// sample size is.
		half := clusterstat.WilsonEff(ev.Accuracy, ev.EffectiveN).Width() / 2
		a.Uncertainty = &half
		c := scoreConfidence(ev, half)
		a.Confidence = &c
		a.Reasons = append(a.Reasons, fmt.Sprintf(
			"confidence %.2f from a %+.1f-point edge over the base rate on %.0f effective out-of-sample observations (%d rows)",
			c, ev.Lift()*100, ev.EffectiveN, ev.N))
		if ev.Lift() <= 0 {
			a.Reasons = append(a.Reasons,
				"the model has NOT beaten its base rate out of sample — the probability is a number, not a demonstrated edge")
		}
	}

	if len(a.Reasons) == 0 {
		a.Reasons = append(a.Reasons,
			"nothing about this prediction is measurable yet beyond the probability itself")
	}
	return a
}

// ConditionalReturn is the expectancy input: the mean forward return of
// comparable episodes and the sample behind it.
type ConditionalReturn struct {
	Mean  float64
	N     int
	Valid bool
	Note  string
}

// scoreConfidence turns the out-of-sample record into a [0,1] score.
//
// The composition is deliberately simple and stated rather than tuned, because a
// tuned composite invites the reader to trust the third decimal place:
//
//	edge        (0.50) — Lift, normalized against a 10-point edge being "full"
//	calibration (0.25) — 1 - CalibrationErr/0.10, so a 10-point miscalibration scores 0
//	sample      (0.25) — EFFECTIVE observations/300, so confidence keeps growing
//	                     with evidence and saturates where the platform's own
//	                     gates stop caring
//
// The sample term's denominator is the day-clustered effective count, not the
// row count: 300 rows that carry the information of 60 must not saturate a term
// that means "how much evidence is behind this". Assess only reaches here when
// that count was measured, so there is no raw-row fallback to drift back into.
//
// A non-positive Lift scores ZERO on the edge term outright rather than scaling
// smoothly through it: "did it beat the base rate" is a threshold question, and
// the answer being no should not be recoverable by good calibration on a large
// sample of no-edge predictions. When calibration was not measured its weight is
// redistributed rather than assumed good.
func scoreConfidence(ev Evidence, halfWidth float64) float64 {
	const fullEdge = 0.10
	const fullSample = 300.0
	const calFloor = 0.10

	edge := 0.0
	if l := ev.Lift(); l > 0 {
		edge = clamp01(l / fullEdge)
		// An edge inside its own error bar is not yet an edge. Halve the term
		// rather than zero it: the point estimate is still the best guess.
		if l < halfWidth {
			edge *= 0.5
		}
	}
	sample := clamp01(ev.EffectiveN / fullSample)

	if !ev.CalibrationKnown {
		// 0.50 edge + 0.25 sample, renormalized to 1.
		return clamp01((0.50*edge + 0.25*sample) / 0.75)
	}
	cal := clamp01(1 - ev.CalibrationErr/calFloor)
	return clamp01(0.50*edge + 0.25*cal + 0.25*sample)
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(1, v))
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
