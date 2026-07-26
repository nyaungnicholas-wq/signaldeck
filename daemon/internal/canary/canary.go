// Package canary decides whether a NEW model version may replace the one
// currently in production.
//
// # The gap this closes
//
// Until now a new model version reached 100% of decisions the moment it was
// deployed. Every other honesty control here is about not believing a model
// too early — the OOS lift gate, the n>=30 floors, the Wilson-interval
// verdicts — and then deployment threw that discipline away, because a retrained
// model inherited the incumbent's authority automatically. A challenger has to
// earn production the same way any claim earns display: by beating the thing it
// wants to replace, on live forward data, by a margin that survives its own
// confidence interval.
//
// # The rule
//
// BOTH arms must first clear the same floors, measured the same way: at least
// MinObservations graded observations, falling on at least MinWindowDays
// distinct UTC days, spanning at least MinWindowDays. Until they do there is
// nothing to compare and the verdict says so — "no comparison possible —
// holding" — rather than issuing a decision that reads as considered.
//
// That symmetry is the correction to a real defect. Applying the window rule to
// the challenger only meant a challenger had to earn two weeks of evidence
// against an incumbent whose entire record might be four hours long: live on
// 2026-07-26 the 1d incumbent was feature version v8 with 1,046 observations on
// 2 distinct days spanning 0.174 days, and the challenger was graded against
// it. Four hours is one market state, so whichever way the noise in it pointed
// decided which model served — promoting a challenger if those hours went badly
// for the incumbent, and terminating the trial by rejection if they went well.
//
// Once both arms are gradable, a challenger is PROMOTED only when:
//
//   - the LOWER bound of its Wilson interval exceeds the incumbent's point
//     accuracy by MinMarginPp — the same "interval, not point estimate" rule
//     the accuracy registry uses;
//   - and it also clears the naive majority-class baseline, so a challenger
//     cannot win merely by being less bad than a failing incumbent.
//
// It is REJECTED when its own interval sits entirely BELOW the incumbent's
// point accuracy: that is positive evidence of being worse, not absence of
// evidence. Everything else is HOLD — keep observing, keep the incumbent
// serving. Hold is the default and the most common answer, which is correct.
//
// # Traffic
//
// While a challenger is held, it runs in SHADOW: it computes and records its
// forecasts, and serves none of them. This package returns the shadow/serving
// split rather than a probability, because a percentage rollout on one machine
// with one user would be theatre — the meaningful distinction is whether a
// number is displayed and acted on, or merely recorded and graded.
package canary

import (
	"fmt"
	"math"
)

const (
	// MinObservations is the independent-observation floor EACH arm must clear.
	// Matches the platform-wide n>=30 gate.
	MinObservations = 30
	// MinWindowDays is the minimum observation window, counted in DISTINCT UTC
	// days with graded observations and also required of the span. An arm that
	// has only seen one market state has not been tested, however many rows it
	// has — and rows are not days: the live v8 incumbent carried 1,046
	// observations on 2 days. Counted as a span alone the floor is satisfiable
	// by two days a fortnight apart, which is two days of evidence.
	MinWindowDays = 14
	// MinMarginPp is the additional margin, in percentage points, that the
	// challenger's Wilson lower bound must clear the incumbent by. Small but
	// non-zero: promoting on a hairline win invites promoting on noise.
	MinMarginPp = 0.5
)

// Decision is the outcome of a canary evaluation.
type Decision string

const (
	// DecisionPromote — the challenger becomes the serving model.
	DecisionPromote Decision = "promote"
	// DecisionHold — keep observing; incumbent keeps serving.
	DecisionHold Decision = "hold"
	// DecisionReject — the challenger is measurably worse; stop the trial.
	DecisionReject Decision = "reject"
)

