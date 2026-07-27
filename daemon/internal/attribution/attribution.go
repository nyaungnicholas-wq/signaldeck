// Package attribution is SignalDeck's blended-evidence engine.
//
// It combines two evidence sources that MUST NEVER be confused:
//
//   - HISTORICAL PRIOR: the regime/state-conditioned base hit rate from ~2y of
//     bars (the expectancy tables — "when this symbol was last in a state like
//     today's, forward returns were positive X% of N times"). This is the
//     prior/analog/baseline.
//   - LIVE EVIDENCE: the deployed engine's OWN resolved predictions for the same
//     symbol + horizon. This is for calibration and drift, NOT for manufacturing
//     certainty when the sample is thin.
//
// The blend is empirical Bayes: the historical prior leads until enough
// INDEPENDENT live outcomes accrue, after which live evidence earns weight —
// by construction (its weight is liveN/(priorEff+liveN)), never by fiat. The
// engine reports BOTH sample sizes and which source is driving, and it draws
// the honest distinction the doctrine insists on: "underpowered live
// attribution" is NOT "no edge" — the latter requires BOTH sources to be weak.
//
// Pure functions only; all data loading lives in internal/api/attribution.go.
package attribution

import (
	"fmt"
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
)

// LivePriorThreshold is the number of INDEPENDENT live resolved outcomes for a
// (symbol, horizon) below which live evidence cannot, on its own, carry the
// confidence — the historical prior stays the main driver. Mirrors the app's
// other n>=30 honesty gates.
const LivePriorThreshold = 30

// PriorStrength caps how many pseudo-observations the historical prior is worth
// in the blend: a rich analog (large N) is trusted up to this many effective
// samples, a thin one proportionally less. Prevents EITHER source from becoming
// overconfident from thin data.
const PriorStrength = 20.0

// minAnalogN is the historical sample below which the prior itself is too thin
// to call the regime match "good".
const minAnalogN = 20

// Evidence is one source's summary: a directional hit rate over N samples.
// For the prior, HitRate = P(fwd>0) in the matched historical state. For live,
// HitRate = the engine's realized DIRECTIONAL ACCURACY (predUp==actualUp).
type Evidence struct {
	HitRate float64
	N       int
}

// Blend returns the empirical-Bayes posterior hit rate and the WEIGHT the live
// evidence carried (0..1). priorEff = min(prior.N, PriorStrength) so a thin
// prior can't dominate either; with no evidence at all it returns an honest
// 0.5 / 0 weight.
func Blend(prior, live Evidence) (posterior, liveWeight float64) {
	priorEff := math.Min(float64(prior.N), PriorStrength)
	denom := priorEff + float64(live.N)
	if denom <= 0 {
		return 0.5, 0
	}
	posterior = (prior.HitRate*priorEff + live.HitRate*float64(live.N)) / denom
	liveWeight = float64(live.N) / denom
	return posterior, liveWeight
}

// Wilson is the 95% score interval for hits/n — the same honest small-sample
// band used elsewhere in the app. Returns (0,1) on n==0.
//
// It delegates to clusterstat.WilsonEffAt with effN = n. The raw count is
// legitimate at THIS call shape — the live evidence is one symbol's record
// deduplicated to one row per UTC day, so its design effect is 1 by
// construction (see api's attribution_cluster_test) — but the arithmetic lives
// in clusterstat so the tree holds ONE Wilson implementation.
func Wilson(hits, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 1
	}
	iv := clusterstat.WilsonEffAt(float64(hits)/float64(n), float64(n), 1.96)
	return iv.Lo, iv.Hi
}

// RegimeMatch grades how well today's setup is covered by historical analogs.
type RegimeMatch string

const (
	MatchGood RegimeMatch = "good"     // matched state, analog N >= minAnalogN
	MatchWeak RegimeMatch = "weak"     // matched state but thin, or symbol-level fallback
	MatchNone RegimeMatch = "none"     // no usable historical prior at all
)

