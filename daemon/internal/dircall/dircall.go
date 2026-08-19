// Package dircall provides the two fixes for the probability calibrator defect:
// threshold at the prevailing base rate (clamped to [0.3, 0.7]), and refuse a
// cross-section that does not straddle the threshold or exceeds max agreement.
package dircall

import (
	"fmt"
	"math"
)

// DefaultMaxAgreement is the bound above which a day's cross-section is treated
// as one market call rather than N forecasts. Measured on the live DB: healthy
// days ran 0.75-0.86 agreement, the broken ones 0.95-1.00. Must be a constant
// strictly between 0.86 and 0.95 - use 0.90.
const DefaultMaxAgreement = 0.90

// Threshold returns the honest decision boundary for a directional call: the
// prevailing base rate, NOT 0.5. Clamped into [0.3, 0.7] so a degenerate
// window cannot push the boundary to an extreme and make every call one-way.
func Threshold(upRate float64) float64 {
	if upRate < 0.3 {
		return 0.3
	}
	if upRate > 0.7 {
		return 0.7
	}
	return upRate
}

// Call reports whether prob is an UP call at the given threshold. Use >= so it
// matches the `prob >= 0.5` convention every grader in this tree already uses.
func Call(prob, threshold float64) bool {
	return prob >= threshold
}

// Agreement is the fraction of a cross-section's calls pointing the same way:
// 0.5 when balanced, 1.0 when unanimous. Defined as max(f, 1-f) where f is the
// fraction of probs at or above the threshold. Returns 1.0 for an empty or
// single-element slice (one call is trivially unanimous).
func Agreement(probs []float64, threshold float64) float64 {
	n := len(probs)
	if n <= 1 {
		return 1.0
	}
	up := 0
	for _, p := range probs {
		if p >= threshold {
			up++
		}
	}
	f := float64(up) / float64(n)
	return math.Max(f, 1-f)
}

// Straddles reports whether the cross-section falls on BOTH sides of the
// threshold. Empty or single-element slices do not straddle.
func Straddles(probs []float64, threshold float64) bool {
	if len(probs) <= 1 {
		return false
	}
	hasUp, hasDown := false, false
	for _, p := range probs {
		if p >= threshold {
			hasUp = true
		} else {
			hasDown = true
		}
		if hasUp && hasDown {
			return true
		}
	}
	return false
}

// Publishable reports whether a day's cross-section may be published as N
// independent directional forecasts, and why not when it may not. A book that
// does not straddle the threshold, or whose agreement exceeds maxAgreement, is
// ONE market call replicated N times. The reason string must be non-empty on
// every refusal - a silent refusal is how the original defect survived four
// months of green dashboards.
func Publishable(probs []float64, threshold, maxAgreement float64) (bool, string) {
	if len(probs) == 0 {
		return false, "empty cross-section: nothing to publish"
	}
	if !Straddles(probs, threshold) {
		side := "below"
		if probs[0] >= threshold {
			side = "at or above"
		}
		return false, fmt.Sprintf(
			"all %d probabilities fall %s the %.4f threshold: this is ONE market call, not %d forecasts",
			len(probs), side, threshold, len(probs))
	}
	if a := Agreement(probs, threshold); a > maxAgreement {
		return false, fmt.Sprintf(
			"agreement %.3f over %d names exceeds the %.2f bound at threshold %.4f: too near-unanimous to be %d independent forecasts",
			a, len(probs), maxAgreement, threshold, len(probs))
	}
	return true, ""
}
