// Package metalabel implements META-LABELING — the secondary model that decides
// whether a primary signal's call is worth ACTING ON, rather than what the call is.
//
// # The split of labour
//
// The primary model answers "which way?". The secondary answers "should we take
// this one at all?", trained on the only label that matters for that question:
// was the primary's call right BY MORE THAN IT COST to trade. The primary keeps
// the side; the meta-model keeps the trigger. That separation is the point —
// a model asked to do both jobs at once optimises neither, and in this repo the
// combined version optimised the one that does not make money.
//
// # The rule this package exists to enforce
//
// Filtering a signal that has no edge produces fewer trades with the SAME lack
// of edge. This platform proved that on its own data: stacking selection layers
// on top of conviction made returns WORSE, because trend21's most accurate band
// (97.2%) carries a NEGATIVE forward return. So a meta-model here must clear two
// bars that a naive precision-maximiser would sail past:
//
//  1. The PRIMARY must have measurable cost-net edge before filtering it means
//     anything. No edge in, no edge out — Evaluate refuses with a stated reason
//     rather than reporting a flattering precision on a worthless base signal.
//  2. The FILTER must improve EXPECTANCY, not accuracy. Precision rising while
//     expectancy falls is the exact trap trend21 fell into, and it is scored as
//     a rejection here. Expectancy is measured in return per DECISION OFFERED,
//     so a filter cannot win by simply trading less.
//
// # Grading
//
// Strict expanding-window walk-forward with the same fold geometry as
// forecast/gbm/meanrev: fold k trains on everything before it and is scored only
// on rows after it, so a row is never graded by a model that saw its own outcome.
// The secondary learner is internal/gbm (from-scratch gradient-boosted trees),
// reused rather than reimplemented — meta-labeling's value is in the TARGET, not
// in a novel learner, and interactions between context features are exactly what
// a boosted tree finds.
//
// Callers are expected to hand in INDEPENDENT observations (one per symbol-day).
// Pooling intraday rows inflates n roughly 60x on this platform's data and has
// produced fake significance here before; this package grades what it is given
// and states the assumption rather than silently trusting it.
package metalabel

import (
	"errors"
	"math"
	"sort"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
)

var (
	// ErrTooFewSamples is returned when the sample set cannot support a
	// walk-forward grade at all.
	ErrTooFewSamples = errors.New("metalabel: too few samples to grade")
	// ErrNoDirectionalCalls is returned when the primary never took a side, so
	// there is nothing to filter.
	ErrNoDirectionalCalls = errors.New("metalabel: primary made no directional calls")
	// ErrNotAscending is returned when samples are not in ascending time order,
	// which would break the walk-forward no-lookahead guarantee.
	ErrNotAscending = errors.New("metalabel: samples must be sorted ascending by ts")
)

const (
	// MinConviction is how far from 0.5 the primary must lean before the call
	// counts as directional. Below it the primary abstained and there is no
	// trade for the meta-model to accept or reject.
	MinConviction = 0.02
	// MinTakenTrades is the floor on trades the filter must still take before
	// its measured expectancy is reported as meaningful. A filter that takes
	// four trades and wins three has told us nothing.
	MinTakenTrades = 30
	// DefaultThreshold is the meta-probability above which a candidate is taken.
	DefaultThreshold = 0.55
)

// Sample is one candidate decision from the primary model, with its realized
// outcome and the context the meta-model is allowed to judge it by.
type Sample struct {
	Ts int64
	// PrimaryProb is the primary model's P(up) for this row. Its distance from
	// 0.5 is the side and the conviction.
	PrimaryProb float64
	// Context is the meta-feature vector: regime, volatility, liquidity,
	// breadth — the circumstances of the call, NOT a restatement of the call.
	Context []float64
	// FwdReturn is the realized forward return over the primary's horizon.
	FwdReturn float64
}

// Grade is the honest verdict on whether meta-labeling earned its place.
type Grade struct {
	N int `json:"n"` // directional candidates graded out-of-sample

	// Primary-model baseline: what taking EVERY call would have produced.
	PrimaryPrecision  float64 `json:"primaryPrecision"`  // fraction of calls right net of cost
	PrimaryExpectancy float64 `json:"primaryExpectancy"` // mean cost-net return per decision offered

	// Filtered: what taking only the meta-model's accepted calls produced.
	TakenN            int     `json:"takenN"`
	TakeRate          float64 `json:"takeRate"`          // fraction of candidates accepted
	FilteredPrecision float64 `json:"filteredPrecision"` // precision among taken
	// FilteredExpectancy is per DECISION OFFERED, not per trade taken — a
	// filter cannot inflate it by trading less.
	FilteredExpectancy float64 `json:"filteredExpectancy"`

	// ExpectancyLift is the number that decides everything. <= 0 means the
	// filter did not earn its place, however good its precision looks.
	ExpectancyLift float64 `json:"expectancyLift"`
	PrecisionLift  float64 `json:"precisionLift"`

	// PrimaryHasEdge records whether the base signal was worth filtering at all.
	PrimaryHasEdge bool `json:"primaryHasEdge"`
	// Verdict is one of "earned", "rejected", or "insufficient".
	Verdict string `json:"verdict"`
	// Reason states plainly why, and is never empty.
	Reason string `json:"reason"`
}

