// CANDLESTICK-PATTERNS + MODEL-FED INDICATORS wave — featureVersion 7 features.
//
// This file builds the wave's new PREDICTION FEATURES from a symbol's daily
// bars: a subset of the model-fed technical-indicator signals plus a
// candlestick pattern_bias. They JOIN the feature vector so the per-symbol GBM
// and the pooled alphax leg can LEARN whether they carry edge — the OOS-lift
// gate is the referee; nothing is trusted on faith. None is a model output, so
// none is on the self-reference exclusion lists (excludedGBMKey / alphax
// excludedKey), which exclude by base name and match none of these keys.
//
// Same honesty contract as every other feature: each field is ABSENT when it
// cannot be computed (too little history, a degenerate window, or — for
// pattern_bias — no pattern firing on the latest bar). Absence is information,
// not a fabricated zero.
package pipeline

import (
	"github.com/nyaungnicholas-wq/signaldeck/internal/candles"
	"github.com/nyaungnicholas-wq/signaldeck/internal/indicators"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// indicatorPatternFeatures returns the featureVersion-7 model-fed features
// derived from a symbol's daily bars — shared across horizons (all computed
// from the same window). The map is never nil; a field is present only when
// computable.
func indicatorPatternFeatures(daily []md.Bar) map[string]float64 {
	out := map[string]float64{}

	// Model-fed indicator signals. Only the FIVE wired into the vector this
	// wave are added; internal/indicators also computes williams_r14 and
	// keltner_pos, held back until a later wave proves them out.
	sig := indicators.Compute(daily)
	if sig.StochK != nil {
		out["stoch_k"] = *sig.StochK
	}
	if sig.ADX14 != nil {
		out["adx14"] = *sig.ADX14
	}
	if sig.CCI20 != nil {
		out["cci20"] = *sig.CCI20
	}
	if sig.BBPctB != nil {
		out["bb_pctb"] = *sig.BBPctB
	}
	if sig.SupertrendDir != nil {
		out["supertrend_dir"] = *sig.SupertrendDir
	}

	// pattern_bias: the NET directional lean of the candlestick patterns firing
	// on the LATEST bar (sum of each firing pattern's Bias). Present only when a
	// pattern actually fires — the key's PRESENCE carries "a candlestick signal
	// exists here" and its value the net direction (0 when firing patterns
	// conflict); it is ABSENT when none fire or history is too thin, never a
	// fabricated neutral zero.
	if pats := candles.Detect(daily); len(pats) > 0 {
		bias := 0
		for _, p := range pats {
			bias += p.Bias
		}
		out["pattern_bias"] = float64(bias)
	}
	return out
}
