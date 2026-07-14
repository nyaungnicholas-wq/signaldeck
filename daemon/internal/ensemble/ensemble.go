// Package ensemble fuses SignalDeck's independent directional signals into ONE
// calibrated probability of an up move, and — because honesty is the entire
// point of this layer — ships the tools to GRADE that probability out of
// sample: a reliability (calibration) curve, a Brier score, an isotonic
// monotone recalibration map, and a scalar reliability score.
//
// # What this package does and does not claim
//
// RawProbability blends whatever component signals are available into a single
// P(up) in [0,1]. It is deliberately dumb: an equal-weight average of the
// components that carry information, after converting each to a probability.
// A raw probability is NOT calibrated — a raw 0.70 does not mean "up 70% of
// the time". Calibration is measured, and corrected, only against realized
// history via CalibrationCurve / Calibrate / ReliabilityScore.
//
// # No lookahead
//
// This package computes nothing from future bars. RawProbability is a pure
// function of the present component snapshot. The calibration functions consume
// a history of (prediction, realized-outcome) Pairs that the CALLER is
// responsible for building without lookahead: the prediction for a bar must
// have been computable from bars[..i], and its Actual is the later realized
// direction. Given honest Pairs, every output here is an out-of-sample grade.
//
// # Assumptions / costs (stated because the brand is honesty)
//
//   - The blend is unweighted. Components are assumed roughly independent and
//     equally trustworthy once each is on a probability scale. If one component
//     is known to dominate, weight it upstream before calling — this package
//     will not invent weights it cannot justify.
//   - An "edgeless" forecast (ForecastLift <= 0, or nil) is DROPPED, not
//     down-weighted: a forecast with no measured out-of-sample lift carries no
//     information and must not pollute the blend.
//   - Outcomes are raw close-to-close direction with NO transaction costs,
//     spread, or slippage. P(up) > 0.5 is a directional lean, not a tradeable
//     edge; costs must be subtracted downstream before any P&L claim.
//   - Calibration needs data. With fewer than MinCalibrationPairs pairs,
//     Calibrate refuses to fit and returns the identity map with
//     calibrated=false — an honest "not enough evidence" rather than an
//     overfit curve.
package ensemble

import (
	"math"
	"sort"
)

// MinCalibrationPairs is the minimum number of (prediction, outcome) pairs
// required before Calibrate will fit a recalibration map. Below this, there is
// not enough evidence to distinguish a real miscalibration from noise, so
// Calibrate returns the identity map and reports calibrated=false.
const MinCalibrationPairs = 30

// defaultBins is the bin count CalibrationCurve and derived helpers use when a
// caller does not (or cannot) specify one.
const defaultBins = 10

// Components carries the independent signals to be fused into one probability.
// Every field is on its documented native scale; RawProbability converts each
// to a 0..1 up-probability before blending. Optional components are pointers so
// that "absent" is distinct from a real zero value.
type Components struct {
	// PressureScore is the composite Pressure Score in [-1, +1]
	// (sell..buy). It is always present. Converted via (score+1)/2.
	PressureScore float64
	// ExpectancyHitRate is the measured fraction of positive forward returns
	// for the current state, in [0, 1]. nil when no expectancy is available.
	// Used as-is (it is already an up-probability).
	ExpectancyHitRate *float64
	// ForecastProb is the forecast model's P(up) in [0, 1]. nil when no
	// forecast is available. It is used ONLY when ForecastLift indicates the
	// forecast has measured out-of-sample edge (see ForecastLift).
	ForecastProb *float64
	// ForecastLift is the forecast's out-of-sample lift over its base rate.
	// <= 0 (or nil) means the forecast has no demonstrated edge, so
	// ForecastProb is dropped from the blend regardless of its value.
	ForecastLift *float64
	// SentimentScore is the mean daily news-sentiment score in [-1, +1]
	// (bearish..bullish), nil when no fresh aggregate exists. Converted via
	// 0.5 + score*SentimentScale — a deliberately conservative mapping: even
	// maximal sentiment only moves this leg SentimentScale away from coin-
	// flip, because headline tone is a weak, noisy signal until the adaptive
	// layer measures otherwise.
	SentimentScore *float64

	// STAGE 6 gated model legs. Each is a P(up) in [0,1] paired with the
	// out-of-sample lift that earned it a place in the blend — used ONLY when
	// its *Lift > 0, the identical honesty gate the forecast leg passes through.
	// nil (or edgeless lift) means the leg is absent and contributes nothing.

	// GBMProb is the gradient-boosted-tree model's P(up); GBMLift is its
	// walk-forward out-of-sample lift over base rate.
	GBMProb *float64
	GBMLift *float64
	// MeanRevProb is the gated mean-reversion leg's P(up); MeanRevLift is its
	// walk-forward, COST-NET out-of-sample lift. Dropped unless *MeanRevLift > 0.
	MeanRevProb *float64
	MeanRevLift *float64

	// CROSS-SECTIONAL ALPHA leg (CATEGORY NUANCE — stated because honesty is
	// the brand): AlphaXProb is the pooled cross-sectional model's P(this
	// symbol beats the SAME-DAY UNIVERSE MEDIAN forward return) — a RELATIVE
	// outperformance probability, NOT an absolute P(up) like every other leg.
	// Blending a relative prob into a directional blend is a deliberate
	// category mix: the leg enters as a directional TILT (relative strength
	// correlates with direction, and its purged walk-forward gate proved OOS
	// lift on direction-correlated labels), while the adaptive attribution and
	// self-audit measure per regime whether the tilt actually helps — the
	// system's own referee. AlphaXLift is that walk-forward OOS lift; the leg
	// is dropped unless *AlphaXLift > 0, the identical gate the other model
	// legs pass through.
	AlphaXProb *float64
	AlphaXLift *float64
}

