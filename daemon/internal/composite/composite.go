// Package composite computes the per-symbol SignalScore: a Danelfin/Zacks-
// style 1-10 composite over the platform's OWN stored evidence, honesty-first.
//
// # What the score is (and is not)
//
// The score is a FORCED cross-sectional curve over the edge estimate
// edge = calibrated P(up,1d) − 0.5, taken from the LATEST STORED prediction
// row — this package never recomputes the ensemble. Like Zacks ranks, the
// distribution is fixed by construction (top 5% = 10 … bottom 5% = 1), so a
// 10 means "top of today's cross-section", NOT "90% chance of going up".
// Below MinCurveN symbols with usable predictions there is no cross-section
// to rank against, so no scores are emitted at all — a forced curve over a
// handful of names would manufacture 10s and 1s out of noise.
//
// # Honesty doctrine (mirrors internal/adaptive + internal/ensemble)
//
//   - Every factor tile carries its raw evidence line with real numbers, its
//     measured skill (hit-rate/IC/n from the adaptive attribution) where that
//     component exists, and an EXPLICIT gate state + reason when the factor is
//     absent or withheld. Gates render as reasons, never silently.
//   - The additive ledger sums exactly to (calProb − 0.5). Exactness is only
//     CLAIMED when it holds: the ensemble's blend weights are not persisted on
//     the prediction row, so when the stored raw probability is not the
//     equal-weight mean of the legs the per-leg split is labeled
//     "proportional attribution" — never fake exactness.
//   - The regime factor is context only and never scored; the short-volume
//     factor is never directional (its verbatim caveat: a high ratio is NOT
//     directly bearish).
//
// Pure functions only: storage, scheduling, and per-symbol data loading live
// in pipeline/compositescorer.go and store/composite.go.
package composite

