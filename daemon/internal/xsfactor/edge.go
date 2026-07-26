// Measured-edge constants for the cross-sectional factor read.
//
// PROVENANCE, which is the whole point of this file: every number below is the
// output of tools/xsfactor_edge.py run against the live database, read-only.
// That script is committed, its JSON output sits beside this file as
// derivation.json, and TestConstantsAreRederivable refuses to let the two
// diverge. Re-run it and these constants come back out.
//
// They are DATA, deliberately not code: the API ships them verbatim so a reader
// can see exactly how big the measured effect was and how wide its band. Legs
// and horizons that were not measured have NO entry here — an absent leg is
// reported absent, never filled in with a plausible-looking number.
//
// WHAT CHANGED ON 2026-07-26, and why it matters more than the numbers:
// the previous block came from a script that was never committed. A reviewer
// replicated it independently and the SIZE/LIQUIDITY leg came back with the
// OPPOSITE SIGN. The re-derivation confirms it — liquidity measures -1.10 /
// -1.65 / -1.92pp at 5d/21d/63d, negative at every horizon, and it is the ONLY
// leg in the block whose interval survives Bonferroni over the nine tests. It
// survives pointing the wrong way. It is retracted below rather than sign-
// flipped, because flipping it would be a fresh claim mined from the same data
// that just refuted the original, and because on the ACTIVE universe this
// endpoint actually ranks the same script measures -0.26 / -0.39 / +0.16pp,
// intervals spanning zero. There is no edge here to publish in either
// direction. Low-volatility at 63d is retracted for the plainer reason that its
// re-derived interval [-1.76, +5.26] contains zero.
package xsfactor

// DerivationScript / DerivationJSON make every constant in this file
// reproducible. The script re-derives each leg from the live database
// (read-only); the JSON is its committed output, and a test asserts the
// constants below equal it. Before these existed the numbers came from a script
// nobody could run — which is how a leg shipped with the wrong sign.
const (
	// DerivationScript is the committed derivation, repo-relative.
	DerivationScript = "tools/xsfactor_edge.py"
	// DerivationJSON is its output, beside this file.
	DerivationJSON = "derivation.json"
)

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
	// RETRACTED at every horizon: see the package comment and retraction below.
	LegLiquidity = "liquidity"
	// LegLowVol ranks realized 21d volatility DESCENDING (1 - volPct), so a
	// calmer name scores high — the low-volatility anomaly leg.
	LegLowVol = "lowVol"
	// LegMom121 ranks momentum-12-1 (252d return excluding the most recent
	// 21d) ASCENDING, so a stronger prior-year trend scores high.
	LegMom121 = "mom12_1"
)

// EdgeStatus says what the platform does with a measured leg. It exists so a
// number that failed can still be SHOWN — a leg that quietly vanishes from a
// payload teaches a reader nothing, and a leg that quietly reappears is how the
// wrong sign shipped in the first place.
type EdgeStatus string

const (
	// StatusPublished: the interval is strictly above zero. Only these legs are
	// averaged into the composite.
	StatusPublished EdgeStatus = "published"
	// StatusWithheld: measured, but the interval touches or crosses zero. The
	// percentile is still reported as a diagnostic; it is never weighted.
	StatusWithheld EdgeStatus = "withheld"
	// StatusRetracted: PREVIOUSLY PUBLISHED as a positive edge, and the
	// re-derivation contradicts that. Kept in the payload on purpose.
	StatusRetracted EdgeStatus = "retracted"
)

// LegEdge is ONE leg's MEASURED accuracy edge over the 50% base rate, in
// percentage points, with its day-clustered bootstrap confidence interval.
// Because the label is a median split, the base rate is exactly 50% and this
// edge IS skill — there is no class imbalance to hide behind.
//
// A NEGATIVE EdgePP is a real result and is recorded as one. It is never
// flipped into a positive claim about the opposite leg.
type LegEdge struct {
	Leg    string  `json:"leg"`
	EdgePP float64 `json:"edgePP"`
	CILow  float64 `json:"ciLow"`
	CIHigh float64 `json:"ciHigh"`
	// Bonferroni-corrected bounds over the nine leg x horizon tests the
	// derivation runs against one database. Reported for every leg because
	// quoting only the uncorrected interval on the best-looking leg is the
	// standard way a factor study overstates itself.
	CILowBonferroni  float64 `json:"ciLowBonferroni"`
	CIHighBonferroni float64 `json:"ciHighBonferroni"`
	// N is graded observations; DistinctDays is the number of NON-OVERLAPPING
	// formation days they came from — the real unit, and the one the interval
	// resamples. N without DistinctDays beside it is the counting artifact the
	// audit called out.
	N            int        `json:"n"`
	DistinctDays int        `json:"distinctDays"`
	Status       EdgeStatus `json:"status"`
	Reason       string     `json:"reason,omitempty"`
}

// Positive reports whether the leg's whole 95% interval sits above zero — the
// single condition for publishing a leg and for letting it into the composite.
func (l LegEdge) Positive() bool { return l.EdgePP > 0 && l.CILow > 0 }

