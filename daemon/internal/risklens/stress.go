package risklens

import (
	"fmt"
	"math"
	"strings"
)

// Fixed stress shocks, expressed as a shock to the BROAD EQUITY MARKET and
// translated to the portfolio through each holding's beta to an EXOGENOUS
// market factor supplied by the caller (SPY on the live path). These are
// illustrative, historical-style magnitudes, not forecasts.
const (
	// crash2008Shock is a -40% equity draw, echoing peak-to-trough 2008-style
	// equity losses (the S&P 500 fell ~-38% in calendar 2008, ~-50% peak to
	// trough). Applied as a single-shock market move.
	crash2008Shock = -0.40
	// covidShock is a fast ~-34% equity drop, echoing the Feb–Mar 2020 crash.
	covidShock = -0.34
)

// MarketBetas estimates each holding's sensitivity to an EXOGENOUS market
// factor over the shared window:
//
//	beta_i = Cov(r_i, r_market) / Var(r_market)
//
// WHY THE FACTOR MUST BE EXOGENOUS: this package used to regress each holding
// on the PORTFOLIO's own return series as a "market proxy". That makes
// Sum_i w_i*beta_i identically Cov(r_p,r_p)/Var(r_p) = 1 for every book that
// can be constructed, so a -40% market shock became a -40% portfolio loss for
// a leveraged equity book, an all-Treasury book and a single meme name alike —
// a constant rendered to users as a computation. Only a factor built outside
// the book can tell those three apart.
//
// A beta that cannot be estimated is left nil with a stated Reason. It is never
// defaulted to 1.0: in the payload an assumed beta is indistinguishable from a
// measured one, and that substitution is precisely what made the old stress
// number decorative.
//
// No lookahead: every covariance is computed over the supplied window only.
func MarketBetas(holdings []Holding, series []Series, market Series) ([]HoldingBeta, error) {
	norm, rets, err := alignSeries(holdings, series)
	if err != nil {
		return nil, err
	}
	return marketBetas(norm, rets, market), nil
}

// marketBetas is the internal form of MarketBetas, taking the already-validated
// normalized holdings and return matrix so callers that have both do not
// re-align. Always returns one entry per holding, in holdings order.
func marketBetas(norm []Holding, rets [][]float64, market Series) []HoldingBeta {
	out := make([]HoldingBeta, len(norm))
	for i, h := range norm {
		out[i] = HoldingBeta{Symbol: h.Symbol, Weight: h.Weight}
	}

	window := 0
	if len(rets) > 0 {
		window = len(rets[0])
	}
	mktRets := DailyReturns(market.Closes)

	// Whole-factor failures: no beta is estimable for ANY holding, so every
	// entry carries the same reason rather than a fabricated 1.0.
	reason := ""
	mktVar := 0.0
	switch {
	case len(market.Closes) == 0:
		reason = "no exogenous market factor supplied"
	case len(mktRets) != window:
		reason = fmt.Sprintf(
			"market factor covers %d return days but the holdings cover %d — no shared window to regress on",
			len(mktRets), window)
	default:
		_, mktStd := meanStd(mktRets)
		mktVar = mktStd * mktStd
		if mktVar == 0 {
			reason = "market factor has zero variance over the window (beta is undefined)"
		}
	}
	if reason != "" {
		for i := range out {
			out[i].Reason = reason
		}
		return out
	}

	for i := range out {
		if window < 2 || len(rets[i]) != window {
			out[i].Reason = "holding has too few observations overlapping the market factor to estimate a beta"
			continue
		}
		b := covariance(rets[i], mktRets) / mktVar
		out[i].Beta = &b
	}
	return out
}

