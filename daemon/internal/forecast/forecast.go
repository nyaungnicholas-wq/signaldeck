// Package forecast is SignalDeck's honest, backtested directional forecast
// model — the "real forecast model" that goes beyond mechanical expectancy.
//
// It fits a from-scratch logistic-regression classifier (no ML dependencies,
// pure stdlib math) that predicts P(next-horizon return > 0) from a small,
// interpretable feature set. Two design commitments make it honest:
//
//   - NO LOOKAHEAD. Every feature at bar i is computed using ONLY bars[..i]
//     (indices <= i). Feature standardization statistics (mean/std) are fit on
//     the TRAIN slice only and then applied to later bars — the model never
//     peeks at data it would not have had at decision time. The forward label
//     for bar i (did close rise fwdBars ahead?) is a supervision target during
//     training only; a prediction for the latest bar carries no realized label.
//
//   - NEVER A PREDICTION WITHOUT ITS GRADE. Predictive quality is measured by
//     expanding-window WALK-FORWARD evaluation (Evaluate), which only ever
//     scores out-of-sample bars — each fold trains on the past and predicts a
//     strictly-later block it never trained on. The reported Grade (accuracy,
//     Brier score, AUC, base rate, lift) is what callers must surface next to
//     any probability. On a pure-noise series the model correctly reports ~zero
//     lift: it claims no edge when it has none.
//
// Costs / assumptions (documented here because honesty is the brand):
//   - Labels use raw close-to-close direction with NO transaction costs, spread
//     or slippage. P(up) > 0.5 is a directional signal, not a tradeable edge;
//     round-trip costs must be subtracted downstream before any P&L claim.
//   - Overlapping forward windows (fwdBars > 1) make consecutive samples
//     autocorrelated, so effective sample size < N and reported metrics are
//     mildly optimistic in their confidence (not in their point estimate).
//   - The model is a linear logit on standardized features; it captures only
//     linear separability in this feature space and will honestly grade near
//     the base rate whenever no such structure exists.
package forecast

