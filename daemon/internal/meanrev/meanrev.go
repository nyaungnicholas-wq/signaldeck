// Package meanrev is SignalDeck's gated MEAN-REVERSION forecast leg — the
// deliberate counterweight to the momentum-leaning pressure/forecast legs.
//
// # Why it exists
//
// The resolved-outcome data shows a mild per-symbol mean-reversion tilt: an
// extreme pressure score (very bullish OR very bearish) is slightly more likely
// to be followed by a pullback than a continuation. Momentum legs lean the wrong
// way in exactly that regime. Rather than muddy the momentum model, we add a
// SEPARATE leg that inverts the directional lean, and — critically — we make it
// EARN its place: it is graded walk-forward, out-of-sample, NET OF COST, and the
// caller only ships it into the ensemble when that graded edge is positive.
//
// # What it does
//
// For each labeled feature-store row we already have the momentum blend's raw
// P(up) (the "pred_raw" the ensemble produced). The mean-reversion signal is
// simply its reflection about 0.5:
//
//	pMR = 0.5 + strength * (0.5 - pRaw)
//
// strength in (0,1] scales how hard we lean against the momentum call (1.0 =
// full inversion). A pRaw of 0.80 (strong up lean) becomes a pMR of 0.20 (lean
// down) at full strength. The leg makes a DIRECTIONAL call only when the
// momentum lean was itself directional (pRaw != 0.5).
//
// # Honest grading, net of cost (no lookahead)
//
// The grade is the SAME expanding-window walk-forward as forecast/gbm, but with
// two honesty tightenings suited to a contrarian trade:
//
//   - COST: a mean-reversion trade fights the prevailing move, so it is graded
//     net of a round-trip cost. We convert each inverted probability into an
//     expected costed edge and only count it as "correct" when the realized move
//     covers the cost. Concretely, the Grade's accuracy/lift are computed on
//     COST-ADJUSTED outcomes: a directional call is scored a win only if the
//     realized forward return moved in the predicted direction by MORE than the
//     per-trade cost. This is stricter than raw direction and is the honest bar a
//     contrarian signal must clear.
//   - GATE: exposed via CostedGrade so the caller applies the identical lift>0
//     gate the momentum forecast passes through. An edgeless or ungraded
//     mean-reversion leg is DROPPED, never down-weighted.
//
// The samples are assembled by the caller from the feature store (each row's
// stored pRaw + its realized up/return), so the no-lookahead guarantee is
// inherited: a row is a training example only after its own outcome resolved.
package meanrev

import (
	"errors"
	"math"
	"sort"
)

// Errors returned by this package.
var (
	ErrInsufficientData = errors.New("meanrev: insufficient labeled data")
	ErrBadParams        = errors.New("meanrev: invalid parameters")
)

// DefaultStrength is how hard the leg leans against the momentum call. 1.0 is a
// full reflection about 0.5; kept at 1.0 because a half-hearted contrarian
// signal is just noise near 0.5 and would rarely clear the costed gate.
const DefaultStrength = 1.0

// Sample is one labeled row: the momentum blend's raw P(up) at prediction time,
// the realized direction (Up), the realized forward return (for cost-adjusted
// grading), and Ts for temporal ordering. Assembled by the caller from the
// feature store.
type Sample struct {
	Ts        int64
	RawProb   float64 // momentum blend's raw P(up) at prediction time
	Up        int     // realized 1/0
	FwdReturn float64 // realized forward return (fraction, e.g. 0.012 = +1.2%)
}

// Grade is the out-of-sample, cost-adjusted report card — identical shape to
// forecast.Grade / gbm.Grade so the same UI + honesty gate apply.
type Grade struct {
	N          int     `json:"n"`
	Accuracy   float64 `json:"accuracy"`   // fraction of COST-COVERED correct calls
	BrierScore float64 `json:"brierScore"` // mean (pMR - y)^2 on raw direction
	AUC        float64 `json:"auc"`        // ROC AUC of pMR vs raw direction
	BaseRate   float64 `json:"baseRate"`   // majority-class floor of the costed target
	Lift       float64 `json:"lift"`       // Accuracy - BaseRate (net of cost); <=0 => drop
}

// Invert reflects a momentum probability about 0.5 by the given strength,
// producing the mean-reversion leg's P(up). A neutral momentum call (0.5) stays
// neutral. Result clamped to [0,1].
func Invert(rawProb, strength float64) float64 {
	if strength <= 0 {
		strength = DefaultStrength
	}
	return clamp01(0.5 + strength*(0.5-rawProb))
}

// PredictLatest returns the mean-reversion P(up) for a fresh momentum raw prob.
// It is a pure reflection — no fitting — so there is nothing to leak; the honest
// question is only whether the GRADE (Evaluate) shows the reflection has edge.
func PredictLatest(rawProb, strength float64) float64 {
	return Invert(rawProb, strength)
}

