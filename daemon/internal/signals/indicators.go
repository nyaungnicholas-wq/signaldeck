// Package signals is SignalDeck's pure indicator library and Pressure Score
// engine. Every function is pure: bars in (ASCENDING by Ts), numbers out —
// no I/O, no persistence, no clock. Insufficient or degenerate input yields
// ok=false rather than NaN/Inf, so callers drop a signal instead of
// propagating junk into scores or the UI.
package signals

import (
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// closes extracts the close series; indicators that only need closes share it.
func closes(bars []marketdata.Bar) []float64 {
	out := make([]float64, len(bars))
	for i, b := range bars {
		out[i] = b.Close
	}
	return out
}

// smaFloat is the SMA of the last n values of vals.
func smaFloat(vals []float64, n int) (float64, bool) {
	if n <= 0 || len(vals) < n {
		return 0, false
	}
	sum := 0.0
	for _, v := range vals[len(vals)-n:] {
		sum += v
	}
	return sum / float64(n), true
}

// emaSeries returns the EMA(n) series aligned so out[0] corresponds to
// vals[n-1]. Standard convention: seed with the SMA of the first n values,
// multiplier 2/(n+1). ok=false when fewer than n values exist.
func emaSeries(vals []float64, n int) ([]float64, bool) {
	if n <= 0 || len(vals) < n {
		return nil, false
	}
	out := make([]float64, len(vals)-n+1)
	sum := 0.0
	for _, v := range vals[:n] {
		sum += v
	}
	out[0] = sum / float64(n)
	k := 2.0 / float64(n+1)
	for i := 1; i < len(out); i++ {
		out[i] = vals[n-1+i]*k + out[i-1]*(1-k)
	}
	return out, true
}

// SMA is the simple moving average of the last n closes.
func SMA(bars []marketdata.Bar, n int) (float64, bool) {
	return smaFloat(closes(bars), n)
}

// EMA is the standard exponential moving average of the closes: seeded with
// the SMA of the first n closes, multiplier 2/(n+1), run over the rest.
func EMA(bars []marketdata.Bar, n int) (float64, bool) {
	s, ok := emaSeries(closes(bars), n)
	if !ok {
		return 0, false
	}
	return s[len(s)-1], true
}

// RSI is the Wilder-smoothed Relative Strength Index over the closes: the
// first average gain/loss is a simple mean of the first `period` changes,
// subsequent bars use Wilder smoothing avg=(avg*(p-1)+x)/p. Needs period+1
// bars. A dead-flat series (no gains AND no losses) reads 50 (neutral)
// rather than the formula's degenerate 100 — flat is not overbought.
func RSI(bars []marketdata.Bar, period int) (float64, bool) {
	if period <= 0 || len(bars) < period+1 {
		return 0, false
	}
	cs := closes(bars)
	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		if d := cs[i] - cs[i-1]; d > 0 {
			avgGain += d
		} else {
			avgLoss -= d
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)
	for i := period + 1; i < len(cs); i++ {
		var g, l float64
		if d := cs[i] - cs[i-1]; d > 0 {
			g = d
		} else {
			l = -d
		}
		avgGain = (avgGain*float64(period-1) + g) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + l) / float64(period)
	}
	if avgLoss == 0 {
		if avgGain == 0 {
			return 50, true
		}
		return 100, true
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs), true
}

// MACD returns the MACD line (EMA fast − EMA slow), its signal line
// (EMA over the MACD line) and the histogram (macd − signal), all at the
// last bar. Needs slow+signal-1 bars (34 for the classic 12,26,9).
func MACD(bars []marketdata.Bar, fast, slow, signal int) (macd, sig, hist float64, ok bool) {
	if fast <= 0 || slow <= fast || signal <= 0 {
		return 0, 0, 0, false
	}
	cs := closes(bars)
	if len(cs) < slow+signal-1 {
		return 0, 0, 0, false
	}
	fastS, _ := emaSeries(cs, fast)
	slowS, _ := emaSeries(cs, slow)
	// The MACD line exists only where both EMAs do: from index slow-1 on.
	line := make([]float64, len(slowS))
	off := slow - fast
	for i := range line {
		line[i] = fastS[i+off] - slowS[i]
	}
	sigS, sok := emaSeries(line, signal)
	if !sok {
		return 0, 0, 0, false
	}
	macd = line[len(line)-1]
	sig = sigS[len(sigS)-1]
	return macd, sig, macd - sig, true
}

