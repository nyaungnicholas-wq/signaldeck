// Package ranking scores symbols by CROSS-SECTIONAL relative strength — it
// ranks each symbol against every OTHER symbol in the same universe, not
// against its own history. The output is a percentile (0..100) and an integer
// rank (1 = strongest), so "AAPL is at 92" means "AAPL's momentum beat 92% of
// its peers today", a statement that only exists relative to the peer set.
//
// Every function is pure: metrics/bars in, numbers out. No I/O, no
// persistence, no clock, no network. It depends only on the stdlib and the
// marketdata contract types.
//
// HONESTY NOTES (this is the brand):
//
//   - NO LOOKAHEAD. Everything a symbol contributes at ranking time is derived
//     only from bars up to and including the ranking bar. FromBars computes a
//     Metric at the END of the supplied series using bars[..last]; index i of a
//     rolling value would use only bars[..i]. The ranking itself is a snapshot
//     of one moment across the universe and peeks at no future bar.
//
//   - HONEST OUTPUT. A rank is a description of TODAY's cross-section, never a
//     promise about tomorrow. Whether "strong today" actually leads to higher
//     forward returns is an empirical question, so we expose Spread: pair each
//     symbol's rank score with its realized forward return and measure whether
//     the top cohort out-returns the bottom cohort out-of-sample. A ranking
//     that does not produce a positive spread is not separating winners from
//     losers, and Spread is how you catch that.
package ranking

import (
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Composite blend weights. The composite momentum score is a weighted average
// of the 1-month and 3-month total returns. The 3-month leg is weighted more
// heavily because longer-lookback relative strength is the more persistent
// cross-sectional signal, while the 1-month leg keeps the score responsive to
// recent regime shifts. Weights sum to 1.
const (
	// Weight1M is the composite weight on the ~1-month (21-bar) return.
	Weight1M = 0.4
	// Weight3M is the composite weight on the ~3-month (63-bar) return.
	Weight3M = 0.6
)

// Lookback bar counts. These are approximate trading-day windows: ~21 bars for
// one month, ~63 bars for three months, 200 (or 50 when history is short) for
// the long trend filter.
const (
	// Bars1M is the ~1-month return lookback in trading bars.
	Bars1M = 21
	// Bars3M is the ~3-month return lookback in trading bars.
	Bars3M = 63
	// BarsTrendLong is the preferred long-trend SMA length (~200 trading days).
	BarsTrendLong = 200
	// BarsTrendShort is the fallback trend SMA length used when there are fewer
	// than BarsTrendLong+1 bars available.
	BarsTrendShort = 50
)

// Metric is the per-symbol cross-sectional input to RelativeStrength. It is a
// pre-computed snapshot (typically produced by FromBars) describing one
// symbol's momentum and trend state at a single point in time.
type Metric struct {
	// Symbol is the canonical instrument symbol (e.g. "AAPL", "BTC/USD").
	Symbol string
	// Return1M is the ~1-month (Bars1M) trailing total return, as a fraction
	// (0.05 == +5%).
	Return1M float64
	// Return3M is the ~3-month (Bars3M) trailing total return, as a fraction.
	Return3M float64
	// Above200 reports whether the last close sat above the long-trend SMA
	// (SMA200, or SMA50 when fewer than 200 bars were available).
	Above200 bool
	// Vol is the realized volatility of the series (stdev of bar-to-bar
	// returns), as a fraction. Descriptive only; it does not enter the
	// composite.
	Vol float64
}

// Ranked is one symbol's place in the cross-section produced by
// RelativeStrength.
type Ranked struct {
	// Symbol is the canonical instrument symbol.
	Symbol string
	// Score is the cross-sectional percentile of the composite momentum,
	// 0..100, where 100 is the strongest symbol in the set. With N symbols the
	// weakest scores 0 and the strongest scores 100 (linear rank percentile).
	Score float64
	// Rank is the integer rank, 1 = strongest composite. Ties are broken
	// deterministically by symbol (ascending) so equal-momentum symbols get
	// stable, reproducible ranks.
	Rank int
	// Return1M echoes the input ~1-month return for display.
	Return1M float64
	// Return3M echoes the input ~3-month return for display.
	Return3M float64
	// Momentum is the raw composite score (Weight1M*Return1M +
	// Weight3M*Return3M) before percentile transformation. Kept so callers can
	// see the underlying magnitude, not just the relative rank.
	Momentum float64
}

// composite returns the blended momentum score for a metric.
func composite(m Metric) float64 {
	return Weight1M*m.Return1M + Weight3M*m.Return3M
}

// RelativeStrength ranks the given metrics against EACH OTHER by composite
// momentum and returns them in rank order (Rank 1, the strongest, first).
//
// The composite is Weight1M*Return1M + Weight3M*Return3M. Symbols are sorted by
// composite descending, with a deterministic ascending-symbol tie-break, so
// the result is fully reproducible for any input. Rank is the 1-based position
// in that order. Score is the cross-sectional percentile of the composite:
// with N symbols the weakest gets 0 and the strongest gets 100, spaced
// linearly by rank (Score = 100*(N-Rank)/(N-1)); tied composites still receive
// distinct rank-based percentiles decided by the symbol tie-break. A single
// symbol scores 100. The input slice is not modified.
func RelativeStrength(ms []Metric) []Ranked {
	out := make([]Ranked, len(ms))
	for i, m := range ms {
		out[i] = Ranked{
			Symbol:   m.Symbol,
			Return1M: m.Return1M,
			Return3M: m.Return3M,
			Momentum: composite(m),
		}
	}

	// Strongest first. Deterministic tie-break: equal momentum -> lower symbol
	// string ranks first.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Momentum != out[j].Momentum {
			return out[i].Momentum > out[j].Momentum
		}
		return out[i].Symbol < out[j].Symbol
	})

	n := len(out)
	for i := range out {
		out[i].Rank = i + 1
		if n == 1 {
			out[i].Score = 100
			continue
		}
		// Linear rank percentile: strongest (i==0) -> 100, weakest -> 0.
		out[i].Score = 100 * float64(n-1-i) / float64(n-1)
	}
	return out
}