// Evaluate grades the mean-reversion leg out-of-sample, net of a round-trip
// cost, using the same expanding-window walk-forward as the other legs. Because
// the signal is a fixed reflection (no parameters fit from data), every fold's
// "prediction" is deterministic; the walk-forward structure is retained so the
// grade is computed on strictly time-ordered, out-of-sample blocks and stays
// directly comparable to the momentum forecast's grade. Cost enters the TARGET:
// a directional call counts as correct only when the realized move exceeded the
// cost in the predicted direction.
//
// Errors: ErrBadParams for folds<2 or strength<0; ErrInsufficientData when there
// are too few rows for a usable split.
func Evaluate(samples []Sample, folds int, strength, cost float64) (Grade, error) {
	if folds < 2 || strength < 0 || cost < 0 {
		return Grade{}, ErrBadParams
	}
	if len(samples) < minSamples || len(samples) < folds*minPerFold {
		return Grade{}, ErrInsufficientData
	}
	if strength == 0 {
		strength = DefaultStrength
	}
	// Defensive time ordering.
	ordered := samples
	if !ascendingTs(samples) {
		ordered = make([]Sample, len(samples))
		copy(ordered, samples)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Ts < ordered[j].Ts })
	}

	n := len(ordered)
	var preds, dirTargets, costLabels []float64
	for f := 1; f < folds; f++ {
		trainEnd := n * f / folds
		testEnd := n * (f + 1) / folds
		if trainEnd < minPerFold || testEnd <= trainEnd {
			continue
		}
		// No parameters are fit; the "train" slice exists only to enforce that we
		// grade strictly-later blocks (identical fold geometry to the other legs).
		for _, s := range ordered[trainEnd:testEnd] {
			pMR := Invert(s.RawProb, strength)
			preds = append(preds, pMR)
			// Raw-direction target (for Brier/AUC diagnostics).
			dirTargets = append(dirTargets, float64(s.Up))
			// COST-NET directional label of the realized move: +1 if the forward
			// return cleared +cost, -1 if it broke below -cost, 0 if the move was
			// too small to trade profitably either way. This is the ground truth a
			// costed directional call is graded against.
			costLabels = append(costLabels, costLabel(s.FwdReturn, cost))
		}
	}
	if len(preds) == 0 {
		return Grade{}, ErrInsufficientData
	}
	return gradeFrom(preds, dirTargets, costLabels), nil
}

// costLabel returns the cost-net directional label of a realized move: +1 when
// the forward return cleared +cost (a profitable long), -1 when it broke below
// -cost (a profitable short), 0 when the move was smaller than cost in both
// directions (untradeable — cost would eat any position).
func costLabel(fwdReturn, cost float64) float64 {
	switch {
	case fwdReturn > cost:
		return 1
	case fwdReturn < -cost:
		return -1
	default:
		return 0
	}
}

const (
	minSamples = 60
	minPerFold = 12
)

// gradeFrom builds the Grade for the mean-reversion leg, measured NET OF COST and
// against the SAME honest benchmark the momentum legs use: base rate is the
// win-rate of the best no-skill CONSTANT directional strategy (always-long OR
// always-short) on the cost-net labels, so Lift = Accuracy - BaseRate is the
// edge over the strongest constant call. A signal that merely reproduces the
// dominant drift shows ~zero lift; only genuine reversion edge lifts above it.
//
//   - A sample whose cost-net label is 0 (move too small to trade) is a scored
//     opportunity the signal SHOULD have skipped: the signal takes a position
//     (pMR != 0.5) but no tradeable move existed, so it counts as a loss for both
//     the signal AND the constant benchmarks — it cannot inflate lift.
//
// Brier/AUC use the raw realized direction so they stay comparable to the other
// legs' probability calibration diagnostics.
func gradeFrom(preds, dirTargets, costLabels []float64) Grade {
	n := len(preds)

	// Signal accuracy: its directional call matches a tradeable cost-net move.
	signalWins := 0
	// Constant-benchmark wins: always-long wins on every +1 label; always-short
	// wins on every -1 label. The base rate is the better of the two.
	longWins, shortWins := 0, 0
	for i := range preds {
		call := 0
		if preds[i] > 0.5 {
			call = 1
		} else if preds[i] < 0.5 {
			call = -1
		}
		lbl := int(costLabels[i])
		if call != 0 && call == lbl {
			signalWins++
		}
		if lbl == 1 {
			longWins++
		} else if lbl == -1 {
			shortWins++
		}
	}
	acc := float64(signalWins) / float64(n)
	baseRate := math.Max(float64(longWins), float64(shortWins)) / float64(n)

	// Brier + AUC on raw direction (diagnostic, not the gate).
	brier := 0.0
	for i := range preds {
		d := preds[i] - dirTargets[i]
		brier += d * d
	}
	return Grade{
		N:          n,
		Accuracy:   acc,
		BrierScore: brier / float64(n),
		AUC:        aucRank(preds, dirTargets),
		BaseRate:   baseRate,
		Lift:       acc - baseRate,
	}
}

// Run walk-forward grades the mean-reversion leg net of cost, then returns the
// latest reflected probability for a fresh momentum raw prob. ok=false whenever
// grading has insufficient data — so a caller never surfaces an ungraded leg.
func Run(samples []Sample, latestRaw float64, folds int, strength, cost float64) (prob float64, grade Grade, ok bool) {
	g, err := Evaluate(samples, folds, strength, cost)
	if err != nil {
		return 0, Grade{}, false
	}
	if strength == 0 {
		strength = DefaultStrength
	}
	return PredictLatest(latestRaw, strength), g, true
}

// ── shared helpers (local so the package is dependency-free) ──

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

func ascendingTs(samples []Sample) bool {
	for i := 1; i < len(samples); i++ {
		if samples[i].Ts < samples[i-1].Ts {
			return false
		}
	}
	return true
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