import (
	"errors"
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Fixed hyperparameters. These are constants (not config) so that a Grade
// produced today is reproducible and comparable to one produced later:
// changing them silently changes what a "grade" means.
const (
	rsiPeriod = 14 // RSI window
	smaShort  = 20 // short trend / volume reference window
	smaLong   = 50 // long trend window
	volWindow = 20 // realized-vol window (stdev of last 20 log returns)

	// warmup is the first bar index at which every feature is computable:
	// SMA50 needs 50 prior closes, and the 10-bar return / log-return vol
	// windows are shorter, so 50 dominates. Bars before warmup are unusable.
	warmup = smaLong

	// numFeatures must match the feature vector built in features().
	numFeatures = 8

	// Training defaults for batch gradient descent. Deterministic: fixed
	// iteration count, no RNG, weights initialised to zero.
	defaultIters = 800
	learningRate = 0.1
	l2Reg        = 1e-3

	// minLabeledSamples guards against fitting an 8-feature logit on too few
	// rows (overfitting / unstable weights). ~150 keeps rows/feature ~19+.
	minLabeledSamples = 150
)

// Errors returned by this package.
var (
	// ErrInsufficientData is returned when there are too few labeled samples
	// to fit or evaluate a model responsibly.
	ErrInsufficientData = errors.New("forecast: insufficient labeled data")
	// ErrBadParams is returned for invalid arguments (e.g. fwdBars < 1).
	ErrBadParams = errors.New("forecast: invalid parameters")
)

// Model is a fitted logistic-regression directional classifier. It stores the
// learned weights plus the TRAIN-slice standardization statistics so that
// prediction re-applies exactly the transform seen during fitting (no
// lookahead: stats never come from bars later than the train slice).
type Model struct {
	Weights []float64 // length numFeatures, per-standardized-feature coefficients
	Bias    float64   // intercept
	Mean    []float64 // per-feature train mean used to standardize
	Std     []float64 // per-feature train std (guarded >= tiny) used to standardize
	FwdBars int       // forward horizon in bars this model was trained for
}

// Grade is an out-of-sample report card for a forecast. It is the honesty
// contract: a probability is only ever shown alongside a Grade so the reader
// can see whether the model has demonstrated any edge on held-out data.
//
// Lift = Accuracy - BaseRate is the headline honesty number: <= 0 means the
// model did no better than always predicting the majority class.
type Grade struct {
	N          int     // number of out-of-sample predictions scored
	Accuracy   float64 // fraction correct at a 0.5 threshold
	BrierScore float64 // mean squared error of predicted prob vs {0,1} (lower better)
	AUC        float64 // area under ROC curve via rank statistic (0.5 = no skill)
	BaseRate   float64 // fraction of actual positives (majority-class accuracy floor)
	Lift       float64 // Accuracy - BaseRate (honest edge; <= 0 means no edge)
}

// Forecast bundles a latest-bar probability with the walk-forward Grade that
// earned the right to show it and the training-set size behind the point model.
type Forecast struct {
	Prob   float64 // P(up) for the most recent bar's forward window
	Grade  Grade   // out-of-sample grade from expanding-window walk-forward
	NTrain int     // labeled samples used to fit the point model
}

// features builds the feature vector for bar i using ONLY bars[..i] (indices
// <= i). It returns ok=false when i is before warmup (features not computable).
// No value in the returned slice depends on any bar with index > i, which is
// the no-lookahead guarantee enforced at the source.
func features(bars []marketdata.Bar, i int) ([]float64, bool) {
	if i < warmup || i >= len(bars) {
		return nil, false
	}
	c := bars[i].Close

	// Returns over last 1/5/10 bars (simple pct change, uses past closes only).
	ret1 := pctReturn(bars, i, 1)
	ret5 := pctReturn(bars, i, 5)
	ret10 := pctReturn(bars, i, 10)

	// RSI(14) recentered to 0 and scaled to roughly [-1,1] (RSI in [0,100]).
	rsi := (rsiAt(bars, i, rsiPeriod) - 50.0) / 50.0

	// Price vs SMA20 & SMA50 as pct deviations.
	sma20 := smaClose(bars, i, smaShort)
	sma50 := smaClose(bars, i, smaLong)
	vsS20 := (c - sma20) / sma20
	vsS50 := (c - sma50) / sma50

	// Realized volatility: stdev of last volWindow log returns.
	rv := realizedVol(bars, i, volWindow)

	// Volume ratio: current volume / SMA20 of volume (guarded).
	volAvg := smaVolume(bars, i, smaShort)
	volRatio := 1.0
	if volAvg > 0 {
		volRatio = bars[i].Volume / volAvg
	}

	return []float64{ret1, ret5, ret10, rsi, vsS20, vsS50, rv, volRatio}, true
}

// pctReturn is close[i]/close[i-n]-1 using only past closes; 0 if unavailable.
func pctReturn(bars []marketdata.Bar, i, n int) float64 {
	if i-n < 0 {
		return 0
	}
	prev := bars[i-n].Close
	if prev == 0 {
		return 0
	}
	return bars[i].Close/prev - 1
}

// smaClose is the simple moving average of the last n closes ending at i.
func smaClose(bars []marketdata.Bar, i, n int) float64 {
	if i-n+1 < 0 {
		n = i + 1
	}
	sum := 0.0
	for j := i - n + 1; j <= i; j++ {
		sum += bars[j].Close
	}
	return sum / float64(n)
}

// smaVolume is the simple moving average of the last n volumes ending at i.
func smaVolume(bars []marketdata.Bar, i, n int) float64 {
	if i-n+1 < 0 {
		n = i + 1
	}
	sum := 0.0
	for j := i - n + 1; j <= i; j++ {
		sum += bars[j].Volume
	}
	return sum / float64(n)
}

// rsiAt computes Wilder-style RSI(period) at bar i from the last `period`
// close-to-close changes ending at i. Uses only bars[..i]. Returns 50
// (neutral) when history is insufficient or there is no price movement.
func rsiAt(bars []marketdata.Bar, i, period int) float64 {
	if i-period < 0 {
		return 50
	}
	gain, loss := 0.0, 0.0
	for j := i - period + 1; j <= i; j++ {
		ch := bars[j].Close - bars[j-1].Close
		if ch >= 0 {
			gain += ch
		} else {
			loss -= ch
		}
	}
	avgGain := gain / float64(period)
	avgLoss := loss / float64(period)
	if avgLoss == 0 {
		if avgGain == 0 {
			return 50
		}
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs)
}

// realizedVol is the sample standard deviation of the last n log returns
// ending at i, computed from bars[..i] only. Returns 0 if not computable.
func realizedVol(bars []marketdata.Bar, i, n int) float64 {
	if i-n < 0 {
		return 0
	}
	rets := make([]float64, 0, n)
	for j := i - n + 1; j <= i; j++ {
		p0, p1 := bars[j-1].Close, bars[j].Close
		if p0 > 0 && p1 > 0 {
			rets = append(rets, math.Log(p1/p0))
		}
	}
	return stdev(rets)
}

// stdev is the sample standard deviation (n-1) of xs; 0 for < 2 elements.
func stdev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	mean := 0.0
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	ss := 0.0
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)-1))
}