// Report is the mandated blended-evidence output.
type Report struct {
	Symbol  string `json:"symbol"`
	Horizon string `json:"horizon"`
	Regime  string `json:"regime"` // current regime label ("" = unclassified)

	// The two sample sizes, ALWAYS reported and never conflated.
	LiveN     int `json:"liveResolvedN"`
	HistN     int `json:"historicalPriorN"`

	PriorHit  float64 `json:"priorHitRate"` // historical analog P(up)
	LiveAcc   float64 `json:"liveAccuracy"` // realized directional accuracy (blank meaning if LiveN small)

	RegimeMatch     RegimeMatch `json:"regimeMatch"`
	CalibratedProb  float64     `json:"calibratedProbability"` // the blended posterior
	BandLo          float64     `json:"uncertaintyLo"`
	BandHi          float64     `json:"uncertaintyHi"`
	LiveWeight      float64     `json:"liveWeight"` // 0..1 — how much the blend leans on live vs prior
	Driver          string      `json:"driver"`     // "historical-prior" | "blended" | "live" | "insufficient"

	AttributionSupported bool   `json:"attributionSupported"`
	Explanation          string `json:"explanation"` // the most honest statement available
}

// Assess produces the report from the two evidence sources + context, following
// the doctrine's truthfulness rules verbatim in the Explanation.
func Assess(symbol, horizon, regime string, prior, live Evidence, matchedState bool) Report {
	post, liveW := Blend(prior, live)
	hits := int(math.Round(live.HitRate * float64(live.N)))
	lo, hi := Wilson(hits, live.N)
	// When live is thin, its own band is uninformative — widen honesty by
	// reporting the prior's band instead as the effective uncertainty.
	if live.N < LivePriorThreshold {
		phits := int(math.Round(prior.HitRate * float64(prior.N)))
		lo, hi = Wilson(phits, prior.N)
	}

	r := Report{
		Symbol: symbol, Horizon: horizon, Regime: regime,
		LiveN: live.N, HistN: prior.N,
		PriorHit: prior.HitRate, LiveAcc: live.HitRate,
		CalibratedProb: post, BandLo: lo, BandHi: hi, LiveWeight: liveW,
	}

	// Regime match quality.
	switch {
	case prior.N == 0:
		r.RegimeMatch = MatchNone
	case matchedState && prior.N >= minAnalogN:
		r.RegimeMatch = MatchGood
	default:
		r.RegimeMatch = MatchWeak
	}

	// Driver + attribution support.
	switch {
	case prior.N == 0 && live.N == 0:
		r.Driver, r.AttributionSupported = "insufficient", false
	case live.N >= LivePriorThreshold:
		r.Driver, r.AttributionSupported = "blended", true
		if liveW >= 0.75 {
			r.Driver = "live"
		}
	default:
		r.Driver, r.AttributionSupported = "historical-prior", false
	}

	r.Explanation = explain(r, matchedState)
	return r
}

// explain writes the single most honest sentence for this evidence state,
// using the doctrine's preferred phrasing. It NEVER fabricates a cause and
// NEVER conflates thin live volume with lack of edge.
func explain(r Report, matchedState bool) string {
	switch r.Driver {
	case "insufficient":
		return fmt.Sprintf(
			"Neither source is strong enough: %d live resolved outcomes and %d historical analogs. Report uncertainty, not a story — this is genuine lack of evidence, not a measured lack of edge.",
			r.LiveN, r.HistN)
	case "historical-prior":
		base := fmt.Sprintf(
			"Live sample is insufficient for strong attribution (%d resolved, need %d); historical priors remain the main driver: %d analogs in a %s-match state put the base hit rate at %.0f%%. ",
			r.LiveN, LivePriorThreshold, r.HistN, r.RegimeMatch, r.PriorHit*100)
		if r.RegimeMatch == MatchGood {
			return base + "The historical analog is well-populated, so the prior is informative even though live attribution is still underpowered."
		}
		return base + "Both live and analog evidence are thin here, so treat the calibrated probability as weakly held — underpowered, but not evidence of no edge."
	case "blended", "live":
		return fmt.Sprintf(
			"Enough live evidence (%d resolved) to earn %.0f%% of the blend; live directional accuracy is %.0f%% against a %.0f%% historical prior (%d analogs). Calibrated probability %.0f%% [%.0f–%.0f%% 95%%].",
			r.LiveN, r.LiveWeight*100, r.LiveAcc*100, r.PriorHit*100, r.HistN, r.CalibratedProb*100, r.BandLo*100, r.BandHi*100)
	default:
		return "No specific attribution is supported with confidence yet."
	}
}
