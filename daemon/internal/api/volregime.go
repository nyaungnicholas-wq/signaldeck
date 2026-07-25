package api

import "net/http"

// volRegime serves the platform's one validated-edge forecast: per-stock
// volatility-regime calls (elevated vs calm next quarter), highest conviction
// first, each carrying the MEASURED walk-forward accuracy at its conviction
// tier — never an invented probability. The methodology + honest caveats ship
// in the payload so the surface can never overstate skill.
func (d Deps) volRegime(w http.ResponseWriter, r *http.Request) {
	fcs, err := d.St.VolForecasts(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	high := 0
	for _, f := range fcs {
		if f.Conviction >= 0.8 {
			high++
		}
	}
	writeJSON(w, map[string]any{
		"forecasts":      fcs,
		"highConviction": high,
		"what":           "Will this stock's realized volatility over the next ~quarter (63 trading days) be ELEVATED (upper half of its recent range) or CALM (lower half)? This is NOT a price-direction call.",
		"whyHonest":      "Direction is a coin flip (~52-55% ceiling, proven). Volatility is persistent and genuinely forecastable. historicalAccuracy is the MEASURED out-of-sample number at each conviction tier, walk-forward + non-overlapping + quarter-clustered over ~1,000 stocks / 7.5 years — not a claim. Independently re-verified 2026-07-17 (second implementation, 904 stocks): every band replicated at or above its claim except the top tier, which re-measured 0.733 — inside the original CI (0.696-0.789); treat 0.76 as the optimistic end of ~0.73-0.76. Symbols whose recent window contains a wild (likely split-corrupted) move are refused rather than forecast.",
		"accuracyTiers": map[string]string{
			"very-high conviction (>0.9)": "76.0% (95% CI 0.696-0.789)",
			"high conviction (>0.8)":      "74.3% (0.691-0.782)",
			"moderate conviction (>0.5)":  "69.7%",
			"all decisions":               "63.7%",
		},
		"caveat": "A regime call is not a trade by itself: the natural expression is options/vol (e.g. straddles), whose real P&L depends on implied-vol pricing this platform does not yet ingest. Treat elevated/calm as situational awareness with a measured hit rate, not a guaranteed outcome.",
	})
}