// FromBars computes a Metric for one symbol from its OHLCV history, using ONLY
// the supplied bars (no lookahead). Returns are measured to the LAST bar's
// close: Return1M over the trailing Bars1M bars and Return3M over the trailing
// Bars3M bars. Above200 compares the last close to the SMA over the last
// BarsTrendLong closes, falling back to BarsTrendShort when fewer than
// BarsTrendLong+1 bars are present. Vol is the standard deviation of
// bar-to-bar simple returns across the whole series.
//
// ok is false (and the Metric zero) when there are too few bars to compute the
// 3-month return — i.e. fewer than Bars3M+1 bars — since a symbol without a
// full 3-month lookback cannot be fairly compared to peers that have one.
func FromBars(symbol string, bars []marketdata.Bar) (Metric, bool) {
	// Need Bars3M+1 closes so the 3-month return has a valid base bar.
	if len(bars) < Bars3M+1 {
		return Metric{}, false
	}
	last := len(bars) - 1
	lastClose := bars[last].Close

	// Guard against a non-positive base price producing a garbage return.
	base1M := bars[last-Bars1M].Close
	base3M := bars[last-Bars3M].Close
	if base1M <= 0 || base3M <= 0 || lastClose <= 0 {
		return Metric{}, false
	}

	m := Metric{
		Symbol:   symbol,
		Return1M: lastClose/base1M - 1,
		Return3M: lastClose/base3M - 1,
		Above200: aboveTrend(bars),
		Vol:      realizedVol(bars),
	}
	return m, true
}

// aboveTrend reports whether the last close is above the trailing trend SMA,
// using the last BarsTrendLong closes when available, else the last
// BarsTrendShort. Uses only bars up to and including the last (no lookahead).
func aboveTrend(bars []marketdata.Bar) bool {
	n := len(bars)
	window := BarsTrendLong
	if n < BarsTrendLong+1 {
		window = BarsTrendShort
	}
	if n < window {
		window = n
	}
	sum := 0.0
	for _, b := range bars[n-window:] {
		sum += b.Close
	}
	sma := sum / float64(window)
	return bars[n-1].Close > sma
}

