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

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
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
	// ReadmitMinDistinctDays is the distinct-UTC-day floor of the RE-ADMISSION
	// threshold — the one coded door back to emitting for a retired model, and
	// identically the day floor a successor must clear to be promoted, so the
	// canary gate and the re-admission gate can never disagree about the same
	// record. It is 2x clusterstat.MinDistinctDays deliberately: ten days is
	// the floor below which no interval exists at all, and a model that has
	// already been retired on live evidence earns its way back at double that,
	// not at the bare minimum where the interval first becomes computable.
	ReadmitMinDistinctDays = 2 * clusterstat.MinDistinctDays
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
	// DayTallies is the record broken out per UTC day: the unit this platform
	// resamples on. It is REQUIRED for a promotion, because the interval that
	// decides a promotion has to be a day-count interval and there is no way to
	// recover the between-day variance from N and Correct alone. Fill it with
	// TallyDays. An arm that omits it can be rejected and can be held, but can
	// never be promoted — see Evaluate.
	DayTallies []DayTally `json:"dayTallies,omitempty"`
	// BaselineAccuracy is the majority-class accuracy over the SAME
	// observations — the null any model must beat to be worth serving.
	// Callers must fill it PREQUENTIALLY (each day's constant guess is the
	// majority class over days strictly before it, as PrequentialBaseline
	// does), never as max(base, 1-base) over the finished window — that hands
	// the null hindsight the models never had.
	BaselineAccuracy float64 `json:"baselineAccuracy"`
}

// DayTally is one UTC day's graded observations. It mirrors clusterstat.Day so
// a Record can be handed to the platform's cluster-robust estimator without the
// caller reshaping it.
type DayTally struct {
	Day  int64 `json:"day"`
	N    int   `json:"n"`
	Hits int   `json:"hits"`
}

