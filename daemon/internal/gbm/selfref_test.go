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

// ── A7 · the four outputs the original five-name list let through ────────────
//
// The 2026-07-26 re-audit found the list naming only the legs that had been
// caught, not the CLASS. Four more model outputs were reaching the training set
// on the live DB (counts re-measured 2026-07-26, mode=ro, over the features
// table): forecast_prob 231,981 rows, forecast_lift 231,981,
// expectancy_hit_rate 238,910, n_used 248,391.
//
// Point-in-time is preserved for all four, so this is not look-ahead — it is
// DOUBLE COUNTING. The GBM can copy the forecast leg, and the copy then enters
// the blend beside the original as if it were independent evidence.
// forecast_lift is the worst of them: an accuracy statistic computed FROM THE
// LABELS has no business being an input at all.
func TestSelfReferentialKey_CoversLegOutputsAndLabelDerivedStats(t *testing.T) {
	for _, k := range []string{
		"forecast_prob",        // the walk-forward logistic leg's own output
		"forecast_lift",        // that leg's OOS accuracy — computed from labels
		"expectancy_hit_rate",  // the expectancy leg's output, also a label statistic
		"n_used",               // how many legs cleared their (label-graded) gates
		"gbm_lift", "meanrev_lift", "alphax_lift", // class rule, not yet stored
		"pressure_hit_rate", // class rule: any hit rate is a label statistic
	} {
		if !SelfReferentialKey(k) {
			t.Fatalf("%q is a model output or a label-derived statistic and must be refused as a model input", k)
		}
		if !SelfReferentialKey(k + PresenceSuffix) {
			t.Fatalf("%q presence indicator must be excluded with its base key", k)
		}
	}
}

func TestSelfReferentialKey_AllowsGenuineInputs(t *testing.T) {
	// Real observations must NOT be swept up: over-excluding starves the model
	// as surely as under-excluding corrupts it.
	//
	// pressure_score and rank_pct are deliberately here. Both were considered
	// for exclusion under A7 and kept: pressure_score is a fixed-weight sum of
	// technical components (no weight is fit to an outcome), and rank_pct is
	// cross-sectional relative strength computed by ranking.FromBars off daily
	// bars. Neither reads a label nor a model probability.
	for _, k := range []string{
		"pressure_score", "rank_pct", "sentiment_score", "sentiment_n",
		"vix_level", "micro_spread", "adx14", "pattern_bias", "pred_rawness",
		"comp_rsi", "trend_class", "tv_reco",
	} {
		if SelfReferentialKey(k) {
			t.Fatalf("%q is an observation, not a model output — must remain trainable", k)
		}
	}
}

func TestFilterSelfReferential_DropsOutputsKeepsOrder(t *testing.T) {
	in := []string{"adx14", "pred_raw", "forecast_prob", "meanrev_prob", "vix_level", "pred_cal__has", "forecast_lift", "n_used"}
	got := FilterSelfReferential(in)
	want := []string{"adx14", "vix_level"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
