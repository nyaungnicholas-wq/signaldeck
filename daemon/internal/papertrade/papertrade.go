// Package papertrade is SignalDeck's INTERNAL, SIMULATED paper-trading engine.
//
// It answers one honest question: "if you had actually traded the platform's
// own flagship calibrated prediction — long when it crosses the long threshold,
// flat when it crosses the flat threshold, filling at the NEXT bar's open, and
// paying realistic per-side costs — what would your P&L be?". The output is a
// costed, out-of-sample track record that grades the platform's own signal.
//
// CRITICAL SAFETY: nothing in this package (or the worker that drives it) talks
// to any broker. There is no order API, no key, no network. A "fill" is a pure
// arithmetic event computed from the daemon's own stored bar data. The result is
// a simulated book, clearly labeled as such everywhere it surfaces.
//
// Two honesty rules, identical in spirit to the backtester:
//
//   - NO LOOKAHEAD. The signal is a prediction made at bar-ts P; the fill is the
//     OPEN of the FIRST bar strictly after P. You can never trade on the same
//     bar whose data (or prediction) you are reacting to.
//   - COSTS ARE EXPLICIT. Every entry and every exit pays a per-side cost in
//     basis points of the traded notional (a round trip pays it twice), using
//     the same spread+slippage proxy convention as internal/backtest. Stocks pay
//     more than crypto by default (wider effective spreads on IEX-only fills).
//
// The engine here is PURE: given a decision + a fill price it computes the cash,
// position, and cost deltas; given an equity curve it computes summary stats. It
// performs no I/O and holds no clock — the store and worker own persistence and
// cadence. This keeps every rule unit-testable without a database.
package papertrade