// sample is one labeled training/eval row: the raw feature vector at a bar,
// the source bar index, and the binary forward-direction label.
type sample struct {
	idx  int       // source bar index (used to enforce temporal ordering)
	feat []float64 // raw (un-standardized) features from features()
	y    float64   // label: 1 if close[idx+fwdBars] > close[idx], else 0
}

// buildSamples produces every labeled sample computable from bars for the
// given forward horizon: bar i qualifies when i >= warmup AND i+fwdBars is in
// range (so the label exists). No sample's features use any bar > i, and its
// label uses exactly one future bar (i+fwdBars) — used for supervision only,
// never as a feature. Samples come back in ascending time order.
func buildSamples(bars []marketdata.Bar, fwdBars int) []sample {
	out := make([]sample, 0, len(bars))
	for i := warmup; i+fwdBars < len(bars); i++ {
		f, ok := features(bars, i)
		if !ok {
			continue
		}
		y := 0.0
		if bars[i+fwdBars].Close > bars[i].Close {
			y = 1
		}
		out = append(out, sample{idx: i, feat: f, y: y})
	}
	return out
}

// standardizer holds per-feature mean/std fit on a TRAIN slice only.
type standardizer struct {
	mean []float64
	std  []float64
}

// fitStandardizer computes per-feature mean and std over the given samples
// (the train slice). Std is floored at a tiny epsilon to avoid divide-by-zero
// on constant features. Fitting on train-only is what prevents lookahead in
// the normalization step.
func fitStandardizer(samples []sample) standardizer {
	mean := make([]float64, numFeatures)
	std := make([]float64, numFeatures)
	n := float64(len(samples))
	if n == 0 {
		for k := range std {
			std[k] = 1
		}
		return standardizer{mean: mean, std: std}
	}
	for _, s := range samples {
		for k := 0; k < numFeatures; k++ {
			mean[k] += s.feat[k]
		}
	}
	for k := range mean {
		mean[k] /= n
	}
	for _, s := range samples {
		for k := 0; k < numFeatures; k++ {
			d := s.feat[k] - mean[k]
			std[k] += d * d
		}
	}
	for k := range std {
		std[k] = math.Sqrt(std[k] / n)
		if std[k] < 1e-9 {
			std[k] = 1e-9
		}
	}
	return standardizer{mean: mean, std: std}
}