// Record is one model version's graded live record over independent
// observations.
type Record struct {
	// Version identifies the model build being graded.
	Version string `json:"version"`
	// N is independent graded observations.
	N int `json:"n"`
	// Correct among N.
	Correct int `json:"correct"`
	// Days is how many DISTINCT UTC days those observations fall on. It is
	// required, and it cannot be derived from N or from the span: 1,046
	// observations spread across four hours span 0.174 days and touch 2 days.
	// A record that leaves it zero is not graded at all — an unchecked floor is
	// not a floor, and assuming the span stands in for it is the assumption
	// this field exists to remove. Fill it with DistinctDays.
	Days int `json:"days"`
	// FirstTs / LastTs bound the observation window (Unix seconds).
	FirstTs int64 `json:"firstTs"`
	LastTs  int64 `json:"lastTs"`
	// BaselineAccuracy is the naive majority-class accuracy over the SAME
	// observations — the null any model must beat to be worth serving.
	BaselineAccuracy float64 `json:"baselineAccuracy"`
}

// Accuracy is Correct/N, or 0 when N is 0.
func (r Record) Accuracy() float64 {
	if r.N == 0 {
		return 0
	}
	return float64(r.Correct) / float64(r.N)
}

// WindowDays is the observed span in days. It is an upper bound on how much
// market the record has seen, never a substitute for Days.
func (r Record) WindowDays() float64 {
	if r.LastTs <= r.FirstTs {
		return 0
	}
	return float64(r.LastTs-r.FirstTs) / 86400
}

// Gradable reports whether this arm has enough record to take part in a
// comparison at all, and what it still lacks. Challenger and incumbent go
// through this one function, which is the point: the earlier version applied
// the window rule to the challenger alone, so a four-hour incumbent could
// promote or reject a model that had run for months.
//
// lack is a sentence fragment naming the shortfall, for the verdict to state.
func (r Record) Gradable() (ok bool, lack string, obsNeeded int, daysNeeded float64) {
	if r.N < MinObservations {
		return false, fmt.Sprintf("has %d graded observations, short of the %d it needs to be judged",
			r.N, MinObservations), MinObservations - r.N, 0
	}
	// The span is checked before the day count because it settles the question
	// on its own when it fails: a record that spans four hours has not seen two
	// weeks whatever its Days says, so it earns the specific reason rather than
	// the weaker "cannot be checked" one below.
	if span := r.WindowDays(); span < MinWindowDays {
		return false, fmt.Sprintf("spans only %.2f days, short of the %d it needs to have seen more than one market state",
			span, MinWindowDays), 0, MinWindowDays - span
	}
	if r.Days <= 0 {
		// Withheld, not assumed. A long span does not imply days of data inside
		// it, so there is nothing here to fall back on.
		return false, "does not report how many distinct days its observations cover, so its window cannot be checked",
			0, float64(MinWindowDays)
	}
	if r.Days < MinWindowDays {
		return false, fmt.Sprintf("has observations on only %d distinct days, short of the %d it needs to have seen more than one market state",
			r.Days, MinWindowDays), 0, float64(MinWindowDays - r.Days)
	}
	return true, "", 0, 0
}

// DistinctDays counts the distinct UTC days covered by observation timestamps
// (Unix seconds) — the days-not-rows unit the rest of the platform resamples
// on, and the only correct way to fill Record.Days.
func DistinctDays(ts []int64) int {
	seen := make(map[int64]struct{}, len(ts))
	for _, t := range ts {
		seen[t/86400] = struct{}{}
	}
	return len(seen)
}

