package gbm

import "testing"

// ── H6 · the self-reference doctrine, made testable ──────────────────────────
//
// The adversarial review found the mean-reversion leg's sample builder reading
// pred_raw — the blend output that CONTAINS the mean-reversion leg — in the same
// file that declares pred_raw off-limits. The list was correct and the builder
// ignored it, because the list was a private switch statement rather than a
// shared predicate anything could be held to. These tests pin the shared one.

func TestSelfReferentialKey_CoversEveryBlendAndLegOutput(t *testing.T) {
	// pred_raw heads this list deliberately: it is the blend output the
	// mean-reversion leg consumes and simultaneously contributes to, so a builder
	// reading it closes a loop on itself.
	for _, k := range []string{"pred_raw", "pred_cal", "gbm_prob", "meanrev_prob", "alphax_prob"} {
		if !SelfReferentialKey(k) {
			t.Fatalf("%q is a model output and must be refused as a model input", k)
		}
		// The presence bit leaks the same self-reference the value does.
		if !SelfReferentialKey(k + PresenceSuffix) {
			t.Fatalf("%q presence indicator must be excluded with its base key", k)
		}
	}
}

func TestSelfReferentialKey_AllowsGenuineInputs(t *testing.T) {
	// Real observations must NOT be swept up: over-excluding starves the model
	// as surely as under-excluding corrupts it.
	for _, k := range []string{
		"pressure_score", "forecast_prob", "expectancy_hit_rate", "sentiment_score",
		"vix_level", "micro_spread", "adx14", "pattern_bias", "pred_rawness",
	} {
		if SelfReferentialKey(k) {
			t.Fatalf("%q is an observation, not a model output — must remain trainable", k)
		}
	}
}

func TestFilterSelfReferential_DropsOutputsKeepsOrder(t *testing.T) {
	in := []string{"adx14", "pred_raw", "forecast_prob", "meanrev_prob", "vix_level", "pred_cal__has"}
	got := FilterSelfReferential(in)
	want := []string{"adx14", "forecast_prob", "vix_level"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