// TallyDays folds parallel timestamp/correctness slices into per-day tallies —
// the only correct way to fill Record.DayTallies, and the companion to
// DistinctDays.
func TallyDays(ts []int64, correct []bool) []DayTally {
	if len(ts) != len(correct) {
		return nil
	}
	idx := map[int64]int{}
	var out []DayTally
	for i, t := range ts {
		d := t / 86400
		j, ok := idx[d]
		if !ok {
			idx[d] = len(out)
			out = append(out, DayTally{Day: d})
			j = len(out) - 1
		}
		out[j].N++
		if correct[i] {
			out[j].Hits++
		}
	}
	return out
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

// Interval returns the challenger interval this gate decides on, with the
// method that produced it.
//
// # Why this is not a Wilson interval over N
//
// Until 2026-07-26 it was, and that was the defect. Every observation on this
// platform is one symbol on one day, and on any given day ~1,000 symbols share
// ONE market move. A Wilson interval over 10,000 such rows asserts 10,000
// independent trials in a sample that holds roughly twenty. Measured on the
// live 1d record the design effect is ~14x, so the shipped interval was ~3.8x
// too narrow — and this gate promotes when the LOWER BOUND clears the incumbent
// by half a point. A bound that is four times too tight turns twenty days of
// noise into a promotion.
//
// The display surfaces were corrected for this when clusterstat was written;
// this gate was not, so the platform's most consequential decision was the last
// one still reading a row-count interval. It now resamples days, like
// everything else.
//
// method is one of:
//   - "day-clustered-wilson": corrected, promotable.
//   - "withheld": the arm did not supply per-day tallies, so no honest interval
//     exists. Reported as [0,1] — maximal uncertainty, which cannot promote and
//     cannot reject. Falling back to the pooled interval here would reintroduce
//     the defect through the back door.
func (r Record) Interval() (lo, hi float64, method string, deff, effN float64) {
	days := r.clusterDays()
	if days == nil {
		return 0, 1, "withheld", 0, 0
	}
	// The same refusal floor clusterstat applies everywhere else. A between-day
	// variance estimated from two or three days is not a correction — it is a
	// second way to be overconfident, and it is worse than none because the
	// result carries a label saying it was corrected. Measured live on
	// 2026-07-26 the v8 incumbent held 1,046 rows on 2 days: the estimator
	// floors that to deff 1.0 and would have published a 6.0pp interval as
	// "day-clustered". This gate already demands 14 distinct days to be
	// gradable at all, so the floor can never loosen a check that existed.
	if len(days) < clusterstat.MinDistinctDays {
		return 0, 1, "withheld", 0, 0
	}
	d, ok := clusterstat.DesignEffect(days)
	if !ok || d <= 0 {
		return 0, 1, "withheld", 0, 0
	}
	eff := float64(r.N) / d
	iv := clusterstat.WilsonEff(r.Accuracy(), eff)
	return iv.Lo, iv.Hi, "day-clustered-wilson", d, eff
}

// clusterDays converts the arm's tallies to clusterstat's unit, returning nil
// when they are absent or do not reconcile with the headline counts. A tally
// set that disagrees with N/Correct is a bug in the caller, and silently
// preferring one over the other would hide it.
func (r Record) clusterDays() []clusterstat.Day {
	if len(r.DayTallies) < 2 {
		return nil
	}
	out := make([]clusterstat.Day, 0, len(r.DayTallies))
	var n, hits int
	for _, t := range r.DayTallies {
		if t.N <= 0 || t.Hits < 0 || t.Hits > t.N {
			return nil
		}
		n += t.N
		hits += t.Hits
		out = append(out, clusterstat.Day{Day: t.Day, N: t.N, Hits: t.Hits})
	}
	if n != r.N || hits != r.Correct {
		return nil
	}
	return out
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
	// IntervalMethod names the estimator behind ChallengerLower/Upper, so a
	// reader can tell a day-clustered bound from a withheld one.
	IntervalMethod string `json:"intervalMethod"`
	// DesignEffect is the measured clustering penalty, and EffectiveN is
	// N/DesignEffect — the sample size the interval was actually computed at.
	// Both are 0 when the interval was withheld. Publishing them is the point:
	// "6,957 observations, effective 480" is the honest description.
	DesignEffect float64 `json:"designEffect"`
	EffectiveN   float64 `json:"effectiveN"`
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
	lo, hi, method, deff, effN := challenger.Interval()
	v.ChallengerLower, v.ChallengerUpper = lo, hi
	v.IntervalMethod, v.DesignEffect, v.EffectiveN = method, deff, effN

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

	// A withheld interval is not a wide interval to reason about — it is the
	// absence of one, and it stops the comparison here rather than flowing into
	// the switch below where it would be reported as "does not clear the naive
	// baseline". That reason would be a statement about the challenger; the
	// true statement is about the platform's own bookkeeping.
	if v.IntervalMethod == "withheld" {
		v.Reason = "holding: the challenger does not report its observations per UTC day, " +
			"so no day-resampled interval can be computed — and a row-count interval over " +
			"one market move per day is the overstatement this gate exists to avoid"
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
		// The promotion bar IS the re-admission threshold. A successor that
		// clears the incumbent but not Readmit is held, not promoted: were
		// promotion allowed at fewer days than re-admission, a retired model
		// could reach production faster by re-badging itself as a successor
		// than through the door built for it, and the two gates would disagree
		// about the same record.
		if ra := Readmit(challenger); !ra.Eligible {
			v.Reason = "holding: the challenger clears the incumbent but not the re-admission threshold — " + ra.Reason
			return v
		}
		v.Decision = DecisionPromote
		v.Serving, v.Shadow = challenger.Version, ""
		v.Reason = "promoted: the challenger's confidence interval clears the incumbent's live accuracy, the naive baseline, and the re-admission threshold"
	default:
		v.Reason = "holding: the challenger leads on the point estimate but not by more than its own confidence interval"
	}
	return v
}

// Readmission is the coded verdict on whether a retired model's shadow record
// has earned emission back — or, identically, whether a successor's record
// clears the promotion bar. Every number the decision turned on is in the
// struct, designEffect and effectiveN included, because a threshold whose
// inputs are not published is a judgment call with a constant in it.
type Readmission struct {
	Eligible bool `json:"eligible"`
	// Lower/Upper are the day-clustered Wilson bounds of the shadow record —
	// clusterstat.DesignEffect + WilsonEff, the same machinery as the canary
	// interval, so the two gates read one estimator.
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
	// Null is the prequential baseline the LOWER bound must clear.
	Null float64 `json:"null"`
	// DistinctDays is the shadow record's distinct-UTC-day count, judged
	// against MinDistinctDays (= ReadmitMinDistinctDays).
	DistinctDays    int    `json:"distinctDays"`
	MinDistinctDays int    `json:"minDistinctDays"`
	IntervalMethod  string `json:"intervalMethod"`
	// DesignEffect and EffectiveN describe the sample the bounds were actually
	// computed at: N/DesignEffect, never raw rows. Both 0 when withheld.
	DesignEffect float64 `json:"designEffect"`
	EffectiveN   float64 `json:"effectiveN"`
	// Reason states the shortfall (or the clearance) in one sentence.
	Reason string `json:"reason"`
}

// Readmit applies the re-admission threshold to a retired model's live shadow
// record. Shadow rows keep accruing in prediction_outcomes after retirement
// precisely so this can be a measurement instead of a judgment call: emitting
// may flip back to true ONLY when the record's day-clustered CI lower bound
// clears the prequential null on at least ReadmitMinDistinctDays distinct UTC
// days. There is no discretionary path around any leg of that conjunction.
func Readmit(shadow Record) Readmission {
	lo, hi, method, deff, effN := shadow.Interval()
	days := shadow.Days
	if len(shadow.DayTallies) > 0 && len(shadow.DayTallies) < days {
		// The tallies are the interval's actual input; a headline day count
		// they cannot back does not get to satisfy the floor.
		days = len(shadow.DayTallies)
	}
	r := Readmission{
		Lower: lo, Upper: hi, Null: shadow.BaselineAccuracy,
		DistinctDays: days, MinDistinctDays: ReadmitMinDistinctDays,
		IntervalMethod: method, DesignEffect: deff, EffectiveN: effN,
	}
	switch {
	case days < ReadmitMinDistinctDays:
		r.Reason = fmt.Sprintf("not re-admitted: the shadow record covers %d distinct days, short of the %d the threshold requires — a streak below the day floor is not evidence however good it looks", days, ReadmitMinDistinctDays)
	case method != "day-clustered-wilson":
		r.Reason = "not re-admitted: the shadow record does not yield a day-clustered interval (per-day tallies missing or irreconcilable), and a row-count interval cannot re-admit what a day-clustered one retired"
	case lo <= shadow.BaselineAccuracy:
		r.Reason = fmt.Sprintf("not re-admitted: the day-clustered lower bound %.1f%% does not clear the prequential null %.1f%% (design effect %.1fx, effective n %.0f of %d rows)", lo*100, shadow.BaselineAccuracy*100, deff, effN, shadow.N)
	default:
		r.Eligible = true
		r.Reason = fmt.Sprintf("re-admitted: the day-clustered lower bound %.1f%% clears the prequential null %.1f%% on %d distinct days (threshold %d; design effect %.1fx, effective n %.0f of %d rows)", lo*100, shadow.BaselineAccuracy*100, days, ReadmitMinDistinctDays, deff, effN, shadow.N)
	}
	return r
}

// PrequentialBaseline grades the hindsight-free constant guess over an arm's
// day sequence (chronological): for each UTC day the guess is the majority
// class over the days strictly before it — expected accuracy 0.5 on day one or
// on a tied prior — and the return is that guess-sequence's pooled accuracy.
// It mirrors prequential_null in tools/accuracy_registry.py deliberately, and
// it lives HERE so the canary runner and the re-admission gate replay one
// null: a registry verdict, a canary decision and a re-admission must never
// disagree about the same numbers.
func PrequentialBaseline(days []DayTally, dayUps map[int64]int) float64 {
	var priorN, priorUps, n int
	var hits float64
	for _, d := range days {
		ups := dayUps[d.Day]
		switch {
		case priorN == 0 || priorUps*2 == priorN:
			hits += float64(d.N) / 2 // no majority to lean on yet — a coin flip
		case priorUps*2 > priorN:
			hits += float64(ups) // constant "up" guess
		default:
			hits += float64(d.N - ups) // constant "down" guess
		}
		n += d.N
		priorN += d.N
		priorUps += ups
	}
	if n == 0 {
		return 0.5
	}
	return hits / float64(n)
}

// WilsonInterval returns the 95% Wilson score interval for k successes in n
// trials — the same estimator the accuracy registry uses, so a canary decision
// and a registry verdict can never disagree about the same numbers.
//
// It delegates to clusterstat.WilsonEffAt with effN = n: the RAW-count reading
// is deliberate here (registry parity is its whole purpose, and nothing on a
// decision path calls it — gates read Record.Interval, which is day-clustered),
// but the arithmetic lives in clusterstat so the tree holds ONE Wilson
// implementation, an invariant clusterstat's gates_test enforces.
func WilsonInterval(k, n int) (lo, hi float64) {
	if n <= 0 {
		return 0, 1
	}
	iv := clusterstat.WilsonEffAt(float64(k)/float64(n), float64(n), 1.959963984540054)
	return iv.Lo, iv.Hi
}
