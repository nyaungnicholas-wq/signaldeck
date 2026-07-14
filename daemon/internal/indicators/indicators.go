// Package indicators computes MODEL-FED indicator SIGNALS: compact latest-bar
// scalars that feed the prediction feature vector, distinct from the web app's
// display math. Each is a bounded/O(1) encoding of a classic technical
// indicator so the linear/adaptive legs compose them without one dominating a
// scale-sensitive standardizer, while the GBM (scale-invariant) learns whatever
// structure they carry.
//
// HONESTY / CONTRACT (mirrors internal/signals):
//
//   - PURE. Bars in (ASCENDING by Ts), scalars out — no I/O, no clock.
//   - NO LOOKAHEAD. Every signal reads a trailing window ending at the last
//     bar; nothing after it is used.
//   - ABSENT, NOT ZERO. A signal that cannot be computed (too few bars, or a
//     degenerate zero-range/zero-dispersion window) is left nil rather than
//     emitted as a misleading 0 — the same absence-is-information discipline the
//     feature store relies on.
//
// Where internal/signals already implements the math (ATR, EMA) it is reused;
// the rest is implemented here against the same conventions.
package indicators

import (
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/signals"
)

// Standard periods for the model-fed signals. Fixed constants so a stored
// feature means the same thing across time.
const (
	stochK      = 14
	stochD      = 3
	adxPeriod   = 14
	cciPeriod   = 20
	williamsLen = 14
	bbPeriod    = 20
	bbK         = 2.0
	stAtr       = 10
	stMult      = 3.0
	keltnerEMA  = 20
	keltnerAtr  = 20
	keltnerMult = 2.0
)

// Signals holds the model-fed indicator scalars for the latest bar. Each field
// is nil when it could not be computed (absent-is-information). Values are
// bounded/O(1) encodings, NOT raw display values.
type Signals struct {
	// StochK is the 3-smoothed Stochastic %K (14,3) scaled to [0,1].
	StochK *float64
	// ADX14 is Wilder's ADX(14) scaled to ~[0,1] (adx/100).
	ADX14 *float64
	// CCI20 is CCI(20)/100 (roughly [-3,3], O(1)).
	CCI20 *float64
	// WilliamsR14 is Williams %R(14)/100, in [-1,0].
	WilliamsR14 *float64
	// BBPctB is Bollinger %B (20,2): (close-lower)/(upper-lower); ~[0,1] but may
	// exceed on a band break.
	BBPctB *float64
	// SupertrendDir is the Supertrend (ATR 10, x3) direction: +1 up / -1 down.
	SupertrendDir *float64
	// KeltnerPos is the close's position in the Keltner channel (EMA20 ± 2*ATR):
	// 0 at the mid, +1 at the upper band, -1 at the lower band; may exceed on a
	// break.
	KeltnerPos *float64
}

// Compute returns the model-fed signals computable from bars. Each is filled
// only when its own minimum-bar / non-degenerate condition holds.
func Compute(bars []marketdata.Bar) Signals {
	var s Signals
	if v, ok := stochSlowK(bars, stochK, stochD); ok {
		s.StochK = &v
	}
	if v, ok := adx(bars, adxPeriod); ok {
		s.ADX14 = &v
	}
	if v, ok := cci(bars, cciPeriod); ok {
		s.CCI20 = &v
	}
	if v, ok := williamsR(bars, williamsLen); ok {
		s.WilliamsR14 = &v
	}
	if v, ok := bbPctB(bars, bbPeriod, bbK); ok {
		s.BBPctB = &v
	}
	if v, ok := supertrendDir(bars, stAtr, stMult); ok {
		s.SupertrendDir = &v
	}
	if v, ok := keltnerPos(bars, keltnerEMA, keltnerAtr, keltnerMult); ok {
		s.KeltnerPos = &v
	}
	return s
}

// Map returns the present signals as a name->value map. Absent signals are
// simply not keyed. Keys are the stable feature names used by the model.
func (s Signals) Map() map[string]float64 {
	m := map[string]float64{}
	if s.StochK != nil {
		m["stoch_k"] = *s.StochK
	}
	if s.ADX14 != nil {
		m["adx14"] = *s.ADX14
	}
	if s.CCI20 != nil {
		m["cci20"] = *s.CCI20
	}
	if s.WilliamsR14 != nil {
		m["williams_r14"] = *s.WilliamsR14
	}
	if s.BBPctB != nil {
		m["bb_pctb"] = *s.BBPctB
	}
	if s.SupertrendDir != nil {
		m["supertrend_dir"] = *s.SupertrendDir
	}
	if s.KeltnerPos != nil {
		m["keltner_pos"] = *s.KeltnerPos
	}
	return m
}

