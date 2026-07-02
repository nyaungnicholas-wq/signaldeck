// Package risklens turns a portfolio's daily price history into plain risk
// numbers — Value-at-Risk, per-holding risk contributions, and historical
// stress scenarios — plus a plain-English summary.
//
// Every function is pure: closes/holdings in, numbers out. No I/O, no
// persistence, no clock, no network. It depends only on the stdlib and the
// marketdata contract types.
//
// HONESTY NOTES (this is the brand):
//   - NO LOOKAHEAD. Everything here is a descriptive, backward-looking measure
//     over the supplied history. A VaR or stress number at the end of a series
//     uses only the returns in that series (indices <= last). We make no
//     forward forecast; VaR is "given this history, a day this bad or worse
//     happened (1-confidence) of the time", not "tomorrow will be X".
//   - OUT-OF-SAMPLE GRADE. These are IN-SAMPLE risk statistics computed on the
//     same window they describe. They are NOT out-of-sample predictions and
//     carry no OOS grade because they make no prediction — they summarize the
//     past. Realized future losses can and do exceed historical VaR (fat tails,
//     regime change). Treat every number as "what the recent past would have
//     done", never a guarantee.
//   - COSTS/ASSUMPTIONS. Returns are simple (arithmetic) daily close-to-close.
//     No trading costs, slippage, dividends, or intraday risk are modeled —
//     close-to-close only. Weights are assumed constant over the window (no
//     rebalancing drift). ParametricVaR additionally assumes normally
//     distributed returns, which understates tail risk for real markets.
package risklens

import "errors"

// Errors returned by the portfolio-level functions.
var (
	// ErrNoHoldings is returned when the holdings slice is empty.
	ErrNoHoldings = errors.New("risklens: no holdings")
	// ErrNoSeries is returned when the series slice is empty.
	ErrNoSeries = errors.New("risklens: no series")
	// ErrMissingSeries is returned when a holding has no matching price series.
	ErrMissingSeries = errors.New("risklens: holding has no matching series")
	// ErrShortSeries is returned when a series has fewer than MinCloses closes.
	ErrShortSeries = errors.New("risklens: series too short (need >= 60 closes)")
	// ErrUnequalLength is returned when series differ in length (caller must align).
	ErrUnequalLength = errors.New("risklens: series lengths differ (caller must align)")
	// ErrZeroWeight is returned when holding weights sum to zero and cannot be normalized.
	ErrZeroWeight = errors.New("risklens: holding weights sum to zero")
)

// MinCloses is the minimum number of aligned closes required per series so the
// resulting return sample (MinCloses-1) is large enough to be meaningful.
const MinCloses = 60

// Holding is one position as a fraction of the portfolio. Weights across
// holdings should sum to ~1; helpers normalize when they do not.
type Holding struct {
	Symbol string
	Weight float64
}

// Series is one holding's aligned daily closes, oldest first, newest last.
// All series passed together must share the same length; the caller aligns
// them by date and this package validates equal length.
type Series struct {
	Symbol string
	Closes []float64
}

// Contribution is one holding's share of total portfolio variance. PctOfRisk
// values across holdings sum to ~100 (they are normalized). Weight is the
// (normalized) portfolio weight and Vol is the holding's own daily-return
// standard deviation over the window.
type Contribution struct {
	Symbol    string
	PctOfRisk float64 // percent of portfolio variance, sums to ~100
	Weight    float64
	Vol       float64 // daily return stdev (sample)
}

// Scenario is one stress test's estimated portfolio profit/loss. PnLPct is the
// portfolio return under the shock (negative = loss). Detail explains the shock
// and its assumptions.
type Scenario struct {
	Name   string
	PnLPct float64
	Detail string
}

// Report bundles a full RiskLens run for a portfolio so Summary can render it.
// Confidence is the VaR confidence used (e.g. 0.95). NotionalUSD is the
// portfolio size used to translate percentages into dollars in the summary
// (e.g. 100000 for "per $100k"); zero defaults to 100000.
type Report struct {
	Confidence    float64
	NotionalUSD   float64
	HistVaRPct    float64
	HistCVaRPct   float64
	ParamVaRPct   float64
	Contributions []Contribution
	Scenarios     []Scenario
}
