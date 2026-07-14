// Package stratlib is the STRATEGY LAB's library of classic PUBLISHED trading
// strategies, each a pure deterministic function that turns a daily bar
// series into a long/flat position series for the bias-free backtest engine
// (backtest.BacktestPositions).
//
// Contract (the no-lookahead guarantee):
//
//   - Positions(bars)[i] is computed ONLY from bars[0..i] (indices <= i).
//     Appending future bars can never change an earlier position — asserted
//     in tests by recomputing on extended series.
//   - +1 = want long for the NEXT bar, 0 = want flat. All eight classics are
//     long/flat (the engine takes no shorts); a strategy that cannot evaluate
//     yet (warm-up window) holds its previous position, starting flat.
//   - No I/O, no clock, no randomness — bars in, ints out.
//
// HONESTY: these are published, widely known rules replayed on our own bars
// with costs — in-sample history, not live performance and not advice; a
// strategy is only as good as its next trade.
package stratlib

import (
	"math"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Strategy is one named classic with its published origin and its pure
// position generator.
type Strategy struct {
	Name string
	Doc  string // published origin, cited
	// Positions returns one desired position per bar (+1 long / 0 flat),
	// each computed only from bars[0..i].
	Positions func(bars []md.Bar) []int
}

// All returns the eight classics in a fixed, deterministic order.
func All() []Strategy {
	return []Strategy{
		{
			Name: "sma_cross_50_200",
			Doc:  "Golden cross: long while SMA(50) >= SMA(200) (classic moving-average crossover; e.g. Brock, Lakonishok & LeBaron 1992)",
			Positions: func(bars []md.Bar) []int {
				return holdWhile(bars, func(i int) (bool, bool) {
					f, okf := smaAt(bars, i, 50)
					s, oks := smaAt(bars, i, 200)
					return f >= s, okf && oks
				})
			},
		},
		{
			Name: "donchian_20",
			Doc:  "Donchian 20-day channel breakout, 10-day exit (Richard Donchian; the Turtle trading rule)",
			Positions: donchianPositions,
		},
		{
			Name: "rsi2_meanrev",
			Doc:  "Connors RSI-2 mean reversion: long when RSI(2) < 10, exit when RSI(2) > 90 (Connors & Alvarez, 'Short Term Trading Strategies That Work')",
			Positions: rsi2Positions,
		},
		{
			Name: "momentum_12_1",
			Doc:  "12-1 cross-sectional momentum applied time-series: long when the trailing 12-month return skipping the last month is positive (Jegadeesh & Titman 1993)",
			Positions: func(bars []md.Bar) []int {
				return holdWhile(bars, func(i int) (bool, bool) {
					if i < 252 || bars[i-252].Close == 0 {
						return false, false
					}
					return bars[i-21].Close/bars[i-252].Close-1 > 0, true
				})
			},
		},
		{
			Name: "macd_trend",
			Doc:  "MACD trend: long while MACD(12,26) is above its 9-period signal line (Gerald Appel)",
			Positions: macdPositions,
		},
		{
			Name: "bollinger_meanrev",
			Doc:  "Bollinger mean reversion: long when close < lower band (20, 2σ), hold until close >= the 20-day middle band (John Bollinger)",
			Positions: bollingerPositions,
		},
		{
			Name: "breakout_52w_high",
			Doc:  "52-week-high breakout: long on a new 252-day closing high, exit when close falls below SMA(50) (George & Hwang 2004, '52-week high and momentum investing')",
			Positions: breakout52wPositions,
		},
		{
			Name: "dual_momentum",
			Doc:  "Absolute momentum leg of dual momentum: long while the trailing 12-month return is positive (Antonacci, 'Dual Momentum Investing'; single-asset absolute-momentum form)",
			Positions: func(bars []md.Bar) []int {
				return holdWhile(bars, func(i int) (bool, bool) {
					if i < 252 || bars[i-252].Close == 0 {
						return false, false
					}
					return bars[i].Close/bars[i-252].Close-1 > 0, true
				})
			},
		},
	}
}

// holdWhile builds a stateless long/flat series: cond(i) evaluated at each
// bar from bars[0..i] only; ok=false (warm-up) holds the previous position
// (starting flat).
func holdWhile(bars []md.Bar, cond func(i int) (bool, bool)) []int {
	pos := make([]int, len(bars))
	for i := range bars {
		want, ok := cond(i)
		switch {
		case !ok && i > 0:
			pos[i] = pos[i-1]
		case ok && want:
			pos[i] = 1
		}
	}
	return pos
}

// smaAt is the simple moving average of the n closes ending at index i.
func smaAt(bars []md.Bar, i, n int) (float64, bool) {
	if i+1 < n {
		return 0, false
	}
	sum := 0.0
	for j := i - n + 1; j <= i; j++ {
		sum += bars[j].Close
	}
	return sum / float64(n), true
}

// donchianPositions: enter when close exceeds the highest HIGH of the PRIOR
// 20 bars (index i uses bars[i-20..i-1] — never bar i itself); exit when
// close drops below the lowest LOW of the prior 10 bars.
func donchianPositions(bars []md.Bar) []int {
	pos := make([]int, len(bars))
	long := false
	for i := range bars {
		if i >= 20 {
			hi := bars[i-20].High
			for j := i - 19; j < i; j++ {
				if bars[j].High > hi {
					hi = bars[j].High
				}
			}
			lo := bars[i-10].Low
			for j := i - 9; j < i; j++ {
				if bars[j].Low < lo {
					lo = bars[j].Low
				}
			}
			if !long && bars[i].Close > hi {
				long = true
			} else if long && bars[i].Close < lo {
				long = false
			}
		}
		if long {
			pos[i] = 1
		}
	}
	return pos
}

// rsiAt is Wilder's RSI over `period` closes ending at index i.
func rsiAt(bars []md.Bar, i, period int) (float64, bool) {
	if i < period {
		return 0, false
	}
	// Wilder smoothing seeded from the first `period` changes, then smoothed
	// through bar i — uses bars[0..i] only.
	var avgGain, avgLoss float64
	for j := 1; j <= period; j++ {
		d := bars[j].Close - bars[j-1].Close
		if d > 0 {
			avgGain += d
		} else {
			avgLoss -= d
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)
	for j := period + 1; j <= i; j++ {
		d := bars[j].Close - bars[j-1].Close
		gain, loss := 0.0, 0.0
		if d > 0 {
			gain = d
		} else {
			loss = -d
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
	}
	if avgLoss == 0 {
		return 100, true
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs), true
}

// rsi2Positions: Connors RSI-2 — long when RSI(2)<10, exit when RSI(2)>90.
func rsi2Positions(bars []md.Bar) []int {
	pos := make([]int, len(bars))
	long := false
	for i := range bars {
		if v, ok := rsiAt(bars, i, 2); ok {
			if !long && v < 10 {
				long = true
			} else if long && v > 90 {
				long = false
			}
		}
		if long {
			pos[i] = 1
		}
	}
	return pos
}

// macdPositions: long while MACD(12,26) > signal(9). EMAs are seeded from
// the first close and updated bar by bar — strictly causal.
func macdPositions(bars []md.Bar) []int {
	pos := make([]int, len(bars))
	if len(bars) == 0 {
		return pos
	}
	const warmup = 26 + 9 // don't trust the seeded EMAs before this
	k12, k26, k9 := 2.0/13, 2.0/27, 2.0/10
	e12, e26 := bars[0].Close, bars[0].Close
	sig := 0.0
	for i := range bars {
		if i > 0 {
			e12 += k12 * (bars[i].Close - e12)
			e26 += k26 * (bars[i].Close - e26)
		}
		macd := e12 - e26
		sig += k9 * (macd - sig)
		if i < warmup {
			continue
		}
		if macd > sig {
			pos[i] = 1
		}
	}
	return pos
}

// bollingerPositions: long when close < SMA(20) - 2*stddev(20); hold until
// close >= SMA(20) (the middle band).
func bollingerPositions(bars []md.Bar) []int {
	pos := make([]int, len(bars))
	long := false
	for i := range bars {
		mid, ok := smaAt(bars, i, 20)
		if ok {
			var ss float64
			for j := i - 19; j <= i; j++ {
				d := bars[j].Close - mid
				ss += d * d
			}
			sd := math.Sqrt(ss / 20)
			c := bars[i].Close
			if !long && c < mid-2*sd {
				long = true
			} else if long && c >= mid {
				long = false
			}
		}
		if long {
			pos[i] = 1
		}
	}
	return pos
}

// breakout52wPositions: enter on a new 252-day closing high (close > max
// close of the prior 252 bars); exit when close < SMA(50).
func breakout52wPositions(bars []md.Bar) []int {
	pos := make([]int, len(bars))
	long := false
	for i := range bars {
		if i >= 252 {
			hi := bars[i-252].Close
			for j := i - 251; j < i; j++ {
				if bars[j].Close > hi {
					hi = bars[j].Close
				}
			}
			if !long && bars[i].Close > hi {
				long = true
			} else if long {
				if s, ok := smaAt(bars, i, 50); ok && bars[i].Close < s {
					long = false
				}
			}
		}
		if long {
			pos[i] = 1
		}
	}
	return pos
}