// ── individual signals ──────────────────────────────────────────────────

// stochSlowK is the d-smoothed Stochastic %K over a k-bar window, scaled to
// [0,1]. Needs k+d-1 bars; a zero-range (flat) window is degenerate → absent.
func stochSlowK(bars []marketdata.Bar, k, d int) (float64, bool) {
	n := len(bars)
	if k <= 0 || d <= 0 || n < k+d-1 {
		return 0, false
	}
	sum := 0.0
	for j := 0; j < d; j++ {
		end := n - 1 - j
		hi, lo := windowHighLow(bars, end-k+1, end)
		rng := hi - lo
		if rng <= 0 {
			return 0, false
		}
		sum += (bars[end].Close - lo) / rng * 100
	}
	return sum / float64(d) / 100.0, true
}

// adx is Wilder's ADX(period), scaled by /100. Needs 2*period+1 bars for a
// smoothed value (period DX seeds + period-bar TR/DM smoothing).
func adx(bars []marketdata.Bar, period int) (float64, bool) {
	n := len(bars)
	if period <= 0 || n < 2*period+1 {
		return 0, false
	}
	plusDM := make([]float64, n)
	minusDM := make([]float64, n)
	tr := make([]float64, n)
	for i := 1; i < n; i++ {
		up := bars[i].High - bars[i-1].High
		down := bars[i-1].Low - bars[i].Low
		if up > down && up > 0 {
			plusDM[i] = up
		}
		if down > up && down > 0 {
			minusDM[i] = down
		}
		tr[i] = trueRange(bars[i], bars[i-1])
	}
	var trS, pS, mS float64
	for i := 1; i <= period; i++ {
		trS += tr[i]
		pS += plusDM[i]
		mS += minusDM[i]
	}
	dxs := make([]float64, 0, n)
	dxs = append(dxs, dxFrom(trS, pS, mS))
	for i := period + 1; i < n; i++ {
		trS = trS - trS/float64(period) + tr[i]
		pS = pS - pS/float64(period) + plusDM[i]
		mS = mS - mS/float64(period) + minusDM[i]
		dxs = append(dxs, dxFrom(trS, pS, mS))
	}
	seed := period
	if seed > len(dxs) {
		seed = len(dxs)
	}
	sum := 0.0
	for i := 0; i < seed; i++ {
		sum += dxs[i]
	}
	adxV := sum / float64(seed)
	for i := seed; i < len(dxs); i++ {
		adxV = (adxV*float64(period-1) + dxs[i]) / float64(period)
	}
	return adxV / 100.0, true
}

// cci is the Commodity Channel Index(period)/100. TP=(H+L+C)/3; CCI =
// (TP - SMA(TP)) / (0.015 * meanDeviation). Needs `period` bars; a zero
// mean-deviation window is degenerate → absent.
func cci(bars []marketdata.Bar, period int) (float64, bool) {
	n := len(bars)
	if period <= 0 || n < period {
		return 0, false
	}
	tp := make([]float64, period)
	sum := 0.0
	for j := 0; j < period; j++ {
		b := bars[n-period+j]
		tp[j] = (b.High + b.Low + b.Close) / 3
		sum += tp[j]
	}
	mean := sum / float64(period)
	md := 0.0
	for _, v := range tp {
		md += math.Abs(v - mean)
	}
	md /= float64(period)
	if md == 0 {
		return 0, false
	}
	cciV := (tp[period-1] - mean) / (0.015 * md)
	return cciV / 100.0, true
}

// williamsR is Williams %R(period)/100, in [-1,0]. Needs `period` bars; a
// zero-range window is degenerate → absent.
func williamsR(bars []marketdata.Bar, period int) (float64, bool) {
	n := len(bars)
	if period <= 0 || n < period {
		return 0, false
	}
	hi, lo := windowHighLow(bars, n-period, n-1)
	rng := hi - lo
	if rng <= 0 {
		return 0, false
	}
	r := (hi - bars[n-1].Close) / rng * -100
	return r / 100.0, true
}

