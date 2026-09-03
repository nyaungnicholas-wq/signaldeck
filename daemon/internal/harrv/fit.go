package harrv

import "math"

// The HAR specification (Corsi 2009), in logs:
//
//	ln RV_{t+1} = b0 + bd*ln RV_t + bw*ln RVbar_{t-4..t} + bm*ln RVbar_{t-21..t} + e
//
// Daily, weekly and monthly components. There is NO tunable hyperparameter
// here and no grid was searched: 1/5/22 are the canonical cascade, 500 is a
// plain two-year training floor, and the refit cadence is a compute decision
// that cannot move a forecast's direction. That matters more than it sounds.
// This repository's settled verdict is that config search over its own signal
// "manufactures false positives", and every previous predictor here died to a
// selection effect. With no search there is nothing to charge multiplicity
// for, and probability-of-backtest-overfitting is zero by construction rather
// than by argument.
const (
	// LagW and LagM are the weekly and monthly aggregation windows.
	LagW = 5
	LagM = 22

	// MinTrainW / MinTrainM are how many real observations each aggregate needs
	// before it is used. A mean over 1 of 22 days is not a monthly average.
	MinTrainW = 4
	MinTrainM = 18

	// MinTrain is the number of complete training rows before any coefficient
	// is trusted. Roughly two years of sessions.
	MinTrain = 500

	// MinHistory is the shortest series that can produce a fit at all:
	// the monthly warm-up plus MinTrain plus slack.
	MinHistory = 530
)

// Fit is one symbol's fitted HAR coefficients, taken at a point in time.
//
// ResidVar is carried because the model is fitted in LOGS and published in
// LEVELS, and the retransform needs it. See PredictAt.
type Fit struct {
	Beta0, BetaD, BetaW, BetaM float64
	ResidVar                   float64 // s^2 of the TRAINING residuals only
	N                          int     // training rows behind these coefficients
	FitIdx                     int     // the bar index the fit was taken at
}

// features builds the HAR regressor row for index i, reading indices <= i only.
// ok=false when any component is unavailable, which is how a hole in the RV
// series propagates into an honest "no forecast" instead of a guess.
func features(rv []float64, i int) ([4]float64, bool) {
	var f [4]float64
	if i < 0 || i >= len(rv) || math.IsNaN(rv[i]) || rv[i] <= 0 {
		return f, false
	}
	w, okW := RollingMean(rv, i, LagW, MinTrainW)
	m, okM := RollingMean(rv, i, LagM, MinTrainM)
	if !okW || !okM || w <= 0 || m <= 0 {
		return f, false
	}
	f[0] = 1
	f[1] = math.Log(rv[i])
	f[2] = math.Log(w)
	f[3] = math.Log(m)
	return f, true
}

// solve4 solves a 4x4 system by Gauss-Jordan with partial pivoting.
// Deliberately hand-rolled: it keeps this package dependency-free and pure,
// and a 4x4 normal-equations solve is not where a linear-algebra library earns
// its keep.
func solve4(a [4][4]float64, b [4]float64) ([4]float64, bool) {
	var x [4]float64
	for col := 0; col < 4; col++ {
		p := col
		for r := col + 1; r < 4; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[p][col]) {
				p = r
			}
		}
		if math.Abs(a[p][col]) < 1e-12 {
			return x, false // singular: refuse rather than return noise
		}
		a[col], a[p] = a[p], a[col]
		b[col], b[p] = b[p], b[col]
		for r := 0; r < 4; r++ {
			if r == col {
				continue
			}
			f := a[r][col] / a[col][col]
			for c := col; c < 4; c++ {
				a[r][c] -= f * a[col][c]
			}
			b[r] -= f * b[col]
		}
	}
	for i := 0; i < 4; i++ {
		x[i] = b[i] / a[i][i]
	}
	return x, true
}

// FitAt fits the HAR coefficients using ONLY information available at bar t.
//
// The training rows are (features at s, target ln RV_{s+1}) for s < t, so the
// newest target used is ln RV_t. Nothing at index > t is read, by construction
// rather than by convention -- which is what TestTruncationInvariance pins.
func FitAt(rv []float64, t int) (Fit, bool) {
	if t < MinHistory || t >= len(rv) {
		return Fit{}, false
	}
	var xtx [4][4]float64
	var xty [4]float64
	type row struct {
		f [4]float64
		y float64
	}
	rows := make([]row, 0, t)
	for s := LagM; s < t; s++ {
		f, ok := features(rv, s)
		if !ok {
			continue
		}
		nxt := rv[s+1]
		if math.IsNaN(nxt) || nxt <= 0 {
			continue
		}
		y := math.Log(nxt)
		rows = append(rows, row{f, y})
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				xtx[i][j] += f[i] * f[j]
			}
			xty[i] += f[i] * y
		}
	}
	if len(rows) < MinTrain {
		return Fit{}, false
	}
	beta, ok := solve4(xtx, xty)
	if !ok {
		return Fit{}, false
	}
	// Residual variance from the TRAINING rows only. Using the evaluation
	// window here would leak the future into the level forecast.
	var ss float64
	for _, r := range rows {
		yhat := beta[0]*r.f[0] + beta[1]*r.f[1] + beta[2]*r.f[2] + beta[3]*r.f[3]
		d := r.y - yhat
		ss += d * d
	}
	dof := float64(len(rows) - 4)
	if dof <= 0 {
		return Fit{}, false
	}
	return Fit{
		Beta0: beta[0], BetaD: beta[1], BetaW: beta[2], BetaM: beta[3],
		ResidVar: ss / dof,
		N:        len(rows),
		FitIdx:   t,
	}, true
}

// PredictAt returns the one-step-ahead VARIANCE forecast for bar t+1, in
// levels, reading indices <= t only.
//
// THE RETRANSFORM IS NOT OPTIONAL. The model is fitted on ln RV, so
// exp(yhat) estimates the MEDIAN of RV_{t+1}, not its mean. Publishing that as
// a level biases every forecast low by roughly 10-20% at typical residual
// variance. It costs nothing in a log-scale loss, which is exactly why it is
// easy to miss, but it is punished twice over downstream:
//
//   - QLIKE is asymmetric and penalises under-forecasting hard, so the model
//     would look worse than it is against its own nulls;
//   - a VaR built from a variance biased low is too tight, so it breaches more
//     often than its nominal rate and fails Kupiec for an arithmetic reason
//     that has nothing to do with the model.
//
// exp(yhat + s^2/2) is the mean of the lognormal implied by the fit.
func PredictAt(rv []float64, t int, f Fit) (float64, bool) {
	x, ok := features(rv, t)
	if !ok {
		return 0, false
	}
	yhat := f.Beta0*x[0] + f.BetaD*x[1] + f.BetaW*x[2] + f.BetaM*x[3]
	v := math.Exp(yhat + f.ResidVar/2)
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, false
	}
	return v, true
}