// SentimentScale converts a sentiment score in [-1,1] to a probability leg:
// p = 0.5 + score*SentimentScale. Kept small on purpose (honesty doctrine:
// an unproven signal must not be able to dominate the blend on its own).
const SentimentScale = 0.15

// Canonical component (leg) names used by LegProbabilities and
// WeightedProbability. Exported so the adaptive-weights layer and this
// package can never drift apart on naming.
const (
	LegPressure   = "pressure"
	LegExpectancy = "expectancy"
	LegForecast   = "forecast"
	LegSentiment  = "sentiment"
	// STAGE 6 gated model legs.
	LegGBM     = "gbm"     // gradient-boosted-tree directional model
	LegMeanRev = "meanrev" // gated mean-reversion (inverted-momentum) leg
	// Cross-sectional alpha leg (relative-to-universe, blended as a
	// directional tilt — see Components.AlphaXProb).
	LegAlphaX = "alphax"
)

// LegNames lists every possible component leg in canonical order. The two
// Stage-6 model legs (and the later cross-sectional alphax leg) are appended
// so existing weight maps (keyed by leg name) remain valid — a leg absent
// from a stored map simply gets no learned weight and falls back to
// equal-weight, exactly as before.
var LegNames = []string{LegPressure, LegExpectancy, LegForecast, LegSentiment, LegGBM, LegMeanRev, LegAlphaX}

// LegProbabilities converts each AVAILABLE component to its 0..1
// up-probability leg, keyed by canonical leg name. Exactly the legs that
// RawProbability would blend are returned (pressure always; expectancy when
// present; forecast only with demonstrated edge; sentiment when present).
func LegProbabilities(c Components) map[string]float64 {
	legs := map[string]float64{
		LegPressure: clamp01((c.PressureScore + 1) / 2),
	}
	if c.ExpectancyHitRate != nil {
		legs[LegExpectancy] = clamp01(*c.ExpectancyHitRate)
	}
	if c.ForecastProb != nil && c.ForecastLift != nil && *c.ForecastLift > 0 {
		legs[LegForecast] = clamp01(*c.ForecastProb)
	}
	if c.SentimentScore != nil {
		legs[LegSentiment] = clamp01(0.5 + *c.SentimentScore*SentimentScale)
	}
	// STAGE 6 gated model legs: included ONLY with demonstrated out-of-sample
	// edge (*Lift > 0), the same rule the forecast leg obeys. An edgeless or
	// absent model leg is dropped, never down-weighted — honesty doctrine.
	if c.GBMProb != nil && c.GBMLift != nil && *c.GBMLift > 0 {
		legs[LegGBM] = clamp01(*c.GBMProb)
	}
	if c.MeanRevProb != nil && c.MeanRevLift != nil && *c.MeanRevLift > 0 {
		legs[LegMeanRev] = clamp01(*c.MeanRevProb)
	}
	// Cross-sectional alpha leg: identical gate. Its prob is RELATIVE (beat
	// the same-day universe median), entering the directional blend as a tilt
	// — see the Components.AlphaXProb comment for the category nuance.
	if c.AlphaXProb != nil && c.AlphaXLift != nil && *c.AlphaXLift > 0 {
		legs[LegAlphaX] = clamp01(*c.AlphaXProb)
	}
	return legs
}

