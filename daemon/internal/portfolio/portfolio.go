// Package portfolio provides pure correlation and paper-position math for
// SignalDeck's "Correlation & Portfolio" feature.
//
// Everything here is a pure function of its inputs (price series or logged
// positions) and returns results — there is no persistence, no clock, and no
// I/O. Persistence is wired later by the caller.
//
// Honesty notes (this is the brand):
//   - No lookahead. Correlations are computed over the FULL supplied history
//     because a correlation matrix is a descriptive statistic of past
//     co-movement, not a forward prediction. It is not used to make a
//     time-t decision from future data. If a caller wants a rolling/point-in-
//     time correlation for a decision at time t, it must slice each series to
//     index <= t BEFORE calling here — this package never peeks past the data
//     it is given.
//   - PositionPnL and Evaluate grade already-logged reads against a supplied
//     "last price". They are pure accounting of realized/unrealized P&L, not a
//     backtest, so they carry no out-of-sample grade — they measure what your
//     logged discretionary calls actually did.
//   - Costs/assumptions: none of these functions model transaction costs,
//     slippage, financing, dividends, or fees. P&L is gross. Callers that need
//     net P&L must subtract costs themselves. Correlations assume the supplied
//     closes are clean, split/adjustment-consistent, and sampled on a common
//     (daily) cadence; misaligned calendars are only crudely handled by
//     min-length truncation from the tail (see CorrelationMatrix).
package portfolio

import (
	"errors"
	"math"
)

// minAlignedPoints is the minimum number of aligned return observations
// required to compute a correlation. Returns need N+1 prices, so we require
// at least 31 aligned closes. Below this the estimate is too noisy to trust.
const minAlignedPoints = 30

// ErrTooFewSeries is returned when fewer than two series are supplied to
// CorrelationMatrix — a correlation matrix needs at least a pair.
var ErrTooFewSeries = errors.New("portfolio: need at least 2 series")

// ErrTooFewPoints is returned when the aligned overlap across series yields
// fewer than the minimum required return observations.
var ErrTooFewPoints = errors.New("portfolio: need at least 30 aligned return points")

// Series is a single instrument's daily closing prices, oldest to newest.
type Series struct {
	Symbol string
	Closes []float64
}

// Position is a logged paper "read" — a discretionary call recorded with the
// context it was made in (entry price/time, the score at entry, and a note).
type Position struct {
	Symbol       string
	Qty          float64
	EntryPrice   float64
	EntryTs      int64
	Note         string
	ScoreAtEntry float64
}

// PortfolioStat aggregates the P&L of a book of logged positions.
type PortfolioStat struct {
	GrossValue  float64 // sum of |Qty| * lastPrice across positions with a known last price
	TotalPnLAbs float64 // sum of absolute P&L across positions
	TotalPnLPct float64 // total P&L as a fraction of total cost basis
	Winners     int     // positions with strictly positive P&L
	Losers      int     // positions with strictly negative P&L
	Best        string  // symbol with the highest absolute P&L
	Worst       string  // symbol with the lowest absolute P&L
}

// dailyReturns converts a price series into simple daily returns
// r[i] = closes[i+1]/closes[i] - 1. A zero or non-finite prior price yields a
// 0 return for that step so a single bad tick does not poison the whole series.
// The result has len(closes)-1 elements (empty if fewer than 2 closes).
func dailyReturns(closes []float64) []float64 {
	if len(closes) < 2 {
		return nil
	}
	rets := make([]float64, len(closes)-1)
	for i := 0; i+1 < len(closes); i++ {
		prev := closes[i]
		if prev == 0 || math.IsNaN(prev) || math.IsInf(prev, 0) {
			rets[i] = 0
			continue
		}
		rets[i] = closes[i+1]/prev - 1
	}
	return rets
}

