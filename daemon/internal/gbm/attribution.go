// Per-feature attribution for a single GBM prediction — SAABAS PATH
// ATTRIBUTION, stated plainly because honesty is the brand:
//
// Method: walk each tree along the prediction's path. Every node carries an
// expected value; at every split, the difference between the chosen child's
// expected value and the parent's expected value is credited to the SPLIT
// FEATURE. Summed over the path this telescopes to (leaf value − root value),
// so across all trees:
//
//	Bias + LR·Σ rootValue(tree)  +  Σ per-feature contributions
//	    == Bias + LR·Σ leafValue(tree) == the model's raw log-odds score, EXACTLY
//
// (the identity is asserted in the tests, not assumed).
//
// Two known biases, disclosed rather than papered over:
//
//  1. This is Saabas attribution, NOT exact SHAP. Saabas credits each split
//     using only the single root→leaf path, so with DEEP INTERACTIONS the
//     credit is biased toward features split NEARER THE LEAVES: an early
//     split's effect is partially re-attributed to the later splits that
//     condition on it. Exact TreeSHAP averages over all feature orderings and
//     removes that bias at exponential-in-depth cost; at MaxDepth 3 (the
//     pipeline default) the discrepancy is modest, but it is real and grows
//     with depth. Do not present these numbers as Shapley values.
//
//  2. A fitted Model stores values only at LEAVES, and this file must not
//     restructure the training code, so an internal node's expected value is
//     reconstructed as the UNWEIGHTED mean of its subtree's leaf values —
//     Saabas proper weights by training-sample coverage. With unbalanced
//     leaves this shifts credit between the features along a path; it never
//     breaks the sum identity above, which holds for ANY choice of internal
//     node values because the path sum telescopes.
package gbm

// Attribution decomposes one prediction's raw log-odds score into a base plus
// one contribution per feature index. See the file header for the method
// (Saabas path attribution) and its disclosed biases.
type Attribution struct {
	// Base is the score of the "average" input: the model bias plus the
	// learning-rate-weighted sum of every tree's root expected value.
	Base float64
	// Contribs has one entry per feature index (len == Model.NFeat). Base +
	// sum(Contribs) equals the model's raw log-odds score for x exactly, so
	// sigmoid(Base + sum(Contribs)) == Predict(x).
	Contribs []float64
}

// Attribute returns the Saabas path attribution of the model's raw score for
// one feature vector. ok=false mirrors Predict's defensive posture: a vector
// of the wrong dimension gets no attribution (Predict returns the neutral 0.5
// there, and there is nothing honest to decompose about a refused input).
func (m *Model) Attribute(x []float64) (Attribution, bool) {
	if len(x) != m.NFeat || m.NFeat == 0 {
		return Attribution{}, false
	}
	a := Attribution{Base: m.Bias, Contribs: make([]float64, m.NFeat)}
	for _, t := range m.Trees {
		vals := nodeValues(t)
		a.Base += m.LR * vals[t]
		n := t
		for !n.Leaf {
			var child *treeNode
			if x[n.Feature] <= n.Threshold {
				child = n.Left
			} else {
				child = n.Right
			}
			a.Contribs[n.Feature] += m.LR * (vals[child] - vals[n])
			n = child
		}
	}
	return a, true
}

// nodeValues computes every node's expected value for one tree: a leaf's own
// value at the leaves, and the unweighted mean of the subtree's leaf values at
// internal nodes (see file-header caveat 2 — training coverage is not stored,
// so equal leaf weighting stands in for it).
func nodeValues(root *treeNode) map[*treeNode]float64 {
	vals := make(map[*treeNode]float64)
	var walk func(n *treeNode) (sum float64, leaves int)
	walk = func(n *treeNode) (float64, int) {
		if n.Leaf {
			vals[n] = n.Value
			return n.Value, 1
		}
		ls, lc := walk(n.Left)
		rs, rc := walk(n.Right)
		sum, leaves := ls+rs, lc+rc
		vals[n] = sum / float64(leaves)
		return sum, leaves
	}
	walk(root)
	return vals
}

// RawScore returns the model's raw additive log-odds score for x (the quantity
// Attribute decomposes; Predict is its sigmoid). Exposed so callers and tests
// can check the sum identity without re-deriving the logit from a clamped
// probability. Wrong-dimension inputs return the bias alone, matching the
// "no information" posture of Predict's 0.5.
func (m *Model) RawScore(x []float64) float64 {
	if len(x) != m.NFeat {
		return m.Bias
	}
	score := m.Bias
	for _, t := range m.Trees {
		score += m.LR * t.predict(x)
	}
	return score
}