// RawProbability converts each available component to a 0..1 up-probability and
// returns the mean of the contributing components together with how many
// contributed (nUsed).
//
// Conversions:
//   - PressureScore in [-1,1] -> (score+1)/2 (always contributes).
//   - ExpectancyHitRate in [0,1] -> used as-is (contributes when non-nil).
//   - ForecastProb in [0,1] -> used as-is, but ONLY when ForecastLift is
//     non-nil AND *ForecastLift > 0. An edgeless or absent forecast is dropped
//     so it cannot pollute the blend.
//   - SentimentScore in [-1,1] -> 0.5 + score*SentimentScale (contributes when
//     non-nil). Absent sentiment leaves the blend exactly as before.
//
// The returned probability is clamped to [0,1]. If no component contributes
// (which cannot happen while PressureScore is always counted, but is handled
// defensively), prob=0.5 and nUsed=0 — a neutral, information-free prior.
func RawProbability(c Components) (prob float64, nUsed int) {
	legs := LegProbabilities(c)
	if len(legs) == 0 {
		return 0.5, 0
	}
	var sum float64
	for _, p := range legs {
		sum += p
	}
	return clamp01(sum / float64(len(legs))), len(legs)
}

// WeightedProbability blends the available component legs using the given
// per-leg weights (keyed by canonical leg name; see LegNames) and returns the
// weighted mean plus how many legs contributed.
//
// Fallback semantics (the honesty gates live UPSTREAM in the adaptive layer —
// this function only refuses to invent weight where none applies):
//   - weights nil or empty -> identical to RawProbability (equal-weight mean).
//   - the weights carry no positive mass over the AVAILABLE legs -> identical
//     to RawProbability (learned weights for absent legs say nothing about the
//     legs that are actually present).
//
// A leg with weight <= 0 (or missing from the map) contributes nothing and is
// not counted in nUsed. Pure function; the result is clamped to [0,1].
func WeightedProbability(c Components, weights map[string]float64) (prob float64, nUsed int) {
	if len(weights) == 0 {
		return RawProbability(c)
	}
	legs := LegProbabilities(c)
	var sum, wsum float64
	var n int
	for name, p := range legs {
		w := weights[name]
		if w <= 0 {
			continue
		}
		sum += w * p
		wsum += w
		n++
	}
	if wsum <= 0 {
		return RawProbability(c)
	}
	return clamp01(sum / wsum), n
}

// Pair is one graded prediction: a raw predicted up-probability (Pred, in
// [0,1]) paired with the realized outcome (Actual, 1 for an up move, 0
// otherwise). A history of Pairs is the raw material for every calibration
// grade in this package.
type Pair struct {
	Pred   float64 // predicted P(up), [0,1]
	Actual float64 // realized outcome in {0,1}
}

// Bin is one bucket of the reliability (calibration) curve: predictions whose
// value fell in [Lo, Hi) (the final bin is closed on the right), with the mean
// predicted probability, the mean realized frequency, and the count in the
// bucket. A perfectly calibrated model has MeanPred == MeanActual in every
// populated bin.
type Bin struct {
	Lo         float64 // inclusive lower edge of the bin over [0,1]
	Hi         float64 // exclusive upper edge (inclusive for the top bin)
	MeanPred   float64 // mean predicted probability among pairs in this bin
	MeanActual float64 // mean realized outcome among pairs in this bin
	N          int     // number of pairs in this bin
}

