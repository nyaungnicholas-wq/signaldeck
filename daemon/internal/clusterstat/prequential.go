package clusterstat

// PREQUENTIAL NULL — the constant-guess baseline a model must actually beat.
//
// # Why this exists
//
// A grade is only as honest as the baseline it is measured against, and this
// tree had TWO baselines that disagreed.
//
// The accuracy registry retired the hindsight null in favour of the walk-forward
// one (registry null_policy: "prequential-majority only: each day's constant
// guess is the majority class over days strictly before it. The hindsight null
// was retired after the dual-null transition cycle"). The LIVE leg-admission
// gate never got that message: gbm, forecast, alphax and meanrev each computed
// their own floor as max(pPos, 1-pPos) over the very window being graded, then
// published Lift = Accuracy - that.
//
// max(pPos, 1-pPos) is an ORACLE. It is the accuracy of a constant predictor
// that already knows which class ends up in the majority — a choice no forecaster
// can make in advance. Measured on live rows 2026-08-05, that floor sat at
// 0.725 for the pressure leg and 0.966 for gbm, so admission required beating a
// number no directional model can reach. The result was not a strict gate, it
// was a closed door: 0 of 43 gbm legs admitted, 0 of 7 meanrev-1w, 0 alphax
// ever — while those same legs scored a mean out-of-sample accuracy of 0.647
// and 0.604. Every feature added to the platform was benched on arrival, by
// arithmetic rather than by evidence.
//
// The floor was that high because the window it was measured over was itself
// pseudo-replicated: the runner re-scores a symbol dozens of times a day and
// every row of one (symbol, day) carries the same label, so a few days of rows
// can be 96% one class. An oracle floor over a degenerate window is unbeatable
// twice over.
//
// # What replaces it
//
// The hindsight-free version of the same idea: walk the days in order and, on
// each, guess the majority class of the days STRICTLY BEFORE it. Day one (and
// any exactly tied prior) has no majority to lean on and scores 0.5, which is
// what a forecaster without history actually earns. This is a real strategy
// someone could have run, so beating it is a real claim.
//
// It lives here, in the package every clustered statistic already routes
// through, so the registry verdict, the canary decision, the re-admission gate
// and the live leg-admission gate all replay ONE null. Two implementations of a
// null is two answers to the same question, and this file exists because the
// tree shipped for weeks with exactly that.

// DayLabel is one UTC day of a graded record: N observations of which Ups
// realized the positive class. It is deliberately NOT Day — Day counts HITS
// (how often the model was right), and a null needs LABELS (which way the day
// went). Conflating the two silently grades the null against the model's own
// correctness instead of against the market.
type DayLabel struct {
	Day int64
	N   int
	Ups int
}

// DayWins is one UTC day scored for a THREE-WAY outcome: of N observations, Pos
// were won by the always-positive constant call and Neg by the always-negative
// one. Pos+Neg may be less than N — a cost-net grader labels a move too small to
// trade as a loss for BOTH constant calls, and folding those into "whatever the
// other side won" would hand the baseline free wins it never earned.
type DayWins struct {
	Day int64
	N   int
	Pos int
	Neg int
}

// PrequentialBaseline returns the pooled accuracy of the hindsight-free
// constant guess over `days`, which MUST already be in chronological order.
//
// For each day the guess is the majority class of every day strictly before it;
// with no prior days, or an exactly tied prior, the guess scores N/2 — the
// coin-flip a forecaster with no history is entitled to and no more.
//
// Returns 0.5 for an empty record: no evidence means no baseline to beat, and
// 0.5 is the value that makes Lift = Accuracy - Baseline read as "no measured
// edge" rather than as a free pass.
func PrequentialBaseline(days []DayLabel) float64 {
	wins := make([]DayWins, len(days))
	for i, d := range days {
		wins[i] = DayWins{N: d.N, Pos: d.Ups, Neg: d.N - d.Ups}
	}
	return PrequentialBaselineWins(wins)
}

