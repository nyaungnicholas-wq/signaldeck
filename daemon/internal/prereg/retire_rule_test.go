// The auto-retire rule is enforced in Python (tools/accuracy_registry.py) and
// chained in Go — two implementations of one commitment. The digest is what
// keeps them one: tools/test_accuracy_registry.py pins its canonicalization to
// the SAME hex constant below, so if either side's thresholds or wording move,
// exactly one pinned test breaks and the divergence is caught in the same
// commit that caused it.
package prereg

import "testing"

// frozenRetireRuleDigest is sha256 of the canonical rule string, computed at
// registration (2026-07-26, while every directional verdict was still
// INSUFFICIENT). Changing AutoRetireRule() legitimately requires updating this
// constant AND the Python pin in the same commit — and the chain will append
// an AMENDMENT record for the changed hash, which is the visibility the
// freeze exists to buy.
// Moved 2026-08-06 from d02c3374…: the rule's wording named UTC days while the
// code had been migrated to fold on trading days, so the chained rule and the
// enforced rule had stopped being one rule — the exact failure the pin exists to
// catch. Only the UNIT LABEL changed; the thresholds (30 observations, 10
// distinct days, upper bound below the prequential null) are untouched, and
// folding on trading days merges the phantom days a UTC-midnight cut invented,
// so clearing the 10-day floor gets HARDER, never easier.
const frozenRetireRuleDigest = "353450b995a52bcea9257a8520369d8841e7fa71c70295aae925dc7cf03b429f"

func TestAutoRetireRuleDigestIsFrozen(t *testing.T) {
	if got := AutoRetireRule().Hash(); got != frozenRetireRuleDigest {
		t.Fatalf("auto-retire rule digest = %s, want the frozen %s — the kill criterion "+
			"moved after registration; if intended, update this pin and the Python pin "+
			"(tools/test_accuracy_registry.py) in the same commit", got, frozenRetireRuleDigest)
	}
}

func TestAutoRetireRuleThresholdsMatchTheRegistryFloors(t *testing.T) {
	r := AutoRetireRule()
	// These mirror MIN_INDEPENDENT_N / MIN_DISTINCT_DAYS in the grader and
	// MinIndependentN / MinDistinctDays in the chained grading protocol — one
	// pair of evidence floors governs every verdict.
	if r.MinIndependentN != 30 || r.MinDistinctDays != 10 {
		t.Fatalf("retire rule floors = (%d, %d), want (30, 10) — the rule no longer "+
			"matches the registered grading protocol", r.MinIndependentN, r.MinDistinctDays)
	}
	p := GradingProtocol("", "")
	if r.MinIndependentN != p.MinIndependentN || r.MinDistinctDays != p.MinDistinctDays {
		t.Fatalf("retire rule floors (%d, %d) diverge from the grading protocol's (%d, %d)",
			r.MinIndependentN, r.MinDistinctDays, p.MinIndependentN, p.MinDistinctDays)
	}
}