// CalibrationCurve buckets predictions into `bins` equal-width bins over [0,1]
// and, for each bin, reports the mean predicted probability against the mean
// realized outcome. This IS the reliability curve: plotting MeanActual vs
// MeanPred against the diagonal shows where the model is over- or
// under-confident.
//
// Bins are always returned (length == bins) in ascending order, including empty
// ones (N==0, MeanPred==MeanActual==0), so callers get a stable-width curve.
// If bins < 1 it is coerced to defaultBins. Pairs with Pred outside [0,1] are
// clamped into the edge bins. On an empty history the bins are returned empty.
func CalibrationCurve(pairs []Pair, bins int) []Bin {
	if bins < 1 {
		bins = defaultBins
	}
	width := 1.0 / float64(bins)

	out := make([]Bin, bins)
	sumPred := make([]float64, bins)
	sumActual := make([]float64, bins)
	for i := range out {
		out[i].Lo = float64(i) * width
		out[i].Hi = float64(i+1) * width
	}
	out[bins-1].Hi = 1.0

	for _, p := range pairs {
		idx := binIndex(p.Pred, bins)
		out[idx].N++
		sumPred[idx] += clamp01(p.Pred)
		sumActual[idx] += p.Actual
	}
	for i := range out {
		if out[i].N > 0 {
			out[i].MeanPred = sumPred[i] / float64(out[i].N)
			out[i].MeanActual = sumActual[i] / float64(out[i].N)
		}
	}
	return out
}

// BrierScore returns the mean squared error between predicted probabilities and
// realized outcomes: mean over pairs of (Pred-Actual)^2. Lower is better; 0 is
// perfect, 0.25 is the score of a constant 0.5 forecast. Returns 0 for an empty
// history (no evidence, no error to report). Pred is clamped to [0,1] first.
func BrierScore(pairs []Pair) float64 {
	if len(pairs) == 0 {
		return 0
	}
	var sum float64
	for _, p := range pairs {
		d := clamp01(p.Pred) - p.Actual
		sum += d * d
	}
	return sum / float64(len(pairs))
}

// Calibrate fits a monotone recalibration map from raw predicted probabilities
// to calibrated ones and returns it together with a flag reporting whether a
// real fit was performed.
//
// Method: pool-adjacent-violators (isotonic regression). Pairs are sorted by
// Pred, then the realized outcomes are averaged into a non-decreasing step
// function of the prediction via PAV. The returned mapFn linearly interpolates
// between the fitted (Pred, calibratedActual) knots and clamps outside the
// observed prediction range. Because a raw 0.70 is mapped to the realized
// frequency of outcomes near 0.70, an overconfident model (predictions pushed
// to the extremes) is pulled back toward the base rate, and a well-calibrated
// model maps ~identically.
//
// Honesty guard: with fewer than MinCalibrationPairs pairs there is not enough
// evidence to fit, so Calibrate returns the identity map and calibrated=false.
// The identity map is also returned (calibrated=false) when every prediction is
// identical (no spread to fit against).
func Calibrate(pairs []Pair) (mapFn func(float64) float64, calibrated bool) {
	if len(pairs) < MinCalibrationPairs {
		return identity, false
	}

	// Sort a copy by prediction so PAV sees monotone x.
	sorted := make([]Pair, len(pairs))
	copy(sorted, pairs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Pred < sorted[j].Pred
	})

	if clamp01(sorted[0].Pred) == clamp01(sorted[len(sorted)-1].Pred) {
		// No spread in predictions: nothing to calibrate against.
		return identity, false
	}

	// Aggregate to one point per distinct prediction level (x, mean-actual,
	// weight=count), then run weighted PAV so the fitted knots are the
	// isotonic-regressed realized frequency AT each observed prediction value.
	// Knots are honesty-bounded per block inside poolAdjacentViolators (rule
	// of succession over each block's OWN pooled weight), so interpolation can
	// never surface a certainty claim from a thin one-sided bin.
	kx, ky := poolAdjacentViolators(aggregateByPred(sorted))

	fn := func(v float64) float64 {
		return clamp01(interpolate(kx, ky, clamp01(v)))
	}
	return fn, true
}

// levelStat is one distinct prediction value with its realized-outcome mean and
// the number of pairs at that value (the PAV weight).
type levelStat struct {
	x      float64 // the (clamped) prediction value
	mean   float64 // mean Actual among pairs at this prediction
	weight int     // number of pairs at this prediction
}