// StressScenarios applies a fixed battery of historical-style shocks to the
// portfolio and reports the estimated one-shot P&L for each.
//
// Shocks fall into two families:
//
//   - MARKET SHOCKS ("2008 equity -40%", "COVID-2020 -34%"). Each holding moves
//     by beta_i * shock, where beta_i is its beta to the EXOGENOUS market factor
//     in `market` (see MarketBetas). Portfolio P&L is the weighted sum, i.e.
//     bookBeta * shock. A book of low-beta names now produces a materially
//     smaller loss than a high-beta book — under the old portfolio-as-proxy
//     model both produced exactly the shock.
//
//   - EMPIRICAL SHOCKS ("worst historical day", "3σ down day"). These are read
//     straight off the portfolio's realized return distribution over the
//     supplied window, so they are exact for this history (no beta model) and
//     need no market factor.
//
// WITHHOLDING: a market shock is published only when EVERY holding has an
// estimated beta. A holding without one contributes nothing to the sum, and a
// shock summed over part of a book is not that book's P&L — so the scenario is
// withheld (PnLPct nil, Withheld true) with the reason and the uncovered
// symbols named. It is also withheld when the linear model puts a long-only
// book below -100%, which is a loss larger than its capital. Callers must
// render a withheld scenario as "—", never as 0.
//
// No lookahead: betas and empirical shocks use only the supplied window.
func StressScenarios(holdings []Holding, series []Series, market Series) ([]Scenario, error) {
	norm, rets, err := alignSeries(holdings, series)
	if err != nil {
		return nil, err
	}

	// Portfolio return series (used by the EMPIRICAL scenarios only — it is
	// deliberately NOT the beta regressor any more; see MarketBetas).
	m := len(rets[0])
	port := make([]float64, m)
	for i, h := range norm {
		for t := 0; t < m; t++ {
			port[t] += h.Weight * rets[i][t]
		}
	}

	// Book beta to the exogenous factor: Sum_i w_i * beta_i. Unlike the old
	// portfolio-proxy version this is a real number that varies by book.
	betas := marketBetas(norm, rets, market)
	bookBeta := 0.0
	longOnly := true
	var uncovered []string
	blockReason := ""
	for _, b := range betas {
		if b.Weight < 0 {
			longOnly = false
		}
		if b.Beta == nil {
			uncovered = append(uncovered, b.Symbol)
			if blockReason == "" {
				blockReason = b.Reason
			}
			continue
		}
		bookBeta += b.Weight * *b.Beta
	}

	proxy := strings.TrimSpace(market.Symbol)
	if proxy == "" {
		proxy = "the market factor"
	}
	marketScenario := func(name, blurb string, shock float64) Scenario {
		if len(uncovered) > 0 {
			return Scenario{
				Name:     name,
				Withheld: true,
				Detail: fmt.Sprintf(
					"WITHHELD — %s. No beta for %s; a shock summed over part of a book is not that book's P&L, and an assumed beta of 1.0 is what made this number a constant.",
					blockReason, strings.Join(uncovered, ", ")),
			}
		}
		pnl := bookBeta * shock
		// SANITY GATE: an unlevered long-only book cannot lose more than the
		// capital in it. A linear beta model can produce one (book beta 2.8 x
		// -40% = -112%), and this package has no leverage or borrowing model
		// with which to mean it. Publish the reason, not the impossible number.
		if longOnly && pnl < -1 {
			return Scenario{
				Name:     name,
				Withheld: true,
				Detail: fmt.Sprintf(
					"WITHHELD — a linear beta model puts this long-only book at %.1f%% under the shock, which is a loss larger than the capital in it. Book beta %.2f to %s is outside the range this one-factor model can express.",
					pnl*100, bookBeta, proxy),
			}
		}
		return Scenario{
			Name:   name,
			PnLPct: &pnl,
			Detail: fmt.Sprintf(
				"%s Each holding shocked by its beta to %s (exogenous factor, %d-day window); book beta %.2f.",
				blurb, proxy, m, bookBeta),
		}
	}

	scenarios := []Scenario{
		marketScenario("2008 equity -40%", "Broad equity market -40% (2008-style).", crash2008Shock),
		marketScenario("COVID-2020 -34%", "Fast ~-34% equity crash (Feb-Mar 2020 style).", covidShock),
	}

	// Empirical: 3-sigma down day off the portfolio distribution.
	pMean, pStd := meanStd(port)
	threeSig := pMean - 3*pStd
	scenarios = append(scenarios, Scenario{
		Name:   "3-sigma down day",
		PnLPct: &threeSig,
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
		PnLPct: &worst,
		Detail: fmt.Sprintf("worst single realized daily portfolio return in the %d-day window; exactly what happened, no model.", m),
	})

	return scenarios, nil
}