// Verdict is the full evaluation, built to be rendered verbatim: the reason is
// part of the output, not something a caller reconstructs.
type Verdict struct {
	Decision Decision `json:"decision"`
	// Challenger/Incumbent accuracies and the challenger's interval.
	ChallengerAccuracy float64 `json:"challengerAccuracy"`
	ChallengerLower    float64 `json:"challengerLower"`
	ChallengerUpper    float64 `json:"challengerUpper"`
	IncumbentAccuracy  float64 `json:"incumbentAccuracy"`
	Baseline           float64 `json:"baseline"`
	// Comparable is false when a comparison was never made because an arm did
	// not clear the floors. It separates "we looked and held" from "we could
	// not look", which the single word "hold" cannot.
	Comparable bool `json:"comparable"`
	// Each arm's sample size beside its distinct-day count, because 1,046
	// observations on 2 days is two days of evidence and the row count alone
	// hides that.
	ChallengerN    int `json:"challengerN"`
	ChallengerDays int `json:"challengerDays"`
	IncumbentN     int `json:"incumbentN"`
	IncumbentDays  int `json:"incumbentDays"`
	// Serving is the version that should serve after this decision.
	Serving string `json:"serving"`
	// Shadow is the version that records without serving, empty when the trial
	// has concluded either way.
	Shadow string `json:"shadow"`
	// Reason states, in one sentence, why.
	Reason string `json:"reason"`
	// ObservationsNeeded is how many more the challenger needs, 0 when the
	// floor is met.
	ObservationsNeeded int `json:"observationsNeeded"`
	// DaysNeeded is how much longer the window must run, 0 when met.
	DaysNeeded float64 `json:"daysNeeded"`
	// The same two shortfalls for the INCUMBENT. They are published for the
	// same reason the challenger's are: a bar that cannot be verified is a fact
	// about the platform, not a detail to keep on the inside.
	IncumbentObservationsNeeded int     `json:"incumbentObservationsNeeded"`
	IncumbentDaysNeeded         float64 `json:"incumbentDaysNeeded"`
}

// Evaluate applies the promotion rule. It is pure and total: every input,
// including empty records, produces a defensible verdict rather than an error.
func Evaluate(incumbent, challenger Record) Verdict {
	v := Verdict{
		ChallengerAccuracy: challenger.Accuracy(),
		IncumbentAccuracy:  incumbent.Accuracy(),
		Baseline:           challenger.BaselineAccuracy,
		ChallengerN:        challenger.N,
		ChallengerDays:     challenger.Days,
		IncumbentN:         incumbent.N,
		IncumbentDays:      incumbent.Days,
		Serving:            incumbent.Version,
		Shadow:             challenger.Version,
		Decision:           DecisionHold,
	}
	lo, hi := WilsonInterval(challenger.Correct, challenger.N)
	v.ChallengerLower, v.ChallengerUpper = lo, hi

	// Both arms, same floors, before any comparison. The incumbent is checked
	// first because without a bar there is nothing to measure a challenger
	// against, however complete the challenger's own record is.
	if ok, lack, need, days := incumbent.Gradable(); !ok {
		v.IncumbentObservationsNeeded, v.IncumbentDaysNeeded = need, days
		v.Reason = "no comparison possible — holding: the incumbent " + lack +
			", so there is no bar a challenger could be measured against"
		return v
	}
	if ok, lack, need, days := challenger.Gradable(); !ok {
		v.ObservationsNeeded, v.DaysNeeded = need, days
		v.Reason = "no comparison possible — holding: the challenger " + lack
		return v
	}
	v.Comparable = true

	margin := MinMarginPp / 100
	switch {
	case hi < incumbent.Accuracy():
		// The challenger's whole interval is below the incumbent: positive
		// evidence of being worse.
		v.Decision, v.Shadow = DecisionReject, ""
		v.Reason = "rejected: the challenger's entire confidence interval sits below the incumbent's live accuracy"
	case lo <= challenger.BaselineAccuracy:
		v.Reason = "holding: the challenger does not clear the naive baseline, so replacing a failing incumbent with it would change nothing that matters"
	case lo > incumbent.Accuracy()+margin:
		v.Decision = DecisionPromote
		v.Serving, v.Shadow = challenger.Version, ""
		v.Reason = "promoted: the challenger's confidence interval clears both the incumbent's live accuracy and the naive baseline"
	default:
		v.Reason = "holding: the challenger leads on the point estimate but not by more than its own confidence interval"
	}
	return v
}

// WilsonInterval returns the 95% Wilson score interval for k successes in n
// trials — the same estimator the accuracy registry uses, so a canary decision
// and a registry verdict can never disagree about the same numbers.
func WilsonInterval(k, n int) (lo, hi float64) {
	if n <= 0 {
		return 0, 1
	}
	const z = 1.959963984540054
	p := float64(k) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	center := (p + z*z/(2*nf)) / denom
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	lo, hi = center-half, center+half
	return math.Max(0, lo), math.Min(1, hi)
}