import (
	"math"
	"os"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/envcfg"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// StartingCash is the notional the simulated book begins with (flat, all cash).
// A round number so the equity curve reads as dollars. Overridable via
// SIGNALDECK_PAPER_CASH for experiments; a bad/empty value keeps the default.
const defaultStartingCash = 100_000.0

// StartingCash returns the notional a fresh book starts with.
func StartingCash() float64 {
	return envFloat("SIGNALDECK_PAPER_CASH", defaultStartingCash)
}

// Signal thresholds on the CALIBRATED probability. At/above LongThreshold we
// target a long; at/below FlatThreshold we target flat; strictly between the two
// we HOLD the current position (a deliberate deadband so we don't churn on noise
// around 0.5, which would bleed the book dry on costs). Overridable via env.
func LongThreshold() float64 { return envFloat("SIGNALDECK_PAPER_LONG", 0.60) }
func FlatThreshold() float64 { return envFloat("SIGNALDECK_PAPER_FLAT", 0.40) }

// MaxPositions bounds how many symbols the book can hold at once and, with it,
// the per-position budget: each new entry targets equity/MaxPositions dollars
// (clamped to the cash actually on hand). Without this, an all-in first entry
// would starve every later signal — the book could only ever hold ONE name.
// Env-overridable; default 10 (a diversified but not fragmented slice).
func MaxPositions() int {
	if v := int(envFloat("SIGNALDECK_PAPER_MAX_POSITIONS", 10)); v > 0 {
		return v
	}
	return 10
}

// PositionBudget is the dollar slice a new entry should target: equity split
// across MaxPositions, but never more than the cash on hand (so the book stays
// funded and never negative). equity is the book's current total value; cash is
// the uninvested portion available to deploy right now.
func PositionBudget(equity, cash float64) float64 {
	slice := equity / float64(MaxPositions())
	if slice > cash {
		slice = cash
	}
	if slice < 0 {
		slice = 0
	}
	return slice
}

// CostBpsFor returns the per-SIDE HALF-SPREAD in basis points for a market's
// fills, consistent with internal/backtest's per-side CostBps convention.
// Defaults sit in the middle of the ranges the design calls for (stocks
// ~5-10bps, crypto ~2-5bps): stocks 7.5bps, crypto 3.5bps. Both are
// env-overridable so the cost assumption is explicit and tunable, never hidden.
//
// This is no longer the WHOLE cost of a fill. It is the spread component; the
// size-dependent market-impact component is added by the execution model (see
// execution.go), because a flat constant is a cost assumption a fill can beat,
// and the live book's fills were beating it.
func CostBpsFor(market md.Market) float64 {
	if market == md.Crypto {
		return envFloat("SIGNALDECK_PAPER_COST_CRYPTO_BPS", 3.5)
	}
	return envFloat("SIGNALDECK_PAPER_COST_STOCK_BPS", 7.5)
}

// Target is the desired position state for a symbol given its latest calibrated
// prediction, under the deadband rule above.
type Target int

const (
	Hold   Target = iota // strictly between the thresholds — keep current state
	GoLong               // cal_prob >= LongThreshold
	GoFlat               // cal_prob <= FlatThreshold
)

// DecideTarget maps a calibrated probability to a desired position state.
//
//	cal >= LongThreshold -> GoLong
//	cal <= FlatThreshold -> GoFlat
//	otherwise            -> Hold (deadband)
//
// The thresholds are read fresh so an env override takes effect without a
// rebuild. GoLong wins ties at exactly LongThreshold; GoFlat wins ties at
// exactly FlatThreshold (the thresholds cannot overlap: FLAT < LONG by config).
func DecideTarget(cal float64) Target {
	if cal >= LongThreshold() {
		return GoLong
	}
	if cal <= FlatThreshold() {
		return GoFlat
	}
	return Hold
}

// Fill, EnterLong and ExitLong live in execution.go: pricing a fill is no
// longer "the bar open times a constant", it is a spread + impact + capacity
// model, and it earns its own file.

// EquityPoint is one mark on the simulated equity curve.
type EquityPoint struct {
	Ts             int64   `json:"ts"`
	Cash           float64 `json:"cash"`
	PositionsValue float64 `json:"positionsValue"`
	Equity         float64 `json:"equity"`
}

// Summary is the costed track-record readout for one strategy. Every field is
// GATED: a statistic that the sample can't support is flagged so the UI shows
// "n/a" rather than a fabricated number — the same honesty discipline as the
// backtester's annualization guards.
type Summary struct {
	StartEquity float64 `json:"startEquity"`
	LastEquity  float64 `json:"lastEquity"`
	TotalReturn float64 `json:"totalReturn"` // lastEquity/startEquity - 1
	MaxDrawdown float64 `json:"maxDrawdown"` // worst peak-to-trough of the equity curve, positive fraction

	Sharpe      float64 `json:"sharpe"`      // annualized per-mark Sharpe (rf=0) — only when SharpeValid
	SharpeValid bool    `json:"sharpeValid"` // needs >= minSharpeMarks equity marks

	WinRate      float64 `json:"winRate"`      // fraction of CLOSED round-trips that were net-positive
	WinRateValid bool    `json:"winRateValid"` // only when ClosedTrades >= minWinRateTrades
	ClosedTrades int     `json:"closedTrades"` // completed round-trips (a sell closing a prior buy)

	Turnover  float64 `json:"turnover"`  // total traded notional / starting equity (round-trip churn proxy)
	NumFills  int     `json:"numFills"`  // total buy+sell fills
	SpanYears float64 `json:"spanYears"` // calendar span of the equity curve, years
}

// Gating thresholds. Below these, the corresponding statistic is withheld
// (Valid=false) because it would misrepresent skill on a thin sample.
const (
	minSharpeMarks   = 5 // need a handful of equity marks before a Sharpe means anything
	minWinRateTrades = 5 // a "win rate" over <5 round-trips is not a statistic
)

const secondsPerYear = 365.25 * 24 * 3600

// Trade is the minimal closed-round-trip record the summary needs: whether a
// completed buy->sell round trip made money, plus the notional it traded (for
// turnover). The store builds these from the trade log.
type Trade struct {
	Won      bool    // sell proceeds (net of both-side costs) exceeded buy outlay
	Notional float64 // absolute notional of the fill (for turnover)
}

// Summarize computes the costed track-record stats from an equity curve and the
// realized round-trips. curve must be ascending by ts. closed are completed
// round-trips (for win rate); numFills / tradedNotional cover ALL fills (open +
// close) for turnover. It never annualizes a span it can't support.
func Summarize(curve []EquityPoint, closed []Trade, numFills int, tradedNotional float64) Summary {
	var s Summary
	s.NumFills = numFills
	s.ClosedTrades = len(closed)
	if len(curve) == 0 {
		return s
	}
	s.StartEquity = curve[0].Equity
	s.LastEquity = curve[len(curve)-1].Equity
	if s.StartEquity > 0 {
		s.TotalReturn = s.LastEquity/s.StartEquity - 1
		s.Turnover = tradedNotional / s.StartEquity
	}
	s.MaxDrawdown = maxDrawdown(curve)

	// Win rate over closed round-trips only.
	wins := 0
	for _, t := range closed {
		if t.Won {
			wins++
		}
	}
	if s.ClosedTrades > 0 {
		s.WinRate = float64(wins) / float64(s.ClosedTrades)
	}
	s.WinRateValid = s.ClosedTrades >= minWinRateTrades

	// Sharpe over per-mark equity returns, annualized by the inferred mark
	// cadence. Withheld below minSharpeMarks marks.
	rets := equityReturns(curve)
	if len(curve) >= minSharpeMarks && len(rets) >= 2 {
		mpy := marksPerYear(curve)
		s.Sharpe = sharpe(rets, mpy)
		s.SharpeValid = true
	}

	s.SpanYears = spanYears(curve)
	return s
}

// equityReturns is the per-mark simple return series of the equity curve.
func equityReturns(curve []EquityPoint) []float64 {
	out := make([]float64, 0, len(curve)-1)
	for i := 1; i < len(curve); i++ {
		prev := curve[i-1].Equity
		if prev > 0 {
			out = append(out, curve[i].Equity/prev-1)
		} else {
			out = append(out, 0)
		}
	}
	return out
}

// maxDrawdown is the worst peak-to-trough decline of the equity curve as a
// positive fraction. A monotonically rising curve returns 0.
func maxDrawdown(curve []EquityPoint) float64 {
	peak := math.Inf(-1)
	worst := 0.0
	for _, p := range curve {
		if p.Equity > peak {
			peak = p.Equity
		}
		if peak > 0 {
			dd := 1 - p.Equity/peak
			if dd > worst {
				worst = dd
			}
		}
	}
	return worst
}

// sharpe is mean/stdev of the return series annualized by sqrt(marksPerYear),
// rf=0, sample stdev (n-1). A constant or empty series returns 0.
func sharpe(rets []float64, marksPerYear float64) float64 {
	if len(rets) < 2 {
		return 0
	}
	if marksPerYear <= 0 {
		marksPerYear = 252
	}
	var sum float64
	for _, r := range rets {
		sum += r
	}
	mean := sum / float64(len(rets))
	var ss float64
	for _, r := range rets {
		d := r - mean
		ss += d * d
	}
	variance := ss / float64(len(rets)-1)
	if variance <= 0 {
		return 0
	}
	return mean / math.Sqrt(variance) * math.Sqrt(marksPerYear)
}

// spanYears is the calendar span of the equity curve in years.
func spanYears(curve []EquityPoint) float64 {
	if len(curve) < 2 {
		return 0
	}
	span := curve[len(curve)-1].Ts - curve[0].Ts
	if span <= 0 {
		return 0
	}
	return float64(span) / secondsPerYear
}

// marksPerYear infers how many equity marks fit in a year from the MEDIAN
// spacing of the curve timestamps (median so gaps/weekends don't distort it).
// Falls back to 252 (daily) when the spacing is unusable.
func marksPerYear(curve []EquityPoint) float64 {
	const fallback = 252.0
	if len(curve) < 2 {
		return fallback
	}
	gaps := make([]int64, 0, len(curve)-1)
	for i := 1; i < len(curve); i++ {
		g := curve[i].Ts - curve[i-1].Ts
		if g > 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return fallback
	}
	// median without a full sort dependency: simple insertion is fine for small
	// curves, but use a copy-sort for clarity.
	sortInt64(gaps)
	med := gaps[len(gaps)/2]
	if med <= 0 {
		return fallback
	}
	const day = int64(86400)
	if med >= day {
		return 252.0 * (float64(day) / float64(med))
	}
	const sessionSecs = 6.5 * 3600
	return (sessionSecs / float64(med)) * 252.0
}

func sortInt64(a []int64) {
	// insertion sort — curves are short and this avoids an import just for sort.
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && a[j] > v {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}

// envFloat reads a positive float from env, falling back on empty/invalid/<=0.
func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		switch {
		case err != nil:
			envcfg.Reject(key, v, "not a number", strconv.FormatFloat(def, 'g', -1, 64))
		case f <= 0:
			envcfg.Reject(key, v, "must be > 0", strconv.FormatFloat(def, 'g', -1, 64))
		default:
			return f
		}
	}
	return def
}
