// Package gbm is SignalDeck's from-scratch, dependency-free gradient-boosted
// decision-tree classifier — the non-linear sibling of internal/forecast's
// logistic regression. It exists to give the honest directional signal its best
// shot at capturing NON-linear structure the linear logit cannot, while obeying
// the same three commitments that make everything in this project honest:
//
//   - NO LOOKAHEAD. The engine is fed labeled Samples the CALLER assembled from
//     the feature store, where each Sample's feature vector was computable at its
//     bar and its label is the later-realized direction. Walk-forward Evaluate
//     only ever trains on samples that resolved strictly BEFORE the block it
//     scores (temporal, expanding-window folds over time-ordered samples), so a
//     future row can never train a past prediction — enforced by construction.
//
//   - NEVER A PREDICTION WITHOUT ITS GRADE. Predictive quality is measured by
//     the SAME out-of-sample report card forecast.Evaluate produces (accuracy,
//     Brier, AUC, base rate, and the headline Lift = accuracy - base rate). On a
//     pure-noise target the GBM correctly reports ~zero lift.
//
//   - GATED BY MEASURED EDGE. The caller ships a GBM forecast into the ensemble
//     ONLY when its walk-forward Lift > 0 — the identical honesty gate the logit
//     forecast already passes through. An un-graded or edgeless GBM is dropped,
//     not down-weighted.
//
// # Model
//
// Binary classification via gradient boosting on the log-loss (logistic)
// objective. The ensemble starts from the log-odds of the base rate and adds
// depth-limited regression trees, each fit to the negative gradient (residual =
// label - p) of the current predictions, shrunk by a learning rate. This is the
// standard Friedman GBM specialized to one output. Trees are CART-style, split
// on best variance reduction of the residual, depth-limited (a depth-1 tree is a
// stump), with a minimum-samples-per-leaf guard. Deterministic: no RNG, fixed
// iteration/So identical input always yields an identical model and grade.
//
// # Costs / assumptions (stated because honesty is the brand)
//
//   - Labels are raw close-to-close direction with NO transaction costs. A
//     P(up) > 0.5 is a directional lean, not a tradeable edge; round-trip costs
//     must be subtracted downstream before any P&L claim (the ensemble + paper
//     book already do this for the fused probability).
//   - Overlapping forward windows make consecutive samples autocorrelated, so
//     effective N < len(samples); reported confidence is mildly optimistic (the
//     point estimate is not).
//   - Trees can overfit; the depth limit, min-leaf guard, learning-rate shrink,
//     and (above all) the strictly out-of-sample walk-forward grade are the
//     defenses. If there is no non-linear structure, the grade honestly lands
//     near the base rate and the caller drops the model.
package gbm

import (
	"errors"
	"math"
	"sort"
)

// Errors returned by this package.
var (
	// ErrInsufficientData is returned when there are too few labeled samples to
	// fit or grade a model responsibly.
	ErrInsufficientData = errors.New("gbm: insufficient labeled data")
	// ErrBadParams is returned for invalid arguments (e.g. folds < 2, empty
	// feature vectors, mismatched dimensions).
	ErrBadParams = errors.New("gbm: invalid parameters")
)

// Params are the GBM hyperparameters. Defaults() gives the fixed, reproducible
// set the pipeline uses; a grade is only comparable across time when the params
// that produced it are stable, so callers should treat these as constants.
type Params struct {
	NEstimators  int     // number of boosting rounds (trees)
	MaxDepth     int     // per-tree depth limit (1 = stumps)
	LearningRate float64 // shrinkage applied to each tree's contribution
	MinLeaf      int     // minimum samples required to form a leaf
	L2           float64 // ridge shrink on leaf values (guards tiny leaves)
}

// Defaults returns the standard hyperparameters. Small and shallow on purpose:
// the training sets here (feature-store rows) are modest, so a large deep
// ensemble would memorize noise. These are fixed so grades stay comparable.
func Defaults() Params {
	return Params{
		NEstimators:  60,
		MaxDepth:     3,
		LearningRate: 0.1,
		MinLeaf:      8,
		L2:           1.0,
	}
}

// Sample is one labeled training/eval row. Ts orders samples in time so
// walk-forward folds never let a later sample train an earlier prediction.
// Feat is the raw feature vector (any fixed dimension, shared across samples);
// Y is the binary label (1 for an up move, 0 otherwise).
type Sample struct {
	Ts   int64
	Feat []float64
	Y    float64
}