import (
	"fmt"
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// MinCurveN is the minimum number of symbols with usable (fresh, ungated)
// predictions before a forced curve is emitted at all. Below this the whole
// pass is gated — a cross-sectional rank over a thin set fabricates extremes.
// Mirrors ensemble.MinCalibrationPairs / adaptive.MinCellSamples.
const MinCurveN = 30

// probDeadBand is the tile-arrow dead band around 0.5 for probability legs:
// a leg within ±probDeadBand of coin-flip gets a neutral verdict. A display
// convention for the tile arrow, not a measured statistic — the evidence line
// always carries the raw number.
const probDeadBand = 0.05

// Ranking verdict thresholds: cross-sectional percentile at/above rankHi reads
// bullish, at/below rankLo bearish, in between neutral (stated, not hidden).
const (
	rankHi = 70.0
	rankLo = 30.0
)

// ShortVolMinPrior is the minimum number of PRIOR days of short-volume ratios
// before a z is computed at all — mirrors shortsZMinPrior in api/shorts.go.
const ShortVolMinPrior = 10

// ShortVolCaveat ships VERBATIM with the shortvol factor tile. It MUST stay
// byte-identical to shortsCaveat in internal/api/shorts.go (asserted by an api
// package test) — the classic retail trap this platform refuses to feed.
const ShortVolCaveat = "short sale volume ratio (Reg SHO daily) — NOT short interest; includes market-maker activity; a high ratio is NOT directly bearish"

// Factor keys, in tile display order.
const (
	FactorTechnical  = "technical"
	FactorExpectancy = "expectancy"
	FactorForecast   = "forecast"
	FactorSentiment  = "sentiment"
	FactorGBM        = "gbm"
	FactorMeanRev    = "meanrev"
	FactorAlphaX     = "alphax"
	FactorRanking    = "ranking"
	FactorRegime     = "regime"
	FactorInsiders   = "insiders"
	FactorShortVol   = "shortvol"
	FactorBreakout   = "breakout"
)

// legByFactor maps the seven ensemble-leg factors to their canonical leg names
// (the adaptive attribution's keys), so skill chips can never drift.
var legByFactor = map[string]string{
	FactorTechnical:  ensemble.LegPressure,
	FactorExpectancy: ensemble.LegExpectancy,
	FactorForecast:   ensemble.LegForecast,
	FactorSentiment:  ensemble.LegSentiment,
	FactorGBM:        ensemble.LegGBM,
	FactorMeanRev:    ensemble.LegMeanRev,
	FactorAlphaX:     ensemble.LegAlphaX,
}

// Factor is one evidence tile: a verdict, the raw evidence line, the measured
// skill behind the component (when the adaptive attribution has graded it),
// and an explicit gate state. Verdict is -1 (bearish) | 0 (neutral/context) |
// +1 (bullish); a gated factor always has Verdict 0.
type Factor struct {
	Key      string `json:"key"`
	Verdict  int    `json:"verdict"`
	Evidence string `json:"evidence"`
	// Measured skill from the adaptive attribution cell (hit-rate / IC / n) —
	// present only for ensemble-leg factors the attribution has samples for.
	SkillHitRate *float64 `json:"skillHitRate,omitempty"`
	SkillIC      *float64 `json:"skillIC,omitempty"`
	SkillN       *int     `json:"skillN,omitempty"`
	// Gated=true means the factor is absent or withheld; GateReason says why.
	Gated      bool   `json:"gated"`
	GateReason string `json:"gateReason,omitempty"`
}

// LedgerEntry is one signed contribution (in percentage points) to the edge.
// Prob carries the leg's P(up) for ensemble legs; nil for the calibration line.
type LedgerEntry struct {
	Leg       string   `json:"leg"`
	Prob      *float64 `json:"prob,omitempty"`
	ContribPP float64  `json:"contribPp"`
}

// Ledger is the additive waterfall from the 50% coin-flip baseline to the
// calibrated P(up): per-leg signed pp contributions plus one calibration
// line, summing exactly to (calProb−0.5)·100 by construction.
type Ledger struct {
	Method   string        `json:"method"`
	Exact    bool          `json:"exact"`
	Entries  []LedgerEntry `json:"entries"`
	SumPP    float64       `json:"sumPp"`
	TargetPP float64       `json:"targetPp"`
}

// BuildLedger decomposes (calProb − 0.5) into per-leg contributions plus a
// calibration line. Legs come from ensemble.LegProbabilities — the SAME code
// path the live blend used, so gated legs are absent here exactly as they
// were absent from the blend.
//
// Exactness is measured, never assumed: when rawProb equals the equal-weight
// mean of the legs (the static-prior blend), each leg's contribution
// (p−0.5)/n is an EXACT decomposition of the raw edge. Otherwise learned
// weights were used at prediction time and are not recoverable from the
// stored row, so the raw edge is split proportionally to each leg's signed
// deviation and the method says so. The calibration line (calProb − rawProb)
// is exact by definition in both cases.
func BuildLedger(c ensemble.Components, rawProb, calProb float64) Ledger {
	legs := ensemble.LegProbabilities(c)
	n := len(legs)
	l := Ledger{TargetPP: (calProb - 0.5) * 100}
	if n == 0 {
		// Defensive: pressure is always present, but never divide by zero.
		l.Method = "calibration line only (no legs stored)"
		l.Entries = []LedgerEntry{{Leg: "calibration", ContribPP: l.TargetPP}}
		l.SumPP = l.TargetPP
		return l
	}

	var devSum float64
	for _, p := range legs {
		devSum += p - 0.5
	}
	rawEdge := rawProb - 0.5
	l.Exact = math.Abs(devSum/float64(n)-rawEdge) <= 1e-9
	if l.Exact {
		l.Method = "equal-weight decomposition (exact) + calibration adjustment"
	} else {
		l.Method = "proportional attribution (blend weights are not persisted on the prediction row) + calibration adjustment"
	}

	for _, leg := range ensemble.LegNames { // canonical order, present legs only
		p, ok := legs[leg]
		if !ok {
			continue
		}
		var contrib float64
		switch {
		case l.Exact:
			contrib = (p - 0.5) / float64(n)
		case math.Abs(devSum) > 1e-9:
			contrib = rawEdge * (p - 0.5) / devSum
		default:
			// Legs net to coin-flip yet the stored raw edge is nonzero
			// (weighted blend): no per-leg basis remains — split evenly.
			contrib = rawEdge / float64(n)
		}
		pv := p
		l.Entries = append(l.Entries, LedgerEntry{Leg: leg, Prob: &pv, ContribPP: contrib * 100})
	}
	l.Entries = append(l.Entries, LedgerEntry{Leg: "calibration", ContribPP: (calProb - rawProb) * 100})
	for _, e := range l.Entries {
		l.SumPP += e.ContribPP
	}
	return l
}

// ── forced curve ─────────────────────────────────────────────────────────

// Edge is one symbol's edge estimate entering the forced curve.
type Edge struct {
	SymbolID int64
	Symbol   string // deterministic tie-break for equal edges
	Edge     float64
}

// CurveScore is one symbol's forced-curve assignment.
type CurveScore struct {
	SymbolID int64
	Score    int     // 1..10
	Pct      float64 // cross-sectional percentile of the edge, 0..100
}

// ForcedCurve assigns 1-10 scores by cross-sectional percentile of the edge
// estimate: top 5% → 10, next 10% → 9, next 20% → 8-7, middle 30% → 6-5,
// next 20% → 4-3, next 10% → 2, bottom 5% → 1.
//
// TIES ARE HONEST: symbols with bit-identical edges carry identical evidence,
// so the whole tie block receives the SAME percentile (the block's mean
// ordinal percentile) and therefore the same score. Without this, alphabetical
// tie-breaking alone could hand two identical-evidence symbols a 10 and a 1 —
// fabricated differentiation. Symbol-name ordering is kept only so the output
// order is deterministic across reruns.
// ok=false below MinCurveN symbols — no scores are fabricated from a thin
// cross-section.
func ForcedCurve(edges []Edge) ([]CurveScore, bool) {
	n := len(edges)
	if n < MinCurveN {
		return nil, false
	}
	sorted := make([]Edge, n)
	copy(sorted, edges)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Edge != sorted[j].Edge {
			return sorted[i].Edge > sorted[j].Edge
		}
		return sorted[i].Symbol < sorted[j].Symbol
	})
	out := make([]CurveScore, n)
	for i := 0; i < n; {
		// Find the tie block [i, j) of equal edges.
		j := i + 1
		for j < n && sorted[j].Edge == sorted[i].Edge {
			j++
		}
		// Mean ordinal percentile over the block, shared by every member.
		mid := (float64(i) + float64(j-1)) / 2
		pct := 100 * (1 - (mid+0.5)/float64(n))
		sc := scoreFromPct(pct)
		for k := i; k < j; k++ {
			out[k] = CurveScore{SymbolID: sorted[k].SymbolID, Score: sc, Pct: pct}
		}
		i = j
	}
	return out, true
}