// bbPctB is Bollinger %B (period, k): (close-lower)/(upper-lower) with a
// population-σ band. Needs `period` bars; a zero-σ window has no band → absent.
func bbPctB(bars []marketdata.Bar, period int, k float64) (float64, bool) {
	n := len(bars)
	if period <= 0 || n < period {
		return 0, false
	}
	mid, ok := signals.SMA(bars, period)
	if !ok {
		return 0, false
	}
	sd := 0.0
	for _, b := range bars[n-period:] {
		d := b.Close - mid
		sd += d * d
	}
	sd = math.Sqrt(sd / float64(period))
	if sd == 0 {
		return 0, false
	}
	upper := mid + k*sd
	lower := mid - k*sd
	return (bars[n-1].Close - lower) / (upper - lower), true
}

// supertrendDir is the Supertrend direction (+1 up / -1 down) using a Wilder
// ATR(period) and multiplier mult. Needs period+2 bars.
func supertrendDir(bars []marketdata.Bar, period int, mult float64) (float64, bool) {
	n := len(bars)
	if period <= 0 || n < period+2 {
		return 0, false
	}
	atr, ok := atrSeries(bars, period)
	if !ok {
		return 0, false
	}
	var finalUpper, finalLower float64
	dir := 1
	started := false
	for i := period; i < n; i++ {
		hl2 := (bars[i].High + bars[i].Low) / 2
		bu := hl2 + mult*atr[i]
		bl := hl2 - mult*atr[i]
		if !started {
			finalUpper, finalLower, started = bu, bl, true
			continue
		}
		if bu < finalUpper || bars[i-1].Close > finalUpper {
			finalUpper = bu
		}
		if bl > finalLower || bars[i-1].Close < finalLower {
			finalLower = bl
		}
		switch {
		case bars[i].Close > finalUpper:
			dir = 1
		case bars[i].Close < finalLower:
			dir = -1
		}
	}
	return float64(dir), true
}

// keltnerPos is the close's position in a Keltner channel (EMA(emaLen) ±
// mult*ATR(atrLen)): 0 at the mid, +1 at the upper band, -1 at the lower.
// Needs max(emaLen, atrLen+1) bars; a zero-ATR channel is degenerate → absent.
func keltnerPos(bars []marketdata.Bar, emaLen, atrLen int, mult float64) (float64, bool) {
	mid, ok := signals.EMA(bars, emaLen)
	if !ok {
		return 0, false
	}
	atrV, ok := signals.ATR(bars, atrLen)
	if !ok || atrV <= 0 {
		return 0, false
	}
	half := mult * atrV
	return (bars[len(bars)-1].Close - mid) / half, true
}

// ── shared helpers ──────────────────────────────────────────────────────

// windowHighLow returns the highest High and lowest Low over bars[lo..hi]
// (inclusive). Callers guarantee 0 <= lo <= hi < len(bars).
func windowHighLow(bars []marketdata.Bar, lo, hi int) (high, low float64) {
	high, low = bars[lo].High, bars[lo].Low
	for i := lo + 1; i <= hi; i++ {
		if bars[i].High > high {
			high = bars[i].High
		}
		if bars[i].Low < low {
			low = bars[i].Low
		}
	}
	return high, low
}

// trueRange is the classic TR with the previous close (gaps count).
func trueRange(cur, prev marketdata.Bar) float64 {
	hl := cur.High - cur.Low
	hc := math.Abs(cur.High - prev.Close)
	lc := math.Abs(cur.Low - prev.Close)
	return math.Max(hl, math.Max(hc, lc))
}

// dxFrom is Wilder's DX from smoothed TR/+DM/-DM sums.
func dxFrom(trS, pS, mS float64) float64 {
	if trS <= 0 {
		return 0
	}
	plusDI := 100 * pS / trS
	minusDI := 100 * mS / trS
	den := plusDI + minusDI
	if den <= 0 {
		return 0
	}
	return 100 * math.Abs(plusDI-minusDI) / den
}

// atrSeries is a Wilder ATR(period) series aligned to bars: out[i] is valid for
// i >= period (seeded with the mean of the first `period` TRs), 0 before that.
// ok=false with fewer than period+1 bars.
func atrSeries(bars []marketdata.Bar, period int) ([]float64, bool) {
	n := len(bars)
	if period <= 0 || n < period+1 {
		return nil, false
	}
	tr := make([]float64, n)
	for i := 1; i < n; i++ {
		tr[i] = trueRange(bars[i], bars[i-1])
	}
	out := make([]float64, n)
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += tr[i]
	}
	out[period] = sum / float64(period)
	for i := period + 1; i < n; i++ {
		out[i] = (out[i-1]*float64(period-1) + tr[i]) / float64(period)
	}
	return out, true
}