// Grade is the out-of-sample report card — identical in shape and meaning to
// forecast.Grade so the two models are directly comparable and the same UI /
// honesty gate applies. Lift = Accuracy - BaseRate is the headline number:
// <= 0 means the model did no better than always predicting the majority class.
type Grade struct {
	N          int     `json:"n"`          // out-of-sample predictions scored
	Accuracy   float64 `json:"accuracy"`   // fraction correct at a 0.5 threshold
	BrierScore float64 `json:"brierScore"` // mean (p - y)^2 (lower better)
	AUC        float64 `json:"auc"`        // ROC AUC via rank statistic (0.5 = no skill)
	BaseRate   float64 `json:"baseRate"`   // majority-class accuracy floor
	Lift       float64 `json:"lift"`       // Accuracy - BaseRate (<=0 => no edge)
}

// treeNode is one node of a CART regression tree. A leaf carries Value; an
// internal node splits on Feature at Threshold (go Left when feat <= threshold).
type treeNode struct {
	Leaf      bool
	Value     float64
	Feature   int
	Threshold float64
	Left      *treeNode
	Right     *treeNode
}

// predict walks the tree for one feature vector and returns the leaf value.
func (n *treeNode) predict(x []float64) float64 {
	for !n.Leaf {
		if x[n.Feature] <= n.Threshold {
			n = n.Left
		} else {
			n = n.Right
		}
	}
	return n.Value
}

// Model is a fitted GBM: an initial log-odds bias plus a sequence of shrunk
// regression trees. P(up) = sigmoid(bias + lr * sum(tree(x))).
type Model struct {
	Bias  float64     // initial score = logit(base rate)
	Trees []*treeNode // boosting rounds, applied additively
	LR    float64     // learning rate baked in at fit time
	NFeat int         // feature dimension the model expects
}

// Predict returns P(up) for a single feature vector. A vector of the wrong
// dimension returns 0.5 (a neutral, information-free prior) rather than panicking
// — defensive against a caller feeding a stale-version vector.
func (m *Model) Predict(x []float64) float64 {
	if len(x) != m.NFeat {
		return 0.5
	}
	score := m.Bias
	for _, t := range m.Trees {
		score += m.LR * t.predict(x)
	}
	return sigmoid(score)
}

// Train fits a GBM on labeled samples. It errors with ErrBadParams for empty /
// ragged feature vectors and ErrInsufficientData when there are too few rows to
// fit responsibly (fewer than minTrainSamples). Deterministic given identical
// input + params.
func Train(samples []Sample, p Params) (*Model, error) {
	if len(samples) < minTrainSamples {
		return nil, ErrInsufficientData
	}
	nfeat, err := checkDims(samples)
	if err != nil {
		return nil, err
	}
	if p.NEstimators < 1 || p.MaxDepth < 1 || p.LearningRate <= 0 || p.MinLeaf < 1 {
		return nil, ErrBadParams
	}

	n := len(samples)
	y := make([]float64, n)
	for i := range samples {
		y[i] = samples[i].Y
	}

	// Initial prediction = log-odds of the base rate (the optimal constant for
	// log-loss). Guarded away from 0/1 so the logit is finite.
	base := mean(y)
	base = clampEps(base)
	bias := math.Log(base / (1 - base))

	// scores holds the current additive log-odds per sample.
	scores := make([]float64, n)
	for i := range scores {
		scores[i] = bias
	}

	m := &Model{Bias: bias, LR: p.LearningRate, NFeat: nfeat}
	for round := 0; round < p.NEstimators; round++ {
		// Residuals = negative gradient of log-loss = y - sigmoid(score).
		resid := make([]float64, n)
		for i := range resid {
			resid[i] = y[i] - sigmoid(scores[i])
		}
		tree := buildTree(samples, resid, 0, p)
		m.Trees = append(m.Trees, tree)
		// Update running scores with this shrunk tree.
		for i := range samples {
			scores[i] += p.LearningRate * tree.predict(samples[i].Feat)
		}
	}
	return m, nil
}

// buildTree grows one CART regression tree fitting `target` (the residual)
// depth-first, respecting depth and min-leaf limits. It returns a leaf when it
// cannot (or should not) split further.
func buildTree(samples []Sample, target []float64, depth int, p Params) *treeNode {
	// Leaf value = shrunken mean residual (ridge toward 0 by L2). Using the raw
	// residual mean keeps this a plain gradient-descent step in function space.
	leafVal := func(idx []int) float64 {
		if len(idx) == 0 {
			return 0
		}
		var sum float64
		for _, i := range idx {
			sum += target[i]
		}
		return sum / (float64(len(idx)) + p.L2)
	}

	all := make([]int, len(samples))
	for i := range all {
		all[i] = i
	}
	var grow func(idx []int, depth int) *treeNode
	grow = func(idx []int, depth int) *treeNode {
		// Stop: depth limit reached or too few to split into two valid leaves.
		if depth >= p.MaxDepth || len(idx) < 2*p.MinLeaf {
			return &treeNode{Leaf: true, Value: leafVal(idx)}
		}
		feat, thr, ok := bestSplit(samples, target, idx, p.MinLeaf)
		if !ok {
			return &treeNode{Leaf: true, Value: leafVal(idx)}
		}
		var left, right []int
		for _, i := range idx {
			if samples[i].Feat[feat] <= thr {
				left = append(left, i)
			} else {
				right = append(right, i)
			}
		}
		// Defensive: a degenerate split (everything on one side) becomes a leaf.
		if len(left) < p.MinLeaf || len(right) < p.MinLeaf {
			return &treeNode{Leaf: true, Value: leafVal(idx)}
		}
		return &treeNode{
			Feature:   feat,
			Threshold: thr,
			Left:      grow(left, depth+1),
			Right:     grow(right, depth+1),
		}
	}
	return grow(all, depth)
}

