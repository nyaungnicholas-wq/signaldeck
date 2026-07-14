// CONVICTION — the second axis, orthogonal to the forced-curve rank.
//
// Research (Danelfin AI Score vs its separate Low-Risk subscore; Seeking Alpha
// DISQUALIFYING a top rating when any factor is weak; Zacks/TipRanks publishing
// per-bucket hit rates) converges on one rule: a top RANK must never be read as
// certainty. The rank answers "where does this symbol sit in today's cross-
// section?"; CONVICTION answers "how much should you trust that rank as a real
// forward edge?" A #1 rank built on a coin-flip-sized, statistically-unproven,
// or self-contradictory edge deserves LOW conviction — that is the honest
// antidote to "10/10 = sure thing" (the user's exact complaint).
//
// Conviction is computed from facts already on the stored prediction (edge
// magnitude, blend depth, freshness, factor agreement) plus ONE fleet fact —
// whether the platform's own calibrated predictions have proven a live edge
// out-of-sample. It is a small ordinal (low/moderate/high), never a false-
// precise number, so it can't manufacture the certainty it exists to deny.
package composite

import (
	"fmt"
	"math"
)

// Band is the conviction level.
type Band string

const (
	BandLow      Band = "low"
	BandModerate Band = "moderate"
	BandHigh     Band = "high"
)

// Edge-magnitude thresholds on |calProb − 0.5| — the size of the directional
// lean over a 50% coin flip, the primary conviction driver.
const (
	// EdgeSlight: below this the "edge" is inside coin-flip range.
	EdgeSlight = 0.02
	// EdgeClear: at/above this the read is materially directional.
	EdgeClear = 0.05
	// convStaleAgeSec: a prediction older than ~a day is discounted a band.
	convStaleAgeSec = 26 * 3600
)

// ConvictionInputs are everything the assessment needs: per-symbol facts from
// the stored prediction plus the fleet-level "is the edge proven live?" fact.
type ConvictionInputs struct {
	Edge       float64 // calProb − 0.5 (signed)
	PredAgeSec int64   // age of the underlying prediction
	NUsed      int     // ensemble legs behind the probability
	// Directional factor tallies for the agreement check (BuildFactors verdicts;
	// gated + context tiles are verdict 0 and so naturally excluded).
	Bull, Bear int
	// EdgeProvenLive: the platform's OWN calibrated predictions have cleared the
	// live out-of-sample track-record gate (enough independent resolutions AND a
	// win rate significantly above a coin flip). Currently false (in-sample
	// only) → conviction is capped below High for EVERY symbol, honestly.
	EdgeProvenLive bool
	SkillNote      string // one-line reason for the skill state, rendered verbatim
}

// ConvictionResult is the assessed conviction with its plain-English drivers and
// the persistent risk caveat that must ride with every score.
type ConvictionResult struct {
	Band     Band     `json:"band"`
	Label    string   `json:"label"`    // "LOW conviction" etc.
	Drivers  []string `json:"drivers"`  // reasons, always shown (never hidden)
	SkillNote string  `json:"skillNote"` // the fleet live-edge status, verbatim
	RiskNote string   `json:"riskNote"` // persistent "not a certainty" caveat
}

// riskNote is the always-visible caveat. Framed like Danelfin's "probabilities,
// not certainties" and states the rank-not-probability distinction the user
// found missing.
const riskNote = "A high score is a RELATIVE rank of today's cross-section, not a probability of profit — even the top-ranked symbol can fall, and every position carries real risk of loss. Probabilities, not certainties; not investment advice."

// Assess computes conviction. Only the edge magnitude can set the base band;
// every other signal can lower it but never raise it — conviction is
// deliberately conservative, so a top rank on a thin, stale, unproven, or
// contradictory edge cannot present as high conviction.
func Assess(in ConvictionInputs) ConvictionResult {
	mag := math.Abs(in.Edge)
	var drivers []string

	// 1) Base band from the size of the lean (the unarguable primary driver).
	var band Band
	switch {
	case mag >= EdgeClear:
		band = BandHigh
		drivers = append(drivers, fmt.Sprintf("edge %+.1fpp — a clear directional lean", in.Edge*100))
	case mag >= EdgeSlight:
		band = BandModerate
		drivers = append(drivers, fmt.Sprintf("edge %+.1fpp — only a slight lean over a coin flip", in.Edge*100))
	default:
		band = BandLow
		drivers = append(drivers, fmt.Sprintf("edge %+.1fpp — within coin-flip range (±%.0fpp)", in.Edge*100, EdgeSlight*100))
	}

	// 2) Cap-on-unproven-model (Seeking Alpha's disqualify-on-weak-signal): if
	//    the platform hasn't proven a live edge, NOTHING can be high conviction.
	if !in.EdgeProvenLive {
		if band == BandHigh {
			band = BandModerate
			drivers = append(drivers, "capped below high: the model's edge is not yet proven on live out-of-sample results")
		} else {
			drivers = append(drivers, "model edge not yet proven live (in-sample only)")
		}
	}

	// 3) Discounts — each lowers the band once (floored at low).
	if in.PredAgeSec > convStaleAgeSec {
		band = lower(band)
		drivers = append(drivers, fmt.Sprintf("underlying prediction is %.0fh old", float64(in.PredAgeSec)/3600))
	}
	if in.NUsed < 2 {
		band = lower(band)
		drivers = append(drivers, fmt.Sprintf("only %d signal(s) blended", in.NUsed))
	}
	// Factor disagreement: bullish and bearish tiles both present and neither
	// clearly dominates (a contradictory read is low-conviction on its face).
	if in.Bull > 0 && in.Bear > 0 {
		dominant := in.Bull >= 2*in.Bear || in.Bear >= 2*in.Bull
		if !dominant {
			band = lower(band)
			drivers = append(drivers, fmt.Sprintf("factors disagree (%d bullish vs %d bearish)", in.Bull, in.Bear))
		}
	}

	return ConvictionResult{
		Band:      band,
		Label:     label(band),
		Drivers:   drivers,
		SkillNote: in.SkillNote,
		RiskNote:  riskNote,
	}
}

// FactorAgreement tallies directional (verdict ±1) factor tiles. Gated and
// context-only tiles carry verdict 0, so they are excluded automatically.
func FactorAgreement(factors []Factor) (bull, bear int) {
	for _, f := range factors {
		switch {
		case f.Verdict > 0:
			bull++
		case f.Verdict < 0:
			bear++
		}
	}
	return bull, bear
}

// lower drops a band one step, floored at low.
func lower(b Band) Band {
	switch b {
	case BandHigh:
		return BandModerate
	case BandModerate:
		return BandLow
	default:
		return BandLow
	}
}

// label is the human-facing conviction label.
func label(b Band) string {
	switch b {
	case BandHigh:
		return "HIGH conviction"
	case BandModerate:
		return "MODERATE conviction"
	default:
		return "LOW conviction"
	}
}
