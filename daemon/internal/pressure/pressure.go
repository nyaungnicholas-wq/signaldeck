// Package pressure is SignalDeck's OOS gate for the PRESSURE leg — the
// platform's oldest, historically always-on base leg (a fixed-weight composite
// of trend/momentum/rsi/macd/vwap/rvol/imbalance in [-1,+1]).
//
// # Why it exists
//
// Every model leg (logit forecast, GBM, mean-reversion, alphax) must EARN its
// place: it is graded walk-forward, out-of-sample, and shipped into the ensemble
// only when its graded lift is positive. The pressure leg alone bypassed that
// bar — it was blended unconditionally. The resolved-outcome record shows the
// fixed-weight pressure score's directional call (pressure_score > 0 => up) is
// anti-predictive at the 1d and 1w horizons (accuracy below the naive
// majority-class baseline). This package holds the pressure leg to the identical
// honesty gate the model legs pass.
//
// # Honest grading, no lookahead, no fitting
//
// The pressure score has NO parameters fit from data, so — exactly like the
// mean-reversion leg — every fold's "prediction" is deterministic. The
// expanding-window walk-forward structure is retained purely so the grade is
// computed on strictly-later, out-of-sample blocks and stays directly
// comparable to the other legs' grades. The samples are assembled by the caller
// from the feature store (each row's stored pressure_score + its realized up),
// so the no-lookahead guarantee is inherited: a row is a labeled example only
// after its own outcome resolved.
//
// The leg is graded DIRECT — no inversion, no cost. It is a directional leg like
// the logit forecast (the contrarian, cost-net treatment lives in package
// meanrev). Grade shape is identical to forecast.Grade / meanrev.Grade so the
// same UI and lift>0 gate apply unchanged.
package pressure

import (
	"errors"
	"math"
	"sort"
)

// Errors returned by this package.
var (
	ErrInsufficientData = errors.New("pressure: insufficient labeled data")
	ErrBadParams        = errors.New("pressure: invalid parameters")
)

const (
	minSamples = 60
	minPerFold = 12
)

// Sample is one labeled row: the composite pressure score in [-1,+1] at
// prediction time and the realized direction (Up). Assembled by the caller from
// the feature store's stored pressure_score.
type Sample struct {
	Ts       int64
	Pressure float64 // composite pressure score, [-1,+1]
	Up       int     // realized 1/0
}

// Grade is the out-of-sample report card — identical shape to forecast.Grade /
// meanrev.Grade so the same UI + honesty gate apply.
type Grade struct {
	N          int     `json:"n"`
	Accuracy   float64 `json:"accuracy"`   // fraction of correct directional calls
	BrierScore float64 `json:"brierScore"` // mean (pPress - y)^2
	AUC        float64 `json:"auc"`        // ROC AUC of pPress vs realized direction
	BaseRate   float64 `json:"baseRate"`   // naive majority-class floor
	Lift       float64 `json:"lift"`       // Accuracy - BaseRate; <=0 => bench the leg
}

// prob maps a pressure score in [-1,+1] to its up-probability leg in [0,1], the
// identical transform ensemble.LegProbabilities applies to the live leg.
func prob(pressureScore float64) float64 { return clamp01((pressureScore + 1) / 2) }

// Evaluate grades the pressure leg out-of-sample using the same expanding-window
// walk-forward as the other legs. Because the signal is a fixed transform (no
// parameters fit), every fold's prediction is deterministic; the fold structure
// only guarantees strictly-later, out-of-sample scoring.
//
// Errors: ErrBadParams for folds<2; ErrInsufficientData when there are too few
// rows for a usable split.
func Evaluate(samples []Sample, folds int) (Grade, error) {
	if folds < 2 {
		return Grade{}, ErrBadParams
	}
	if len(samples) < minSamples || len(samples) < folds*minPerFold {
		return Grade{}, ErrInsufficientData
	}
	// Defensive time ordering.
	ordered := samples
	if !ascendingTs(samples) {
		ordered = make([]Sample, len(samples))
		copy(ordered, samples)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Ts < ordered[j].Ts })
	}

	n := len(ordered)
	var preds, targets []float64
	for f := 1; f < folds; f++ {
		trainEnd := n * f / folds
		testEnd := n * (f + 1) / folds
		if trainEnd < minPerFold || testEnd <= trainEnd {
			continue
		}
		// No parameters are fit; the "train" slice exists only to enforce that we
		// grade strictly-later blocks (identical fold geometry to the other legs).
		for _, s := range ordered[trainEnd:testEnd] {
			preds = append(preds, prob(s.Pressure))
			targets = append(targets, float64(s.Up))
		}
	}
	if len(preds) == 0 {
		return Grade{}, ErrInsufficientData
	}
	return gradeFrom(preds, targets), nil
}

// gradeFrom builds the Grade against the SAME benchmark the momentum legs use:
// base rate = the win-rate of the best no-skill CONSTANT directional call
// (always-up OR always-down), so Lift = Accuracy - BaseRate is the edge over the
// strongest constant. A leg that merely reproduces the prevailing drift shows
// ~zero lift; an anti-predictive leg shows a negative one.
func gradeFrom(preds, targets []float64) Grade {
	n := len(preds)
	signalWins, ups := 0, 0
	for i := range preds {
		call := 0
		if preds[i] > 0.5 {
			call = 1
		} else if preds[i] < 0.5 {
			call = -1
		}
		y := -1
		if targets[i] >= 0.5 {
			y = 1
			ups++
		}
		if call != 0 && call == y {
			signalWins++
		}
	}
	acc := float64(signalWins) / float64(n)
	baseRate := math.Max(float64(ups), float64(n-ups)) / float64(n)

	brier := 0.0
	for i := range preds {
		d := preds[i] - targets[i]
		brier += d * d
	}
	return Grade{
		N:          n,
		Accuracy:   acc,
		BrierScore: brier / float64(n),
		AUC:        aucRank(preds, targets),
		BaseRate:   baseRate,
		Lift:       acc - baseRate,
	}
}

// Run walk-forward grades the pressure leg, then returns the latest leg
// probability for a fresh pressure score. ok=false whenever grading has
// insufficient data — so a caller never surfaces an ungraded lift.
func Run(samples []Sample, latestPressure float64, folds int) (probOut float64, grade Grade, ok bool) {
	g, err := Evaluate(samples, folds)
	if err != nil {
		return 0, Grade{}, false
	}
	return prob(latestPressure), g, true
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
