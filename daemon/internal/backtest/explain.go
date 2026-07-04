package backtest

import (
	"fmt"
	"strings"
)

// Explain renders a Result as an honest, plain-English paragraph. It always
// states the trade count and the cost assumption alongside the return so a
// reader can judge confidence: a headline return from 2 trades after costs is
// noise, and the sentence is written so that reads that way. It compares the
// strategy against buy-and-hold and says plainly whether it beat or lagged
// holding AFTER costs.
//
// The wording never promises the future — it reports what happened on the
// supplied bars and flags low sample sizes as low-confidence.
func Explain(s Strategy, r Result) string {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		name = "The strategy"
	}

	bh := r.TotalReturn - r.VsBuyHold // buy-and-hold total return
	verdict := "lagged"
	if r.VsBuyHold > 0 {
		verdict = "beat"
	} else if r.VsBuyHold == 0 {
		verdict = "matched"
	}

	var b strings.Builder
	// Only quote CAGR when the window is long/thick enough for annualizing to be
	// honest (Result.CAGRReported); otherwise report total return over the span.
	if r.CAGRReported {
		fmt.Fprintf(&b, "%s returned %s (CAGR %s) vs %s buy-and-hold, ",
			name, pct(r.TotalReturn), pct(r.CAGR), pct(bh))
	} else {
		fmt.Fprintf(&b, "%s returned %s over %s vs %s buy-and-hold (span too short to annualize), ",
			name, pct(r.TotalReturn), spanStr(r.SpanYears), pct(bh))
	}
	// Only quote win rate when >=2 closed trades make it a real fraction.
	if r.WinRateMeaningful {
		fmt.Fprintf(&b, "with %d trade%s (win rate %s, %s of bars in the market), ",
			r.NumTrades, plural(r.NumTrades), pct(r.WinRate), pct(r.ExposurePct))
	} else {
		fmt.Fprintf(&b, "with %d trade%s (too few to quote a win rate, %s of bars in the market), ",
			r.NumTrades, plural(r.NumTrades), pct(r.ExposurePct))
	}
	fmt.Fprintf(&b, "and a worst drawdown of %s. ", pct(r.MaxDrawdown))
	fmt.Fprintf(&b, "After %s costs it %s holding (%s vs buy-and-hold). ",
		bpsStr(s.CostBps), verdict, signedPct(r.VsBuyHold))
	fmt.Fprintf(&b, "Sharpe was %.2f (rf=0, annualized ~%.0f bars/yr). ", r.Sharpe, r.BarsPerYear)

	// Honesty tail: flag low-N and note that this is in-sample.
	b.WriteString(confidenceNote(r.NumTrades))
	return b.String()
}

// confidenceNote appends an out-of-sample honesty caveat sized to the trade
// count. Every backtest here is in-sample by construction, so we never let a
// result read as proof.
func confidenceNote(numTrades int) string {
	base := "This is an in-sample backtest, not a forward test — treat it as a hypothesis."
	switch {
	case numTrades == 0:
		return "No trades fired, so there is nothing to conclude. " + base
	case numTrades < 5:
		return fmt.Sprintf("With only %d trade%s this is LOW CONFIDENCE — the result is likely noise. %s",
			numTrades, plural(numTrades), base)
	case numTrades < 20:
		return fmt.Sprintf("With %d trades this is MODERATE CONFIDENCE at best. %s", numTrades, base)
	default:
		return base
	}
}

// spanStr renders a span in years as a human phrase, e.g. 0.04 -> "~2 weeks",
// 0.5 -> "~6 months", 1.5 -> "~1.5 years". Used only when CAGR is suppressed.
func spanStr(years float64) string {
	switch {
	case years <= 0:
		return "the tested span"
	case years < 0.16: // < ~2 months
		w := years * 52.18
		if w < 1.5 {
			return "~1 week"
		}
		return fmt.Sprintf("~%.0f weeks", w)
	case years < 1:
		return fmt.Sprintf("~%.0f months", years*12)
	default:
		return fmt.Sprintf("~%.1f years", years)
	}
}

// pct formats a fraction as a percentage string, e.g. 0.1234 -> "12.3%".
func pct(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// signedPct formats a fraction as a signed percentage, e.g. -0.05 -> "-5.0%".
func signedPct(f float64) string { return fmt.Sprintf("%+.1f%%", f*100) }

// bpsStr formats a basis-point cost, e.g. 10 -> "10bps".
func bpsStr(costBps float64) string {
	if costBps == float64(int64(costBps)) {
		return fmt.Sprintf("%dbps", int64(costBps))
	}
	return fmt.Sprintf("%gbps", costBps)
}

// plural returns "s" unless n == 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
