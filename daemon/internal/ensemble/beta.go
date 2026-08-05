package ensemble

import (
	"errors"
	"math"
	"sort"
)

// BETA CALIBRATION — a calibration map that cannot destroy the ranking.
//
// WHY THIS EXISTS
// Isotonic regression (Calibrate, above) is only WEAKLY monotone: pool-adjacent-
// violators emits flat blocks, and every raw probability inside a block maps to
// one identical calibrated value. Measured live on 2026-08-02, seven crypto
// symbols scored at one instant produced
//
//	raw:  ADA .536  BTC .479  SOL .451  ETH .419  DOGE .413  XRP .359  LINK .324
//	cal:  .4635     .4635     .4635     .4635     .4635      .4635     .4635
//
// The legs had done real per-symbol work and calibration threw all of it away.
// Every symbol received one identical call, so a batch of seven "independent"
// predictions was one prediction counted seven times — and when it was wrong,
// it was wrong seven times. Across 322 stocks at one timestamp there were six
// distinct calibrated values.
//
// The existing monotonicity guard did not catch this because it checks for
// INVERSION (a more bullish input publishing a lower probability). Ties are not
// inversions. Weak monotonicity is exactly strong enough to pass that check and
// exactly weak enough to erase a cross-sectional ranking.
//
// THE MAP
// Beta calibration (Kull, Silva Filho & Flach, 2017) fits
//
//	cal(p) = 1 / (1 + 1 / (exp(c) · p^a / (1-p)^b))
//
// by logistic regression of the outcome on [ln p, −ln(1−p)]. With a>0 and b>0
// it is STRICTLY increasing on (0,1), so distinct inputs cannot collide: the
// ranking survives by construction rather than by luck. It is also a three-
// parameter family, which cannot carve the step artifacts isotonic produces on
// thin financial data.
//
// WHAT IT DOES NOT DO
// It does not invent discrimination. If the raw scores genuinely carry no
// signal, beta calibration flattens them toward the base rate too — the curve
// just stays strictly increasing while doing it, so the ORDER is preserved even
// when the LEVEL is uninformative. That distinction is the whole point: an
// ordering worth ranking on can survive a probability level that is not worth
// betting on outright.

// betaParams are the fitted coefficients of the beta calibration map.
type betaParams struct{ a, b, c float64 }

// apply evaluates the fitted map. Strictly increasing whenever a > 0 and b > 0.
func (p betaParams) apply(v float64) float64 {
	// Clamp away from the open interval's ends: ln(0) and ln(1-1) are infinite,
	// and a raw probability of exactly 0 or 1 is a certainty claim no leg earns.
	const eps = 1e-6
	x := math.Min(1-eps, math.Max(eps, v))
	z := p.c + p.a*math.Log(x) - p.b*math.Log(1-x)

	// Clamp Z, not the output. Special-casing `z < -500 -> eps` INVERTED the
	// map: at z = -499 the sigmoid underflows to exactly 0, which is smaller
	// than the 1e-6 the branch returned just below it, so a lower raw score
	// published a HIGHER probability. Caught at raw=0.40 by the pipeline's
	// inversion check.
	//
	// Clamping the sigmoid's input is non-decreasing, and clamping the result
	// into [eps, 1-eps] is non-decreasing, so the composition cannot invert.
	// Both tails flatten in the saturated region — unavoidable in float64, and
	// harmless at probabilities no leg has earned the right to claim.
	z = math.Max(-500, math.Min(500, z))
	return math.Min(1-eps, math.Max(eps, 1/(1+math.Exp(-z))))
}

