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
// # Two blend modes
//
// In cold-start mode (RequireMeasuredLegs false, the default), a leg whose
// out-of-sample lift was never measured is KEPT — a dead or erroring trainer
// cannot blank the platform. In production mode (RequireMeasuredLegs true),
// EVERY leg must carry a measured positive lift to be admitted; absence of
// evidence stops meaning evidence of edge. AdmittedProbability returns ok=false
// when no leg is admitted, so a caller emits "no forecast" instead of a 0.5
// that reads on a wire exactly like a real coin-flip call.
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
	"errors"
	"fmt"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"math"
	"sort"
)

// ErrNonMonotone reports a calibration map whose fitted frequencies decrease as
// the prediction increases. It is the one property isotonic regression exists
// to guarantee, and the property every cal_prob ranking depends on: if a more
// bullish raw input can publish a LOWER probability, sorting by the published
// probability sorts nothing. Returned by ValidateKnots and refused (never
// served) by MapFromKnotsChecked.
var ErrNonMonotone = errors.New("ensemble: calibration knots are not monotone")

// ValidateKnots is the WRITE-TIME assertion on a calibration map: a map that
// fails here must fail loudly rather than ship. It checks everything the live
// interpolation assumes and the published probability depends on:
//
//   - the knots are non-empty and the same length;
//   - kx is STRICTLY increasing (interpolate divides by the segment width, and
//     aggregateByPred is supposed to emit one knot per distinct prediction);
//   - ky is non-decreasing (monotonicity — see ErrNonMonotone);
//   - every ky is a frequency in [0,1].
//
// This exists because the 2026-07-26 review found 495 of 928 PERSISTED live
// maps violating monotonicity with nothing in the codebase checking. PAV
// itself is monotone by construction; the violations came from weight-
// dependent per-block transforms applied AFTER it. An assertion at the write
// and read boundaries is what makes that class of bug impossible to ship
// silently again.
func ValidateKnots(kx, ky []float64) error {
	if len(kx) == 0 || len(ky) == 0 {
		return errors.New("ensemble: calibration knots are empty")
	}
	if len(kx) != len(ky) {
		return fmt.Errorf("ensemble: calibration knot length mismatch: kx=%d ky=%d", len(kx), len(ky))
	}
	for i := range ky {
		if math.IsNaN(ky[i]) || ky[i] < 0 || ky[i] > 1 {
			return fmt.Errorf("ensemble: calibrated frequency ky[%d]=%v is not a probability", i, ky[i])
		}
		if math.IsNaN(kx[i]) {
			return fmt.Errorf("ensemble: prediction knot kx[%d] is NaN", i)
		}
		if i == 0 {
			continue
		}
		if kx[i] <= kx[i-1] {
			return fmt.Errorf("ensemble: prediction knots not strictly increasing at %d: %v <= %v", i, kx[i], kx[i-1])
		}
		if ky[i] < ky[i-1]-monotoneEps {
			return fmt.Errorf("%w: at kx=%v the fitted frequency drops %v -> %v", ErrNonMonotone, kx[i], ky[i-1], ky[i])
		}
	}
	return nil
}

// monotoneEps is the float slack ValidateKnots allows before calling a
// decrease a real inversion. Sized for accumulated float error in a weighted
// mean, far below any difference a published percentage could show.
const monotoneEps = 1e-12

// discriminationEps is the smallest spread in fitted frequencies that still
// counts as a map telling symbols apart. Well under a tenth of a percentage
// point: below this, every symbol receives the same published probability to
// three decimal places.
const discriminationEps = 1e-4