// PrequentialBaselineWins is PrequentialBaseline for three-way outcomes, and is
// the single implementation both entry points run: a binary record is just the
// case where Neg = N - Pos.
//
// The running choice is made on which constant call has won MORE so far, and a
// tie (including the empty prior) scores N/2 rather than picking a side.
func PrequentialBaselineWins(days []DayWins) float64 {
	var priorPos, priorNeg, n int
	var hits float64
	for _, d := range days {
		switch {
		case priorPos == priorNeg:
			hits += float64(d.N) / 2 // no leader to follow yet
		case priorPos > priorNeg:
			hits += float64(d.Pos) // constant positive call
		default:
			hits += float64(d.Neg) // constant negative call
		}
		n += d.N
		priorPos += d.Pos
		priorNeg += d.Neg
	}
	if n == 0 {
		return 0.5
	}
	return hits / float64(n)
}

// DayGrade is one UTC day scored for BOTH sides of a lift comparison: how many
// observations each constant call won (Pos/Neg) and how many the MODEL got right
// (Hits). It exists because a lift is only meaningful when the model and the null
// were scored on the same rows.
type DayGrade struct {
	Day  int64
	N    int
	Pos  int
	Neg  int
	Hits int
}

// PrequentialBaselineVsModel is the null accuracy to subtract from a model's
// accuracy to get an honest lift, over the SAME rows the model was scored on.
//
// It is PrequentialBaselineWins with one correction, and that correction is
// load-bearing: on a day where the null has NO side to take — the first day, or
// an exactly tied prior — the null is credited with the MODEL's own hits for
// that day, so the day contributes exactly zero to the lift.
//
// # Why, and what it prevents
//
// Paying the null 0.5 on an undefined day while the model is scored in full
// hands the model free lift it did not earn. The failure is not hypothetical: a
// pool whose feature is CONSTANT (literally no information) and whose label is
// 100% one class scores the model 1.000 and a coin-flip-on-day-one null 0.958,
// which reads as +4.2pp of edge and admits a leg that knows nothing. That is the
// mirror image of the hindsight-oracle bug this file was written to remove, and
// swapping one for the other would not be a fix.
//
// Crediting the model's hits makes an undefined day neutral instead of
// generous. It can only ever REDUCE a lift toward zero, never inflate one, so it
// cannot manufacture an edge. Days where the null IS defined are untouched and
// still carry the whole comparison.
//
// # NO PRIOR is not the same as a TIED prior, and conflating them benches a
// whole model class
//
// The neutralization applies ONLY to a day with NO PRIOR AT ALL — the first day,
// where the null has no history to read. A day whose prior EXISTS but is exactly
// balanced is different: the honest constant guess there is a coin flip, and it
// scores N/2.
//
// The distinction is load-bearing because one real label is balanced BY
// CONSTRUCTION. The cross-sectional alphax leg labels a row 1 when it beats the
// same-day universe MEDIAN, so every day is ~50/50 and the running prior is
// permanently tied. Neutralizing on every tie would credit the null with the
// model's own hits forever, pin lift at exactly 0.000 no matter how good the
// model was, and gate the leg permanently — reproducing the closed door this
// file exists to remove, from the opposite direction. Scoring a tied prior at
// 0.5 is both correct and lets a genuinely skilful ranker earn its lift.
//
// The returned value stays a real accuracy over all N rows, so the invariant
// Lift = Accuracy - BaseRate holds exactly and callers need no special cases.
func PrequentialBaselineVsModel(days []DayGrade) float64 {
	var priorPos, priorNeg, n int
	var hits float64
	for _, d := range days {
		switch {
		case priorPos == 0 && priorNeg == 0:
			// NO history yet: the null cannot call this day at all, so make it
			// contribute exactly zero lift instead of paying it a free 0.5.
			hits += float64(d.Hits)
		case priorPos == priorNeg:
			// History exists but is exactly balanced — an honest coin flip.
			hits += float64(d.N) / 2
		case priorPos > priorNeg:
			hits += float64(d.Pos)
		default:
			hits += float64(d.Neg)
		}
		n += d.N
		priorPos += d.Pos
		priorNeg += d.Neg
	}
	if n == 0 {
		return 0.5
	}
	return hits / float64(n)
}