// fitBeta fits the three parameters by IRLS (Newton) on the logistic likelihood
// with design [1, ln p, −ln(1−p)].
//
// Returns an error rather than a degenerate map when the fit does not converge
// or produces a non-increasing curve (a<=0 or b<=0). A calibration that cannot
// be shown to be increasing is not a calibration, and the caller falls back to
// isotonic rather than shipping a scrambler.
func fitBeta(pairs []Pair) (betaParams, error) {
	const eps = 1e-6
	n := len(pairs)
	if n < MinCalibrationPairs {
		return betaParams{}, errors.New("too few pairs to fit")
	}

	x1 := make([]float64, n) // ln p
	x2 := make([]float64, n) // −ln(1−p)
	y := make([]float64, n)
	for i, p := range pairs {
		v := math.Min(1-eps, math.Max(eps, clamp01(p.Pred)))
		x1[i] = math.Log(v)
		x2[i] = -math.Log(1 - v)
		if p.Actual >= 0.5 {
			y[i] = 1
		}
	}

	// beta = [c, a, b]; design row is [1, x1, x2].
	beta := [3]float64{0, 1, 1} // start at the identity-ish map
	for iter := 0; iter < 50; iter++ {
		var grad [3]float64
		var hess [3][3]float64
		for i := 0; i < n; i++ {
			row := [3]float64{1, x1[i], x2[i]}
			z := beta[0] + beta[1]*x1[i] + beta[2]*x2[i]
			mu := 1 / (1 + math.Exp(-math.Max(-500, math.Min(500, z))))
			w := mu * (1 - mu)
			if w < 1e-10 {
				w = 1e-10 // keep the Hessian positive-definite on saturated rows
			}
			r := y[i] - mu
			for j := 0; j < 3; j++ {
				grad[j] += row[j] * r
				for k := 0; k < 3; k++ {
					hess[j][k] += row[j] * row[k] * w
				}
			}
		}
		// Ridge term: the two log features are strongly collinear when the raw
		// scores cluster (exactly the thin-data case here), which makes the
		// Hessian near-singular and the solve explode.
		for j := 0; j < 3; j++ {
			hess[j][j] += 1e-6
		}
		delta, ok := solve3(hess, grad)
		if !ok {
			return betaParams{}, errors.New("singular Hessian")
		}
		var maxStep float64
		for j := 0; j < 3; j++ {
			beta[j] += delta[j]
			maxStep = math.Max(maxStep, math.Abs(delta[j]))
		}
		if maxStep < 1e-8 {
			break
		}
		if math.IsNaN(beta[0]) || math.IsNaN(beta[1]) || math.IsNaN(beta[2]) {
			return betaParams{}, errors.New("diverged")
		}
	}

	p := betaParams{a: beta[1], b: beta[2], c: beta[0]}
	if !(p.a > 0) || !(p.b > 0) {
		// A non-increasing curve would reorder symbols. Refuse it.
		return betaParams{}, errors.New("fitted map is not strictly increasing")
	}

	// NO separate saturation guard, deliberately.
	//
	// A steep fit CAN drive the tails toward certainty (measured: 0.10 -> 1e-6
	// on a sample where outcomes tracked the raw score deterministically). The
	// first attempt at a fix rescaled the output into isotonic's knot range,
	// which is an arbitrary linear squeeze of a sigmoid: it degraded held-out
	// Brier enough that isotonic then won every comparison, i.e. the guard
	// disabled the feature it was protecting.
	//
	// The out-of-sample Brier gate in CalibrateRanking already covers this. A
	// map that saturates further than the evidence supports scores WORSE on
	// held-out rows, so isotonic ships and `ranked` reports false. Saturation
	// that survives that test is saturation the data actually earned. One
	// measurement beats two rules that disagree.
	return p, nil
}

// solve3 solves H·d = g for a 3x3 system by Gauss-Jordan with partial pivoting.
func solve3(h [3][3]float64, g [3]float64) ([3]float64, bool) {
	m := [3][4]float64{}
	for i := 0; i < 3; i++ {
		copy(m[i][:3], h[i][:])
		m[i][3] = g[i]
	}
	for col := 0; col < 3; col++ {
		piv := col
		for r := col + 1; r < 3; r++ {
			if math.Abs(m[r][col]) > math.Abs(m[piv][col]) {
				piv = r
			}
		}
		if math.Abs(m[piv][col]) < 1e-12 {
			return [3]float64{}, false
		}
		m[col], m[piv] = m[piv], m[col]
		d := m[col][col]
		for j := col; j < 4; j++ {
			m[col][j] /= d
		}
		for r := 0; r < 3; r++ {
			if r == col {
				continue
			}
			f := m[r][col]
			for j := col; j < 4; j++ {
				m[r][j] -= f * m[col][j]
			}
		}
	}
	return [3]float64{m[0][3], m[1][3], m[2][3]}, true
}