// aggregateByPred collapses pairs (sorted ascending by Pred) into one levelStat
// per distinct prediction value, carrying the realized-outcome mean and count.
func aggregateByPred(sorted []Pair) []levelStat {
	var out []levelStat
	for _, p := range sorted {
		x := clamp01(p.Pred)
		if n := len(out); n > 0 && out[n-1].x == x {
			// Running mean update for this level.
			out[n-1].weight++
			out[n-1].mean += (p.Actual - out[n-1].mean) / float64(out[n-1].weight)
			continue
		}
		out = append(out, levelStat{x: x, mean: p.Actual, weight: 1})
	}
	return out
}

// ReliabilityScore summarizes calibration as a single number in [0,1]:
// 1 - mean over populated bins of |MeanPred - MeanActual|. A perfectly
// calibrated model (every bin's mean prediction equals its realized frequency)
// scores 1.0; a maximally miscalibrated one approaches 0. Empty bins are
// ignored so sparsely populated regions of the curve do not distort the score.
//
// It uses defaultBins buckets. On an empty history it returns 1.0 by convention
// (no observed miscalibration), which callers should treat as "ungraded" rather
// than "perfect" and gate on sample size separately.
func ReliabilityScore(pairs []Pair) float64 {
	curve := CalibrationCurve(pairs, defaultBins)
	var sumAbs float64
	var populated int
	for _, b := range curve {
		if b.N > 0 {
			sumAbs += math.Abs(b.MeanPred - b.MeanActual)
			populated++
		}
	}
	if populated == 0 {
		return 1.0
	}
	return clamp01(1.0 - sumAbs/float64(populated))
}

// --- internal helpers ---

// identity is the no-op recalibration map used when there is not enough
// evidence to calibrate.
func identity(v float64) float64 { return clamp01(v) }

// clamp01 clamps v to [0,1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// binIndex maps a prediction to its equal-width bin index over [0,1], clamping
// out-of-range predictions into the edge bins. The top edge (Pred==1) lands in
// the final bin rather than overflowing.
func binIndex(pred float64, bins int) int {
	p := clamp01(pred)
	idx := int(p * float64(bins))
	if idx >= bins {
		idx = bins - 1
	}
	if idx < 0 {
		idx = 0
	}
	return idx
}

// poolAdjacentViolators runs weighted isotonic regression (the pool-adjacent-
// violators algorithm) over per-prediction levels (sorted ascending by x) and
// returns the interpolation knots: kx are the distinct prediction values and ky
// are the corresponding non-decreasing, count-weighted calibrated frequencies.
// Adjacent levels that violate monotonicity (a higher prediction with a lower
// realized frequency) are pooled into a single block whose weighted mean is
// assigned to every level in the block — so a raw prediction maps to the
// realized frequency of outcomes at and around it. len(kx) == len(ky) ==
// len(levels).
func poolAdjacentViolators(levels []levelStat) (kx, ky []float64) {
	// Each block tracks its weighted mean, total pair weight, and how many
	// input levels it spans (so the pooled value can be expanded back over
	// exactly those levels).
	type block struct {
		mean   float64
		weight int // total pair count
		span   int // number of levels covered
	}
	blocks := make([]block, 0, len(levels))

	for _, lv := range levels {
		b := block{mean: lv.mean, weight: lv.weight, span: 1}
		// Merge with the previous block while it violates monotonicity, i.e.
		// while the previous block's mean exceeds this block's mean.
		for len(blocks) > 0 {
			prev := blocks[len(blocks)-1]
			if prev.mean <= b.mean {
				break
			}
			totalW := prev.weight + b.weight
			b.mean = (prev.mean*float64(prev.weight) + b.mean*float64(b.weight)) / float64(totalW)
			b.weight = totalW
			b.span += prev.span
			blocks = blocks[:len(blocks)-1]
		}
		blocks = append(blocks, b)
	}

	// Expand each block's pooled mean back over the levels it covers, keeping
	// one knot per distinct prediction value. Each knot's fitted frequency is
	// bounded by the RULE OF SUCCESSION over the block's OWN pooled pair
	// weight: a block backed by w pairs can never claim a frequency outside
	// [1/(w+2), (w+1)/(w+2)]. This is the honesty bound that matters — a lone
	// thin-tail pair whose one outcome went "up" pools to w=1 and is capped at
	// 2/3, instead of dragging the whole map to a displayed P(up)=100.0%.
	// (A bound keyed to the TOTAL pair count is useless here: at n=3000 it is
	// 1/3002 ≈ 0.0003, which still renders as 100.0%.)
	kx = make([]float64, 0, len(levels))
	ky = make([]float64, 0, len(levels))
	li := 0
	for _, b := range blocks {
		lo := 1.0 / float64(b.weight+2)
		v := math.Min(1.0-lo, math.Max(lo, b.mean))
		for k := 0; k < b.span; k++ {
			kx = append(kx, levels[li].x)
			ky = append(ky, v)
			li++
		}
	}
	return kx, ky
}

