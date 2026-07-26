package gbm

import "strings"

// SelfReferentialKey reports whether a feature-store key is a MODEL OUTPUT that
// must never be fed back into a model as an input.
//
// Two kinds live here:
//
//   - the BLEND's own probabilities (pred_raw / pred_cal). A leg trained on
//     these learns "copy the ensemble" instead of finding independent structure,
//     and couples the leg to the very blend it exists to diversify.
//   - each LEG's own prior output (gbm_prob / meanrev_prob / alphax_prob). A leg
//     fed its own past prediction is grading a fixed point, not a forecast.
//
// It lives in this package because this package is what every one of those
// consumers is ultimately feeding: the per-symbol GBM trainer, the pooled
// cross-sectional engine and the research lab each kept a private copy of this
// list, and finding H6 is what a private copy costs. The mean-reversion leg's
// sample builder reads pred_raw ~40 lines below the file that declares the
// doctrine — and pred_raw is the blend output computed WITH the mean-reversion
// leg inside it, so the leg inverts a number that already contains its own
// inversion. A switch statement in one file cannot stop that; a shared predicate
// every builder calls can.
//
// A derived presence indicator (KEY+"__has") is excluded with its base key: a
// bit saying "the blend had an opinion here" leaks the same self-reference the
// value does.
func SelfReferentialKey(k string) bool {
	switch strings.TrimSuffix(k, PresenceSuffix) {
	case "pred_raw", "pred_cal", "gbm_prob", "meanrev_prob", "alphax_prob":
		return true
	}
	return false
}

// PresenceSuffix marks a derived per-feature presence indicator: for base key K,
// K+PresenceSuffix is 1 when K was present in the raw row and 0 when absent.
// Named here so the exclusion predicate and the builders that generate the
// indicators agree on one spelling.
const PresenceSuffix = "__has"

// FilterSelfReferential returns the subset of keys safe to train on, preserving
// order. Builders assembling a feature layout should run their key union through
// this rather than re-deriving the exclusion list.
func FilterSelfReferential(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if SelfReferentialKey(k) {
			continue
		}
		out = append(out, k)
	}
	return out
}
