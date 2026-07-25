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
// A challenger is PROMOTED only when all of these hold:
//
//   - it has at least MinObservations independent graded observations of its own;
//   - it has been observed for at least MinWindowDays (a challenger can look
//     excellent for two days in one regime);
//   - the LOWER bound of its Wilson interval exceeds the incumbent's point
//     accuracy — the same "interval, not point estimate" rule the accuracy
//     registry uses;
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

import "math"

const (
	// MinObservations is the challenger's independent-observation floor. Matches
	// the platform-wide n>=30 gate.
	MinObservations = 30
	// MinWindowDays is the minimum observation span. A challenger that has only
	// seen one market regime has not been tested, however many rows it has.
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

// WindowDays is the observed span in days.
func (r Record) WindowDays() float64 {
	if r.LastTs <= r.FirstTs {
		return 0
	}
	return float64(r.LastTs-r.FirstTs) / 86400
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
}

// Evaluate applies the promotion rule. It is pure and total: every input,
// including empty records, produces a defensible verdict rather than an error.
func Evaluate(incumbent, challenger Record) Verdict {
	v := Verdict{
		ChallengerAccuracy: challenger.Accuracy(),
		IncumbentAccuracy:  incumbent.Accuracy(),
		Baseline:           challenger.BaselineAccuracy,
		Serving:            incumbent.Version,
		Shadow:             challenger.Version,
		Decision:           DecisionHold,
	}
	lo, hi := WilsonInterval(challenger.Correct, challenger.N)
	v.ChallengerLower, v.ChallengerUpper = lo, hi

	if challenger.N < MinObservations {
		v.ObservationsNeeded = MinObservations - challenger.N
		v.Reason = "holding: the challenger has too few independent observations to judge"
		return v
	}
	if d := challenger.WindowDays(); d < MinWindowDays {
		v.DaysNeeded = MinWindowDays - d
		v.Reason = "holding: the challenger has not been observed long enough to have seen more than one market state"
		return v
	}

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