// CalibrateRanking returns a calibration map that preserves the ordering of its
// inputs, choosing between beta and isotonic on OUT-OF-SAMPLE Brier score.
//
// Selection is honest about what it is buying. Beta is only preferred when it
// is at least as good as isotonic on held-out data — preserving a ranking is
// worthless if the probabilities get worse to do it. When isotonic wins on
// Brier, isotonic ships and the ranking collapse is reported by `ranked=false`
// so a caller can say so rather than assume.
//
// The holdout is the LAST 25% of pairs by timestamp, not a random split: these
// pairs are a time series, and a random split lets the fit see the future.
func CalibrateRanking(pairs []Pair) (mapFn func(float64) float64, calibrated, ranked bool) {
	if len(pairs) < MinCalibrationPairs {
		return identity, false, false
	}

	iso, isoOK := Calibrate(pairs)

	ordered := make([]Pair, len(pairs))
	copy(ordered, pairs)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Ts < ordered[j].Ts })
	cut := len(ordered) * 3 / 4
	// SNAP THE CUT TO A Ts BOUNDARY. Callers that stamp Ts with a real period
	// (the fleet-wide fit stamps the UTC day) put many rows on one Ts, and those
	// rows share one market move. A cut at a raw index lands mid-period and puts
	// the SAME move on both sides of the train/test line, so the held-out Brier
	// comparison below scores a map partly on data it was fitted on — which
	// silently disarms the one gate that is supposed to catch a bad map.
	//
	// Advance to the first row of the next Ts. Harmless when Ts is a pure
	// ordinal (every Ts is unique, so the cut does not move).
	for cut < len(ordered) && ordered[cut].Ts == ordered[cut-1].Ts {
		cut++
	}
	train, test := ordered[:cut], ordered[cut:]
	if len(train) < MinCalibrationPairs || len(test) == 0 {
		// Not enough history to hold anything out; fall back rather than fit on
		// the same rows we would score.
		return iso, isoOK, false
	}

	bp, err := fitBeta(train)
	if err != nil {
		return iso, isoOK, false
	}
	isoTrain, isoTrainOK := Calibrate(train)
	if !isoTrainOK {
		isoTrain = identity
	}

	// PAIRED comparison. The two maps score the SAME held-out rows, so the
	// per-row difference in squared error is the statistic, and its standard
	// error says whether the gap is real.
	//
	// A bare `betaBrier > isoBrier` test was too strict: on a sample with a
	// genuinely informative ordering the two came out 0.228176 vs 0.227094, a
	// delta of +0.001 that is pure noise, and the ranking was discarded over
	// it. Isotonic must be SIGNIFICANTLY better to justify collapsing a
	// ranking, not merely luckier on the third decimal.
	diffs := make([]float64, len(test))
	var mean float64
	for i, p := range test {
		db := bp.apply(p.Pred) - p.Actual
		di := isoTrain(p.Pred) - p.Actual
		diffs[i] = db*db - di*di // >0 means beta did worse on this row
		mean += diffs[i]
	}
	mean /= float64(len(diffs))

	var variance float64
	for _, d := range diffs {
		variance += (d - mean) * (d - mean)
	}
	if len(diffs) > 1 {
		variance /= float64(len(diffs) - 1)
	}
	se := math.Sqrt(variance / float64(len(diffs)))

	// Only defer to isotonic when it beats beta by more than ~2 standard
	// errors. Ties go to the map that keeps the ranking, because preserving the
	// ordering is free when the probabilities are indistinguishable.
	if se > 0 && mean/se > 1.96 {
		return iso, isoOK, false
	}
	if se == 0 && mean > 0 {
		return iso, isoOK, false
	}

	// Refit on everything now that beta has earned the slot.
	full, err := fitBeta(pairs)
	if err != nil {
		return iso, isoOK, false
	}
	return func(v float64) float64 { return full.apply(v) }, true, true
}