// bestSplit finds the (feature, threshold) that maximizes reduction in squared
// error of `target` over the given sample indices, honoring minLeaf on both
// sides. Thresholds are midpoints between consecutive distinct sorted values of
// a feature. ok=false when no valid split improves on the parent (constant
// features, or every split violates minLeaf).
func bestSplit(samples []Sample, target []float64, idx []int, minLeaf int) (feature int, threshold float64, ok bool) {
	nfeat := len(samples[idx[0]].Feat)

	// Parent SSE baseline (we maximize the reduction = parentSSE - childSSE).
	parentSSE := sse(target, idx)
	bestGain := 0.0

	for f := 0; f < nfeat; f++ {
		// Sort indices by this feature ascending (copy so we don't disturb idx).
		order := make([]int, len(idx))
		copy(order, idx)
		sort.Slice(order, func(a, b int) bool {
			return samples[order[a]].Feat[f] < samples[order[b]].Feat[f]
		})

		// Prefix sums of target let us evaluate every threshold in O(n).
		n := len(order)
		var totalSum, totalSq float64
		for _, i := range order {
			totalSum += target[i]
			totalSq += target[i] * target[i]
		}
		var leftSum, leftSq float64
		var leftCount int
		for k := 0; k < n-1; k++ {
			i := order[k]
			leftSum += target[i]
			leftSq += target[i] * target[i]
			leftCount++
			// Only split BETWEEN distinct feature values (no split inside a tie).
			vk := samples[order[k]].Feat[f]
			vNext := samples[order[k+1]].Feat[f]
			if vk == vNext {
				continue
			}
			rightCount := n - leftCount
			if leftCount < minLeaf || rightCount < minLeaf {
				continue
			}
			rightSum := totalSum - leftSum
			rightSq := totalSq - leftSq
			leftSSE := leftSq - leftSum*leftSum/float64(leftCount)
			rightSSE := rightSq - rightSum*rightSum/float64(rightCount)
			gain := parentSSE - (leftSSE + rightSSE)
			if gain > bestGain {
				bestGain = gain
				feature = f
				threshold = (vk + vNext) / 2
				ok = true
			}
		}
	}
	return feature, threshold, ok
}

// sse returns the sum of squared errors of target[idx] about its own mean.
func sse(target []float64, idx []int) float64 {
	if len(idx) == 0 {
		return 0
	}
	var sum, sq float64
	for _, i := range idx {
		sum += target[i]
		sq += target[i] * target[i]
	}
	return sq - sum*sum/float64(len(idx))
}

// minTrainSamples guards against fitting trees on a handful of rows. Mirrors the
// spirit of forecast.minLabeledSamples but lower — the GBM often trains on the
// (currently tiny) feature store, and Evaluate's own per-fold gate is the real
// defense against overfitting a grade.
const minTrainSamples = 60

// checkDims verifies every sample has the same non-empty feature dimension and
// returns it.
func checkDims(samples []Sample) (int, error) {
	if len(samples) == 0 {
		return 0, ErrBadParams
	}
	d := len(samples[0].Feat)
	if d == 0 {
		return 0, ErrBadParams
	}
	for _, s := range samples {
		if len(s.Feat) != d {
			return 0, ErrBadParams
		}
	}
	return d, nil
}