// Verdicts.
const (
	VerdictEarned       = "earned"
	VerdictRejected     = "rejected"
	VerdictInsufficient = "insufficient"
)

// Evaluate grades meta-labeling walk-forward over samples, with cost applied to
// every realized outcome, and returns the honest verdict.
//
// folds is the number of expanding-window folds; cost is the round-trip cost as
// a return fraction (e.g. 0.001 = 10bps); threshold is the meta-probability at
// or above which a candidate is taken.
func Evaluate(samples []Sample, folds int, cost, threshold float64) (Grade, error) {
	if !ascendingTs(samples) {
		return Grade{}, ErrNotAscending
	}
	cands := directional(samples)
	if len(cands) == 0 {
		return Grade{}, ErrNoDirectionalCalls
	}
	if folds < 2 || len(cands) < minTrain+foldStride {
		return Grade{}, ErrTooFewSamples
	}

	// The meta-label: did the primary's side clear cost on this row?
	metaSamples := make([]gbm.Sample, len(cands))
	for i, c := range cands {
		metaSamples[i] = gbm.Sample{
			Ts:   c.Ts,
			Feat: c.Context,
			Y:    metaLabel(c, cost),
		}
	}

	// Walk-forward: collect out-of-sample meta-probabilities aligned to cands.
	probs, scored, err := walkForward(metaSamples, folds)
	if err != nil {
		return Grade{}, err
	}
	if len(scored) == 0 {
		return Grade{}, ErrTooFewSamples
	}

	g := Grade{N: len(scored)}

	// Baseline: take every graded candidate.
	var primHits int
	var primSum float64
	for _, i := range scored {
		c := cands[i]
		primSum += costNetReturn(c, cost)
		if metaLabel(c, cost) == 1 {
			primHits++
		}
	}
	g.PrimaryPrecision = float64(primHits) / float64(len(scored))
	g.PrimaryExpectancy = primSum / float64(len(scored))
	// The primary is worth filtering only if acting on it is not already a
	// losing proposition after cost.
	g.PrimaryHasEdge = g.PrimaryExpectancy > 0

	// Filtered: take only where the meta-model clears the threshold. Expectancy
	// stays divided by ALL decisions offered so trading less cannot flatter it.
	var takenHits int
	var takenSum float64
	for _, i := range scored {
		if probs[i] < threshold {
			continue
		}
		c := cands[i]
		g.TakenN++
		takenSum += costNetReturn(c, cost)
		if metaLabel(c, cost) == 1 {
			takenHits++
		}
	}
	g.TakeRate = float64(g.TakenN) / float64(len(scored))
	if g.TakenN > 0 {
		g.FilteredPrecision = float64(takenHits) / float64(g.TakenN)
	}
	g.FilteredExpectancy = takenSum / float64(len(scored))
	g.ExpectancyLift = g.FilteredExpectancy - g.PrimaryExpectancy
	g.PrecisionLift = g.FilteredPrecision - g.PrimaryPrecision

	g.Verdict, g.Reason = judge(g)
	return g, nil
}

// judge applies the two gates in the order that matters: an edgeless primary is
// disqualifying on its own, and expectancy overrules precision.
func judge(g Grade) (verdict, reason string) {
	if !g.PrimaryHasEdge {
		return VerdictRejected, "the primary signal has no cost-net edge to filter " +
			"(expectancy " + pct(g.PrimaryExpectancy) + " per decision); filtering it yields " +
			"fewer trades with the same lack of edge, so no meta-model can rescue it"
	}
	if g.TakenN < MinTakenTrades {
		return VerdictInsufficient, "the filter took only " + itoa(g.TakenN) + " of " +
			itoa(g.N) + " candidates — below the " + itoa(MinTakenTrades) +
			"-trade floor, so its measured expectancy is not yet distinguishable from luck"
	}
	if g.ExpectancyLift <= 0 {
		r := "the filter did not improve expectancy (" + pct(g.FilteredExpectancy) +
			" vs " + pct(g.PrimaryExpectancy) + " per decision offered)"
		if g.PrecisionLift > 0 {
			r += "; its precision DID rise (+" + pct(g.PrecisionLift) +
				"), which is the trend21 trap — it is selecting calls that are more often " +
				"right and less often worth taking"
		}
		return VerdictRejected, r
	}
	return VerdictEarned, "the filter improved expectancy by " + pct(g.ExpectancyLift) +
		" per decision offered while taking " + pct(g.TakeRate) + " of candidates"
}

