package api

import (
	"context"
	"net/http"
	"time"
)

// structuralRegimes serves the market-structure regime forecasts validated by
// the 2026-07-17 alpha-discovery loop (internal/structregime): trend21,
// trend63, liquidity21 and vol21. Methodology + MEASURED per-band accuracy +
// every honesty caveat ship in the payload so the surface can never overstate
// skill. Gap-fill was validated as a behavior but is NOT served — see
// whyHonest below and internal/structregime/gapfill.go.
//
// Credibility wave: symbols whose filing-cadence earnings ESTIMATE falls in
// the next 7 days are flagged in earningsWindows (label only — forecasts are
// never suppressed; the engines were validated over windows including
// earnings).
// structuralRegimes is the UNCACHED handler — tests drive it directly so
// seeded forecasts are always visible. Production traffic goes through
// structuralRegimesCached.
func (d Deps) structuralRegimes(w http.ResponseWriter, r *http.Request) {
	resp, err := d.buildStructuralRegimes(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, resp)
}

// structuralRegimesCached serves the same payload through the SWR cache. Perf
// wave 2026-07-24: the fleet-wide earnings annotation walks the 710k-row
// filings table (~4.6s per request) and the payload is identical for every
// user (5m TTL — the regime runner writes every 6h; earnings labels drift by
// the day, not the minute).
func (d Deps) structuralRegimesCached(w http.ResponseWriter, r *http.Request) {
	resp, err := sharedRegimesCache.get(r.Context(), "regimes", d.buildStructuralRegimes)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, resp)
}