// interpolate linearly interpolates y at x over the knots (kx, ky), where kx is
// strictly increasing. Values of x below kx[0] or above the last knot are
// clamped to the endpoint y (the fitted map does not extrapolate a trend beyond
// the observed prediction range). Assumes len(kx) == len(ky) >= 1.
func interpolate(kx, ky []float64, x float64) float64 {
	if len(kx) == 1 {
		return ky[0]
	}
	if x <= kx[0] {
		return ky[0]
	}
	if x >= kx[len(kx)-1] {
		return ky[len(ky)-1]
	}
	// Binary search for the segment [kx[lo], kx[hi]] containing x.
	lo, hi := 0, len(kx)-1
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if kx[mid] <= x {
			lo = mid
		} else {
			hi = mid
		}
	}
	span := kx[hi] - kx[lo]
	if span == 0 {
		return ky[lo]
	}
	t := (x - kx[lo]) / span
	return ky[lo] + t*(ky[hi]-ky[lo])
}

// ─────────────────────────────────────────────────────────────────────────
// PER-SYMBOL AGENTS WAVE (appended block — keep at END of ensemble.go so
// parallel edits never collide). A calibration map that can be PERSISTED:
// CalibrateKnots exposes the same isotonic fit Calibrate performs, but returns
// the raw (kx, ky) interpolation knots instead of a closure, so a per-symbol
// calibration can be stored as JSON and rebuilt later with MapFromKnots — the
// SAME interpolation used live, so a persisted per-symbol calibration behaves
// identically to a freshly-fit one (no reimplementation drift).
// ─────────────────────────────────────────────────────────────────────────

// CalibrateKnots fits the identical monotone isotonic recalibration as
// Calibrate but returns the fitted knots (kx strictly increasing, ky the
// non-decreasing calibrated frequencies) instead of a closure. Same honesty
// guard: with fewer than MinCalibrationPairs pairs, or no spread in the
// predictions, it returns nil knots and calibrated=false (the caller should
// then use the identity map). The returned knots are safe to JSON-persist and
// reconstruct with MapFromKnots.
func CalibrateKnots(pairs []Pair) (kx, ky []float64, calibrated bool) {
	if len(pairs) < MinCalibrationPairs {
		return nil, nil, false
	}
	sorted := make([]Pair, len(pairs))
	copy(sorted, pairs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Pred < sorted[j].Pred
	})
	if clamp01(sorted[0].Pred) == clamp01(sorted[len(sorted)-1].Pred) {
		return nil, nil, false
	}
	// Knots come back honesty-bounded per block (rule of succession over each
	// block's own pooled weight), so persisted knots never encode certainty.
	kx, ky = poolAdjacentViolators(aggregateByPred(sorted))
	return kx, ky, true
}

// MapFromKnots rebuilds a recalibration map from persisted knots. With empty or
// mismatched knots it returns the identity map, so a symbol without a fitted
// calibration transparently falls back to "no correction". The rebuilt map is
// the SAME clamped linear interpolation Calibrate returns.
func MapFromKnots(kx, ky []float64) func(float64) float64 {
	if len(kx) == 0 || len(kx) != len(ky) {
		return identity
	}
	// Defensive copy so a caller mutating the slices can't change the closure.
	xs := append([]float64(nil), kx...)
	ys := append([]float64(nil), ky...)
	// Honesty bound for PERSISTED knots (incl. ones stored before CalibrateKnots
	// bounded them): any legitimate fit had n >= MinCalibrationPairs pairs, so
	// the loosest justified frequency bound is 1/(MinCalibrationPairs+2) — a
	// rebuilt map must never surface P(up)=0 or 1.
	lo := 1.0 / float64(MinCalibrationPairs+2)
	hi := 1.0 - lo
	return func(v float64) float64 {
		m := interpolate(xs, ys, clamp01(v))
		return math.Min(hi, math.Max(lo, m))
	}
}