// Run grades the meta-model and, when it is earned, returns a take/skip decision
// for the latest candidate. ok is false whenever the verdict is not "earned" —
// an unearned meta-model must not gate anything.
func Run(samples []Sample, latestContext []float64, folds int, cost, threshold float64) (take bool, prob float64, g Grade, ok bool) {
	g, err := Evaluate(samples, folds, cost, threshold)
	if err != nil || g.Verdict != VerdictEarned {
		if err != nil {
			g.Verdict, g.Reason = VerdictInsufficient, err.Error()
		}
		return false, 0, g, false
	}
	cands := directional(samples)
	metaSamples := make([]gbm.Sample, len(cands))
	for i, c := range cands {
		metaSamples[i] = gbm.Sample{Ts: c.Ts, Feat: c.Context, Y: metaLabel(c, cost)}
	}
	m, err := gbm.Train(metaSamples, gbm.Defaults())
	if err != nil {
		return false, 0, g, false
	}
	prob = m.Predict(latestContext)
	return prob >= threshold, prob, g, true
}

// walkForward returns out-of-sample meta-probabilities indexed to samples, plus
// the indices actually scored. Fold k trains strictly on rows before it.
//
// The fold boundaries are a FIXED stride from a fixed origin, deliberately NOT
// derived from len(samples). Deriving them from n is the obvious implementation
// and it is subtly wrong here: every new observation would re-cut the whole
// history, so a row graded today could carry a different out-of-sample
// probability tomorrow purely because unrelated rows arrived after it. That is
// not lookahead — each model still trains only on its own past — but it makes
// recorded grades irreproducible, and this platform re-grades continuously and
// tracks model health over time. With a fixed stride, a row's grade is decided
// once and never moves. The test pins exactly this.
//
// folds is the minimum number of retrain boundaries the caller demands before a
// grade is considered structurally sound.
func walkForward(samples []gbm.Sample, folds int) (probs []float64, scored []int, err error) {
	n := len(samples)
	probs = make([]float64, n)
	if n < minTrain+minPerFold {
		return nil, nil, ErrTooFewSamples
	}
	// Boundaries: minTrain, minTrain+stride, minTrain+2*stride, ... independent of n.
	available := (n - minTrain + foldStride - 1) / foldStride
	if available < folds {
		return nil, nil, ErrTooFewSamples
	}
	for trainEnd := minTrain; trainEnd < n; trainEnd += foldStride {
		testEnd := trainEnd + foldStride
		if testEnd > n {
			testEnd = n
		}
		m, e := gbm.Train(samples[:trainEnd], gbm.Defaults())
		if e != nil {
			continue
		}
		for i := trainEnd; i < testEnd; i++ {
			probs[i] = m.Predict(samples[i].Feat)
			scored = append(scored, i)
		}
	}
	return probs, scored, nil
}

const (
	// minTrain mirrors gbm's own floor: below it a boosted tree is fitting noise.
	minTrain = 60
	// minPerFold keeps each fold's out-of-sample slice large enough to score.
	minPerFold = 12
	// foldStride is how often the model retrains, in observations. Fixed on
	// purpose — see walkForward.
	foldStride = 30
)

// metaLabel is 1 when the primary's side cleared cost, 0 otherwise. This — not
// raw direction — is what the secondary model learns to predict.
func metaLabel(s Sample, cost float64) float64 {
	if costNetReturn(s, cost) > 0 {
		return 1
	}
	return 0
}

// costNetReturn is the realized return of following the primary's side once,
// after paying the round-trip cost.
func costNetReturn(s Sample, cost float64) float64 {
	if s.PrimaryProb >= 0.5 {
		return s.FwdReturn - cost
	}
	return -s.FwdReturn - cost
}

// directional keeps only rows where the primary actually took a side.
func directional(samples []Sample) []Sample {
	out := make([]Sample, 0, len(samples))
	for _, s := range samples {
		if math.Abs(s.PrimaryProb-0.5) >= MinConviction && len(s.Context) > 0 {
			out = append(out, s)
		}
	}
	return out
}

func ascendingTs(samples []Sample) bool {
	return sort.SliceIsSorted(samples, func(i, j int) bool {
		return samples[i].Ts < samples[j].Ts
	})
}

// pct renders a fraction as a percentage string for the human-facing Reason.
func pct(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 2, 64) + "%"
}

func itoa(v int) string { return strconv.Itoa(v) }