// scoreFromPct maps a cross-sectional percentile (100 = best edge) to the
// forced 1-10 band.
func scoreFromPct(pct float64) int {
	switch {
	case pct >= 95:
		return 10
	case pct >= 85:
		return 9
	case pct >= 75:
		return 8
	case pct >= 65:
		return 7
	case pct >= 50:
		return 6
	case pct >= 35:
		return 5
	case pct >= 25:
		return 4
	case pct >= 15:
		return 3
	case pct >= 5:
		return 2
	default:
		return 1
	}
}

// ── factor tiles ─────────────────────────────────────────────────────────

// BreakoutInfo is the newest stored breakout event for a symbol (already
// filtered to the recency window by the caller).
type BreakoutInfo struct {
	Kind    string
	Detail  string
	AgeDays float64
}

// Inputs carries everything BuildFactors needs, all read from the store by
// the worker. Optional evidence uses pointers/nil slices so "absent" is
// distinct from a real zero — absence is information and gates the tile.
type Inputs struct {
	// Components is the prediction row's parsed components JSON — the exact
	// legs the blend saw, gates included.
	Components ensemble.Components
	// Per-leg measured skill from the adaptive attribution cell that applies
	// to this symbol's regime (nil maps = no attribution stored yet).
	SkillHitRates map[string]float64
	SkillICs      map[string]float64
	SkillLegN     map[string]int

	RankPct     *float64 // latest cross-sectional ranking percentile, 0..100
	RegimeLabel string   // "" = never classified

	// Form 4 open-market activity (codes P/S only) over the last 90d.
	InsiderBuys, InsiderSells   float64 // dollar values
	InsiderNBuys, InsiderNSells int

	// Daily short-sale volume ratios, ascending, ≤30d window (the z baseline).
	ShortRatios []float64

	Breakout *BreakoutInfo // newest breakout within 7d; nil = none

	// TradingView external rating (the 12th factor). TVRating nil = no rating
	// stored. TVSkill* carry the POOLED measured skill of the external rating
	// (store.TVRatingSkill: IC/hit-rate/N of past reco_all vs realized forward
	// returns); the factor stays CONTEXT-only until that skill clears the gate.
	TVRating       *TVRatingInfo
	TVSkillHitRate float64
	TVSkillIC      float64
	TVSkillN       int
}

