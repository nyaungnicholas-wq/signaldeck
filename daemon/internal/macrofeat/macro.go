// MACRO-BREADTH WAVE (2026-07-25) — the rest of the free FRED panel, turned
// into model features.
//
// # The gap this closes
//
// The platform ingested ~104k FRED observations across twelve series and fed
// exactly one of them (VIXCLS) to the model. Everything else — the yield curve,
// credit spreads, financial conditions, the policy rate — was collected,
// charted, and never allowed to inform a prediction. Every other input in the
// vector is derived from price, so the macro panel is one of the few genuinely
// ORTHOGONAL things available on a free stack.
//
// # Two rules that decide what is admissible
//
//  1. REVISED SERIES ARE EXCLUDED. CPIAUCSL, M2SL and UNRATE are restated after
//     first publication, and this platform stores only the CURRENT value of each
//     observation — not the vintage that was actually public on the day. Feeding
//     them would let the model see a number nobody had at decision time, which
//     is lookahead of the purest kind. They stay charted and stay out of the
//     vector until point-in-time vintages exist (ALFRED, not FRED). The
//     market-observed series (yields, spreads, VIX, fed funds, oil, NFCI) are
//     printed once and not restated, so their latest value IS what was public.
//
//  2. LEVELS ARE NOT FEATURES. A 10-year yield of 4.5% meant something entirely
//     different in 2020 than in 2026, so a raw level teaches a tree to split on
//     an era rather than on a state — it will happily learn "2021 was good" and
//     call it signal. Each series is therefore encoded as its PERCENTILE within
//     a trailing window (where does today sit in its own recent history?) and as
//     a CHANGE over a short lookback (which way is it moving?). Both are bounded
//     and roughly stationary, and both are answerable on the day without
//     knowing the future.
//
// No lookahead: the caller passes observations at or before the decision time,
// newest last, and every statistic is computed from that slice alone.
package macrofeat

import (
	"math"
	"sort"
)

// Series is one admissible FRED series and how it is encoded.
type Series struct {
	// ID is the FRED series id.
	ID string
	// Key is the feature-name stem (macro_<key>_pct / macro_<key>_chg).
	Key string
	// ChangeLookback is how many observations back the change is measured over.
	// Roughly one trading month for daily series.
	ChangeLookback int
}

// AdmissibleSeries are the market-observed, non-revised FRED series the model
// may see. Adding a series here is a deliberate act: it must be printed once and
// never restated, or rule 1 above is violated.
//
// Deliberately ABSENT and why:
//
//	CPIAUCSL, M2SL, UNRATE — revised after publication; no vintage stored.
var AdmissibleSeries = []Series{
	// The policy and curve block: level of rates, and the two curve slopes that
	// carry most of the recession/risk information.
	{ID: "DGS10", Key: "dgs10", ChangeLookback: 21},
	{ID: "DGS2", Key: "dgs2", ChangeLookback: 21},
	{ID: "T10Y2Y", Key: "curve_10y2y", ChangeLookback: 21},
	{ID: "T10Y3M", Key: "curve_10y3m", ChangeLookback: 21},
	{ID: "DFF", Key: "fedfunds", ChangeLookback: 21},
	// Risk appetite: high-yield credit spread is the cleanest free risk-off
	// gauge that is not itself an equity price, and NFCI is the Chicago Fed's
	// composite financial-conditions index.
	{ID: "BAMLH0A0HYM2", Key: "hy_spread", ChangeLookback: 21},
	{ID: "NFCI", Key: "nfci", ChangeLookback: 4}, // weekly series
	// A real-economy input that is priced continuously.
	{ID: "DCOILWTICO", Key: "oil", ChangeLookback: 21},
}

const (
	// PercentileWindow is the trailing observation count a level is ranked
	// within. ~1 trading year for a daily series: long enough to describe a
	// regime, short enough that a decade-old rate environment does not define
	// today's percentile.
	PercentileWindow = 252
	// MinObservations is the floor below which a series produces NO features
	// rather than a percentile computed from a handful of points. Absence is a
	// legitimate answer; a percentile over 5 observations is not.
	MinObservations = 30
)

// Point is one observation, as stored.
type Point struct {
	Ts    int64
	Value float64
}

// FromSeries builds the market-wide macro feature map from per-series
// observation histories, each sorted ASCENDING by ts and containing only
// observations at or before the decision time.
//
// A series that is missing, too short, or degenerate contributes NOTHING — the
// keys are simply absent, which the feature store already distinguishes from
// zero. It is never filled with a neutral value, because "the curve is exactly
// at its median" is a real state and must not be manufactured.
func FromSeries(hist map[string][]Point) map[string]float64 {
	out := map[string]float64{}
	for _, s := range AdmissibleSeries {
		pts := hist[s.ID]
		if len(pts) < MinObservations {
			continue
		}
		vals := make([]float64, 0, len(pts))
		for _, p := range pts {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			vals = append(vals, p.Value)
		}
		if len(vals) < MinObservations {
			continue
		}
		window := vals
		if len(window) > PercentileWindow {
			window = window[len(window)-PercentileWindow:]
		}
		latest := window[len(window)-1]

		if pct, ok := percentileOf(latest, window); ok {
			out["macro_"+s.Key+"_pct"] = pct
		}
		if chg, ok := changeOver(vals, s.ChangeLookback); ok {
			out["macro_"+s.Key+"_chg"] = chg
		}
	}
	return out
}

// percentileOf returns where v sits within window, in [0,1]. A window with no
// spread (every value identical) has no meaningful percentile and returns false
// rather than the misleading 0.5.
func percentileOf(v float64, window []float64) (float64, bool) {
	if len(window) < MinObservations {
		return 0, false
	}
	sorted := append([]float64(nil), window...)
	sort.Float64s(sorted)
	if sorted[0] == sorted[len(sorted)-1] {
		return 0, false
	}
	// Fraction of observations at or below v.
	idx := sort.SearchFloat64s(sorted, v)
	// SearchFloat64s finds the first index >= v; count entries strictly below
	// plus half the ties, so an exact match sits mid-band rather than at an edge.
	lo := idx
	hi := idx
	for hi < len(sorted) && sorted[hi] == v {
		hi++
	}
	rank := float64(lo)
	if hi > lo {
		rank += float64(hi-lo) / 2
	}
	return clamp01(rank / float64(len(sorted))), true
}

// changeOver returns the change from n observations ago to the latest, scaled to
// a bounded, roughly-stationary quantity.
//
// The scaling is deliberately NOT a percentage change: several of these series
// legitimately cross zero (the 10y-2y curve inverts; NFCI is centered on zero),
// and a percentage change through zero explodes. The change is instead expressed
// in units of the series' own trailing standard deviation, which is stable
// across regimes and finite everywhere, then squashed to keep outliers from
// dominating a scale-sensitive leg.
func changeOver(vals []float64, n int) (float64, bool) {
	if n < 1 || len(vals) < n+1 || len(vals) < MinObservations {
		return 0, false
	}
	latest := vals[len(vals)-1]
	prior := vals[len(vals)-1-n]
	window := vals
	if len(window) > PercentileWindow {
		window = window[len(window)-PercentileWindow:]
	}
	sd := stddev(window)
	if sd <= 0 || math.IsNaN(sd) || math.IsInf(sd, 0) {
		return 0, false
	}
	z := (latest - prior) / sd
	// tanh squash to (-1,1): keeps a crisis-sized move distinguishable from a
	// normal one without letting it swamp every other feature.
	return math.Tanh(z), true
}

func stddev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)-1))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
