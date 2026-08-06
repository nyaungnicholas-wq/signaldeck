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
// "Before it" means before BY LABEL, not by index — each fold is PURGED and
// EMBARGOED (de Prado). A candidate's meta-label is "did the primary's call clear
// cost over its forward horizon", so a row ordered before the split boundary
// still carries an answer decided AFTER it, and this platform's candidates are
// symbol-days: ~530 rows share one UTC day, so a boundary cutting a day in half
// hands the tree that day's outcomes and then grades it on the rest of the same
// day through a feature region those rows share. Callers therefore declare when
// their label resolved (Sample.LabelEnd, one line via WithLabelSpan); a set that
// declares nothing is REFUSED (ErrNoLabelSpan) rather than graded unpurged.
//
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
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"errors"
	"fmt"
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
	// ErrNoLabelSpan is returned when NO candidate declares when its label
	// resolved (Sample.LabelEnd). Without the horizon the purge width is
	// unknowable, so the walk-forward split cannot be certified free of the
	// overlap leak — and this package publishes a VERDICT, so an uncertifiable
	// verdict is withheld rather than printed. Callers declare the horizon they
	// labeled with in one line via WithLabelSpan.
	ErrNoLabelSpan = errors.New("metalabel: candidates do not declare a label horizon (LabelEnd) — cannot purge")
	// ErrPurgedTooThin is returned when purging leaves fewer usable retrain
	// boundaries than the caller demanded. It is the honest answer for a history
	// shorter than a few label spans: a filter graded on one surviving retrain is
	// not walk-forward evidence, and loosening the gate to publish something
	// would be the exact flattering arithmetic this package exists to refuse.
	ErrPurgedTooThin = errors.New("metalabel: purge left too few trainable folds to grade")
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
	// MinTakenDays is the floor on DISTINCT days those trades span. It is the
	// gate that actually binds: trade count is inflated by cross-sectional
	// clustering (every symbol shares the day's market move) while day count
	// is not. This platform has twice shipped a false result by counting
	// clustered rows as independent, so the day floor is enforced separately
	// and cannot be satisfied by simply trading more symbols.
	MinTakenDays = 20
	// DefaultThreshold is the meta-probability above which a candidate is taken.
	DefaultThreshold = 0.55
)

// Sample is one candidate decision from the primary model, with its realized
// outcome and the context the meta-model is allowed to judge it by.
type Sample struct {
	Ts int64
	// LabelEnd is when this candidate's outcome was REALIZED — Ts plus the
	// primary's forward horizon, in the same units as Ts. It is what makes the
	// purge possible: index order alone cannot tell walkForward that a row
	// sitting before a fold boundary carries an answer decided after it. Callers
	// that labeled with a single horizon declare it in one line with
	// WithLabelSpan; a set where no row declares it is refused (ErrNoLabelSpan)
	// rather than graded unpurged.
	LabelEnd int64
	// PrimaryProb is the primary model's P(up) for this row. Its distance from
	// 0.5 is the side and the conviction.
	PrimaryProb float64
	// Context is the meta-feature vector: regime, volatility, liquidity,
	// breadth — the circumstances of the call, NOT a restatement of the call.
	Context []float64
	// FwdReturn is the realized forward return over the primary's horizon.
	FwdReturn float64
}

// WithLabelSpan returns a COPY of samples with LabelEnd filled in as Ts+span for
// every row that has not already declared a later one. span is the primary's
// forward window in Ts units (e.g. 604800 for a 1-week horizon on unix seconds).
// This is the one-line declaration the purge needs; without it the caller gets
// ErrNoLabelSpan, by design.
//
// It mirrors gbm.WithLabelSpan deliberately: the two packages grade the same
// rows through the same learner, and a horizon declared one way here and another
// way there would make their purges — and therefore their verdicts —
// incomparable.
func WithLabelSpan(samples []Sample, span int64) []Sample {
	out := make([]Sample, len(samples))
	copy(out, samples)
	for i := range out {
		if end := out[i].Ts + span; end > out[i].LabelEnd {
			out[i].LabelEnd = end
		}
	}
	return out
}