// ATR is the Wilder-smoothed Average True Range: TR uses the previous close
// (gaps count), the seed is a simple mean of the first `period` TRs, then
// atr=(atr*(p-1)+tr)/p. Needs period+1 bars (the first bar has no previous
// close).
func ATR(bars []marketdata.Bar, period int) (float64, bool) {
	if period <= 0 || len(bars) < period+1 {
		return 0, false
	}
	trAt := func(i int) float64 {
		h, l, pc := bars[i].High, bars[i].Low, bars[i-1].Close
		tr := h - l
		if d := math.Abs(h - pc); d > tr {
			tr = d
		}
		if d := math.Abs(l - pc); d > tr {
			tr = d
		}
		return tr
	}
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trAt(i)
	}
	atr := sum / float64(period)
	for i := period + 1; i < len(bars); i++ {
		atr = (atr*float64(period-1) + trAt(i)) / float64(period)
	}
	return atr, true
}

// ROC is the rate of change close[last]/close[last-n]-1. Needs n+1 bars and
// a non-zero base close.
func ROC(bars []marketdata.Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	base := bars[len(bars)-1-n].Close
	if base == 0 {
		return 0, false
	}
	return bars[len(bars)-1].Close/base - 1, true
}

// RealizedVol is the sample standard deviation (n-1 denominator) of the last
// n log returns, annualized by sqrt(365). The 365 is a deliberate
// crypto-agnostic simplification: it assumes daily bars and a market that
// trades every calendar day. For stocks (≈252 sessions) this overstates
// annualized vol by ~sqrt(365/252) ≈ 1.20 — acceptable for a display-only
// regime reading, and consistent across both markets. Needs n+1 bars with
// strictly positive closes; n must be ≥ 2 for a stdev to exist.
func RealizedVol(bars []marketdata.Bar, n int) (float64, bool) {
	if n < 2 || len(bars) < n+1 {
		return 0, false
	}
	cs := closes(bars[len(bars)-n-1:])
	rets := make([]float64, n)
	for i := 1; i <= n; i++ {
		if cs[i-1] <= 0 || cs[i] <= 0 {
			return 0, false
		}
		rets[i-1] = math.Log(cs[i] / cs[i-1])
	}
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(n)
	ss := 0.0
	for _, r := range rets {
		d := r - mean
		ss += d * d
	}
	return math.Sqrt(ss/float64(n-1)) * math.Sqrt(365), true
}

// rvolSMAPeriod is the volume-average window RVOL compares against.
const rvolSMAPeriod = 20

// RVOL is relative volume: the last bar's volume divided by the SMA20 of
// volume. The SMA window INCLUDES the last bar (simple trailing-20 mean), so
// a lone spike reads slightly under rawVolume/priorAverage. Needs 20 bars
// and non-zero average volume.
func RVOL(bars []marketdata.Bar) (float64, bool) {
	if len(bars) < rvolSMAPeriod {
		return 0, false
	}
	sum := 0.0
	for _, b := range bars[len(bars)-rvolSMAPeriod:] {
		sum += b.Volume
	}
	avg := sum / rvolSMAPeriod
	if avg <= 0 {
		return 0, false
	}
	return bars[len(bars)-1].Volume / avg, true
}

// VWAP is the volume-weighted average price over the trailing n bars:
// sum(typical*volume)/sum(volume), typical=(H+L+C)/3. Needs n bars and
// non-zero total volume.
func VWAP(bars []marketdata.Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n {
		return 0, false
	}
	var pv, v float64
	for _, b := range bars[len(bars)-n:] {
		typ := (b.High + b.Low + b.Close) / 3
		pv += typ * b.Volume
		v += b.Volume
	}
	if v <= 0 {
		return 0, false
	}
	return pv / v, true
}

const (
	// bbPeriod is the Bollinger window (20, population σ, ±2σ bands).
	bbPeriod = 20
	// bbLookback is how many trailing band widths the percentile ranks over.
	bbLookback = 90
)

// BollingerWidthPercentile ranks the current Bollinger band width against
// the widths of the last 90 bar-ending windows. Width = (upper−lower)/middle
// = 4σ/|mean| with the classic 20-period population σ. The rank is a midrank
// percentile in [0,100] — ties share rank, so an all-equal history reads 50
// (normal) instead of a misleading 100. Needs 90+20-1 = 109 bars and
// non-zero window means.
func BollingerWidthPercentile(bars []marketdata.Bar) (float64, bool) {
	if len(bars) < bbLookback+bbPeriod-1 {
		return 0, false
	}
	cs := closes(bars)
	widths := make([]float64, bbLookback)
	for j := range widths {
		end := len(cs) - bbLookback + j + 1
		w := cs[end-bbPeriod : end]
		mean := 0.0
		for _, c := range w {
			mean += c
		}
		mean /= bbPeriod
		if mean == 0 {
			return 0, false
		}
		ss := 0.0
		for _, c := range w {
			d := c - mean
			ss += d * d
		}
		widths[j] = 4 * math.Sqrt(ss/bbPeriod) / math.Abs(mean)
	}
	cur := widths[bbLookback-1]
	less, equal := 0, 0
	for _, w := range widths {
		switch {
		case w < cur:
			less++
		case w == cur:
			equal++
		}
	}
	return 100 * (float64(less) + float64(equal-1)/2) / float64(bbLookback-1), true
}
