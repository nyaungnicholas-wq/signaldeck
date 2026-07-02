package backtest

import "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"

// These are point-in-time indicator helpers: each reads only hist[0..len-1]
// (the last element is "now") and returns the indicator value AT that point.
// The backtester calls them with a growing prefix of the full bar series, so
// by construction they can never see the future. They intentionally duplicate
// the small amount of math the signals package also implements, because this
// package is not allowed to import internal/signals — keeping backtest a pure
// leaf with only stdlib + marketdata dependencies. Conventions (Wilder RSI,
// flat series reads RSI 50) match signals so results are comparable.

// sma is the simple moving average of the last n closes. ok=false when fewer
// than n bars exist yet.
func sma(hist []marketdata.Bar, n int) (float64, bool) {
	if n <= 0 || len(hist) < n {
		return 0, false
	}
	sum := 0.0
	for _, b := range hist[len(hist)-n:] {
		sum += b.Close
	}
	return sum / float64(n), true
}

// rsi is the Wilder-smoothed Relative Strength Index over the closes, computed
// at the last bar. Needs period+1 bars. A dead-flat series (no gains AND no
// losses) reads 50 (neutral) rather than the formula's degenerate 100 — flat
// is not overbought. This mirrors signals.RSI exactly.
func rsi(hist []marketdata.Bar, period int) (float64, bool) {
	if period <= 0 || len(hist) < period+1 {
		return 0, false
	}
	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		if d := hist[i].Close - hist[i-1].Close; d > 0 {
			avgGain += d
		} else {
			avgLoss -= d
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)
	for i := period + 1; i < len(hist); i++ {
		var g, l float64
		if d := hist[i].Close - hist[i-1].Close; d > 0 {
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

// roc is the rate of change close[last]/close[last-n]-1 as a fraction. Needs
// n+1 bars and a non-zero base close.
func roc(hist []marketdata.Bar, n int) (float64, bool) {
	if n <= 0 || len(hist) < n+1 {
		return 0, false
	}
	base := hist[len(hist)-1-n].Close
	if base == 0 {
		return 0, false
	}
	return hist[len(hist)-1].Close/base - 1, true
}