// DayGradesFrom folds timestamps, model predictions and binary labels into
// chronological per-UTC-day clusters carrying both the constant-call wins and
// the model's hits. Predictions and labels are read at the >= 0.5 threshold
// every grader in this tree calls a positive at.
func DayGradesFrom(ts []int64, preds, actuals []float64) []DayGrade {
	if len(ts) != len(actuals) || len(ts) != len(preds) {
		return nil
	}
	idx := map[int64]int{}
	out := make([]DayGrade, 0, 8)
	for i, t := range ts {
		day := t / 86400
		j, seen := idx[day]
		if !seen {
			j = len(out)
			idx[day] = j
			out = append(out, DayGrade{Day: day})
		}
		out[j].N++
		up := actuals[i] >= 0.5
		if up {
			out[j].Pos++
		} else {
			out[j].Neg++
		}
		if (preds[i] >= 0.5) == up {
			out[j].Hits++
		}
	}
	sortDayGrades(out)
	return out
}

// DayGradesFromWins is DayGradesFrom for THREE-WAY outcomes: labels are +1 when
// the always-positive constant call wins, -1 when the always-negative one does,
// and 0 when NEITHER does (a cost-net move too small to trade is a loss for both
// constant calls and for the model). `correct` carries the model's own per-row
// verdict, index-aligned, because a three-way grader decides "correct" by its
// own costed rule rather than by thresholding a probability.
func DayGradesFromWins(ts []int64, labels []float64, correct []bool) []DayGrade {
	if len(ts) != len(labels) || len(ts) != len(correct) {
		return nil
	}
	idx := map[int64]int{}
	out := make([]DayGrade, 0, 8)
	for i, t := range ts {
		day := t / 86400
		j, seen := idx[day]
		if !seen {
			j = len(out)
			idx[day] = j
			out = append(out, DayGrade{Day: day})
		}
		out[j].N++
		switch {
		case labels[i] > 0:
			out[j].Pos++
		case labels[i] < 0:
			out[j].Neg++
		}
		if correct[i] {
			out[j].Hits++
		}
	}
	sortDayGrades(out)
	return out
}

// sortDayGrades orders clusters ascending by day.
func sortDayGrades(d []DayGrade) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j].Day < d[j-1].Day; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}

// DayWinsFrom folds paired timestamps and three-way outcome labels (+1 positive,
// -1 negative, 0 neither) into chronological per-UTC-day clusters.
func DayWinsFrom(ts []int64, labels []float64) []DayWins {
	if len(ts) != len(labels) {
		return nil
	}
	idx := map[int64]int{}
	out := make([]DayWins, 0, 8)
	order := make([]int64, 0, 8)
	for i, t := range ts {
		day := t / 86400
		j, seen := idx[day]
		if !seen {
			j = len(out)
			idx[day] = j
			out = append(out, DayWins{})
			order = append(order, day)
		}
		out[j].N++
		switch {
		case labels[i] > 0:
			out[j].Pos++
		case labels[i] < 0:
			out[j].Neg++
		}
	}
	for i := range out {
		out[i].Day = order[i]
	}
	sortDayWins(out)
	return out
}

// sortDayWins orders clusters ascending by day, matching sortDayLabels.
func sortDayWins(d []DayWins) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j].Day < d[j-1].Day; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}

// DayLabelsFrom folds paired timestamps and labels into chronological per-UTC-day
// clusters — the shape PrequentialBaseline consumes.
//
// ts and actuals must be the same length and index-aligned; actuals are read at
// the same >= 0.5 threshold every grader in this tree counts a positive at.
// Input order does not matter, the output is always ascending by day.
func DayLabelsFrom(ts []int64, actuals []float64) []DayLabel {
	if len(ts) != len(actuals) {
		return nil
	}
	idx := map[int64]int{}
	out := make([]DayLabel, 0, 8)
	for i, t := range ts {
		day := t / 86400
		j, seen := idx[day]
		if !seen {
			j = len(out)
			idx[day] = j
			out = append(out, DayLabel{Day: day})
		}
		out[j].N++
		if actuals[i] >= 0.5 {
			out[j].Ups++
		}
	}
	sortDayLabels(out)
	return out
}

// sortDayLabels orders clusters ascending by day. Insertion sort: a graded
// window holds tens of days, not thousands, and this keeps the package free of
// a sort import for one call.
func sortDayLabels(d []DayLabel) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j].Day < d[j-1].Day; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}

// DistinctDays reports how many UTC days a graded record spans — the count that
// says how much INDEPENDENT evidence is behind a lift, since rows inside one
// day share one market move.
func DistinctDays(days []DayLabel) int { return len(days) }