// BuildFactors renders the twelve evidence tiles from one symbol's stored
// inputs (plus the external tvrating tile). Every gate is an explicit reason;
// nothing is silently dropped.
func BuildFactors(in Inputs) []Factor {
	c := in.Components
	out := make([]Factor, 0, 13)

	// technical — the pressure score leg (always present in the blend).
	pProb := clamp01((c.PressureScore + 1) / 2)
	out = append(out, in.withSkill(Factor{
		Key:     FactorTechnical,
		Verdict: verdictFromProb(pProb),
		Evidence: fmt.Sprintf("pressure score %+.2f (1d) → P(up) leg %.1f%%",
			c.PressureScore, pProb*100),
	}))

	// expectancy — measured hit rate of the current historical state.
	if c.ExpectancyHitRate == nil {
		out = append(out, gatedFactor(FactorExpectancy,
			"no expectancy state matched for this symbol — leg absent from the blend"))
	} else {
		hr := clamp01(*c.ExpectancyHitRate)
		out = append(out, in.withSkill(Factor{
			Key:     FactorExpectancy,
			Verdict: verdictFromProb(hr),
			Evidence: fmt.Sprintf("current state's measured hit rate: %.0f%% of matched historical states had positive forward returns",
				hr*100),
		}))
	}

	// forecast — the logistic model leg, gated on measured OOS lift.
	out = append(out, in.modelLegFactor(FactorForecast, "logistic forecast",
		c.ForecastProb, c.ForecastLift,
		"no trained logistic forecast stored for this symbol/horizon"))

	// sentiment — the news-sentiment leg (conservative mapping).
	if c.SentimentScore == nil {
		out = append(out, gatedFactor(FactorSentiment,
			"no fresh rated-headline aggregate (needs ≥3 rated headlines within 3 days) — leg absent from the blend"))
	} else {
		sProb := clamp01(0.5 + *c.SentimentScore*ensemble.SentimentScale)
		out = append(out, in.withSkill(Factor{
			Key:     FactorSentiment,
			Verdict: verdictFromProb(sProb),
			Evidence: fmt.Sprintf("news sentiment %+.2f → P(up) leg %.1f%% (deliberately conservative %.2f scale)",
				*c.SentimentScore, sProb*100, ensemble.SentimentScale),
		}))
	}

	// gbm + meanrev — Stage-6 model legs, same OOS-lift gate as the forecast.
	out = append(out, in.modelLegFactor(FactorGBM, "GBM model",
		c.GBMProb, c.GBMLift,
		"no trained GBM leg stored for this symbol/horizon"))
	out = append(out, in.modelLegFactor(FactorMeanRev, "mean-reversion model (cost-net)",
		c.MeanRevProb, c.MeanRevLift,
		"no trained mean-reversion leg stored for this symbol/horizon"))

	// alphax — the pooled cross-sectional leg, same OOS-lift gate. Its prob is
	// RELATIVE (beat the same-day universe median), not an absolute P(up) —
	// the evidence line carries that category nuance verbatim.
	out = append(out, in.alphaXFactor())

	// ranking — cross-sectional relative strength percentile.
	if in.RankPct == nil {
		out = append(out, gatedFactor(FactorRanking,
			"no cross-sectional ranking snapshot stored yet"))
	} else {
		v := 0
		if *in.RankPct >= rankHi {
			v = 1
		} else if *in.RankPct <= rankLo {
			v = -1
		}
		out = append(out, Factor{
			Key:     FactorRanking,
			Verdict: v,
			Evidence: fmt.Sprintf("cross-sectional relative-strength percentile %.0f/100 (bullish ≥%.0f, bearish ≤%.0f)",
				*in.RankPct, rankHi, rankLo),
		})
	}

	// regime — CONTEXT ONLY. Never scored: a regime label frames the other
	// factors, it is not itself a directional call.
	if in.RegimeLabel == "" {
		out = append(out, gatedFactor(FactorRegime, "no regime classified yet"))
	} else {
		out = append(out, Factor{
			Key:      FactorRegime,
			Verdict:  0,
			Evidence: fmt.Sprintf("regime %q — context only, never scored", in.RegimeLabel),
		})
	}

	// insiders — Form 4 open-market net dollar activity, 90d.
	if in.InsiderNBuys+in.InsiderNSells == 0 {
		out = append(out, gatedFactor(FactorInsiders,
			"no open-market Form 4 trades (codes P/S) stored in the last 90d"))
	} else {
		net := in.InsiderBuys - in.InsiderSells
		v := 0
		if net > 0 {
			v = 1
		} else if net < 0 {
			v = -1
		}
		out = append(out, Factor{
			Key:     FactorInsiders,
			Verdict: v,
			Evidence: fmt.Sprintf("insiders net %s over 90d (%d open-market buy(s) %s, %d sale(s) %s) — Form 4 filings lag ~2 business days",
				fmtSignedMoney(net), in.InsiderNBuys, fmtMoney(in.InsiderBuys), in.InsiderNSells, fmtMoney(in.InsiderSells)),
		})
	}

	// shortvol — latest daily short-sale volume ratio z vs own trailing
	// baseline. NEVER directional: the verbatim caveat is the whole point.
	if z, ok := ShortVolZ(in.ShortRatios); !ok {
		reason := "no Reg SHO short-volume rows stored for this symbol"
		if n := len(in.ShortRatios); n > 0 {
			reason = fmt.Sprintf("only %d day(s) of Reg SHO history (needs %d prior days with a non-flat baseline) — z withheld", n, ShortVolMinPrior)
		}
		out = append(out, gatedFactor(FactorShortVol, reason))
	} else {
		latest := in.ShortRatios[len(in.ShortRatios)-1]
		out = append(out, Factor{
			Key:     FactorShortVol,
			Verdict: 0,
			Evidence: fmt.Sprintf("short-volume z=%+.1f vs own %dd baseline (latest ratio %.0f%%) — %s",
				z, len(in.ShortRatios), latest*100, ShortVolCaveat),
		})
	}

	// breakout — most recent detected event within 7d.
	if in.Breakout == nil {
		out = append(out, gatedFactor(FactorBreakout, "no breakout detected within the last 7d"))
	} else {
		v := 0
		switch in.Breakout.Kind {
		case "donchian_up":
			v = 1
		case "donchian_down":
			v = -1
		}
		out = append(out, Factor{
			Key:      FactorBreakout,
			Verdict:  v,
			Evidence: fmt.Sprintf("%s %.1fd ago: %s", in.Breakout.Kind, in.Breakout.AgeDays, in.Breakout.Detail),
		})
	}

	// tvrating — EXTERNAL TradingView TA rating. Additive 12th tile; NEVER enters
	// the edge/curve (that stays the ensemble prediction). It scores only once its
	// measured skill clears the gate; until then it is CONTEXT-only (see below).
	out = append(out, in.tvRatingFactor())

	return out
}

