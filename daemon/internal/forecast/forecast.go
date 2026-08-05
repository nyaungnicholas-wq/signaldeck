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
//     strictly-later block it never trained on. The split is PURGED and
//     EMBARGOED: because a label is read off the bar fwdBars ahead, training rows
//     near a fold boundary would otherwise be labeled by bars inside the test
//     block, so every such row is dropped along with an embargo gap. The purge
//     width is read off the data, and a sample set that declares no label
//     horizon is refused (ErrNoLabelSpan) rather than graded unpurged. The
//     reported Grade (accuracy,
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

	// clusterstat owns the prequential null every grading surface in this tree
	// shares. See gradeFrom for why a local majority-class floor was wrong.
	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
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
	// ErrNoLabelSpan is returned by Evaluate when NO sample declares when its
	// label resolved (labelEnd). Without the horizon the purge width is
	// unknowable, so the walk-forward split cannot be certified leak-free — and
	// an uncertifiable Lift is the number that admits this leg to the live
	// blend. The grade is withheld rather than published unpurged.
	ErrNoLabelSpan = errors.New("forecast: samples do not declare a label horizon — cannot purge")
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

	// The purge is reported, not assumed. LabelSpan is the forward horizon read
	// off the samples (in bars), EmbargoSpan the extra gap held in front of each
	// test block, and PurgedTrainRows how many training rows the two together
	// removed across all folds. A grade with PurgedTrainRows == 0 on one-per-bar
	// samples means the purge did not run, which is the defect, not a result.
	LabelSpan       int `json:"labelSpan"`
	EmbargoSpan     int `json:"embargoSpan"`
	PurgedTrainRows int `json:"purgedTrainRows"`
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
	// labelEnd is the bar index at which this row's label became known
	// (idx+fwdBars). It is what the walk-forward purge measures itself by: a
	// training row whose labelEnd lands inside a later test block was graded on
	// data that block owns. Declared at construction so no split can run without
	// it — an undeclared set is refused (ErrNoLabelSpan), never graded unpurged.
	labelEnd int
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
		out = append(out, sample{idx: i, feat: f, y: y, labelEnd: i + fwdBars})
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

// Evaluate grades the model out-of-sample with PURGED, EMBARGOED expanding-window
// walk-forward. It splits the labeled samples into `folds` contiguous,
// time-ordered blocks; for each fold after the first it trains a fresh model on
// earlier samples and predicts the current block, which the model has never seen.
//
// Contiguity alone is not enough, and that was finding A15. A sample's label is
// read off the bar fwdBars AHEAD of it, so at the 1w horizon the last five
// training rows before a boundary are labeled by bars sitting INSIDE the test
// block: the fold trains on the outcomes it is about to be graded on, and since
// Lift > 0 is the gate that admits this leg to the live blend, the leak buys real
// allocation. So each fold now drops every training row whose label window
// reaches the test block, plus an embargo gap in front of it for the residual
// serial correlation the exact label window does not capture (the features are
// trailing-window statistics, so neighbouring rows share most of their inputs).
//
// The purge width comes from the DATA — the widest declared label horizon in the
// set — never from a constant. When no row declares one, Evaluate returns
// ErrNoLabelSpan: an unpurgeable grade is withheld, not published.
//
// It errors with ErrBadParams for fwdBars < 1 or folds < 2, and with
// ErrInsufficientData when there are too few samples to give each fold a usable
// train/test split — including when the purge itself leaves every fold's
// training set too thin, which is the honest answer for a series whose history
// is short relative to its own label horizon.
func Evaluate(bars []marketdata.Bar, fwdBars, folds int) (Grade, error) {
	if fwdBars < 1 || folds < 2 {
		return Grade{}, ErrBadParams
	}
	return evaluateSamples(buildSamples(bars, fwdBars), folds)
}

// evaluateSamples is Evaluate's body once the samples exist: it reads the label
// span off the data, refuses when nothing declares one, and runs the purged
// walk-forward. Split out so the purge itself is testable on hand-built sample
// sets (undeclared horizons, over-wide horizons) that no bar series can produce.
func evaluateSamples(samples []sample, folds int) (Grade, error) {
	// Need enough that even the first test block has a real training set and
	// each fold is non-trivial. Require at least minLabeledSamples total and
	// at least a handful of samples per fold.
	if len(samples) < minLabeledSamples || len(samples) < folds*10 {
		return Grade{}, ErrInsufficientData
	}
	span, ok := labelSpanOf(samples)
	if !ok {
		return Grade{}, ErrNoLabelSpan
	}
	return evaluateFolds(samples, folds, span, embargoFor(span), true)
}

// embargoFor returns the embargo gap, in bars, for a measured label span. The
// rule is horizon-aware and matches internal/alphax ("label span + 1"): a gap
// proportionate to the label it guards, scaled by the data rather than picked.
// One extra bar beyond the exact label window is the smallest representable
// buffer against the trailing-window feature overlap that the label window alone
// does not cover. A fixed constant is exactly what finding A15/H1 objected to.
func embargoFor(span int) int { return span + 1 }

