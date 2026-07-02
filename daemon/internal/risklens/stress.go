package risklens

import (
	"fmt"
	"math"
)

// Fixed stress shocks, expressed as a shock to the BROAD EQUITY MARKET, then
// translated to the portfolio through each holding's historical beta to the
// portfolio itself (used as a market proxy — see StressScenarios). These are
// illustrative, historical-style magnitudes, not forecasts.
const (
	// crash2008Shock is a -40% equity draw, echoing peak-to-trough 2008-style
	// equity losses (the S&P 500 fell ~-38% in calendar 2008, ~-50% peak to
	// trough). Applied as a single-shock market move.
	crash2008Shock = -0.40
	// covidShock is a fast ~-34% equity drop, echoing the Feb–Mar 2020 crash.
	covidShock = -0.34
)

// StressScenarios applies a fixed battery of historical-style shocks to the
// portfolio and reports the estimated one-shot P&L for each.
//
// Shocks fall into two families:
//
//   - MARKET SHOCKS ("2008 equity -40%", "COVID-2020 -34%"). Each holding moves
//     by beta_i * shock, where beta_i is the holding's historical beta to the
//     equal-... no — to the PORTFOLIO's own return series, used as a market
//     proxy because this package receives no external market index. Portfolio
//     P&L is the weighted sum. ASSUMPTION/LIMITATION: portfolio-as-proxy makes
//     the portfolio's own beta 1.0, so a diversified book and a concentrated
//     book get similar market-shock P&L; the per-holding beta split still shows
//     which names amplify or dampen. Treat market-shock magnitudes as
//     order-of-magnitude, not precise.
//
//   - EMPIRICAL SHOCKS ("worst historical day", "3σ down day"). These are read
//     straight off the portfolio's realized return distribution over the
//     supplied window, so they are exact for this history (no beta model).
//
// No lookahead: betas and empirical shocks use only the supplied window.
func StressScenarios(holdings []Holding, series []Series) ([]Scenario, error) {
	norm, rets, err := alignSeries(holdings, series)
	if err != nil {
		return nil, err
	}

	// Portfolio return series (proxy for "the market" in beta shocks).
	m := len(rets[0])
	port := make([]float64, m)
	for i, h := range norm {
		for t := 0; t < m; t++ {
			port[t] += h.Weight * rets[i][t]
		}
	}
	_, portStd := meanStd(port)
	portVar := portStd * portStd

	// Per-holding beta to the portfolio proxy.
	betas := make([]float64, len(norm))
	for i := range norm {
		if portVar == 0 {
			betas[i] = 1
		} else {
			betas[i] = covariance(rets[i], port) / portVar
		}
	}

	marketPnL := func(shock float64) float64 {
		pnl := 0.0
		for i, h := range norm {
			pnl += h.Weight * betas[i] * shock
		}
		return pnl
	}

	scenarios := []Scenario{
		{
			Name:   "2008 equity -40%",
			PnLPct: marketPnL(crash2008Shock),
			Detail: "Broad equity market -40% (2008-style). Each holding shocked by its beta to the portfolio proxy; no external index supplied.",
		},
		{
			Name:   "COVID-2020 -34%",
			PnLPct: marketPnL(covidShock),
			Detail: "Fast ~-34% equity crash (Feb-Mar 2020 style), applied via per-holding beta.",
		},
	}

	// Empirical: 3-sigma down day off the portfolio distribution.
	pMean, pStd := meanStd(port)
	threeSig := pMean - 3*pStd
	scenarios = append(scenarios, Scenario{
		Name:   "3-sigma down day",
		PnLPct: threeSig,
		Detail: fmt.Sprintf("mean(%.4f) - 3*stdev(%.4f) of realized daily portfolio returns; empirical, no distributional model.", pMean, pStd),
	})

	// Empirical: worst single realized portfolio day in the window.
	worst := math.Inf(1)
	worstIdx := -1
	for t, r := range port {
		if r < worst {
			worst = r
			worstIdx = t
		}
	}
	if worstIdx == -1 {
		worst = 0
	}
	scenarios = append(scenarios, Scenario{
		Name:   "worst historical day",
		PnLPct: worst,
		Detail: fmt.Sprintf("worst single realized daily portfolio return in the %d-day window; exactly what happened, no model.", m),
	})

	return scenarios, nil
}
