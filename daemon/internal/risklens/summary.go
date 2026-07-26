package risklens

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// defaultNotional is the assumed portfolio size ("per $100k") when a Report
// leaves NotionalUSD at zero.
const defaultNotional = 100000.0

// Analyze runs the full RiskLens battery for a portfolio at the given VaR
// confidence and notional, returning a populated Report ready for Summary. A
// confidence <= 0 or >= 1 defaults to 0.95; a notional <= 0 defaults to
// $100,000.
//
// market is the EXOGENOUS factor the stress betas are estimated against (SPY on
// the live path), aligned to the same window as series. Pass a zero Series when
// none is available: the market-shock scenarios then withhold with that reason
// rather than falling back to the portfolio's own returns, which forced the
// book beta to 1.0 and made every shock equal to itself.
//
// All sub-measures are in-sample, backward-looking descriptions of the supplied
// window (see package doc). Analyze surfaces the same errors as its
// constituents (short/misaligned/missing series, zero weights).
func Analyze(holdings []Holding, series []Series, market Series, confidence, notionalUSD float64) (Report, error) {
	if confidence <= 0 || confidence >= 1 {
		confidence = 0.95
	}
	if notionalUSD <= 0 {
		notionalUSD = defaultNotional
	}

	port, err := PortfolioReturns(holdings, series)
	if err != nil {
		return Report{}, err
	}
	histVaR, histCVaR, gate := HistoricalVaR(port, confidence)
	paramVaR, paramGate := ParametricVaR(port, confidence)

	contribs, err := RiskContributions(holdings, series)
	if err != nil {
		return Report{}, err
	}
	scenarios, err := StressScenarios(holdings, series, market)
	if err != nil {
		return Report{}, err
	}
	betas, err := MarketBetas(holdings, series, market)
	if err != nil {
		return Report{}, err
	}

	return Report{
		Confidence:    confidence,
		NotionalUSD:   notionalUSD,
		HistVaRPct:    histVaR,
		HistCVaRPct:   histCVaR,
		ParamVaRPct:   paramVaR,
		VaRGate:       gate,
		ParamVaRGate:  paramGate,
		MarketProxy:   market.Symbol,
		MarketBetas:   betas,
		Contributions: contribs,
		Scenarios:     scenarios,
	}, nil
}

// Summary renders a Report as 2–4 plain-English sentences a non-quant can read:
// the 1-day VaR in percent and dollars, the tail (CVaR) loss, the single
// biggest risk driver, and the worst modeled stress scenario. It is descriptive
// of past behavior only and says so implicitly ("on a bad day you could
// lose…"). Missing pieces are skipped gracefully.
func Summary(report Report) string {
	notional := report.NotionalUSD
	if notional <= 0 {
		notional = defaultNotional
	}
	conf := report.Confidence
	if conf <= 0 || conf >= 1 {
		conf = 0.95
	}
	confPct := conf * 100

	var b strings.Builder

	// Sentence 1: headline VaR in % and $ — or the reason there isn't one. A
	// withheld VaR must be SAID, not silently rendered as a 0.00% loss.
	if report.HistVaRPct == nil {
		reason := report.VaRGate.Reason
		if reason == "" {
			reason = "the sample is too small to place a loss percentile"
		}
		fmt.Fprintf(&b,
			"We are not publishing a 1-day %.0f%% VaR for this portfolio: %s.",
			confPct, reason)
	} else {
		varDollars := *report.HistVaRPct * notional
		fmt.Fprintf(&b,
			"Your 1-day %.0f%% VaR is %.2f%%: on a bad day (worse than about %.0f%% of days in this history) you could lose roughly $%s per $%s.",
			confPct,
			*report.HistVaRPct*100,
			confPct,
			humanMoney(varDollars),
			humanMoney(notional),
		)
	}

	// Sentence 2: tail severity (CVaR) and the normal-model comparison, each
	// only when it exists.
	if report.HistCVaRPct != nil {
		cvarDollars := *report.HistCVaRPct * notional
		fmt.Fprintf(&b,
			" When it is that bad, the average loss (CVaR) is about %.2f%% (~$%s).",
			*report.HistCVaRPct*100,
			humanMoney(cvarDollars),
		)
	}
	// The normal-model VaR travels with its assumption, ALWAYS — stating it only
	// in a doc comment is how a number that understates fat tails ends up read as
	// a risk limit. When it is withheld, say so: silence reads as "not computed",
	// which is a different (and flattering) claim than "we refused to publish it".
	if report.ParamVaRPct != nil {
		fmt.Fprintf(&b,
			" The normal-model VaR is %.2f%% — it assumes normally distributed returns and therefore understates fat tails; the historical figure above is the one to trust.",
			*report.ParamVaRPct*100)
	} else if report.ParamVaRGate.Withheld {
		reason := report.ParamVaRGate.Reason
		if reason == "" {
			reason = "the sample cannot support a normal-model estimate"
		}
		fmt.Fprintf(&b, " We are not publishing a normal-model VaR: %s.", reason)
	}

	// Sentence 3: biggest risk driver.
	if top, ok := topDriver(report.Contributions); ok {
		fmt.Fprintf(&b,
			" The biggest risk driver is %s at %.0f%% of portfolio variance (weight %.0f%%, daily vol %.2f%%).",
			top.Symbol,
			top.PctOfRisk,
			top.Weight*100,
			top.Vol*100,
		)
	}

	// Sentence 4: worst modeled stress scenario, drawn only from scenarios that
	// were actually computed.
	if worst, ok := worstScenario(report.Scenarios); ok {
		fmt.Fprintf(&b,
			" Under a %q shock the portfolio would move about %.1f%% (~$%s).",
			worst.Name,
			*worst.PnLPct*100,
			humanMoney(*worst.PnLPct*notional),
		)
	}

	return b.String()
}

// topDriver returns the contribution with the largest absolute PctOfRisk.
func topDriver(cs []Contribution) (Contribution, bool) {
	if len(cs) == 0 {
		return Contribution{}, false
	}
	sorted := make([]Contribution, len(cs))
	copy(sorted, cs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return math.Abs(sorted[i].PctOfRisk) > math.Abs(sorted[j].PctOfRisk)
	})
	return sorted[0], true
}

// worstScenario returns the scenario with the most negative PnLPct, skipping
// withheld ones — a withheld scenario has no P&L to compare and must never be
// treated as a 0% move (which would look like the mildest outcome and win the
// "worst case" slot only when everything else was a gain).
func worstScenario(ss []Scenario) (Scenario, bool) {
	var worst Scenario
	found := false
	for _, s := range ss {
		if s.PnLPct == nil {
			continue
		}
		if !found || *s.PnLPct < *worst.PnLPct {
			worst, found = s, true
		}
	}
	return worst, found
}

// humanMoney formats a dollar amount with thousands separators and no cents for
// large values, keeping two decimals under $10 so small per-$100k figures stay
// legible. Negative signs are dropped (callers phrase direction in words).
func humanMoney(v float64) string {
	v = math.Abs(v)
	if v < 10 {
		return fmt.Sprintf("%.2f", v)
	}
	whole := int64(math.Round(v))
	s := fmt.Sprintf("%d", whole)
	// Insert commas every 3 digits from the right.
	n := len(s)
	if n <= 3 {
		return s
	}
	var out strings.Builder
	pre := n % 3
	if pre > 0 {
		out.WriteString(s[:pre])
		if n > pre {
			out.WriteByte(',')
		}
	}
	for i := pre; i < n; i += 3 {
		out.WriteString(s[i : i+3])
		if i+3 < n {
			out.WriteByte(',')
		}
	}
	return out.String()
}
