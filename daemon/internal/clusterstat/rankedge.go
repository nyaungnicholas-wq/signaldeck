package clusterstat

import (
	"math"
	"sort"
)

// RankEdgeZ is the one-sided normal quantile used by RankEdge: 1.2816 = 90%.
// A leg must clear its own sampling error at this confidence before it is
// allowed to move a published probability.
const RankEdgeZ = 1.2816

// RankEdgeMinEval is the fewest out-of-sample evaluations a grade must carry
// before the ranking gate is allowed to judge it at all.
//
// DERIVED, not chosen. At effective N = n/2 the bound clears 0.5 only when
// AUC - RankEdgeZ*sqrt(A(1-A)/(n/2)) > 0.5. For a genuinely strong leg at
// AUC 0.60 that needs n > 79. Below this many evaluations the gate CANNOT admit
// a leg however well it ranks, so applying it would bench on arithmetic rather
// than on evidence — the exact defect that once benched every model leg here
// behind a hindsight-oracle floor (0 of 43 gbm legs admitted at mean OOS
// accuracy 0.647). Under the floor RankEdge reports ok=false and the leg keeps
// its historical gate.
const RankEdgeMinEval = 80

// RankEdge converts a leg's out-of-sample AUC into the quantity a probability
// BLEND actually needs — demonstrated RANKING skill — with the sampling error
// of the estimate charged against it. Positive means "this leg is measured to
// order symbols better than chance"; <= 0 means it is not, and it should not
// reach the blend.
//
// WHY NOT ACCURACY LIFT. The historical admission gate was
// Lift = Accuracy - BaseRate, which is THRESHOLD-dependent: it asks how often
// p > 0.5 landed on the right side, so it moves with the base rate of the
// grading window rather than with the information in the score. In a strong
// up-tape a leg that orders symbols correctly but sits below 0.5 scores a
// NEGATIVE lift and is benched, while a leg with no ordering information at all
// can score a positive one and be admitted. Measured on this repo's own live
// record (2026-07-03..08-04, 16,323 independent symbol-days), the forecast leg
// was benched on 80% of rows and the benched rows were the ones that ranked
// (AUC 0.5341, day-clustered CI [0.5073, 0.5612]) while the admitted rows did
// not (0.5015, [0.4636, 0.5380]). A blend consumes ORDER, so admission has to
// be measured on order.
//
// THE INTERVAL. AUC is a probability — P(a random positive outranks a random
// negative) — so the lower bound comes from the tree's ONE Wilson
// implementation (WilsonEffAt), not a hand-rolled normal interval. The
// effective sample size is nEval/2, the balanced-class reading of min(nPos,
// nNeg): a rank statistic is limited by the SCARCER class, and nEval alone
// cannot say which it is. Assuming balance is the widest honest choice
// available from a single stored count, so the bound errs toward benching a leg
// rather than admitting one — the direction this codebase prefers when evidence
// is thin.
//
// ok=false means there is NO usable ranking grade here — an unscored row, a
// degenerate AUC, or too few evaluations to bound anything. That is different
// from a measured "does not rank", and callers must treat it as such: leave the
// leg on its historical lift gate rather than benching it on a grade that was
// never taken. Absence of evidence is not evidence of no edge.
func RankEdge(auc float64, nEval int) (float64, bool) {
	// EXACTLY 0 or 1 is refused as degenerate, and that is a deliberate,
	// measured choice rather than sloppy range-checking. A real AUC of 1.0 over
	// this many evaluations does not happen on market data; an artifact does —
	// the live model_forecasts table carries 13 rows at auc>=1 and 88 at auc==0,
	// 20 of them above this evidence floor. Admitting the 1.0 rows would hand a
	// large positive edge to whatever produced them, which is precisely the fake
	// signal this gate exists to keep out. Refusing costs only the unobservable
	// case of a leg that ranks perfectly.
	if nEval < RankEdgeMinEval || math.IsNaN(auc) || auc <= 0 || auc >= 1 {
		return 0, false
	}
	return WilsonEffAt(auc, float64(nEval)/2, RankEdgeZ).Lo - 0.5, true
}

// RankAUC is the ROC AUC via the Mann-Whitney rank statistic, with tied scores
// taking average ranks. 0.5 means the scores do not rank positives above
// negatives at all; below 0.5 means they rank them BACKWARDS.
//
// The engine packages (forecast, gbm, meanrev, alphax, pressure) each keep a
// private copy of this so they stay dependency-free, and those copies are
// deliberate. This is the shared one for callers that already depend on
// clusterstat and are grading a leg rather than fitting a model — adding a
// seventh private copy in the pipeline package would be the drift the Wilson
// invariant exists to prevent.
//
// Returns 0.5 when either class is empty: no ranking is observable, which is
// the honest "no evidence" reading and, through RankEdge, a bench.
func RankAUC(preds, actuals []float64) float64 {
	type pa struct{ p, y float64 }
	rows := make([]pa, len(preds))
	for i := range preds {
		rows[i] = pa{preds[i], actuals[i]}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].p < rows[b].p })
	ranks := make([]float64, len(rows))
	i := 0
	for i < len(rows) {
		j := i
		for j+1 < len(rows) && rows[j+1].p == rows[i].p {
			j++
		}
		avg := float64((i+1)+(j+1)) / 2.0
		for k := i; k <= j; k++ {
			ranks[k] = avg
		}
		i = j + 1
	}
	sumPos, nPos, nNeg := 0.0, 0.0, 0.0
	for k, r := range rows {
		if r.y >= 0.5 {
			sumPos += ranks[k]
			nPos++
		} else {
			nNeg++
		}
	}
	if nPos == 0 || nNeg == 0 {
		return 0.5
	}
	return (sumPos - nPos*(nPos+1)/2.0) / (nPos * nNeg)
}