// labelSpanOf reads the label horizon OFF THE DATA: the widest declared
// (labelEnd - idx) in the set. Widest, not median — with mixed horizons a
// narrower purge would leave the long-horizon rows straddling the boundary, and
// over-purging costs training rows while under-purging costs the honesty of the
// grade. ok=false when no row declares a horizon at all.
func labelSpanOf(samples []sample) (int, bool) {
	span := 0
	ok := false
	for _, s := range samples {
		if s.labelEnd <= s.idx {
			continue // undeclared (or a zero-width label, which needs no purge)
		}
		ok = true
		if d := s.labelEnd - s.idx; d > span {
			span = d
		}
	}
	return span, ok
}

// labelEndOf returns the bar at which a sample's label resolved, defaulting an
// undeclared row to the set's widest span. A row that forgot to declare is
// treated as the WORST case, so a partially-declared set cannot smuggle
// unpurged rows through a boundary.
func labelEndOf(s sample, span int) int {
	if end := s.idx + span; end > s.labelEnd {
		return end
	}
	return s.labelEnd
}

// purgedTrain returns the training rows for one fold: those among
// samples[:trainEnd] whose label was fully realized at least `embargo` bars
// before the test block opens at bar testStartIdx. Filtered row by row rather
// than truncated so a set with mixed horizons is handled correctly.
func purgedTrain(samples []sample, trainEnd, testStartIdx, span, embargo int) []sample {
	cutoff := testStartIdx - embargo
	out := make([]sample, 0, trainEnd)
	for i := 0; i < trainEnd; i++ {
		if labelEndOf(samples[i], span) > cutoff {
			continue // label reaches into the test block (or its embargo) — purge
		}
		out = append(out, samples[i])
	}
	return out
}

// evaluateFolds is the shared walk-forward body. purge=false reproduces the
// PRE-FIX, zero-gap split and exists only so the tests can measure what the leak
// was worth; every production path goes through Evaluate with purge=true.
func evaluateFolds(samples []sample, folds, span, embargo int, purge bool) (Grade, error) {
	n := len(samples)
	preds := make([]float64, 0, n)
	actuals := make([]float64, 0, n)
	// scoredIdx carries each scored row's source BAR index, which is this
	// package's clustering unit: samples are one-per-bar for one symbol, so a
	// bar index is a day and every cluster holds exactly one observation. The
	// prequential baseline needs that ordering to know which class was in the
	// majority BEFORE each row — see gradeFrom.
	scoredIdx := make([]int64, 0, n)
	purged := 0

	// Contiguous fold boundaries over the time-ordered samples.
	for f := 1; f < folds; f++ {
		trainEnd := n * f / folds      // train on samples[:trainEnd], purged
		testEnd := n * (f + 1) / folds // predict samples[trainEnd:testEnd]
		if testEnd <= trainEnd {
			continue
		}
		train := samples[:trainEnd]
		if purge {
			train = purgedTrain(samples, trainEnd, samples[trainEnd].idx, span, embargo)
			purged += trainEnd - len(train)
		}
		if len(train) < minLabeledSamples/2 {
			continue // train slice too thin to trust (often BECAUSE of the purge)
		}
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
			scoredIdx = append(scoredIdx, int64(s.idx))
		}
	}

	if len(preds) == 0 {
		return Grade{}, ErrInsufficientData
	}
	g := gradeFrom(scoredIdx, preds, actuals)
	if purge {
		g.LabelSpan, g.EmbargoSpan, g.PurgedTrainRows = span, embargo, purged
	}
	return g, nil
}

// gradeFrom computes the Grade metrics from paired out-of-sample predictions
// and actual labels, with `clusters` the index-aligned ordering key (this
// package passes the source bar index; one bar = one day = one observation).
//
// BaseRate is the PREQUENTIAL majority — on each step, the majority class of
// everything strictly before it — NOT the hindsight floor max(pPos, 1-pPos).
//
// The hindsight floor scored a constant predictor that already knew which class
// would win the window, which is a choice no forecaster can make in advance. It
// therefore set an admission bar that rises with the window's imbalance rather
// than with the difficulty of forecasting, and Lift > 0 is the gate that decides
// whether this leg reaches the live blend at all. The accuracy registry retired
// that null (null_policy: "prequential-majority only"); this is the live gate
// being brought onto the same one, via the shared clusterstat implementation so
// no two surfaces can disagree about the baseline.
func gradeFrom(clusters []int64, preds, actuals []float64) Grade {
	n := len(preds)
	correct := 0
	brier := 0.0
	for i := range preds {
		pred1 := preds[i] >= 0.5
		act1 := actuals[i] >= 0.5
		if pred1 == act1 {
			correct++
		}
		d := preds[i] - actuals[i]
		brier += d * d
	}
	acc := float64(correct) / float64(n)
	// One cluster per row: multiply by 86400 so DayLabelsFrom's ts/86400 folding
	// keeps each bar in its own cluster instead of collapsing 86,400 of them.
	ts := make([]int64, len(clusters))
	for i, c := range clusters {
		ts[i] = c * 86400
	}
	baseRate := clusterstat.PrequentialBaselineVsModel(clusterstat.DayGradesFrom(ts, preds, actuals))
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
