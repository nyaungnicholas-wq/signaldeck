// Measured-edge constants for the cross-sectional factor read.
//
// These numbers come from ONE independent re-validation (968 stocks / 1,900
// trading days, 2019-01..2026-07) that tested absolute direction against
// cross-sectional relative ranking. Absolute direction failed at every horizon
// (20 tests, not one CI above zero); the cross-sectional median-split label
// showed a real, statistically significant edge. Non-overlapping forward
// windows, date-clustered bootstrap CIs.
//
// They are DATA, deliberately not code: the API ships them verbatim so a reader
// can see exactly how big the measured effect was and how wide its band. Legs
// and horizons that were not measured have NO entry here — an absent leg is
// reported absent, never filled in with a plausible-looking number.
package xsfactor

// Horizon is a forward window the cross-sectional label was measured over.
type Horizon string

const (
	// H5d is the 5-trading-day forward window.
	H5d Horizon = "5d"
	// H21d is the 21-trading-day (~1 month) forward window.
	H21d Horizon = "21d"
	// H63d is the 63-trading-day (~1 quarter) forward window.
	H63d Horizon = "63d"
)

// Horizons lists the measured horizons, shortest first.
var Horizons = []Horizon{H5d, H21d, H63d}

// Leg identifiers. Each leg is one cross-sectional percentile in [0,1].
const (
	// LegLiquidity ranks median 21d dollar volume ASCENDING, so a thinner,
	// smaller-turnover name scores high — the size/liquidity premium leg.
	LegLiquidity = "liquidity"
	// LegLowVol ranks realized 21d volatility DESCENDING (1 - volPct), so a
	// calmer name scores high — the low-volatility anomaly leg.
	LegLowVol = "lowVol"
	// LegMom121 ranks momentum-12-1 (252d return excluding the most recent
	// 21d) ASCENDING, so a stronger prior-year trend scores high.
	LegMom121 = "mom12_1"
)

// LegEdge is ONE leg's MEASURED accuracy edge over the 50% base rate, in
// percentage points, with its date-clustered bootstrap confidence interval.
// Because the label is a median split, the base rate is exactly 50% and this
// edge IS skill — there is no class imbalance to hide behind.
type LegEdge struct {
	Leg    string  `json:"leg"`
	EdgePP float64 `json:"edgePP"`
	CILow  float64 `json:"ciLow"`
	CIHigh float64 `json:"ciHigh"`
}

// measuredEdge holds the re-validation results verbatim. Momentum-12-1 was
// only significant at 5d, so 21d/63d carry no momentum entry — the edge grew
// with horizon for the two surviving legs and that is all that was measured.
var measuredEdge = map[Horizon][]LegEdge{
	H5d: {
		{Leg: LegLiquidity, EdgePP: 1.46, CILow: 1.01, CIHigh: 1.86},
		{Leg: LegLowVol, EdgePP: 1.56, CILow: 0.67, CIHigh: 2.54},
		{Leg: LegMom121, EdgePP: 1.47, CILow: 0.67, CIHigh: 2.30},
	},
	H21d: {
		{Leg: LegLiquidity, EdgePP: 2.50, CILow: 1.61, CIHigh: 3.37},
		{Leg: LegLowVol, EdgePP: 2.80, CILow: 0.81, CIHigh: 4.66},
	},
	H63d: {
		{Leg: LegLiquidity, EdgePP: 3.04, CILow: 1.85, CIHigh: 4.27},
		{Leg: LegLowVol, EdgePP: 3.56, CILow: 0.14, CIHigh: 7.00},
	},
}

// MeasuredEdge returns the measured legs for h, shortest-CI-first order as
// recorded. The returned slice is a copy, so a caller cannot mutate the
// constants. An unknown horizon yields nil, never a fabricated row.
func MeasuredEdge(h Horizon) []LegEdge {
	src, ok := measuredEdge[h]
	if !ok {
		return nil
	}
	out := make([]LegEdge, len(src))
	copy(out, src)
	return out
}

// CompositeLegs returns the leg names that carry a measured edge at h — which
// is exactly the set the composite averages. A leg with no measurement at a
// horizon is reported as a diagnostic percentile but never weighted, so the
// composite never leans on an unmeasured assumption.
func CompositeLegs(h Horizon) []string {
	legs := measuredEdge[h]
	out := make([]string, 0, len(legs))
	for _, l := range legs {
		out = append(out, l.Leg)
	}
	return out
}

// ParseHorizon resolves a query value to a measured horizon. ok=false for
// anything else — the caller decides whether to default or to reject.
func ParseHorizon(s string) (Horizon, bool) {
	for _, h := range Horizons {
		if string(h) == s {
			return h, true
		}
	}
	return "", false
}

// Caveat ships VERBATIM with every payload. It is the whole honesty contract of
// this endpoint: what the label actually is, what failed, and why a real,
// replicated public factor is still not free money.
const Caveat = "cross-sectional RELATIVE ranking only — the label is \"will this symbol beat the same-day universe MEDIAN forward return?\", a median split whose base rate is exactly 50%, so accuracy IS skill. Absolute direction FAILED at every horizon tested (20 tests, not one CI above zero) and is deliberately not offered here. The legs that did work are the well-documented LOW-VOLATILITY ANOMALY and SIZE/LIQUIDITY PREMIUM — real and replicated here, but PUBLIC factors, capacity-constrained, and partly risk-compensation rather than free alpha; implied IC is only ~0.03-0.07 (accuracy 51-54%), which is the realistic ceiling, not an oracle."

// MethodNote states how the numbers in a row were produced.
const MethodNote = "every metric is point-in-time from trailing daily bars only (no forward data, no lookahead): realized 21d volatility, median 21d dollar volume, and momentum-12-1 = the 252d return excluding the most recent 21d. Each metric is ranked CROSS-SECTIONALLY into a [0,1] percentile across the symbols in this same request; the composite averages only the measured-edge legs a symbol actually has, renormalized over present legs (an absent leg is absent, never scored 0)."

// EvidenceNote records the provenance of the edge block.
const EvidenceNote = "edge measured over 968 stocks / 1,900 trading days (2019-01..2026-07) on NON-OVERLAPPING forward windows with date-clustered bootstrap CIs; the liquidity leg is far enough from zero to survive Bonferroni over the 15 tests run. Edge GROWS with horizon. Backtested measurement, not this endpoint's live track record."