// modelLegFactor renders a model leg (forecast/gbm/meanrev) tile with the
// SAME lift>0 honesty gate the ensemble applies: an edgeless leg is shown
// gated with its measured lift, never down-weighted or hidden.
func (in Inputs) modelLegFactor(key, label string, prob, lift *float64, absentReason string) Factor {
	if prob == nil || lift == nil {
		return gatedFactor(key, absentReason)
	}
	if *lift <= 0 {
		return in.withSkill(gatedFactor(key, fmt.Sprintf(
			"no measured out-of-sample lift (lift=%+.3f ≤ 0) — leg dropped from the blend, never down-weighted", *lift)))
	}
	p := clamp01(*prob)
	return in.withSkill(Factor{
		Key:     key,
		Verdict: verdictFromProb(p),
		Evidence: fmt.Sprintf("%s P(up) %.1f%% with out-of-sample lift %+.3f over base rate",
			label, p*100, *lift),
	})
}

// alphaXFactor renders the cross-sectional alpha tile with the SAME lift>0
// honesty gate as the other model legs, but its own evidence framing: the
// prob is P(beat the same-day universe median) — RELATIVE outperformance,
// blended into the directional ensemble as a tilt, and the tile says so.
func (in Inputs) alphaXFactor() Factor {
	c := in.Components
	if c.AlphaXProb == nil || c.AlphaXLift == nil {
		return gatedFactor(FactorAlphaX,
			"no cross-sectional alpha score stored for this symbol/horizon")
	}
	if *c.AlphaXLift <= 0 {
		return in.withSkill(gatedFactor(FactorAlphaX, fmt.Sprintf(
			"no measured out-of-sample lift (lift=%+.3f ≤ 0) — leg dropped from the blend, never down-weighted", *c.AlphaXLift)))
	}
	p := clamp01(*c.AlphaXProb)
	return in.withSkill(Factor{
		Key:     FactorAlphaX,
		Verdict: verdictFromProb(p),
		Evidence: fmt.Sprintf("cross-sectional alpha: P(beat universe median) %.1f%% (OOS lift %+.3f) — relative-to-universe, blended as directional tilt",
			p*100, *c.AlphaXLift),
	})
}