// apply standardizes a raw feature vector into z-scores using the fitted stats.
func (s standardizer) apply(feat []float64) []float64 {
	z := make([]float64, numFeatures)
	for k := 0; k < numFeatures; k++ {
		z[k] = (feat[k] - s.mean[k]) / s.std[k]
	}
	return z
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

// fitLogit fits logistic-regression weights on already-standardized rows by
// deterministic batch gradient descent with L2 regularization (bias not
// penalized). Weights start at zero; iters is fixed; there is no RNG, so the
// result is fully reproducible for identical input.
func fitLogit(z [][]float64, y []float64, iters int) ([]float64, float64) {
	w := make([]float64, numFeatures)
	b := 0.0
	n := float64(len(z))
	if n == 0 {
		return w, b
	}
	for it := 0; it < iters; it++ {
		gradW := make([]float64, numFeatures)
		gradB := 0.0
		for r := range z {
			p := predictLogit(w, b, z[r])
			err := p - y[r] // dL/d(logit) for cross-entropy
			for k := 0; k < numFeatures; k++ {
				gradW[k] += err * z[r][k]
			}
			gradB += err
		}
		for k := 0; k < numFeatures; k++ {
			gradW[k] = gradW[k]/n + l2Reg*w[k] // L2 on weights only
			w[k] -= learningRate * gradW[k]
		}
		b -= learningRate * (gradB / n)
	}
	return w, b
}

// predictLogit returns sigmoid(w·z + b) for a standardized row.
func predictLogit(w []float64, b float64, z []float64) float64 {
	s := b
	for k := 0; k < numFeatures; k++ {
		s += w[k] * z[k]
	}
	return sigmoid(s)
}

// Train fits a directional logistic-regression Model on all labeled samples
// derivable from bars for the given forward horizon (label = 1 if
// close[i+fwdBars] > close[i]). It errors with ErrInsufficientData when fewer
// than minLabeledSamples labeled rows exist and with ErrBadParams for
// fwdBars < 1. Standardization statistics are fit on these training samples
// only and stored on the Model so PredictLatest re-applies the same transform
// without lookahead.
func Train(bars []marketdata.Bar, fwdBars int) (*Model, error) {
	if fwdBars < 1 {
		return nil, ErrBadParams
	}
	samples := buildSamples(bars, fwdBars)
	if len(samples) < minLabeledSamples {
		return nil, ErrInsufficientData
	}
	std := fitStandardizer(samples)
	z := make([][]float64, len(samples))
	y := make([]float64, len(samples))
	for r, s := range samples {
		z[r] = std.apply(s.feat)
		y[r] = s.y
	}
	w, b := fitLogit(z, y, defaultIters)
	return &Model{
		Weights: w,
		Bias:    b,
		Mean:    std.mean,
		Std:     std.std,
		FwdBars: fwdBars,
	}, nil
}

// predictRaw standardizes a raw feature vector with the model's stored stats
// and returns P(up).
func (m *Model) predictRaw(feat []float64) float64 {
	z := make([]float64, numFeatures)
	for k := 0; k < numFeatures; k++ {
		s := m.Std[k]
		if s == 0 {
			s = 1e-9
		}
		z[k] = (feat[k] - m.Mean[k]) / s
	}
	return predictLogit(m.Weights, m.Bias, z)
}

// PredictLatest returns P(up) for the forward window anchored at the most
// recent bar (the last element of bars). It uses ONLY bars[..last] to build
// features — there is no future bar to leak — so this is the model's honest
// forward-looking probability with no realized label yet. ok is false when the
// latest bar's features are not computable (too little history).
func (m *Model) PredictLatest(bars []marketdata.Bar) (prob float64, ok bool) {
	if len(bars) == 0 {
		return 0, false
	}
	i := len(bars) - 1
	f, ok := features(bars, i)
	if !ok {
		return 0, false
	}
	return m.predictRaw(f), true
}

// Evaluate grades the model out-of-sample with expanding-window walk-forward.
// It splits the labeled samples into `folds` contiguous, time-ordered blocks;
// for each fold after the first it trains a fresh model on ALL earlier samples
// and predicts the current block, which the model has never seen. Because
// blocks are strictly time-ordered and a fold's training set contains only
// earlier-indexed samples, no future information enters any prediction — this
// is enforced by construction, not by convention. Appending future bars to the
// series cannot alter the predictions of an earlier fold (see tests).
//
// It errors with ErrBadParams for fwdBars < 1 or folds < 2, and with
// ErrInsufficientData when there are too few samples to give each fold a
// usable train/test split.
func Evaluate(bars []marketdata.Bar, fwdBars, folds int) (Grade, error) {
	if fwdBars < 1 || folds < 2 {
		return Grade{}, ErrBadParams
	}
	samples := buildSamples(bars, fwdBars)
	// Need enough that even the first test block has a real training set and
	// each fold is non-trivial. Require at least minLabeledSamples total and
	// at least a handful of samples per fold.
	if len(samples) < minLabeledSamples || len(samples) < folds*10 {
		return Grade{}, ErrInsufficientData
	}

	n := len(samples)
	preds := make([]float64, 0, n)
	actuals := make([]float64, 0, n)

	// Contiguous fold boundaries over the time-ordered samples.
	for f := 1; f < folds; f++ {
		trainEnd := n * f / folds      // train on samples[:trainEnd]
		testEnd := n * (f + 1) / folds // predict samples[trainEnd:testEnd]
		if trainEnd < minLabeledSamples/2 || testEnd <= trainEnd {
			continue // skip folds whose train slice is too thin to trust
		}
		train := samples[:trainEnd]
		test := samples[trainEnd:testEnd]

		// Fit standardizer + logit on the TRAIN slice only (no lookahead).
		std := fitStandardizer(train)
		z := make([][]float64, len(train))
		yv := make([]float64, len(train))
		for r, s := range train {
			z[r] = std.apply(s.feat)
			yv[r] = s.y
		}
		w, b := fitLogit(z, yv, defaultIters)

		for _, s := range test {
			p := predictLogit(w, b, std.apply(s.feat))
			preds = append(preds, p)
			actuals = append(actuals, s.y)
		}
	}

	if len(preds) == 0 {
		return Grade{}, ErrInsufficientData
	}
	return gradeFrom(preds, actuals), nil
}

// gradeFrom computes the Grade metrics from paired out-of-sample predictions
// and actual labels. BaseRate is the majority-class accuracy floor:
// max(pPos, 1-pPos), so Lift is honestly measured against the strongest
// no-skill constant predictor.
func gradeFrom(preds, actuals []float64) Grade {
	n := len(preds)
	correct := 0
	brier := 0.0
	pos := 0
	for i := range preds {
		pred1 := preds[i] >= 0.5
		act1 := actuals[i] >= 0.5
		if pred1 == act1 {
			correct++
		}
		d := preds[i] - actuals[i]
		brier += d * d
		if act1 {
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

// aucRank computes ROC AUC via the Mann-Whitney rank statistic:
// AUC = (sum of ranks of positive scores - nPos*(nPos+1)/2) / (nPos*nNeg),
// with tied scores receiving average ranks. Returns 0.5 (no skill) when either
// class is empty. AUC = 0.5 means predictions do not rank positives above
// negatives at all — the honest "no edge" reading.
func aucRank(preds, actuals []float64) float64 {
	type pa struct {
		p float64
		y float64
	}
	rows := make([]pa, len(preds))
	for i := range preds {
		rows[i] = pa{preds[i], actuals[i]}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].p < rows[b].p })

	// Assign average ranks (1-based) to handle ties.
	ranks := make([]float64, len(rows))
	i := 0
	for i < len(rows) {
		j := i
		for j+1 < len(rows) && rows[j+1].p == rows[i].p {
			j++
		}
		// ranks i..j (0-based) get average of (i+1)..(j+1) 1-based ranks.
		avg := float64((i+1)+(j+1)) / 2.0
		for k := i; k <= j; k++ {
			ranks[k] = avg
		}
		i = j + 1
	}

	sumPosRanks := 0.0
	nPos := 0.0
	nNeg := 0.0
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

// Run is the top-level entry point: it maps a marketdata.Horizon to a forward
// bar count (1d -> 1, 1w -> 5), and returns ok=false for 1h (this package is
// daily-only here). It walk-forward grades the model, trains a point model on
// all-but-the-last forward window of bars, and predicts the latest bar. It
// returns ok=false whenever there is insufficient data for either grading or
// training (so callers never show an ungraded probability).
//
// The point model is trained on bars[:len-fwdBars] deliberately: the final
// fwdBars bars cannot yet have a realized label, and trimming them keeps the
// training set to fully-labeled history while still letting PredictLatest use
// the true most-recent bar for its (unlabeled) forward probability.
func Run(daily []marketdata.Bar, h marketdata.Horizon) (Forecast, bool) {
	fwdBars, ok := fwdBarsForHorizon(h)
	if !ok {
		return Forecast{}, false // 1h unsupported on daily-only data
	}
	if len(daily) <= fwdBars {
		return Forecast{}, false
	}

	// Grade out-of-sample first — no prediction is returned without it.
	grade, err := Evaluate(daily, fwdBars, defaultFolds)
	if err != nil {
		return Forecast{}, false
	}

	// Train the point model on fully-labeled history (drop the final fwdBars
	// bars, which have no realized label yet).
	trainBars := daily[:len(daily)-fwdBars]
	m, err := Train(trainBars, fwdBars)
	if err != nil {
		return Forecast{}, false
	}

	prob, ok := m.PredictLatest(daily)
	if !ok {
		return Forecast{}, false
	}

	// NTrain = number of labeled samples that fit the point model.
	nTrain := len(buildSamples(trainBars, fwdBars))
	return Forecast{Prob: prob, Grade: grade, NTrain: nTrain}, true
}

// defaultFolds is the walk-forward fold count used by Run.
const defaultFolds = 5

// fwdBarsForHorizon maps a horizon to a forward bar count on DAILY bars.
// 1d -> 1 bar, 1w -> 5 trading days. 1h is unsupported here (daily-only),
// signaled by ok=false.
func fwdBarsForHorizon(h marketdata.Horizon) (int, bool) {
	switch h {
	case marketdata.H1d:
		return 1, true
	case marketdata.H1w:
		return 5, true
	default: // H1h and anything else: not supported on daily bars
		return 0, false
	}
}