// allMeasured is the COMPLETE re-derivation: every leg at every horizon, passed
// or failed, in a fixed leg order. Published legs are the subset with a
// strictly positive interval; nothing else can reach the composite. The values
// are tools/xsfactor_edge.py's output rounded to 2dp, with interval bounds
// rounded OUTWARD so rounding can only ever weaken a claim.
var allMeasured = map[Horizon][]LegEdge{
	H5d: {
		{Leg: LegLiquidity, EdgePP: -1.10, CILow: -1.52, CIHigh: -0.67,
			CILowBonferroni: -1.69, CIHighBonferroni: -0.49,
			N: 298822, DistinctDays: 375, Status: StatusRetracted,
			Reason: "shipped as +1.46pp until 2026-07-26; the committed re-derivation measures it NEGATIVE and the interval excludes zero even after Bonferroni. Retracted rather than sign-flipped: on the active universe this endpoint ranks, the same script measures -0.26pp [-0.77, +0.23], so there is no edge to publish in either direction"},
		{Leg: LegLowVol, EdgePP: 1.10, CILow: 0.17, CIHigh: 2.06,
			CILowBonferroni: -0.20, CIHighBonferroni: 2.47,
			N: 301683, DistinctDays: 374, Status: StatusPublished},
		{Leg: LegMom121, EdgePP: 1.24, CILow: 0.34, CIHigh: 2.13,
			CILowBonferroni: -0.04, CIHighBonferroni: 2.49,
			N: 257120, DistinctDays: 328, Status: StatusPublished},
	},
	H21d: {
		{Leg: LegLiquidity, EdgePP: -1.65, CILow: -2.52, CIHigh: -0.79,
			CILowBonferroni: -2.87, CIHighBonferroni: -0.41,
			N: 70679, DistinctDays: 89, Status: StatusRetracted,
			Reason: "shipped as +2.50pp until 2026-07-26; the committed re-derivation measures it NEGATIVE and the interval excludes zero even after Bonferroni. On the active universe: -0.39pp [-1.39, +0.61], spanning zero"},
		{Leg: LegLowVol, EdgePP: 1.99, CILow: 0.17, CIHigh: 3.81,
			CILowBonferroni: -0.53, CIHighBonferroni: 4.47,
			N: 71491, DistinctDays: 89, Status: StatusPublished},
		{Leg: LegMom121, EdgePP: 1.35, CILow: -0.35, CIHigh: 2.99,
			CILowBonferroni: -1.03, CIHighBonferroni: 3.73,
			N: 60918, DistinctDays: 78, Status: StatusWithheld,
			Reason: "interval spans zero — momentum-12-1 was never published beyond 5d and the re-derivation does not change that"},
	},
	H63d: {
		{Leg: LegLiquidity, EdgePP: -1.92, CILow: -3.19, CIHigh: -0.63,
			CILowBonferroni: -3.70, CIHighBonferroni: -0.07,
			N: 22886, DistinctDays: 29, Status: StatusRetracted,
			Reason: "shipped as +3.04pp until 2026-07-26; the committed re-derivation measures it NEGATIVE and the interval excludes zero even after Bonferroni. On the active universe: +0.16pp [-1.58, +1.96], spanning zero"},
		{Leg: LegLowVol, EdgePP: 1.84, CILow: -1.76, CIHigh: 5.26,
			CILowBonferroni: -3.35, CIHighBonferroni: 6.62,
			N: 23150, DistinctDays: 29, Status: StatusRetracted,
			Reason: "shipped as +3.56pp [+0.14, +7.00] until 2026-07-26; the re-derived interval CONTAINS ZERO. Non-overlapping quarterly windows over 7.5 years give only 29 independent formation days, which is not enough to establish a quarterly edge however many symbol-rows it produces"},
		{Leg: LegMom121, EdgePP: 0.79, CILow: -1.41, CIHigh: 2.91,
			CILowBonferroni: -2.41, CIHighBonferroni: 3.84,
			N: 20136, DistinctDays: 26, Status: StatusWithheld,
			Reason: "interval spans zero — 26 independent formation days is too few to separate this from noise"},
	},
}

// pick returns a copy of the legs at h matching want. The copy matters: without
// it a caller could mutate the constants that the whole endpoint's honesty
// rests on. An unknown horizon yields nil, never a fabricated row.
func pick(h Horizon, want func(LegEdge) bool) []LegEdge {
	src, ok := allMeasured[h]
	if !ok {
		return nil
	}
	out := make([]LegEdge, 0, len(src))
	for _, l := range src {
		if want(l) {
			out = append(out, l)
		}
	}
	return out
}

// MeasuredEdge returns the PUBLISHED legs for h — the ones with a strictly
// positive interval, and exactly the set the composite averages. At 63d this is
// empty: nothing survived re-derivation there, and an empty block is the honest
// answer rather than the two legs that used to fill it.
func MeasuredEdge(h Horizon) []LegEdge {
	return pick(h, func(l LegEdge) bool { return l.Status == StatusPublished })
}