// KnotsDiscriminate reports whether a fitted calibration map can still tell two
// inputs apart — that is, whether its output range is wider than a rounding
// error.
//
// It exists because ValidateKnots CANNOT catch this, by construction. That
// function rejects INVERSIONS (a more bullish input publishing a lower
// probability) and a flat map has none: ties are not inversions, so a map whose
// every knot carries the identical frequency passes validation cleanly and is
// then served. The isotonic fits this codebase stores are exactly the shape that
// produces one — pool-adjacent-violators emits flat blocks, and on thin
// financial data the whole map can become one block.
//
// A flat map is not a weak forecast, it is a DIFFERENT KIND of object: every
// symbol receives one identical probability, so a batch of N "independent"
// per-symbol predictions is one prediction counted N times, and when it is
// wrong it is wrong N times. Callers must refuse it and fall back rather than
// publish a market-wide constant as a per-symbol forecast.
func KnotsDiscriminate(ky []float64) bool {
	if len(ky) < 2 {
		return false
	}
	lo, hi := ky[0], ky[0]
	for _, v := range ky[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return hi-lo > discriminationEps
}

// MinCalibrationPairs is the minimum number of (prediction, outcome) pairs
// required before Calibrate will fit a recalibration map. Below this, there is
// not enough evidence to distinguish a real miscalibration from noise, so
// Calibrate returns the identity map and reports calibrated=false.
const MinCalibrationPairs = 30

// MinCalibrationDays is the minimum number of DISTINCT UTC DAYS the pairs must
// span before a per-symbol isotonic map is fitted from them.
//
// MinCalibrationPairs counts PAIRS, and pairs are not observations: the
// predictor runs every 10 minutes against daily labels, so 30 pairs for one
// symbol is about three days of evidence (measured live: 12.1 rows per
// symbol-day, one case of 153 rows inside a single day). An isotonic map fitted
// on three days is fitting three market moves, and it then rewrites every
// published probability for that symbol.
//
// 20 days is the same floor adaptive.MinCellDays uses, generalised from
// metalabel.MinTakenDays for the same stated reason: same-day rows are not
// independent evidence.
//
// NOT YET ENFORCED ON THE FLEET-WIDE MAP — see Calibrate.
const MinCalibrationDays = 20

// DistinctPairDays counts the distinct trading days a set of graded pairs spans —
// the honest sample size behind anything fitted from them. Unstamped pairs
// (Ts==0) all collapse onto day 0, so a caller that supplies no timestamps
// reports one day and is refused rather than silently trusted.
func DistinctPairDays(pairs []Pair) int {
	days := make(map[int64]struct{}, len(pairs))
	for _, p := range pairs {
		days[md.TradingDay(p.Ts)] = struct{}{}
	}
	return len(days)
}

// calibrationPriorStrength is the empirical-Bayes pseudocount used to SHRINK
// each isotonic block's fitted frequency toward the dataset base rate. A block
// backed by w real pairs is pulled toward the base rate as if it also carried
// calibrationPriorStrength coin-flip pairs: with thin per-symbol data (blocks
// of a handful of pairs over ~10 days) this dominates and the calibrated
// probability stays near the base rate; with a heavily-populated global block
// (hundreds of pairs) it barely moves. This is the fix for degenerate per-
// symbol calibration that mapped a short up-streak to P(up,1d)=0.85–0.96 —
// overconfident probabilities no realized 1-day accuracy could support.
//
// It is a convex combination with a constant, so it preserves order between
// blocks of the SAME weight — but NOT across blocks of different weight, which
// is the case that actually ships. That was C3: the shrinkage reordered what
// PAV had just ordered, and 495 of 928 live maps published a lower probability
// for a more bullish input. poolAdjacentViolators therefore re-projects onto
// the monotone cone AFTER shrinking; do not remove that second pass.
const calibrationPriorStrength = 25.0

// persistedCalLo/Hi are the hard DISPLAY ceiling on a rebuilt PERSISTED per-
// symbol calibration map. Shrinkage (poolAdjacentViolators) is the primary
// tamer, but a symbol with hundreds of own outcomes has enough weight that
// shrinkage barely moves an overfit block, and persisted knots fit before
// shrinkage existed stay overconfident until the hourly per-symbol-learner
// refits them. Realized 1-day directional accuracy sits near 51%, so a rebuilt
// per-symbol map surfacing P(up,1d)=0.85 is not credible — cap the DISPLAYED
// calibrated probability at a modest, honest band. Applied at read time, so it
// also fixes stale persisted maps immediately without waiting for a refit.
const (
	persistedCalLo = 0.25
	persistedCalHi = 0.75
)

// defaultBins is the bin count CalibrationCurve and derived helpers use when a
// caller does not (or cannot) specify one.
const defaultBins = 10

// Components carries the independent signals to be fused into one probability.
// Every field is on its documented native scale; RawProbability converts each
// to a 0..1 up-probability before blending. Optional components are pointers so
// that "absent" is distinct from a real zero value.
type Components struct {
	// PressureScore is the composite Pressure Score in [-1, +1]
	// (sell..buy). Converted via (score+1)/2. Whether it CONTRIBUTES is now
	// governed by PressureLift (below) — see LegProbabilities.
	PressureScore float64
	// PressureLift is the pressure leg's measured out-of-sample lift over the
	// naive baseline, graded by the pressure-trainer worker and stored like a
	// model leg. FAIL-SAFE, deliberately asymmetric with the opt-in model legs:
	// the leg is dropped ONLY when this is non-nil AND <= 0 (a MEASURED
	// anti-predictive grade); an unmeasured leg (nil) is KEPT, so a cold or
	// erroring trainer never blanks the platform's oldest base leg. Historically
	// pressure was always-on (PressureLift implicitly nil), so prior behavior is
	// preserved until a negative grade is measured.
	PressureLift *float64

	// RequireMeasuredLegs switches the blend from cold-start (fail-safe) to
	// production (fail-closed). When false — the default, preserving the
	// historical contract — a leg whose out-of-sample lift was never measured
	// is KEPT. When true, EVERY leg must carry a measured positive lift to be
	// admitted, including pressure, expectancy and sentiment. Absence of
	// evidence stops meaning evidence of edge.
	RequireMeasuredLegs bool

	// RankEdge carries each leg's measured RANKING edge, keyed by canonical leg
	// name (see LegNames): clusterstat.RankEdge of the leg's out-of-sample AUC,
	// which is positive only when the leg is demonstrated — net of its own
	// sampling error — to order symbols better than chance.
	//
	// When a leg has an entry here it OVERRIDES the *Lift gate for that leg,
	// because lift is threshold-dependent and a blend consumes order, not
	// thresholded calls: on this repo's live record the lift gate benched the
	// forecast leg on 80% of rows and the benched rows were precisely the ones
	// that ranked. A leg with no entry falls back to its historical *Lift
	// behaviour, so an ungraded leg behaves exactly as before.
	RankEdge map[string]float64

	// ExpectancyHitRate is the measured fraction of positive forward returns
	// for the current state, in [0, 1]. nil when no expectancy is available.
	// Used as-is (it is already an up-probability).
	ExpectancyHitRate *float64
	// ExpectancyLift is the expectancy leg's measured out-of-sample lift. Only
	// consulted when RequireMeasuredLegs is set; nil means never measured.
	ExpectancyLift *float64

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
	// SentimentLift is the sentiment leg's measured out-of-sample lift. Only
	// consulted when RequireMeasuredLegs is set; nil means never measured.
	SentimentLift *float64

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

// admits reports whether a leg with the given measured lift may enter the
// blend. In strict mode an unmeasured leg (nil) is refused; in the default
// cold-start mode it is kept, and only a MEASURED non-positive lift benches
// it. A measured non-positive lift is refused in BOTH modes.
func admits(lift *float64, strict bool) bool {
	if lift != nil {
		return *lift > 0
	}
	return !strict
}

// admitsLeg is admits() with the ranking gate in front of it. A leg that has
// been GRADED for ranking is judged on that grade alone — the threshold-
// dependent lift no longer gets a vote, in either direction. A leg with no
// ranking grade falls through to the historical lift behaviour unchanged, so
// this is additive: nothing that was ungraded changes.
func admitsLeg(c Components, leg string, lift *float64) bool {
	if e, ok := c.RankEdge[leg]; ok {
		return e > 0
	}
	return admits(lift, c.RequireMeasuredLegs)
}

// admitsOptIn is admitsLeg for the legs that were ALWAYS opt-in (forecast, gbm,
// meanrev, alphax): absent evidence has never admitted them and still does not,
// in either mode. Only the measured quantity changes — ranking where it has
// been graded, lift where it has not.
func admitsOptIn(c Components, leg string, lift *float64) bool {
	if e, ok := c.RankEdge[leg]; ok {
		return e > 0
	}
	return lift != nil && *lift > 0
}

// LegProbabilities converts each ADMITTED component to its 0..1 up-probability
// leg, keyed by canonical leg name. Exactly the legs that RawProbability would
// blend are returned.
//
// Admission depends on Components.RequireMeasuredLegs:
//   - false (cold start, the historical default): pressure, expectancy and
//     sentiment enter on availability alone; only a MEASURED non-positive lift
//     benches them.
//   - true (production): every leg must carry a measured positive lift.
//
// The forecast, GBM, meanrev and alphax legs always required a measured
// positive lift and are unaffected by the mode.
func LegProbabilities(c Components) map[string]float64 {
	legs := map[string]float64{}
	// Pressure leg — historically the always-on base leg, now held to an OOS
	// lift gate once that lift has been MEASURED (pressure-trainer). FAIL-SAFE
	// and deliberately asymmetric with the opt-in model legs below: an
	// UNMEASURED leg (PressureLift==nil) is KEPT — we do not bench the base leg
	// on absence of evidence — while a MEASURED anti-predictive leg
	// (*PressureLift <= 0) is DROPPED, never down-weighted (honesty doctrine).
	// The resolved-outcome record shows the fixed-weight pressure score is
	// anti-predictive at 1d/1w, so once graded it benches fleet-wide.
	if admitsLeg(c, LegPressure, c.PressureLift) {
		legs[LegPressure] = clamp01((c.PressureScore + 1) / 2)
	}
	if c.ExpectancyHitRate != nil && admitsLeg(c, LegExpectancy, c.ExpectancyLift) {
		legs[LegExpectancy] = clamp01(*c.ExpectancyHitRate)
	}
	if c.ForecastProb != nil && admitsOptIn(c, LegForecast, c.ForecastLift) {
		legs[LegForecast] = clamp01(*c.ForecastProb)
	}
	if c.SentimentScore != nil && admitsLeg(c, LegSentiment, c.SentimentLift) {
		legs[LegSentiment] = clamp01(0.5 + *c.SentimentScore*SentimentScale)
	}
	// STAGE 6 gated model legs: included ONLY with demonstrated out-of-sample
	// edge (*Lift > 0), the same rule the forecast leg obeys. An edgeless or
	// absent model leg is dropped, never down-weighted — honesty doctrine.
	if c.GBMProb != nil && admitsOptIn(c, LegGBM, c.GBMLift) {
		legs[LegGBM] = clamp01(*c.GBMProb)
	}
	if c.MeanRevProb != nil && admitsOptIn(c, LegMeanRev, c.MeanRevLift) {
		legs[LegMeanRev] = clamp01(*c.MeanRevProb)
	}
	// Cross-sectional alpha leg: identical gate. Its prob is RELATIVE (beat
	// the same-day universe median), entering the directional blend as a tilt
	// — see the Components.AlphaXProb comment for the category nuance.
	if c.AlphaXProb != nil && admitsOptIn(c, LegAlphaX, c.AlphaXLift) {
		legs[LegAlphaX] = clamp01(*c.AlphaXProb)
	}
	return legs
}

// RawProbability converts each available component to a 0..1 up-probability and
// returns the mean of the contributing components together with how many
// contributed (nUsed).
//
// Conversions:
//   - PressureScore in [-1,1] -> (score+1)/2, contributing UNLESS its measured
//     OOS lift is <=0 (PressureLift non-nil and <=0); unmeasured pressure
//     (nil) still contributes — see LegProbabilities.
//   - ExpectancyHitRate in [0,1] -> used as-is (contributes when non-nil).
//   - ForecastProb in [0,1] -> used as-is, but ONLY when ForecastLift is
//     non-nil AND *ForecastLift > 0. An edgeless or absent forecast is dropped
//     so it cannot pollute the blend.
//   - SentimentScore in [-1,1] -> 0.5 + score*SentimentScale (contributes when
//     non-nil). Absent sentiment leaves the blend exactly as before.
//
// The returned probability is clamped to [0,1]. If no component contributes
// (now POSSIBLE when the pressure leg is benched by its OOS gate and no other
// leg qualifies — an honest "no measured signal" state), prob=0.5 and nUsed=0,
// a neutral, information-free prior.
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

// AdmittedProbability is WeightedProbability with an explicit refusal. It
// returns ok=false when NO leg was admitted, so a caller emits "no
// forecast" instead of a 0.5 that reads on a wire exactly like a real
// coin-flip call. When ok is true the returned values are identical to
// WeightedProbability's, so the two can never disagree.
func AdmittedProbability(c Components, weights map[string]float64) (prob float64, nUsed int, ok bool) {
	prob, nUsed = WeightedProbability(c, weights)
	return prob, nUsed, nUsed > 0
}

// Pair is one graded prediction: a raw predicted up-probability (Pred, in
// [0,1]) paired with the realized outcome (Actual, 1 for an up move, 0
// otherwise). A history of Pairs is the raw material for every calibration
// grade in this package.
type Pair struct {
	Pred   float64 // predicted P(up), [0,1]
	Actual float64 // realized outcome in {0,1}
	// Ts is the prediction's unix timestamp. Its trading day is the independence
	// unit: the predictor runs every 10 minutes against daily labels, so a
	// dozen pairs can share one symbol-day and a thousand symbols share one
	// market move. Grading helpers (BrierScore, CalibrationCurve) ignore it;
	// anything that FITS a map from these pairs must count distinct days, not
	// pairs — see MinCalibrationDays. Ts==0 means unstamped, which counts as
	// a single day, so a caller that forgets to stamp is refused rather than
	// silently trusted.
	Ts int64
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

// BrierSkill returns the Brier SKILL score of the history against the only
// honest benchmark — the constant base-rate forecast — together with that base
// rate. skill = 1 - Brier/(base*(1-base)). Positive means the model beats
// always forecasting the observed up-rate; NEGATIVE means it is worse than a
// constant, which is a verdict, not a rounding detail.
//
// A bare Brier score is not interpretable and must never be published alone:
// the live 1d record scores 0.302, which sounds small until the 56.0% base rate
// puts the constant forecast at 0.246 — the model is 23% WORSE than a constant
// (skill -0.226). Omitting the skill score while publishing the raw Brier reads
// as selective, so every surface that shows one must show the other.
//
// ok=false — WITHHOLD, never report 0 — when there is no history or when every
// outcome went the same way (a degenerate zero-variance reference against which
// no skill is defined).
func BrierSkill(pairs []Pair) (skill, baseRate float64, ok bool) {
	if len(pairs) == 0 {
		return 0, 0, false
	}
	var sumActual float64
	for _, p := range pairs {
		sumActual += p.Actual
	}
	baseRate = sumActual / float64(len(pairs))
	ref := baseRate * (1 - baseRate) // Brier of the constant base-rate forecast
	if ref <= 0 {
		return 0, baseRate, false
	}
	return 1 - BrierScore(pairs)/ref, baseRate, true
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
//
// DAY FLOOR — enforced by the CALLER, not here. Calibrate takes bare pairs and
// so cannot count days; pipeline.globalCalibration does it instead, refusing the
// fit below calibrationMinDays using the day column the store now returns.
//
// The 2026-07-26 note this replaces described a gap that has since been closed,
// and is corrected rather than deleted because it argued against enforcing the
// floor — advice that is now wrong. It said ResolvedRawPredictionPairs returned
// "the newest calibrationPairLimit=3000 ROWS with no timestamp", spanning 5
// distinct days for 1d and 1 for 1w, and that reaching the floor "would require
// deduping to one pair per (symbol, trading-day) across the full history — a
// change to the store query". That change was made: the query now dedupes with
// ROW_NUMBER() OVER (PARTITION BY symbol_id, settle_day(...)), returns days
// alongside the pairs, and the cap is 40000.
//
// Measured live 2026-08-13, the deduped window is nowhere near the cap and
// clears both floors: 1d = 11,833 pairs over 38 distinct days, 1w = 11,021 over
// 34, against calibrationMinDays=10 at the caller and MinCalibrationDays=20
// here. The fleet map is NOT fitted on ~5 clustered days.
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
	// ASSERT on write: a map that is not monotone is not a calibration, it is a
	// scrambler — a more bullish input publishing a lower probability breaks
	// every ranking keyed on the result. poolAdjacentViolators guarantees this
	// by construction; if a future change breaks that guarantee the honest
	// output is "no correction applied", never a silently inverted map.
	if err := ValidateKnots(kx, ky); err != nil {
		return identity, false
	}

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
	// Base rate = overall weighted mean of realized outcomes — the empirical
	// prior each block is shrunk toward (below). For 1-day up/down this sits
	// near a coin flip; a market with real drift gets its own honest prior.
	var sumW int
	var sumWM float64
	for _, lv := range levels {
		sumW += lv.weight
		sumWM += lv.mean * float64(lv.weight)
	}
	base := 0.5
	if sumW > 0 {
		base = sumWM / float64(sumW)
	}

	// Pass 1: PAV over the raw per-level realized frequencies. Adjacent levels
	// that violate monotonicity (a higher prediction with a lower realized
	// frequency) pool into one block carrying their weighted mean.
	singles := make([]calBlock, 0, len(levels))
	for _, lv := range levels {
		singles = append(singles, calBlock{mean: lv.mean, weight: lv.weight, span: 1})
	}
	blocks := poolBlocks(singles)

	// Expand each block's pooled mean back over the levels it covers, keeping
	// one knot per distinct prediction value. Each knot's fitted frequency is
	// bounded by the RULE OF SUCCESSION over the block's OWN pooled pair
	// weight: a block backed by w pairs can never claim a frequency outside
	// [1/(w+2), (w+1)/(w+2)]. This is the honesty bound that matters — a lone
	// thin-tail pair whose one outcome went "up" pools to w=1 and is capped at
	// 2/3, instead of dragging the whole map to a displayed P(up)=100.0%.
	// (A bound keyed to the TOTAL pair count is useless here: at n=3000 it is
	// 1/3002 ≈ 0.0003, which still renders as 100.0%.)
	for i, b := range blocks {
		// SHRINK the fitted frequency toward the base rate by sample size
		// (empirical Bayes): (w·mean + k·base)/(w + k). Thin blocks collapse to
		// the base rate; data-rich blocks keep their fit. This is what stops a
		// short per-symbol up-streak from calibrating to an overconfident 0.85+.
		m := (float64(b.weight)*b.mean + calibrationPriorStrength*base) / (float64(b.weight) + calibrationPriorStrength)
		// Rule-of-succession hard cap on top (a block of w pairs can never claim
		// a frequency outside [1/(w+2),(w+1)/(w+2)]) — belt to the shrinkage braces.
		lo := 1.0 / float64(b.weight+2)
		blocks[i].mean = math.Min(1.0-lo, math.Max(lo, m))
	}

	// RE-PROJECT onto the monotone cone. Both transforms above are weight-
	// DEPENDENT — shrinkage pulls a thin block toward the base rate hard and a
	// heavy block barely at all, and the succession bound tightens as the block
	// thins — so applying them to blocks of DIFFERENT weight reorders the values
	// PAV had just ordered. That is C3: 495 of 928 live maps published a lower
	// probability for a more bullish input (symbol 12/1d: raw 0.3611 -> 63.8%,
	// raw 0.3652 -> 41.8%), which scrambles every ranking keyed on cal_prob.
	// A second weighted PAV pass over the transformed block values is the
	// weighted-L2 projection back onto the monotone cone, so the output is
	// non-decreasing BY CONSTRUCTION rather than by assumption. It preserves the
	// honesty bounds: pooling only ever produces a weighted mean of values that
	// already sit inside their own blocks' bounds, and the pooled block's bound
	// is the looser one (more pairs).
	blocks = poolBlocks(blocks)

	kx = make([]float64, 0, len(levels))
	ky = make([]float64, 0, len(levels))
	li := 0
	for _, b := range blocks {
		for k := 0; k < b.span; k++ {
			kx = append(kx, levels[li].x)
			ky = append(ky, b.mean)
			li++
		}
	}
	return kx, ky
}

// calBlock is one pooled isotonic block: its fitted frequency, the pair weight
// backing it, and how many input levels it covers.
type calBlock struct {
	mean   float64
	weight int // total pair count
	span   int // number of levels covered
}

// poolBlocks runs weighted pool-adjacent-violators over already-formed blocks,
// merging any adjacent pair whose values decrease. Used to re-establish
// monotonicity after the weight-dependent shrinkage/bounding transforms, which
// are order-preserving only between blocks of EQUAL weight.
func poolBlocks(in []calBlock) []calBlock {
	out := make([]calBlock, 0, len(in))
	for _, b := range in {
		for len(out) > 0 {
			prev := out[len(out)-1]
			if prev.mean <= b.mean {
				break
			}
			totalW := prev.weight + b.weight
			b.mean = (prev.mean*float64(prev.weight) + b.mean*float64(b.weight)) / float64(totalW)
			b.weight = totalW
			b.span += prev.span
			out = out[:len(out)-1]
		}
		out = append(out, b)
	}
	return out
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
	// DISTINCT-DAY FLOOR (2026-07-26 review, H5). The pair floor counts rows,
	// and the predictor writes ~12 rows per symbol-day, so 30 pairs is about
	// three market moves — SUPERSEDED-SNAPSHOT, the dated 2026-07-26 measurement
	// that sized this floor rather than the current record: 158,204 resolved
	// rows were 13,058
	// symbol-days, with one case of 153 rows inside a single day. A map fitted
	// on three days encodes those three days' moves and then rewrites every
	// published probability for the symbol. Refusing is the honest failure: the
	// caller uses the identity map and ships an uncorrected probability.
	if DistinctPairDays(pairs) < MinCalibrationDays {
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
	// ASSERT before anything can PERSIST these knots. This is the boundary the
	// 2026-07-26 review found unguarded: 495 of 928 stored per-symbol maps were
	// non-monotone and nothing refused them, so they shipped to every ranking
	// keyed on cal_prob. Refusing to fit is the honest failure — the caller then
	// uses the identity map rather than an inverted one.
	if err := ValidateKnots(kx, ky); err != nil {
		return nil, nil, false
	}
	return kx, ky, true
}

// MapFromKnots rebuilds a recalibration map from persisted knots, REFUSING any
// map ValidateKnots rejects — an invalid or inverted persisted map yields the
// identity, so the symbol ships an honestly uncorrected probability instead of
// a scrambled one. See MapFromKnotsChecked for the reason a caller wants; this
// form is for callers that only need the safe map. The rebuilt map is the SAME
// clamped linear interpolation Calibrate returns.
func MapFromKnots(kx, ky []float64) func(float64) float64 {
	fn, err := MapFromKnotsChecked(kx, ky)
	if err != nil {
		return identity
	}
	return fn
}

// MapFromKnotsChecked rebuilds a recalibration map from persisted knots and
// reports WHY a map was refused, so the caller can fall back to a better tier
// (e.g. the global calibration) and count the refusal rather than silently
// degrade to the identity.
//
// This is the read-side half of the C3 fix. 495 of 928 live per-symbol maps
// were persisted non-monotone before poolAdjacentViolators was corrected;
// those rows are only rewritten when the hourly per-symbol learner refits
// them. Until then, serving them inverts the published probability against the
// raw input — so they are refused on read, immediately, rather than served
// until a refit happens to land.
func MapFromKnotsChecked(kx, ky []float64) (func(float64) float64, error) {
	if err := ValidateKnots(kx, ky); err != nil {
		return nil, err
	}
	// Defensive copy so a caller mutating the slices can't change the closure.
	xs := append([]float64(nil), kx...)
	ys := append([]float64(nil), ky...)
	return func(v float64) float64 {
		m := interpolate(xs, ys, clamp01(v))
		return math.Min(persistedCalHi, math.Max(persistedCalLo, m))
	}, nil
}
