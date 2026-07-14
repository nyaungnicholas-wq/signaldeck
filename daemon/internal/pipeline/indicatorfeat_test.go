// CANDLESTICK-PATTERNS + MODEL-FED INDICATORS wave — featureVersion 7 feature
// tests: the indicator+pattern feature builder (present with history, absent
// when thin, pattern_bias only when a shape fires) and a regression guard that
// the new keys are NOT swept up by the self-reference exclusion lists.
package pipeline

import (
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func risingDailyBars(n int) []md.Bar {
	bars := make([]md.Bar, n)
	for i := 0; i < n; i++ {
		c := 100 + float64(i)
		bars[i] = md.Bar{TF: md.TF1d, Ts: int64(i) * 86400, Open: c - 0.5, High: c + 0.2, Low: c - 0.7, Close: c}
	}
	return bars
}

func TestIndicatorPatternFeatures_PresentWithHistory(t *testing.T) {
	m := indicatorPatternFeatures(risingDailyBars(80))
	for _, k := range []string{"stoch_k", "adx14", "cci20", "bb_pctb", "supertrend_dir"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("feature %q absent on an 80-bar series; map=%v", k, m)
		}
	}
	// williams_r14 / keltner_pos are computed by internal/indicators but held
	// back from the vector this wave — they must NOT appear here.
	for _, k := range []string{"williams_r14", "keltner_pos"} {
		if _, ok := m[k]; ok {
			t.Fatalf("feature %q should not be wired into the vector this wave", k)
		}
	}
}

func TestIndicatorPatternFeatures_AbsentWhenThin(t *testing.T) {
	m := indicatorPatternFeatures(risingDailyBars(5))
	for _, k := range []string{"stoch_k", "adx14", "cci20", "bb_pctb", "supertrend_dir"} {
		if _, ok := m[k]; ok {
			t.Fatalf("feature %q should be absent with 5 bars; map=%v", k, m)
		}
	}
}

func TestIndicatorPatternFeatures_PatternBiasOnFiring(t *testing.T) {
	bars := risingDailyBars(80)
	X := bars[len(bars)-1].Close
	// Replace the last two bars with a bearish bar + a bullish engulfing so a
	// directional pattern fires on the latest bar.
	bars[len(bars)-2] = md.Bar{TF: md.TF1d, Ts: bars[len(bars)-2].Ts, Open: X + 2, High: X + 2.5, Low: X - 0.5, Close: X}
	bars[len(bars)-1] = md.Bar{TF: md.TF1d, Ts: bars[len(bars)-1].Ts, Open: X - 1, High: X + 4, Low: X - 1.5, Close: X + 3.5}

	m := indicatorPatternFeatures(bars)
	bias, ok := m["pattern_bias"]
	if !ok {
		t.Fatalf("pattern_bias should be present when a pattern fires; map=%v", m)
	}
	if bias <= 0 {
		t.Fatalf("pattern_bias = %v, want positive for a bullish engulfing", bias)
	}
}

func TestIndicatorPatternFeatures_PatternBiasAbsentWhenNoFiring(t *testing.T) {
	// A perfectly flat series fires no candlestick pattern → pattern_bias absent
	// (absence is information, not a fabricated neutral zero).
	flat := make([]md.Bar, 80)
	for i := range flat {
		flat[i] = md.Bar{TF: md.TF1d, Ts: int64(i) * 86400, Open: 50, High: 50, Low: 50, Close: 50}
	}
	if _, ok := indicatorPatternFeatures(flat)["pattern_bias"]; ok {
		t.Fatal("pattern_bias must be absent when no pattern fires")
	}
}

func TestNewV7KeysNotExcludedFromModels(t *testing.T) {
	// The self-reference exclusion lists (gbmtrain.excludedGBMKey and alphax's
	// excludedKey) must EXCLUDE only model outputs. The v7 features are ordinary
	// inputs — the GBM/alphax legs must be allowed to learn from them.
	for _, k := range []string{"stoch_k", "adx14", "cci20", "bb_pctb", "supertrend_dir", "pattern_bias"} {
		if excludedGBMKey(k) {
			t.Fatalf("v7 feature %q is wrongly excluded from the GBM (it is not a model output)", k)
		}
	}
}