// buildStructuralRegimes computes the full regimes payload. Pure build — no
// HTTP — so the response cache can rebuild it off-request.
func (d Deps) buildStructuralRegimes(ctx context.Context) (map[string]any, error) {
	fcs, err := d.St.RegimeForecasts(ctx)
	if err != nil {
		return nil, err
	}
	byKind := map[string][]any{}
	forecastSyms := map[string]bool{}
	for _, f := range fcs {
		byKind[string(f.Kind)] = append(byKind[string(f.Kind)], f)
		forecastSyms[f.Symbol] = true
	}
	resp := map[string]any{
		"forecasts":   byKind,
		"methodology": "Every accuracy below is MEASURED, walk-forward, with NON-OVERLAPPING forward windows over ~900 stocks / 7.5 years (2019-2026), quarter-block-clustered so autocorrelated days never inflate the sample, and independently re-verified by a second implementation the same day. None of these is a price-direction call — direction's ~52-55% ceiling was re-confirmed a sixth time in the same loop.",
		"kinds": map[string]any{
			"trend21": map[string]any{
				"what": "Will the stock still be on its current side of its 200-day average in 21 trading days?",
				"accuracyTiers": map[string]string{
					"very-high conviction (>=0.9)":  "97.2% (95% CI 0.965-0.978)",
					"high conviction (0.8-0.9)":     "94.6%",
					"moderate conviction (0.5-0.8)": "90.0%",
					"low conviction (<0.5)":         "73.1%",
				},
				"caveat": "Each forecast reports the accuracy of its own conviction BAND (a low-conviction call says 73.1%, never the 83.3% whole-population average). The skill IS trend persistence plus distance (base rate 54-57% up). Universe is currently-tracked stocks, so delisted names are absent — downtrend persistence into delisting is unobserved.",
			},
			"trend63": map[string]any{
				"what": "Will the stock still be on its current side of its 200-day average in 63 trading days (one quarter)? Same predictor as trend21, quarterly horizon.",
				"accuracyTiers": map[string]string{
					"very-high conviction (>=0.9)": "83.7% (95% CI 0.789-0.883)",
					"high conviction (>=0.8)":      "81.7%",
					"moderate conviction (>=0.5)":  "77.7%",
					"all decisions":                "70.0%",
				},
				"caveat": "These are CUMULATIVE tiers (accuracy of all calls at or above the conviction floor), not the per-band decompositions the 21d kinds report — the 63d loop did not record band shares, so a call at the bottom of its band is slightly overstated by its tier number. Measured 2026-07-17, quarter-clustered, ~904 stocks 2019-2026. Same survivorship caveat as trend21: universe is currently-tracked stocks, so delisted names are absent — downtrend persistence into delisting is unobserved.",
			},
			"liquidity21": map[string]any{
				"what": "Will average daily dollar volume over the next 21 sessions be ABOVE (active) or BELOW (quiet) its trailing-200-day median?",
				"accuracyTiers": map[string]string{
					"very-high conviction (>=0.9)":  "87.6% (95% CI 0.857-0.892)",
					"high conviction (0.8-0.9)":     "80.1%",
					"moderate conviction (0.5-0.8)": "73.9%",
					"low conviction (<0.5)":         "59.5%",
				},
				"caveat": "Per-band accuracies. Label base rate is NOT 50/50 (secular volume drift: majority class 56-61% by tier) and a naive persistence rule scores the same accuracy — the skill IS liquidity persistence. The accuracy claim holds; a novelty claim would not be.",
			},
			"vol21": map[string]any{
				"what": "Will realized volatility over the next 21 sessions be ELEVATED (upper half of its recent range) or CALM (lower half)? The quarterly vol-regime method at a monthly horizon.",
				"accuracyTiers": map[string]string{
					"very-high conviction (>=0.9)":  "72.0% (95% CI 0.674-0.759)",
					"high conviction (0.8-0.9)":     "66.8%",
					"moderate conviction (0.5-0.8)": "64.3%",
					"low conviction (<0.5)":         "55.8%",
				},
				"caveat": "Per-band accuracies. Same options/vol expression caveat as the quarterly vol regime: a regime call is situational awareness with a measured hit rate, not a trade.",
			},
			// ── crypto kinds (2026-07-18 crypto discovery loop; appended) ──
			"trend21-crypto": map[string]any{
				"what": "Will the CRYPTO symbol still be on its current side of its 200-day average in 21 sessions? Same predictor as trend21, with the accuracy MEASURED ON CRYPTO (7 symbols, ~728 daily bars each, 2024-07→2026-07).",
				"accuracyTiers": map[string]string{
					"conviction >=0.5": "98.5% (95% CI 0.958-1.000, n=68)",
					"all decisions":    "93.4% (95% CI 0.838-1.000, n=106)",
				},
				"caveat": "MANDATORY: only ~2 years of history and 4 quarterly clusters back these CIs — a single new regime quarter can move them materially, and n is far smaller than the ~900-stock stock tables. The sample is bear-dominated (83% of sampled points below SMA200), so a genuine bull-flip stress test is ABSENT. Conviction <0.5 conservatively reports the all-decisions 93.4%; tiers above 0.5 report the >=0.5 cumulative 98.5% (the higher measured tiers rest on 3 clusters — never quoted).",
			},
			"liquidity21-crypto": map[string]any{
				"what": "Will the CRYPTO symbol's average daily dollar volume over the next 21 sessions be ABOVE (active) or BELOW (quiet) its trailing-200-day median? Same predictor as liquidity21, with the accuracy MEASURED ON CRYPTO.",
				"accuracyTiers": map[string]string{
					"very-high conviction (>=0.9)": "96.4% (95% CI 0.870-1.000, n=28)",
					"high conviction (>=0.8)":      "93.5% (n=46)",
					"moderate conviction (>=0.5)":  "91.2% (95% CI 0.835-0.988, n=91)",
					"all decisions":                "79.5% (95% CI 0.719-0.877, n=161)",
				},
				"caveat": "MANDATORY: only ~2 years / 6 quarterly clusters, much smaller n than the stock tables. The predictor agrees with naive persistence on 98% of samples (persistence baseline 78.3%): the skill IS liquidity-regime stickiness sized by rank extremity — the accuracy claim holds, a novelty claim would not. Tiers are CUMULATIVE (calls at or above the conviction floor); conviction <0.5 reports the all-decisions 79.5%. The crypto VOL regime did NOT replicate and is deliberately not served.",
			},
		},
		"whyHonest": "Accuracy targets were chosen where prediction is genuinely possible (persistence of structure), not where it is impossible (1-day direction). Every forecast reports the measured accuracy of its own conviction band; thin history yields NO forecast rather than a guess. Gap-fill was validated as a market behavior (gaps under 4% fill within 5 sessions 72-87% of the time, matched-null verified over 802k events) but is NOT served: those rates are only true at the OPEN of the gap day, and for gaps surviving day 0 unfilled — the only population an end-of-day worker can forecast — the rate collapses to 44-61%, below the 70% bar. Pulled by the 2026-07-17 verification pass.",
		// #20: every accuracy table above was measured on the currently-tracked
		// universe's bars — the survivorship label travels with the stats.
		"survivorship": survivorshipBlock(),
	}
	// Earnings-window labels (credibility wave): best-effort; a store error
	// omits the block rather than failing the regimes read.
	if ew := d.earningsWindowsForSymbols(ctx, forecastSyms, time.Now().Unix()); ew != nil {
		resp["earningsWindows"] = ew
		resp["earningsNote"] = earningsRiskNote + " — dates are filing-cadence ESTIMATES (see /api/earnings-window); forecasts are labeled, never suppressed"
	}
	return resp, nil
}