// Evaluate grades a GBM out-of-sample with expanding-window walk-forward, using
// the SAME fold construction as forecast.Evaluate: samples MUST already be in
// ascending time order (the caller assembles them that way from the feature
// store). It splits them into `folds` contiguous, time-ordered blocks; for each
// fold after the first it trains a fresh GBM on ALL earlier samples and predicts
// the current block, which the model never trained on. Because blocks are
// strictly time-ordered and a fold's training set contains only earlier samples,
// no future information enters any prediction — a later sample can NOT influence
// an earlier fold's grade (verified in tests).
//
// It errors with ErrBadParams for folds < 2 and ErrInsufficientData when there
// are too few samples to give each fold a usable train/test split.
func Evaluate(samples []Sample, folds int, p Params) (Grade, error) {
	if folds < 2 {
		return Grade{}, ErrBadParams
	}
	if len(samples) < minTrainSamples || len(samples) < folds*minPerFold {
		return Grade{}, ErrInsufficientData
	}
	if _, err := checkDims(samples); err != nil {
		return Grade{}, err
	}
	// Defensive: enforce ascending time order (no reliance on caller memory).
	if !ascendingTs(samples) {
		sorted := make([]Sample, len(samples))
		copy(sorted, samples)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Ts < sorted[j].Ts })
		samples = sorted
	}

	n := len(samples)
	var preds, actuals []float64
	for f := 1; f < folds; f++ {
		trainEnd := n * f / folds
		testEnd := n * (f + 1) / folds
		if trainEnd < minTrainSamples || testEnd <= trainEnd {
			continue // train slice too thin to trust
		}
		train := samples[:trainEnd]
		test := samples[trainEnd:testEnd]
		m, err := Train(train, p)
		if err != nil {
			continue // this fold's train slice was unusable; skip it
		}
		for _, s := range test {
			preds = append(preds, m.Predict(s.Feat))
			actuals = append(actuals, s.Y)
		}
	}
	if len(preds) == 0 {
		return Grade{}, ErrInsufficientData
	}
	return gradeFrom(preds, actuals), nil
}

// minPerFold requires each fold hold a handful of samples so a grade is not one
// or two lucky points.
const minPerFold = 12

// ascendingTs reports whether samples are already in non-decreasing Ts order.
func ascendingTs(samples []Sample) bool {
	for i := 1; i < len(samples); i++ {
		if samples[i].Ts < samples[i-1].Ts {
			return false
		}
	}
	return true
}

// Run is the top-level convenience entry point: it walk-forward grades the model
// FIRST (no probability is returned without a grade), then trains a point model
// on all samples and predicts a caller-supplied latest feature vector. ok=false
// whenever there is insufficient data for either grading or training — so a
// caller never surfaces an ungraded GBM probability.
func Run(samples []Sample, latest []float64, folds int, p Params) (prob float64, grade Grade, ok bool) {
	grade, err := Evaluate(samples, folds, p)
	if err != nil {
		return 0, Grade{}, false
	}
	m, err := Train(samples, p)
	if err != nil {
		return 0, Grade{}, false
	}
	return m.Predict(latest), grade, true
}

// ── shared metric helpers (kept local so the package has no dependency) ──

// gradeFrom computes the Grade from paired out-of-sample predictions + labels.
// BaseRate is the majority-class floor max(pPos, 1-pPos), so Lift is measured
// against the strongest no-skill constant predictor — identical to forecast.
func gradeFrom(preds, actuals []float64) Grade {
	n := len(preds)
	correct := 0
	brier := 0.0
	pos := 0
	for i := range preds {
		if (preds[i] >= 0.5) == (actuals[i] >= 0.5) {
			correct++
		}
		d := preds[i] - actuals[i]
		brier += d * d
		if actuals[i] >= 0.5 {
			pos++
		}
	}
	acc := float64(correct) / float64(n)
	pPos := float64(pos) / float64(n)
	baseRate := math.Max(pPos, 1-pPos)
	return Grade{
		N:          n,
		Accuracy:   acc,
		BrierScore: brier / float64(n),
		AUC:        aucRank(preds, actuals),
		BaseRate:   baseRate,
		Lift:       acc - baseRate,
	}
}

// aucRank computes ROC AUC via the Mann-Whitney rank statistic with average
// ranks for ties. Returns 0.5 (no skill) when either class is empty. Identical
// method to forecast.aucRank so the two models' AUCs are comparable.
func aucRank(preds, actuals []float64) float64 {
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
	sumPosRanks, nPos, nNeg := 0.0, 0.0, 0.0
	for k, r := range rows {
		if r.y >= 0.5 {
			sumPosRanks += ranks[k]
			nPos++
		} else {
			nNeg++
		}
	}
	if nPos == 0 || nNeg == 0 {
		return 0.5
	}
	return (sumPosRanks - nPos*(nPos+1)/2) / (nPos * nNeg)
}

// sigmoid is the logistic function, numerically guarded against overflow.
func sigmoid(z float64) float64 {
	if z >= 0 {
		e := math.Exp(-z)
		return 1 / (1 + e)
	}
	e := math.Exp(z)
	return e / (1 + e)
}

// mean of a slice; 0 for empty.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// clampEps keeps a probability strictly inside (0,1) so its logit is finite.
func clampEps(p float64) float64 {
	const eps = 1e-6
	if p < eps {
		return eps
	}
	if p > 1-eps {
		return 1 - eps
	}
	return p
}