// Grade is the honest verdict on whether meta-labeling earned its place.
type Grade struct {
	N int `json:"n"` // directional candidates graded out-of-sample

	// Primary-model baseline: what taking EVERY call would have produced.
	PrimaryPrecision  float64 `json:"primaryPrecision"`  // fraction of calls right net of cost
	PrimaryExpectancy float64 `json:"primaryExpectancy"` // mean cost-net return per decision offered

	// Filtered: what taking only the meta-model's accepted calls produced.
	TakenN int `json:"takenN"`
	// TakenDays is how many DISTINCT UTC days the accepted trades fall on, and
	// it — not TakenN — is the honest sample size. On any given day every
	// symbol shares one market move, so a thousand same-day trades are closer
	// to one observation than to a thousand. Measured live on this platform,
	// 11,811 symbol-day rows spanned just 22 distinct days across 1,050
	// symbols; a filter selecting the best few percent of those is largely
	// learning WHICH DAYS were good days, which does not generalise forward.
	TakenDays         int     `json:"takenDays"`
	TotalDays         int     `json:"totalDays"`         // distinct days among all graded candidates
	TakeRate          float64 `json:"takeRate"`          // fraction of candidates accepted
	FilteredPrecision float64 `json:"filteredPrecision"` // precision among taken
	// FilteredExpectancy is per DECISION OFFERED, not per trade taken — a
	// filter cannot inflate it by trading less.
	FilteredExpectancy float64 `json:"filteredExpectancy"`

	// ExpectancyLift is the number that decides everything. <= 0 means the
	// filter did not earn its place, however good its precision looks.
	ExpectancyLift float64 `json:"expectancyLift"`
	PrecisionLift  float64 `json:"precisionLift"`

	// The purge is reported, not assumed. LabelSpan is the primary's forward
	// horizon read off the candidates, EmbargoSpan the extra gap held in front of
	// each test block, PurgedTrainRows how many training rows the two together
	// removed across all folds, and TrainedFolds how many retrain boundaries
	// actually produced a model afterwards. PurgedTrainRows == 0 on symbol-day
	// candidates is a red flag that the caller mis-declared its horizon: with
	// ~530 rows sharing a UTC day, every boundary should straddle one.
	LabelSpan       int64 `json:"labelSpan"`
	EmbargoSpan     int64 `json:"embargoSpan"`
	PurgedTrainRows int   `json:"purgedTrainRows"`
	TrainedFolds    int   `json:"trainedFolds"`

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
// folds is the number of expanding-window retrains demanded — boundaries that
// actually FIT a model after purging, not boundaries that merely exist; cost is
// the round-trip cost as a return fraction (e.g. 0.001 = 10bps); threshold is the
// meta-probability at or above which a candidate is taken.
//
// It withholds the verdict entirely, with a stated reason, in two cases the
// purge introduced. ErrNoLabelSpan: no candidate declares when its outcome
// resolved, so the purge width is unknowable and the split cannot be certified.
// ErrPurgedTooThin: purging left fewer trainable retrains than demanded, which is
// what a candidate history shorter than a few of its own label spans produces.
// Both are refusals on purpose — a verdict on filtering the platform's own calls
// is published to readers, and a leak-fed or single-retrain verdict is worse than
// none.
func Evaluate(samples []Sample, folds int, cost, threshold float64) (Grade, error) {
	if !ascendingTs(samples) {
		return Grade{}, ErrNotAscending
	}
	cands := directional(samples)
	if len(cands) == 0 {
		return Grade{}, ErrNoDirectionalCalls
	}
	if folds < 2 || len(cands) < minTrain+minPerFold {
		return Grade{}, ErrTooFewSamples
	}

	// The purge width comes from the DATA — the widest declared horizon among the
	// candidates — never from a constant. Undeclared means unpurgeable, and an
	// unpurgeable verdict is withheld.
	span, ok := labelSpanOf(cands)
	if !ok {
		return Grade{}, ErrNoLabelSpan
	}
	embargo := embargoFor(span)

	// The meta-label: did the primary's side clear cost on this row?
	metaSamples := make([]gbm.Sample, len(cands))
	for i, c := range cands {
		metaSamples[i] = gbm.Sample{
			Ts:       c.Ts,
			LabelEnd: labelEndOf(c, span),
			Feat:     c.Context,
			Y:        metaLabel(c, cost),
		}
	}

	// Walk-forward: collect out-of-sample meta-probabilities aligned to cands.
	probs, scored, wf, err := walkForward(metaSamples, folds, span, embargo)
	if err != nil {
		return Grade{}, err
	}
	if len(scored) == 0 {
		return Grade{}, ErrTooFewSamples
	}
	// A boundary that could not be trained after purging is not a retrain. Folds
	// is the number of retrains the caller demands before a grade is considered
	// structurally sound, so counting boundaries that produced no model would be
	// widening the claim to match what survived.
	if wf.trained < folds {
		return Grade{}, fmt.Errorf("%w: %d of %d fold boundaries had a usable training set "+
			"after purging %d rows whose labels resolve inside their test block "+
			"(label horizon %ds, embargo %ds); %d retrains were demanded. The candidates span "+
			"too few distinct days for their own label horizon — grading on what survived "+
			"would present fewer retrains than were asked for as walk-forward evidence",
			ErrPurgedTooThin, wf.trained, wf.boundaries, wf.purged, span, embargo, folds)
	}

	g := Grade{
		N:               len(scored),
		LabelSpan:       span,
		EmbargoSpan:     embargo,
		PurgedTrainRows: wf.purged,
		TrainedFolds:    wf.trained,
	}

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
	takenDays := map[int64]struct{}{}
	allDays := map[int64]struct{}{}
	for _, i := range scored {
		c := cands[i]
		allDays[md.TradingDay(c.Ts)] = struct{}{}
		if probs[i] < threshold {
			continue
		}
		g.TakenN++
		takenDays[md.TradingDay(c.Ts)] = struct{}{}
		takenSum += costNetReturn(c, cost)
		if metaLabel(c, cost) == 1 {
			takenHits++
		}
	}
	g.TakenDays = len(takenDays)
	g.TotalDays = len(allDays)
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
	if g.TakenDays < MinTakenDays {
		return VerdictInsufficient, "the filter's " + itoa(g.TakenN) + " trades span only " +
			itoa(g.TakenDays) + " distinct days (of " + itoa(g.TotalDays) +
			" available) — below the " + itoa(MinTakenDays) + "-day floor. Every symbol " +
			"shares the same market move on a given day, so same-day trades are not " +
			"independent evidence; a filter measured on this few days is mostly learning " +
			"WHICH DAYS were good, which does not generalise forward"
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
// an unearned meta-model must not gate anything, and that now includes a verdict
// whose walk-forward could not be purged (ErrNoLabelSpan) or whose purge left too
// few retrains (ErrPurgedTooThin): Evaluate refuses, so Run refuses.
//
// The point model below trains on ALL candidates without a purge, and that is
// correct rather than an oversight: there is no test block here. Every candidate
// handed in has a REALIZED outcome, so none of them resolves after the moment
// this prediction is made — the purge exists to keep a fold's training labels out
// of the block it grades, and a live prediction has no such block.
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

// walkReport is what walkForward measured about its own splits, so the purge is
// published as a number rather than asserted in a comment.
type walkReport struct {
	boundaries int // geometric retrain points that fit inside the sample set
	trained    int // boundaries that still had a usable training set after purging
	purged     int // training rows dropped across all folds for straddling a boundary
}

// walkForward returns out-of-sample meta-probabilities indexed to samples, plus
// the indices actually scored. Fold k trains strictly on rows whose LABEL
// resolved before it — not merely on rows indexed before it. There is no
// unpurged mode: the zero-gap split is the defect, not an option.
//
// The fold boundaries are GEOMETRIC from a fixed origin — minTrain, 2*minTrain,
// 4*minTrain, … — and deliberately NOT derived from len(samples). Two properties
// are being bought at once, and both were learned the hard way:
//
//   - REPRODUCIBILITY. Deriving boundaries from n is the obvious implementation
//     and is subtly wrong: every new observation would re-cut the whole history,
//     so a row graded today could carry a different out-of-sample probability
//     tomorrow purely because unrelated rows arrived after it. That is not
//     lookahead — each model still trains only on its own past — but it makes
//     recorded grades irreproducible, and this platform re-grades continuously.
//     With boundaries at fixed positions a row's grade is decided once.
//   - COST. A fixed CONSTANT stride is reproducible but quadratic: it retrains
//     every stride rows on an ever-growing training set, which on this
//     platform's ~13k independent candidates meant ~430 boosted-tree fits and a
//     pass that did not finish. Geometric growth makes it O(log n) — 8 fits for
//     13k rows — while keeping every boundary independent of n.
//
// The trade is that later rows are scored by a model retrained less often. That
// is the honest direction to err: the model is always STALER than it could be,
// never fresher, so no grade is flattered by recency it would not have had live.
//
// folds is the minimum number of retrain boundaries the caller demands before a
// grade is considered structurally sound.
func walkForward(samples []gbm.Sample, folds int, span, embargo int64) (probs []float64, scored []int, rep walkReport, err error) {
	n := len(samples)
	probs = make([]float64, n)
	if n < minTrain+minPerFold {
		return nil, nil, rep, ErrTooFewSamples
	}
	bounds := foldBoundaries(n)
	if len(bounds) < folds {
		return nil, nil, rep, ErrTooFewSamples
	}
	rep.boundaries = len(bounds)
	for bi, trainEnd := range bounds {
		testEnd := n
		if bi+1 < len(bounds) {
			testEnd = bounds[bi+1]
		}
		train := purgedTrain(samples, trainEnd, samples[trainEnd].Ts, span, embargo)
		rep.purged += trainEnd - len(train)
		m, e := gbm.Train(train, gbm.Defaults())
		if e != nil {
			// Usually ErrInsufficientData because the purge emptied the training
			// set. Skipping is right — a fold with nothing legitimate to learn
			// from must not score rows — but the caller counts what survived and
			// refuses when too little did.
			continue
		}
		rep.trained++
		for i := trainEnd; i < testEnd; i++ {
			probs[i] = m.Predict(samples[i].Feat)
			scored = append(scored, i)
		}
	}
	return probs, scored, rep, nil
}

// embargoDenom sets the embargo as a fraction (1/embargoDenom) of the label span.
// de Prado's embargo is a small gap BEYOND the purge, guarding the residual
// serial correlation that survives the exact label window — context features are
// built from trailing windows, so rows just outside the purge still share most
// of their inputs with the first test rows. It matches internal/gbm's embargo
// rule on purpose: the two packages purge the same rows through the same learner,
// and two different embargoes would make their verdicts incomparable.
const embargoDenom = 10

// embargoFor returns the embargo gap for a measured label span.
func embargoFor(span int64) int64 { return span / embargoDenom }

// labelSpanOf reads the primary's forward horizon OFF THE DATA: the widest
// declared (LabelEnd - Ts) among the candidates. Widest, not median — with mixed
// horizons in one set a narrower purge would leave the long-horizon rows
// straddling the boundary, and over-purging costs training rows while
// under-purging costs the honesty of the verdict. ok=false when no row declares
// a horizon at all.
func labelSpanOf(cands []Sample) (int64, bool) {
	var span int64
	ok := false
	for _, c := range cands {
		if c.LabelEnd <= c.Ts {
			continue // undeclared (or a zero-width label, which needs no purge)
		}
		ok = true
		if d := c.LabelEnd - c.Ts; d > span {
			span = d
		}
	}
	return span, ok
}

// labelEndOf returns when a candidate's outcome was realized, defaulting an
// undeclared row to the set's widest span. A row that forgot to declare is
// treated as the WORST case, so a partially-declared set cannot smuggle unpurged
// rows through a boundary.
func labelEndOf(c Sample, span int64) int64 {
	if end := c.Ts + span; end > c.LabelEnd {
		return end
	}
	return c.LabelEnd
}

// purgedTrain returns one fold's training rows: those among samples[:trainEnd]
// whose label was fully realized at least `embargo` before the test block opens
// at testStartTs. Filtered row by row rather than truncated, so a set with mixed
// horizons is handled correctly.
//
// A label realized exactly AT testStartTs is kept: its terminal price is the test
// block's opening price, which the test rows' own features already contain —
// contemporaneous, not future.
func purgedTrain(samples []gbm.Sample, trainEnd int, testStartTs, span, embargo int64) []gbm.Sample {
	cutoff := testStartTs - embargo
	out := make([]gbm.Sample, 0, trainEnd)
	for i := 0; i < trainEnd; i++ {
		end := samples[i].LabelEnd
		if e := samples[i].Ts + span; e > end {
			end = e
		}
		if end > cutoff {
			continue // label reaches into the test block (or its embargo) — purge
		}
		out = append(out, samples[i])
	}
	return out
}

// foldBoundaries returns the geometric retrain points that fit inside n. Each
// position depends only on minTrain and the doubling schedule, never on n, so a
// row always lands in the same fold however much data arrives later.
func foldBoundaries(n int) []int {
	var out []int
	for b := minTrain; b < n; b *= 2 {
		// A boundary is only usable if at least minPerFold rows follow it.
		if n-b < minPerFold {
			break
		}
		out = append(out, b)
	}
	return out
}

const (
	// minTrain mirrors gbm's own floor: below it a boosted tree is fitting noise.
	minTrain = 60
	// minPerFold keeps each fold's out-of-sample slice large enough to score.
	minPerFold = 12
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
