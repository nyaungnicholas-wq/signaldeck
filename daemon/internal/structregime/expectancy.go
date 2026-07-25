// Expectancy — the one metric that unifies accuracy and money (2026-07-25).
//
// THE PROBLEM IT SOLVES
// --------------------
// "Maximize accuracy AND return" reads like one goal and is two, and on this
// data they point in OPPOSITE directions: trend21's conv>=0.9 band has the best
// hit rate in the system (97.2%) and a mean forward 21d return of -0.39%.
// Optimising accuracy walks you directly into the losing band.
//
// Expectancy is what reconciles them, because it is the quantity that actually
// determines whether a rule makes money:
//
//	E = P(win) * avgWin - P(loss) * avgLoss
//
// A 97% hit rate with a tiny average win and a rare enormous loss has negative
// expectancy. A 55% hit rate with a 2:1 win/loss ratio has positive expectancy.
// Accuracy alone cannot distinguish those two, which is exactly why maximising
// it produced the inverted result.
//
// STOPS AND TARGETS ARE DERIVED, NOT INVENTED
// -------------------------------------------
// A stop is a claim about how far price travels against you before the thesis
// is void, so it is sized from the symbol's OWN measured volatility (ATR), not
// from a round number. A target is a claim about the move available, so it comes
// from the band's MEASURED mean forward return — the same number that exposed
// the inversion.
//
// Where a band's forward return was never measured, this returns ok=false rather
// than producing a plausible-looking level. A fabricated stop is worse than no
// stop: it will be acted on.
package structregime

import "math"

// TradePlan is the actionable, expectancy-checked form of a forecast.
type TradePlan struct {
	Kind        Kind    `json:"kind"`
	Conviction  float64 `json:"conviction"`
	Accuracy    float64 `json:"bandAccuracy"`     // measured, this band
	MeanFwdRet  float64 `json:"meanFwdReturnPct"` // measured, this band
	HorizonDays int     `json:"horizonDays"`

	EntryPx  float64 `json:"entry"`
	StopPx   float64 `json:"stop"`
	TargetPx float64 `json:"target"`

	StopPct   float64 `json:"stopPct"`
	TargetPct float64 `json:"targetPct"`
	RR        float64 `json:"riskReward"` // target distance / stop distance

	// Expectancy in percent of position, using the band's measured hit rate and
	// the plan's own stop/target distances.
	ExpectancyPct float64 `json:"expectancyPct"`
	Tradeable     bool    `json:"tradeable"`
	Verdict       string  `json:"verdict"`
}

// atrStopMultiple sizes the stop at 2 ATR. Closer than ~1.5 ATR and ordinary
// daily noise stops you out of a thesis that was never wrong; much wider and
// the loss per trade swamps the measured edge.
const atrStopMultiple = 2.0

// minRR is the floor below which a plan is refused regardless of hit rate. A
// target smaller than the stop needs an extremely high win rate to survive, and
// win rates that high are exactly where this system measured NEGATIVE returns.
const minRR = 1.0

// BuildTradePlan turns a forecast into an expectancy-checked plan, or refuses.
// `atr` is the symbol's ATR(14) in price terms.
func BuildTradePlan(k Kind, conv, entry, atr float64) (TradePlan, bool) {
	if entry <= 0 || atr <= 0 {
		return TradePlan{}, false
	}
	fwdPct, ok := forwardReturnFor(k, conv)
	if !ok {
		// No measured forward return for this band. Inventing a target here is
		// how a number nobody validated ends up sized into a position.
		return TradePlan{}, false
	}
	acc := accuracyFor(k, conv)
	h := horizon
	if k == KindTrend63 {
		h = horizon63
	}

	stopDist := atrStopMultiple * atr
	stopPct := stopDist / entry * 100

	// The target is the band's measured mean forward move. When that is
	// NEGATIVE the honest output is not a smaller target — it is that the band
	// has no long trade in it at all.
	targetPct := fwdPct
	targetDist := targetPct / 100 * entry

	p := TradePlan{
		Kind: k, Conviction: conv, Accuracy: acc, MeanFwdRet: fwdPct,
		HorizonDays: h, EntryPx: entry,
		StopPx:    entry - stopDist,
		TargetPx:  entry + targetDist,
		StopPct:   stopPct,
		TargetPct: targetPct,
	}
	if stopDist > 0 {
		p.RR = targetDist / stopDist
	}

	// Expectancy with the band's measured hit rate. A "win" is reaching the
	// target, a "loss" is reaching the stop — deliberately pessimistic, since
	// real exits land between the two.
	p.ExpectancyPct = acc*targetPct - (1-acc)*stopPct

	switch {
	case fwdPct <= 0:
		p.Verdict = "NO TRADE — this band's measured mean forward return is " +
			"negative. Its high hit rate is a persistence statistic, not an edge: " +
			"high conviction means price is already extended from its 200-day " +
			"average, and extended names mean-revert."
	case p.RR < minRR:
		p.Verdict = "NO TRADE — the measured move is smaller than the volatility " +
			"you must risk to hold it, so the position loses to noise even when the " +
			"forecast is right."
	case p.ExpectancyPct <= 0:
		p.Verdict = "NO TRADE — negative expectancy at this band's measured hit rate."
	default:
		p.Tradeable = true
		p.Verdict = "Positive expectancy at the measured hit rate. Sizes from the " +
			"symbol's own volatility, not a round number."
	}
	return p, true
}

// RankByExpectancy scores candidate plans so selection optimises MONEY rather
// than hit rate. This is the direct fix for the inversion: ranking by accuracy
// surfaces the worst-returning band first.
func RankByExpectancy(plans []TradePlan) []TradePlan {
	out := append([]TradePlan(nil), plans...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ExpectancyPct > out[j-1].ExpectancyPct; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// KellyFraction is the expectancy-optimal position size as a fraction of
// capital, HALVED. Full Kelly is the growth-maximising bet only if the win rate
// and payoff are known exactly; here they are estimates from a finite sample, so
// full Kelly systematically overbets and half-Kelly is the standard correction.
// Negative or absurd results clamp to zero — "bet nothing" is always available.
func KellyFraction(winProb, rr float64) float64 {
	if rr <= 0 || winProb <= 0 || winProb >= 1 {
		return 0
	}
	k := (winProb*(rr+1) - 1) / rr
	if k <= 0 || math.IsNaN(k) {
		return 0
	}
	return math.Min(k/2, 0.25) // half-Kelly, hard-capped at a quarter of capital
}