// WithheldEdge returns the legs that were measured and NOT published, each with
// its reason — including the ones this file retracted. They ship in the payload
// so a reader can see what failed, not just what passed.
func WithheldEdge(h Horizon) []LegEdge {
	return pick(h, func(l LegEdge) bool { return l.Status != StatusPublished })
}

// AllMeasured returns every leg measured at h, published or not, in fixed leg
// order. This is the full derivation as it ships.
func AllMeasured(h Horizon) []LegEdge {
	return pick(h, func(LegEdge) bool { return true })
}

// CompositeLegs returns the leg names that carry a PUBLISHED edge at h — which
// is exactly the set the composite averages. A leg with no surviving
// measurement at a horizon is reported as a diagnostic percentile but never
// weighted, so the composite never leans on an unmeasured assumption, and never
// on a leg the derivation measured pointing the other way.
func CompositeLegs(h Horizon) []string {
	legs := MeasuredEdge(h)
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
const Caveat = "cross-sectional RELATIVE ranking only — the label is \"will this symbol beat the same-day universe MEDIAN forward return?\", a median split whose base rate is exactly 50%, so accuracy IS skill. Absolute direction failed at every horizon in the 2026-07-24 re-validation; that test was NOT re-run here and absolute direction is simply not offered. Of the three legs, only the LOW-VOLATILITY ANOMALY (5d and 21d) and MOMENTUM-12-1 (5d only) survive re-derivation; the SIZE/LIQUIDITY PREMIUM leg is RETRACTED — it was published at +1.46/+2.50/+3.04pp and re-derives NEGATIVE at all three horizons. What survives is a PUBLIC factor, capacity-constrained and partly risk-compensation rather than free alpha: the surviving accuracies are 51.1-52.0% against a 50% base rate, which is the realistic ceiling, not an oracle. And no surviving leg clears Bonferroni over the nine tests the derivation runs — the corrected bounds ship beside every edge, read them."

// MethodNote states how the numbers in a row were produced.
const MethodNote = "every metric is point-in-time from trailing daily bars only (no forward data, no lookahead): realized 21d volatility, median 21d dollar volume, and momentum-12-1 = the 252d return excluding the most recent 21d. Each metric is ranked CROSS-SECTIONALLY into a [0,1] percentile across the symbols in this same request; the composite averages only the PUBLISHED-edge legs a symbol actually has, renormalized over present legs (an absent leg is absent, never scored 0). A leg whose edge was retracted or withheld is still shown as a percentile and never weighted."

// EvidenceNote records the provenance of the edge block — and now names the
// script, because a constant nobody can re-derive is an assertion, not a
// measurement.
const EvidenceNote = "RE-DERIVABLE: every edge below is the output of tools/xsfactor_edge.py, committed in this repo and run read-only against the live database; its full output ships as daemon/internal/xsfactor/derivation.json and a test fails the build if a constant drifts from it. Method: 1,059 US stocks INCLUDING 739 delisted names (survivorship-clean) over 1,900 trading days (2019-01..2026-07); NON-OVERLAPPING forward windows taken every H sessions along a global trading-day calendar; one observation per (symbol, UTC-day), asserted not assumed; the label is the same-day cross-sectional median split; intervals are 20,000-resample bootstraps of FORMATION DAYS, never of rows, with Bonferroni bounds over the nine leg x horizon tests. Series carrying a |daily return| above 0.65 are refused as split artifacts in both the trailing and the forward window. Backtested measurement, not this endpoint's live track record."

// RetractionNote is the correction itself, shipped rather than buried in a
// changelog: the audit finding that produced this file's current state.
const RetractionNote = "2026-07-26 CORRECTION (audit finding H2): the size/liquidity leg was shipped at +1.46pp (5d), +2.50pp (21d) and +3.04pp (63d) by a derivation script that was never committed. An independent replication returned the opposite sign, and the committed re-derivation confirms it: -1.10 / -1.65 / -1.92pp, intervals excluding zero at all three horizons and the only leg in the block to survive Bonferroni — in the wrong direction. The leg is RETRACTED, not sign-flipped: on the active-only universe this endpoint actually ranks it measures -0.26 / -0.39 / +0.16pp with intervals spanning zero, so no claim is supportable in either direction. Low-volatility at 63d is retracted too — its re-derived interval contains zero. The 63d horizon consequently has NO surviving leg and is gated."

// GateNoMeasuredLeg is the stated reason a horizon serves no ranking. It fires
// when every leg at that horizon was retracted or withheld: a composite over
// zero published legs would be a number with nothing behind it, so the surface
// withholds instead.
const GateNoMeasuredLeg = "no leg survives re-derivation at this horizon, so there is no composite to rank by — a score averaged over zero measured legs would be an invented number. The per-leg measurements that failed ship in the withheld block with their reasons; use a shorter horizon"