// realizedVol returns the standard deviation (population) of bar-to-bar simple
// returns over the series. Bars with a non-positive prior close are skipped so
// bad data cannot poison the estimate. Returns 0 when there are too few valid
// return observations.
func realizedVol(bars []marketdata.Bar) float64 {
	rets := make([]float64, 0, len(bars))
	for i := 1; i < len(bars); i++ {
		prev := bars[i-1].Close
		if prev <= 0 {
			continue
		}
		rets = append(rets, bars[i].Close/prev-1)
	}
	if len(rets) < 2 {
		return 0
	}
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	varSum := 0.0
	for _, r := range rets {
		d := r - mean
		varSum += d * d
	}
	return math.Sqrt(varSum / float64(len(rets)))
}

// RankOutcome pairs a symbol's rank score at time t with the realized forward
// return that followed. It is the raw material for the honesty grade: Score is
// the cross-sectional rank score (typically Ranked.Score, 0..100) observed at
// t, and FwdReturn is the return actually realized over the chosen forward
// horizon after t (as a fraction).
type RankOutcome struct {
	// Score is the rank score at time t (0..100 percentile).
	Score float64
	// FwdReturn is the realized forward return after t, as a fraction.
	FwdReturn float64
}

// Spread grades whether the ranking separates winners from losers
// out-of-sample. Given rank-score/forward-return pairs, it forms a top cohort
// (the topPct fraction of outcomes with the HIGHEST scores) and a bottom
// cohort (the bottomPct fraction with the LOWEST scores), then returns the mean
// forward return of each cohort and their difference (top minus bottom).
//
// A positive spread means high-ranked symbols went on to out-return low-ranked
// symbols — the ranking carried real forward information. A spread near zero or
// negative means it did not, no matter how confident the ranks looked. This is
// the out-of-sample honesty check; it makes no forward claim itself, it only
// measures one that was already made.
//
// topPct and bottomPct are fractions in (0,1] (e.g. 0.2 for the top/bottom
// 20%). Each cohort takes at least one outcome. With N outcomes the cohort size
// is max(1, round(pct*N)), then cohorts are shrunk until DISJOINT so no outcome
// is ever counted in both means (with N==1 no disjoint split exists: zeros). Cohorts are selected by sorting a copy on Score
// (input is not modified); ties fall wherever the stable sort places them,
// which does not affect the reported means for well-separated sets. If
// outcomes is empty, or either pct is <= 0, all three results are 0.
func Spread(outcomes []RankOutcome, topPct, bottomPct float64) (topMeanFwd, bottomMeanFwd, spread float64) {
	n := len(outcomes)
	if n == 0 || topPct <= 0 || bottomPct <= 0 {
		return 0, 0, 0
	}

	sorted := make([]RankOutcome, n)
	copy(sorted, outcomes)
	// Ascending by Score: lowest scores first, highest last.
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Score < sorted[j].Score
	})

	topN := cohortSize(topPct, n)
	bottomN := cohortSize(bottomPct, n)
	// Cohorts must be DISJOINT: with small N or generous percentiles the two
	// can overlap in the middle, double-counting outcomes in both means and
	// corrupting the spread. Shrink until they fit, keeping >=1 each.
	for topN+bottomN > n {
		if topN >= bottomN && topN > 1 {
			topN--
		} else if bottomN > 1 {
			bottomN--
		} else {
			// n == 1: a spread needs two disjoint cohorts; report zeros.
			return 0, 0, 0
		}
	}

	// Bottom cohort: the lowest-score outcomes (front of ascending slice).
	bottomSum := 0.0
	for i := 0; i < bottomN; i++ {
		bottomSum += sorted[i].FwdReturn
	}
	bottomMeanFwd = bottomSum / float64(bottomN)

	// Top cohort: the highest-score outcomes (back of ascending slice).
	topSum := 0.0
	for i := n - topN; i < n; i++ {
		topSum += sorted[i].FwdReturn
	}
	topMeanFwd = topSum / float64(topN)

	spread = topMeanFwd - bottomMeanFwd
	return topMeanFwd, bottomMeanFwd, spread
}

// cohortSize returns the number of outcomes in a cohort covering fraction pct
// of n outcomes, rounded to the nearest whole outcome but never fewer than 1
// and never more than n.
func cohortSize(pct float64, n int) int {
	k := int(math.Round(pct * float64(n)))
	if k < 1 {
		k = 1
	}
	if k > n {
		k = n
	}
	return k
}