// withSkill attaches the adaptive attribution's measured evidence for the
// factor's ensemble leg, when that leg has graded samples. Non-leg factors
// pass through unchanged — no skill chip is invented for them.
func (in Inputs) withSkill(f Factor) Factor {
	leg, ok := legByFactor[f.Key]
	if !ok {
		return f
	}
	if n, ok := in.SkillLegN[leg]; ok && n > 0 {
		nv := n
		f.SkillN = &nv
		if hr, ok := in.SkillHitRates[leg]; ok {
			hv := hr
			f.SkillHitRate = &hv
		}
		if ic, ok := in.SkillICs[leg]; ok {
			iv := ic
			f.SkillIC = &iv
		}
	}
	return f
}

func gatedFactor(key, reason string) Factor {
	return Factor{Key: key, Verdict: 0, Gated: true, GateReason: reason}
}

// verdictFromProb turns a P(up) leg into a tile arrow with the stated dead
// band: ≥55% bullish, ≤45% bearish, else neutral.
func verdictFromProb(p float64) int {
	if p >= 0.5+probDeadBand {
		return 1
	}
	if p <= 0.5-probDeadBand {
		return -1
	}
	return 0
}

// ShortVolZ computes the z-score of the LAST daily short-volume ratio vs the
// mean/stddev of all PRIOR ratios — the identical semantics (and gates) of
// shortsZ in internal/api/shorts.go: ok=false below ShortVolMinPrior prior
// days or when the baseline is (near-)flat, because a z against a flat
// baseline is meaningless.
func ShortVolZ(ratios []float64) (z float64, ok bool) {
	if len(ratios) < ShortVolMinPrior+1 {
		return 0, false
	}
	prior := ratios[:len(ratios)-1]
	mean := 0.0
	for _, v := range prior {
		mean += v
	}
	mean /= float64(len(prior))
	varSum := 0.0
	for _, v := range prior {
		varSum += (v - mean) * (v - mean)
	}
	sd := math.Sqrt(varSum / float64(len(prior)))
	if sd < 1e-9 {
		return 0, false
	}
	return (ratios[len(ratios)-1] - mean) / sd, true
}

// Payload is the JSON blob stored in composite_scores.payload and returned by
// GET /api/composite — everything behind one symbol's score.
type Payload struct {
	Horizon string   `json:"horizon"`
	Edge    float64  `json:"edge"` // calProb − 0.5
	RawProb float64  `json:"rawProb"`
	CalProb float64  `json:"calProb"`
	NUsed   int      `json:"nUsed"`
	PredTs  int64    `json:"predTs"` // the prediction row's timestamp
	Factors []Factor `json:"factors"`
	Ledger  Ledger   `json:"ledger"`
}

