package harrv

import "math"

// The HAR specification (Corsi 2009), in logs:
//
//	ln TARGET_{t+1..t+h} = b0 + bd*ln RV_t + bw*ln RVbar_{t-4..t} + bm*ln RVbar_{t-21..t} + e
//
// where TARGET is the mean realised variance over the next h sessions (see
// Horizon). At h = 1 that is simply ln RV_{t+1}.
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

// Horizon is how many sessions ahead the target averages over.
//
// H = 1 targets ln RV_{t+1}: a single day. Consecutive forecasts then share no
// forward sessions, so the observations are NON-OVERLAPPING by construction --
// which matters here, because overlapping windows are what inflated n in every
// previous predictor this repository has retired.
//
// H > 1 targets the MEAN variance over the next H sessions. That is a much
// less noisy estimand -- a single Garman-Klass estimate is mostly proxy noise --
// but consecutive forecasts now share H-1 forward days and the errors are
// autocorrelated by construction. Inference must use a HAC lag of at least H,
// which DieboldMariano's caller is responsible for supplying.
//
// BOTH are registered. Picking whichever scores better after the fact is the
// specification search that this platform's own settled verdicts say
// "manufactures false positives".
type Horizon int

// TargetAt is the realised value the forecast made at bar t is graded against:
// the mean RV over sessions t+1 .. t+h. It reads indices > t on purpose --
// that is the OUTCOME, not a regressor -- so it must never be called from a
// fitting path with a t at or beyond the evaluation point.
//
// Requires every session in the window to be estimable. Averaging over the
// two days that happened to be measurable inside a five-day window would be a
// silently different target on exactly the symbols with patchy data.
func TargetAt(rv []float64, t int, h Horizon) (float64, bool) {
	if h < 1 || t < 0 || t+int(h) >= len(rv) {
		return 0, false
	}
	sum := 0.0
	for i := t + 1; i <= t+int(h); i++ {
		v := rv[i]
		if math.IsNaN(v) || v <= 0 {
			return 0, false
		}
		sum += v
	}
	return sum / float64(h), true
}

// Fit is one symbol's fitted HAR coefficients, taken at a point in time.
//
// ResidVar is carried because the model is fitted in LOGS and published in
// LEVELS, and the retransform needs it. See PredictAt.
type Fit struct {
	Beta0, BetaD, BetaW, BetaM float64
	ResidVar                   float64 // s^2 of the TRAINING residuals only
	N                          int     // training rows behind these coefficients
	FitIdx                     int     // the bar index the fit was taken at
	H                          Horizon // the horizon these coefficients target
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
// The training rows are (features at s, ln TARGET over s+1..s+h) for every s
// with s+h <= t, so the newest outcome used ends exactly at t. Nothing at
// index > t is read, by construction rather than by convention -- which is
// what TestFitAndPredictDoNotReadTheFuture pins by rigging the future and
// requiring the fit not to move by a single bit.
func FitAt(rv []float64, t int, h Horizon) (Fit, bool) {
	if h < 1 || t < MinHistory || t >= len(rv) {
		return Fit{}, false
	}
	var xtx [4][4]float64
	var xty [4]float64
	type row struct {
		f [4]float64
		y float64
	}
	rows := make([]row, 0, t)
	// s+h <= t keeps every training TARGET at or before t. With h > 1 the last
	// usable feature row sits h-1 days further back, because a target whose
	// window runs past t would be an outcome the fit could not have seen.
	for s := LagM; s+int(h) <= t; s++ {
		f, ok := features(rv, s)
		if !ok {
			continue
		}
		tgt, ok := TargetAt(rv, s, h)
		if !ok {
			continue
		}
		y := math.Log(tgt)
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
		H:        h,
	}, true
}

// PredictAt returns the VARIANCE forecast in levels, reading indices <= t only.
//
// What it forecasts is f.H sessions of mean variance starting at t+1, matching
// the target FitAt was trained on. The coefficients and the estimand travel
// together in Fit so they cannot be paired up wrongly by a caller.
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
