// Measured candlestick edge — the honest "does this shape ever pay?" layer,
// mirroring internal/expectancy's measured-outcome discipline: walk the
// symbol's OWN history, and for every bar where a pattern fired, record the
// realized forward K-bar return. The output is a hit rate (share of moves that
// went the pattern's way) with the sample size attached — a measured tendency
// on this instrument, never a forecast.
//
// NO LOOKAHEAD: the pattern at bar i is detected from bars[..i] (Detect reads
// its own prior-trend window backwards), and the outcome is close[i+K]/
// close[i]-1 — strictly forward. A firing whose outcome bar does not yet exist
// (i+K past the end) is not counted.
//
// NEUTRAL PATTERNS ARE EXCLUDED from the edge table: a doji / spinning-top has
// Bias 0, so "share of Bias-aligned moves" is undefined — there is no direction
// to be right about. Their forward behavior is real but carries no measured
// EDGE, so they are omitted rather than graded against an arbitrary side.
package candles

import "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"

const (
	// DefaultHorizon is the forward window (in bars) an edge is measured over
	// when the caller passes k<=0.
	DefaultHorizon = 5
	// MinPatternN is the honesty gate: a pattern with fewer than this many
	// historical firings has no stats emitted — a handful of samples is noise,
	// not an edge (mirrors expectancy's per-state minimum discipline).
	MinPatternN = 15
)

// EdgeStat is one pattern's measured forward-return statistics on a symbol's
// own history.
type EdgeStat struct {
	// Pattern is the pattern id (candles.Pattern.Name).
	Pattern string `json:"pattern"`
	// Bias is the pattern's directional lean (+1/-1) the hit rate is measured
	// against.
	Bias int `json:"bias"`
	// Horizon is the forward window in bars the outcome was measured over (K).
	Horizon int `json:"horizon"`
	// HitRate is the share of firings whose forward K-bar move went the
	// pattern's way (fwd>0 for a bullish bias, fwd<0 for a bearish one).
	HitRate float64 `json:"hitRate"`
	// MeanFwd is the mean forward K-bar return following the pattern (signed,
	// as observed — NOT flipped for bearish patterns).
	MeanFwd float64 `json:"meanFwd"`
	// N is the number of historical firings the stats rest on (>= MinPatternN).
	N int `json:"n"`
}

// MeasureEdges walks daily bars ascending and returns, per DIRECTIONAL pattern,
// the measured forward K-bar edge on this symbol's own history. k<=0 uses
// DefaultHorizon. Only patterns with N >= MinPatternN are returned; the rest
// are withheld (absence is honest — the shape hasn't printed enough here to
// grade). The result is deterministic (sorted by pattern name) and never nil-
// panics on short input.
func MeasureEdges(bars []marketdata.Bar, k int) []EdgeStat {
	if k <= 0 {
		k = DefaultHorizon
	}
	type acc struct {
		bias    int
		n, hits int
		sumFwd  float64
	}
	stats := map[string]*acc{}

	// i+k must exist for an outcome. Detect on bars[:i+1] so the pattern sees
	// only history up to and including bar i.
	for i := 0; i+k < len(bars); i++ {
		base := bars[i].Close
		if base <= 0 {
			continue
		}
		fwd := bars[i+k].Close/base - 1
		for _, p := range Detect(bars[:i+1]) {
			if p.Bias == 0 {
				continue // neutral pattern — no direction to grade
			}
			a := stats[p.Name]
			if a == nil {
				a = &acc{bias: p.Bias}
				stats[p.Name] = a
			}
			a.n++
			a.sumFwd += fwd
			if (p.Bias > 0 && fwd > 0) || (p.Bias < 0 && fwd < 0) {
				a.hits++
			}
		}
	}

	out := make([]EdgeStat, 0, len(stats))
	for name, a := range stats {
		if a.n < MinPatternN {
			continue
		}
		out = append(out, EdgeStat{
			Pattern: name,
			Bias:    a.bias,
			Horizon: k,
			HitRate: float64(a.hits) / float64(a.n),
			MeanFwd: a.sumFwd / float64(a.n),
			N:       a.n,
		})
	}
	sortEdgeStats(out)
	return out
}

// sortEdgeStats orders by pattern name for deterministic output (small n — a
// simple insertion sort avoids pulling in sort just for this).
func sortEdgeStats(s []EdgeStat) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Pattern < s[j-1].Pattern; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