// ── small helpers ────────────────────────────────────────────────────────

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// fmtMoney renders a dollar magnitude compactly ($412K / $1.3M / $2.1B).
func fmtMoney(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e9:
		return fmt.Sprintf("$%.1fB", a/1e9)
	case a >= 1e6:
		return fmt.Sprintf("$%.1fM", a/1e6)
	case a >= 1e3:
		return fmt.Sprintf("$%.0fK", a/1e3)
	default:
		return fmt.Sprintf("$%.0f", a)
	}
}

// fmtSignedMoney is fmtMoney with an explicit sign (+$412K / -$88K / $0).
func fmtSignedMoney(v float64) string {
	switch {
	case v > 0:
		return "+" + fmtMoney(v)
	case v < 0:
		return "-" + fmtMoney(v)
	default:
		return "$0"
	}
}

// ─────────────────────────────────────────────────────────────────────────
// EXTERNAL TRADINGVIEW RATING — the measured 12th factor (appended block).
// A factor TILE with evidence + measured skill, additive to the 11. It NEVER
// enters the edge/curve computation (composite edge stays from the ensemble
// prediction) — it only annotates. Its SKILL is MEASURED before it scores:
// until the pooled IC of past reco_all vs realized forward returns clears the
// gate (n>=TVRatingMinN independent obs AND |IC|>=TVRatingICFloor), the tile is
// CONTEXT-only (verdict 0, gated) exactly like regime/shortvol. Once it clears,
// verdict = sign(reco_all) and it carries its measured skill chip.
// ─────────────────────────────────────────────────────────────────────────

// FactorTVRating is the 12th factor key (external TradingView rating).
const FactorTVRating = "tvrating"

// TVRatingMinN is the minimum independent (symbol, UTC-day) observations of
// past reco_all vs realized forward returns before the external rating may
// score. Mirrors the n>=30 honesty gates elsewhere (adaptive.MinCellSamples).
const TVRatingMinN = 30

// TVRatingICFloor is the minimum |IC| the external rating's measured skill must
// clear before it scores — below it, apparent edge is indistinguishable from
// noise, so the tile stays context-only with its measured number shown.
const TVRatingICFloor = 0.03

// TVRatingInfo is a symbol's latest external TradingView rating (the store's
// LatestTVRating reco_all + label), fed to the tile by the worker.
type TVRatingInfo struct {
	RecoAll float64 // TradingView reco_all in [-1,1]
	Label   string  // "Strong Buy" … "Strong Sell"
}

// tvRatingFactor renders the external-rating tile: gated absent when no rating
// is stored, CONTEXT-only (with the measured n) until the pooled skill clears
// the gate, and directional (verdict = sign(reco_all) + skill chip) once it has.
func (in Inputs) tvRatingFactor() Factor {
	if in.TVRating == nil {
		return gatedFactor(FactorTVRating, "no TradingView rating stored for this symbol")
	}
	label := in.TVRating.Label
	if label == "" {
		label = "Neutral"
	}
	evidence := fmt.Sprintf("TradingView rating: %s (%+.2f) — external, delayed; TradingView's OWN technical-analysis score on delayed data, NOT SignalDeck's model and not advice",
		label, in.TVRating.RecoAll)

	// GATE: the external rating scores only once its measured skill clears both
	// the independent-N floor and the |IC| floor. Below either, it is
	// context-only — shown, with the gate reason and the measured count.
	if in.TVSkillN < TVRatingMinN || math.Abs(in.TVSkillIC) < TVRatingICFloor {
		f := gatedFactor(FactorTVRating, fmt.Sprintf("external rating — not yet measured (%d/%d)", in.TVSkillN, TVRatingMinN))
		f.Evidence = evidence
		return f
	}
	// Cleared: directional by the rating's sign, carrying its measured skill chip.
	v := 0
	if in.TVRating.RecoAll > 0 {
		v = 1
	} else if in.TVRating.RecoAll < 0 {
		v = -1
	}
	hr, ic, n := in.TVSkillHitRate, in.TVSkillIC, in.TVSkillN
	return Factor{
		Key:          FactorTVRating,
		Verdict:      v,
		Evidence:     evidence,
		SkillHitRate: &hr,
		SkillIC:      &ic,
		SkillN:       &n,
	}
}
