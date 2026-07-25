// Package moneymetrics is the PURE "money scoreboard" engine (MONEY SCOREBOARD
// wave). It scores a set of realized per-trade returns by EXPECTED PROFIT —
// expectancy, profit factor, and the average-win-vs-average-loss payoff — rather
// than by win rate, and it exists to make one uncomfortable truth impossible to
// hide: WIN RATE ALONE DOES NOT EQUAL PROFIT.
//
// A strategy can win 80% of the time and still bleed money if the 20% of losers
// are large enough (the canonical +1 ×8 / −5 ×2 case has an 80% win rate and a
// NEGATIVE expectancy). Conversely a 40%-win strategy with big winners and small
// losers is profitable. Expectancy — the average profit per trade AFTER costs —
// is the number that actually decides whether a track record makes money, so it
// leads every surface that renders these metrics; win rate is kept but demoted.
//
// The caller passes COST-ADJUSTED returns (net of fees/slippage); this package
// does no I/O and knows nothing about costs — it only does the arithmetic, so
// every figure is unit-testable in isolation.
package moneymetrics

// meaningfulMinTrades is the sample floor below which the scoreboard is flagged
// not-yet-Meaningful: a handful of trades can show any expectancy by luck, so the
// caller withholds a profitability CLAIM until at least this many resolutions.
const meaningfulMinTrades = 20

// Money is the expected-profit scoreboard over a set of realized trade returns.
// Expectancy leads (average profit per trade after costs); win rate is present
// but deliberately NOT the headline. ProfitFactor / PayoffRatio carry validity
// flags because they are undefined when there are no losing trades — an honest
// "n/a" instead of a fabricated +∞.
type Money struct {
	Trades       int     `json:"trades"`
	WinRate      float64 `json:"winRate"`      // wins / trades — descriptive, NOT profitability
	Expectancy   float64 `json:"expectancy"`   // mean return per trade (net) — the number that matters
	ProfitFactor float64 `json:"profitFactor"` // Σwins / Σ|losses| — valid only when ProfitFactorValid
	AvgWin       float64 `json:"avgWin"`       // mean of the winning returns
	AvgLoss      float64 `json:"avgLoss"`      // mean of the |losing returns| (a positive magnitude)
	PayoffRatio  float64 `json:"payoffRatio"`  // AvgWin / AvgLoss — valid only when PayoffRatioValid
	GrossWin     float64 `json:"grossWin"`     // Σ winning returns
	GrossLoss    float64 `json:"grossLoss"`    // Σ |losing returns|
	// Validity flags for the ratios that divide by the loss side. False = no
	// losing trades in the sample, so the ratio is undefined (report null, never
	// a fabricated infinity).
	ProfitFactorValid bool `json:"profitFactorValid"`
	PayoffRatioValid  bool `json:"payoffRatioValid"`
	// Meaningful is false below meaningfulMinTrades: the arithmetic is still
	// exact, but the sample is too thin to CLAIM the expectancy is real.
	Meaningful bool `json:"meaningful"`
}

// FromReturns computes the expected-profit scoreboard from realized per-trade
// returns. The caller passes returns already NET of costs. A zero return counts
// as a trade (in Trades and the WinRate denominator) but as neither a win nor a
// loss — it is a break-even, not a winner. ProfitFactor / PayoffRatio are only
// marked valid when the sample has at least one losing trade.
func FromReturns(returns []float64) Money {
	m := Money{Trades: len(returns)}
	if len(returns) == 0 {
		return m
	}

	var sum, grossWin, grossLoss float64
	wins, losses := 0, 0
	for _, r := range returns {
		sum += r
		switch {
		case r > 0:
			wins++
			grossWin += r
		case r < 0:
			losses++
			grossLoss += -r // accumulate a positive loss magnitude
		}
	}

	m.Expectancy = sum / float64(len(returns))
	m.WinRate = float64(wins) / float64(len(returns))
	m.GrossWin = grossWin
	m.GrossLoss = grossLoss
	if wins > 0 {
		m.AvgWin = grossWin / float64(wins)
	}
	if losses > 0 {
		m.AvgLoss = grossLoss / float64(losses)
	}
	// Ratios that divide by the loss side are only defined with a loss present.
	if grossLoss > 0 {
		m.ProfitFactor = grossWin / grossLoss
		m.ProfitFactorValid = true
	}
	if m.AvgLoss > 0 {
		m.PayoffRatio = m.AvgWin / m.AvgLoss
		m.PayoffRatioValid = true
	}
	m.Meaningful = m.Trades >= meaningfulMinTrades
	return m
}
