// CONVICTION — the second axis, orthogonal to the forced-curve rank.
//
// Research (Danelfin AI Score vs its separate Low-Risk subscore; Seeking Alpha
// DISQUALIFYING a top rating when any factor is weak; Zacks/TipRanks publishing
// per-bucket hit rates) converges on one rule: a top RANK must never be read as
// certainty. The rank answers "where does this symbol sit in today's cross-
// section?"; CONVICTION answers "how much should you trust that rank as a real
// forward edge?"
//
// The honest anchor is the model's MEASURED realized accuracy, NOT the per-
// symbol calibrated probability. Those stored probabilities are demonstrably
// overconfident (thin per-symbol calibration maps raw 0.74 → cal 0.96 and raw
// 0.70 → cal 0.17), so a large |edge| must never buy conviction. The fleet is
// right only ~54% of the time, so even the #1 symbol is a modest edge at best —
// and an extreme calibrated probability the realized accuracy can't support is
// treated as OVERCONFIDENCE that LOWERS conviction. That is the honest antidote
// to reading "10/10" as a sure thing (the user's exact complaint).
//
// Conviction is a small ordinal (low/moderate/high), never a false-precise
// number, so it can't manufacture the certainty it exists to deny.
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

// Conviction thresholds. The CEILING is set by the model's MEASURED realized
// accuracy (win rate), NEVER by the per-symbol calibrated probability.
const (
	// strongWinRate: measured live accuracy at/above this is a strong edge (HIGH
	// ceiling). ~58% 1-day directional accuracy is genuinely strong.
	strongWinRate = 0.58
	// modestWinRate: at/above this (but below strong) the edge is real but modest
	// (MODERATE ceiling); below it a "proven" edge is still marginal (LOW ceiling).
	modestWinRate = 0.53
	// trustworthyWinRate: below this realized accuracy, an EXTREME calibrated
	// probability is untrustworthy overconfidence — it lowers conviction.
	trustworthyWinRate = 0.60
	// overconfidentEdge: |calProb − 0.5| at/above this (calProb ≥ 0.70 or ≤ 0.30)
	// is an extreme 1-day call the realized accuracy usually can't support.
	overconfidentEdge = 0.20
	// EdgeSlight: below this the symbol's OWN lean is inside coin-flip range.
	EdgeSlight = 0.02
	// convStaleAgeSec: a prediction older than ~a day is discounted a band.
	convStaleAgeSec = 26 * 3600
)

// ConvictionInputs are everything the assessment needs: per-symbol facts from
// the stored prediction plus the fleet-level measured-skill facts.
type ConvictionInputs struct {
	Edge       float64 // calProb − 0.5 (signed) — a RANK input, not a trusted probability
	PredAgeSec int64   // age of the underlying prediction
	NUsed      int     // ensemble legs behind the probability
	// Directional factor tallies for the agreement check (BuildFactors verdicts;
	// gated + context tiles are verdict 0 and so naturally excluded).
	Bull, Bear int
	// EdgeProvenLive: the platform's OWN calibrated predictions have cleared the
	// live out-of-sample track-record gate. WinRate is the measured realized
	// directional accuracy (0..1) — the CEILING on conviction.
	EdgeProvenLive bool
	WinRate        float64 // measured live accuracy (0 if unknown)
	SkillNote      string  // one-line reason for the skill state, rendered verbatim
}

// ConvictionResult is the assessed conviction with its plain-English drivers and
// the persistent risk caveat that must ride with every score.
type ConvictionResult struct {
	Band      Band     `json:"band"`
	Label     string   `json:"label"`     // "LOW conviction" etc.
	Drivers   []string `json:"drivers"`   // reasons, always shown (never hidden)
	SkillNote string   `json:"skillNote"` // the fleet live-edge status, verbatim
	RiskNote  string   `json:"riskNote"`  // persistent "not a certainty" caveat
}

// riskNote is the always-visible caveat. Framed like Danelfin's "probabilities,
// not certainties" and states the rank-not-probability distinction the user
// found missing.
const riskNote = "A high score is a RELATIVE rank of today's cross-section, not a probability of profit — even the top-ranked symbol can fall, and every position carries real risk of loss. Probabilities, not certainties; not investment advice."

// skillCeiling returns the highest conviction the MEASURED live accuracy can
// justify, with a plain-English reason. Nothing else can raise conviction above
// this.
func skillCeiling(proven bool, winRate float64) (Band, string) {
	if !proven {
		return BandLow, "model edge not yet proven on live out-of-sample results — conviction capped low"
	}
	switch {
	case winRate >= strongWinRate:
		return BandHigh, fmt.Sprintf("model's proven live accuracy is %.1f%% — a strong measured edge", winRate*100)
	case winRate >= modestWinRate:
		return BandModerate, fmt.Sprintf("model's proven live accuracy is only %.1f%% — a real but modest edge (capped below high; you are still wrong ~%.0f%% of the time)", winRate*100, (1-winRate)*100)
	default:
		return BandLow, fmt.Sprintf("model's proven live accuracy is %.1f%% — marginal, barely above a coin flip", winRate*100)
	}
}

// Assess computes conviction. The base band is the measured-skill CEILING; every
// per-symbol signal can only LOWER it. A large (calibration-inflated) edge never
// buys conviction the realized accuracy hasn't earned — an extreme calibrated
// probability the model can't back up is flagged as overconfidence.
func Assess(in ConvictionInputs) ConvictionResult {
	band, ceilNote := skillCeiling(in.EdgeProvenLive, in.WinRate)
	drivers := []string{ceilNote}
	mag := math.Abs(in.Edge)

	// Overconfidence: an EXTREME calibrated probability the realized accuracy
	// can't support LOWERS conviction (the raw→cal inflation we measured), rather
	// than raising it.
	if mag >= overconfidentEdge && in.WinRate < trustworthyWinRate {
		band = lower(band)
		impliedPct := (0.5 + mag) * 100
		if in.WinRate > 0 {
			drivers = append(drivers, fmt.Sprintf("calibrated probability is extreme (~%.0f%% implied) but the model is right only %.0f%% of the time — treat as overconfident, not a sure thing", impliedPct, in.WinRate*100))
		} else {
			drivers = append(drivers, fmt.Sprintf("calibrated probability is extreme (~%.0f%% implied) with no proven live accuracy — treat as overconfident", impliedPct))
		}
	}

	// The symbol's OWN lean is a coin flip.
	if mag < EdgeSlight {
		band = lower(band)
		drivers = append(drivers, fmt.Sprintf("edge %+.1fpp — within coin-flip range (±%.0fpp)", in.Edge*100, EdgeSlight*100))
	}

	// Discounts — each lowers the band once (floored at low).
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
