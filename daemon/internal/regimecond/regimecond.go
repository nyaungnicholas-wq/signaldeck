// Package regimecond turns a bar series into regime-conditioned forward-return
// statistics — the honest sentence "in a DOWNTREND, this symbol's next <horizon>
// return was X% (n=..)". It is the marriage of the regime LABEL (from
// internal/regime) with the realized forward RETURN that followed each state.
//
// The pitch is a measured tendency with sample size attached, never a point
// forecast: "when this market was labelled uptrend, over the next fwdBars the
// close moved by this much on average, this often positive, across this many
// historical occurrences." Regimes end, so the past distribution is evidence,
// not a promise about the next bar.
//
// Every function is pure: bars in, stats out. It depends only on the stdlib,
// the marketdata contract types, and internal/regime. No store, no llm, no I/O.
//
// HONESTY NOTES:
//
//   - NO LOOKAHEAD. The regime label at index i is computed from bars[:i+1]
//     only (via regime.Classify), exactly as it could have been known in real
//     time at bar i. The forward return uses close[i+fwdBars] — a FUTURE bar —
//     but only as the realized OUTCOME being measured, never as an input to the
//     label. Feature and outcome never touch: the classifier cannot see the
//     future it is being graded against.
//
//   - IT IS A LABEL-CONDITIONED DISTRIBUTION, NOT A SIGNAL. Cond reports what
//     happened after a regime historically; it does not rank or recommend. Small
//     N (we require >= 5 to report at all) and wide dispersion are the norm, and
//     the caller should treat MeanFwd/MedianFwd/HitRate as a coarse tendency.
package regimecond

import (
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/regime"
)

// MinN is the minimum number of forward-return observations a regime bucket
// must have before Build reports it. Buckets with fewer occurrences are dropped
// as too small to say anything honest about.
const MinN = 5

// Cond is a regime-conditioned forward-return statistic for one regime label:
// across every historical bar classified into Regime, the distribution of the
// return realized over the next fwdBars.
type Cond struct {
	// Regime is the regime label these stats are conditioned on (e.g.
	// "uptrend", "downtrend", "range", "squeeze").
	Regime string `json:"regime"`
	// N is the number of forward-return observations in this bucket.
	N int `json:"n"`
	// MeanFwd is the arithmetic mean forward return (fraction, e.g. 0.012 = +1.2%).
	MeanFwd float64 `json:"meanFwd"`
	// MedianFwd is the median forward return (fraction).
	MedianFwd float64 `json:"medianFwd"`
	// HitRate is the fraction of observations whose forward return was > 0.
	HitRate float64 `json:"hitRate"`
}

// ForBars maps a forward Horizon to the number of daily bars forward used for
// its forward-return window: H1d -> 1, H1w -> 5 (five trading days). Any other
// horizon (including H1h, which is not a daily-bar horizon) returns 0, which
// callers should treat as "skip": Build with fwdBars <= 0 produces no stats.
func ForBars(h marketdata.Horizon) int {
	switch h {
	case marketdata.H1d:
		return 1
	case marketdata.H1w:
		return 5
	default:
		return 0
	}
}

// Build walks the daily bar series and, at each bar from the regime warmup
// through the last bar that still has fwdBars bars ahead of it, classifies the
// regime from history and records the forward return realized over the next
// fwdBars bars. It buckets observations by regime label and returns, per label,
// the count, mean, median and hit-rate — including only labels with N >= MinN.
//
// The forward return at index i is close[i+fwdBars]/close[i]-1. The regime at i
// is regime.Classify(daily[:i+1]) — bars up to and including i only. Because the
// classifier never sees the future close it is graded against, there is NO
// LOOKAHEAD.
//
// Edge cases (no panic): if fwdBars <= 0, or there are too few bars to form even
// one observation (len < regime.MinBars + fwdBars + 1 effectively, i.e. the walk
// range is empty), or the base close at i is non-positive, Build returns an empty
// (non-nil) map. Bars whose regime cannot be classified are skipped. The result
// is deterministic for a given input.
func Build(daily []marketdata.Bar, fwdBars int) map[string]Cond {
	out := make(map[string]Cond)
	if fwdBars <= 0 {
		return out
	}

	// Accumulate forward returns per regime label. We walk i from the regime
	// warmup (regime.MinBars-1, the first index Classify can label) up to the
	// last index that still has a bar fwdBars ahead (len-fwdBars-1).
	buckets := make(map[string][]float64)
	for i := regime.MinBars - 1; i <= len(daily)-fwdBars-1; i++ {
		base := daily[i].Close
		if base <= 0 {
			continue
		}
		st, ok := regime.Classify(daily[:i+1]) // features: bars[..i] only, no lookahead.
		if !ok {
			continue
		}
		fwd := daily[i+fwdBars].Close/base - 1 // outcome: future close, not a feature.
		label := string(st.Label)
		buckets[label] = append(buckets[label], fwd)
	}

	for label, rets := range buckets {
		if len(rets) < MinN {
			continue
		}
		out[label] = Cond{
			Regime:    label,
			N:         len(rets),
			MeanFwd:   mean(rets),
			MedianFwd: median(rets),
			HitRate:   hitRate(rets),
		}
	}
	return out
}

// Current returns the regime label as of the LAST bar in daily, classified from
// the full series (regime.Classify on all bars, no lookahead). ok is false when
// there are too few bars to classify (fewer than regime.MinBars).
func Current(daily []marketdata.Bar) (regimeLabel string, ok bool) {
	st, cls := regime.Classify(daily)
	if !cls {
		return "", false
	}
	return string(st.Label), true
}

// mean returns the arithmetic mean of xs. xs is assumed non-empty (callers gate
// on MinN); an empty slice returns 0.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// median returns the median of xs. It sorts a copy (leaving the caller's slice
// untouched) and averages the two middle values for even lengths. An empty slice
// returns 0.
func median(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	s := make([]float64, n)
	copy(s, xs)
	sort.Float64s(s)
	mid := n / 2
	if n%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// hitRate returns the fraction of xs strictly greater than zero. An empty slice
// returns 0.
func hitRate(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var hits int
	for _, x := range xs {
		if x > 0 {
			hits++
		}
	}
	return float64(hits) / float64(len(xs))
}