// pearson computes the Pearson correlation coefficient of two equal-length
// slices. It returns 0 when either series has zero variance (a flat series has
// no correlation structure to measure) — this is a deliberate, documented
// choice over returning NaN, so downstream matrix math stays finite.
func pearson(a, b []float64) float64 {
	n := len(a)
	if n == 0 || n != len(b) {
		return 0
	}
	var meanA, meanB float64
	for i := 0; i < n; i++ {
		meanA += a[i]
		meanB += b[i]
	}
	meanA /= float64(n)
	meanB /= float64(n)

	var cov, varA, varB float64
	for i := 0; i < n; i++ {
		da := a[i] - meanA
		db := b[i] - meanB
		cov += da * db
		varA += da * da
		varB += db * db
	}
	if varA == 0 || varB == 0 {
		return 0
	}
	r := cov / math.Sqrt(varA*varB)
	// Guard against tiny floating-point overshoot beyond [-1, 1].
	if r > 1 {
		r = 1
	} else if r < -1 {
		r = -1
	}
	return r
}

// CorrelationMatrix computes the Pearson correlation of daily returns across
// every pair of the supplied series.
//
// Alignment: series may differ in length (e.g. different listing dates). All
// series are truncated to the MINIMUM length by keeping the most-recent
// closes (the tail), then converted to returns and correlated over that common
// window. This assumes the series share a common daily calendar and are only
// offset by history length — it does NOT reconcile by timestamp, because
// Series carries no timestamps.
//
// The returned symbols slice is in input order; matrix[i][j] is the
// correlation of series i and j. The matrix is symmetric with a unit diagonal.
//
// It returns ErrTooFewSeries if fewer than 2 series are given, and
// ErrTooFewPoints if the aligned overlap yields fewer than 30 return points.
func CorrelationMatrix(series []Series) (symbols []string, matrix [][]float64, err error) {
	if len(series) < 2 {
		return nil, nil, ErrTooFewSeries
	}

	// Find the common (minimum) close length across all series.
	minLen := len(series[0].Closes)
	for _, s := range series {
		if len(s.Closes) < minLen {
			minLen = len(s.Closes)
		}
	}
	// Need minLen-1 returns >= minAlignedPoints, i.e. minLen >= 31.
	if minLen-1 < minAlignedPoints {
		return nil, nil, ErrTooFewPoints
	}

	symbols = make([]string, len(series))
	rets := make([][]float64, len(series))
	for i, s := range series {
		symbols[i] = s.Symbol
		// Keep the most-recent minLen closes (the tail), then derive returns.
		tail := s.Closes[len(s.Closes)-minLen:]
		rets[i] = dailyReturns(tail)
	}

	n := len(series)
	matrix = make([][]float64, n)
	for i := range matrix {
		matrix[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		matrix[i][i] = 1.0
		for j := i + 1; j < n; j++ {
			r := pearson(rets[i], rets[j])
			matrix[i][j] = r
			matrix[j][i] = r
		}
	}
	return symbols, matrix, nil
}

// MostAndLeastCorrelated scans the off-diagonal of a correlation matrix and
// returns the pair with the highest correlation (mostPair/mostR) and the pair
// with the lowest correlation (leastPair/leastR). The least-correlated pair is
// what actually diversifies a book.
//
// It considers each unordered pair once (i < j). With fewer than two symbols
// there are no pairs, so it returns zero values. Pairs are reported as
// [2]string{symbols[i], symbols[j]}.
func MostAndLeastCorrelated(symbols []string, m [][]float64) (mostPair, leastPair [2]string, mostR, leastR float64) {
	n := len(symbols)
	if n < 2 || len(m) < n {
		return mostPair, leastPair, 0, 0
	}
	mostR = math.Inf(-1)
	leastR = math.Inf(1)
	for i := 0; i < n; i++ {
		if len(m[i]) < n {
			continue
		}
		for j := i + 1; j < n; j++ {
			r := m[i][j]
			if r > mostR {
				mostR = r
				mostPair = [2]string{symbols[i], symbols[j]}
			}
			if r < leastR {
				leastR = r
				leastPair = [2]string{symbols[i], symbols[j]}
			}
		}
	}
	return mostPair, leastPair, mostR, leastR
}

// PositionPnL returns the absolute and percentage profit/loss of a position at
// a given last price. Sign convention: pnlAbs = Qty * (lastPrice - EntryPrice),
// so a negative Qty (short) profits when price falls. pnlPct is measured
// against the position's cost basis |Qty * EntryPrice| and is expressed as a
// fraction (0.10 == +10%). P&L is GROSS — no fees, slippage, or financing.
//
// If the cost basis is zero (EntryPrice or Qty is 0), pnlPct is 0 to avoid a
// division by zero.
func PositionPnL(p Position, lastPrice float64) (pnlAbs, pnlPct float64) {
	pnlAbs = p.Qty * (lastPrice - p.EntryPrice)
	basis := math.Abs(p.Qty * p.EntryPrice)
	if basis == 0 {
		return pnlAbs, 0
	}
	pnlPct = pnlAbs / basis
	return pnlAbs, pnlPct
}

// Evaluate aggregates a book of logged positions against a map of last prices
// (symbol -> price). It grades whether your logged reads actually worked — the
// honesty loop for discretionary calls.
//
// A position whose symbol is absent from last is skipped for value and P&L (an
// unpriceable read cannot be graded) but does not error. Winners are positions
// with strictly positive P&L, Losers strictly negative; break-even positions
// count as neither. Best/Worst are the symbols with the max/min absolute P&L
// among priced positions. TotalPnLPct is total P&L over total cost basis.
// P&L is GROSS of all costs.
func Evaluate(positions []Position, last map[string]float64) PortfolioStat {
	var stat PortfolioStat
	var totalBasis float64
	bestPnL := math.Inf(-1)
	worstPnL := math.Inf(1)

	for _, p := range positions {
		lastPrice, ok := last[p.Symbol]
		if !ok {
			continue
		}
		pnlAbs, _ := PositionPnL(p, lastPrice)

		stat.GrossValue += math.Abs(p.Qty) * lastPrice
		stat.TotalPnLAbs += pnlAbs
		totalBasis += math.Abs(p.Qty * p.EntryPrice)

		switch {
		case pnlAbs > 0:
			stat.Winners++
		case pnlAbs < 0:
			stat.Losers++
		}

		if pnlAbs > bestPnL {
			bestPnL = pnlAbs
			stat.Best = p.Symbol
		}
		if pnlAbs < worstPnL {
			worstPnL = pnlAbs
			stat.Worst = p.Symbol
		}
	}

	if totalBasis != 0 {
		stat.TotalPnLPct = stat.TotalPnLAbs / totalBasis
	}
	return stat
}

// DiversificationScore summarizes a correlation matrix as a single number in
// [0,1]: 1 minus the average ABSOLUTE off-diagonal correlation. Higher means
// better diversified (average pairwise co-movement is weaker). A book of
// identical assets scores 0; a book whose pairwise correlations average zero
// scores 1. Absolute value is used because a strong negative correlation is
// just as much a co-movement relationship as a strong positive one — it is not
// "free" diversification once you account for sign/position direction.
//
// With fewer than two symbols there are no off-diagonal pairs and the score is
// 1 (a single asset trivially has no internal correlation to penalize). The
// result is clamped to [0,1] against floating-point drift.
func DiversificationScore(m [][]float64) float64 {
	n := len(m)
	if n < 2 {
		return 1
	}
	var sum float64
	var count int
	for i := 0; i < n; i++ {
		if len(m[i]) < n {
			continue
		}
		for j := i + 1; j < n; j++ {
			sum += math.Abs(m[i][j])
			count++
		}
	}
	if count == 0 {
		return 1
	}
	score := 1 - sum/float64(count)
	if score < 0 {
		score = 0
	} else if score > 1 {
		score = 1
	}
	return score
}
