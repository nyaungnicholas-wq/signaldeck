package gbm

import "strings"

// SelfReferentialKey reports whether a feature-store key is a MODEL OUTPUT or a
// LABEL-DERIVED STATISTIC that must never be fed back into a model as an input.
//
// Four kinds live here:
//
//   - the BLEND's own probabilities (pred_raw / pred_cal). A leg trained on
//     these learns "copy the ensemble" instead of finding independent structure,
//     and couples the leg to the very blend it exists to diversify.
//   - each LEG's own prior output (gbm_prob / meanrev_prob / alphax_prob /
//     forecast_prob / expectancy_hit_rate). A leg fed another leg's prediction
//     can simply copy it, and the copy then re-enters the blend beside the
//     original as if it were independent evidence — the same forecast counted
//     twice. Point-in-time is preserved for all of these, so the failure is
//     DOUBLE COUNTING and shortcut learning, not look-ahead.
//   - any statistic computed FROM THE LABELS (forecast_lift, and by the suffix
//     rules below every *_lift and *_hit_rate). An out-of-sample accuracy
//     number is a summary of the outcomes the model is being asked to predict;
//     it is the one class of input that has no defensible reading at all.
//   - encodings of which legs cleared their gates (n_used). Every gate is a
//     lift threshold measured on labels, so the count is a coarse label
//     statistic wearing a cardinality's clothes.
//
// The suffix rules exist because the original list named the five legs that had
// been CAUGHT rather than the class, and the 2026-07-26 re-audit then found four
// more outputs on the live training set (forecast_prob 231,981 rows,
// forecast_lift 231,981, expectancy_hit_rate 238,910, n_used 248,391). A future
// gbm_lift or meanrev_hit_rate must be refused the day it is first written, not
// the day an auditor next reads this file.
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
	base := strings.TrimSuffix(k, PresenceSuffix)
	switch base {
	case "pred_raw", "pred_cal", // the blend's own probabilities
		"gbm_prob", "meanrev_prob", "alphax_prob", "forecast_prob", // leg outputs
		"n_used": // which legs cleared their label-graded gates
		return true
	}
	// Class rules, so a leg added later cannot re-open the hole by choosing a
	// name nobody remembered to add above.
	return strings.HasSuffix(base, "_lift") || strings.HasSuffix(base, "_hit_rate")
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
