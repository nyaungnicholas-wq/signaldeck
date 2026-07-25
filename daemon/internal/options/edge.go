package options

import "math"

// DefaultVRP is the ASSUMED variance risk premium in decimal vol (0.02 = 2 vol
// points): the amount by which implied volatility typically exceeds the
// volatility that is subsequently realized, because option sellers are paid to
// carry variance risk. It is an assumption, not a SignalDeck measurement —
// this platform has no options data feed, so it cannot observe implied vol at
// all and therefore cannot estimate the premium from its own data. 2 points is
// a conservative index-like figure; single-name premia are smaller, vary widely
// by symbol and by regime, and are occasionally negative. Every verdict built
// on it reports the premium used AND the premium at which the verdict flips.
const DefaultVRP = 0.02

// InLineBand is how far market IV must sit from fair IV, in decimal vol, before
// the surface calls it anything other than in-line. Two vol points is roughly
// the width of the honest uncertainty in the forecast itself, so anything
// narrower is noise dressed as a signal.
const InLineBand = 0.02

// Edge is the comparison this whole package exists for: what the market charges
// for volatility versus what this symbol's history says volatility will do.
type Edge struct {
	MarketIV    float64 `json:"marketIV"`
	ExpectedVol float64 `json:"expectedVol"`
	// VRP is the assumed premium added to the forecast to get a fair implied.
	VRP float64 `json:"vrp"`
	// FairIV = ExpectedVol + VRP: what implied "should" be if the forecast is
	// right and the premium assumption holds.
	FairIV float64 `json:"fairIV"`
	// EdgeVol is MarketIV - FairIV in DECIMAL vol (positive = market charging
	// more than the forecast justifies).
	EdgeVol float64 `json:"edgeVol"`
	// Verdict is "iv-rich", "iv-cheap" or "in-line".
	Verdict string `json:"verdict"`
	// Expression is the plain-English position that expresses the verdict.
	Expression string `json:"expression"`
	// FairIVIfRight / FairIVIfWrong are the fair implied under each branch of
	// the regime call — the honest bracket around FairIV.
	FairIVIfRight float64 `json:"fairIVIfRight"`
	FairIVIfWrong float64 `json:"fairIVIfWrong"`
	// Robust is true only when the verdict survives the regime call being
	// WRONG. A non-robust verdict is a bet on the call, not on the mispricing.
	Robust     bool   `json:"robust"`
	RobustNote string `json:"robustNote"`
	// BreakevenVRP is the premium at which the verdict flips to in-line:
	// MarketIV - ExpectedVol. If the true premium is above it, "rich" was an
	// artifact of the assumption, not of the forecast.
	BreakevenVRP float64 `json:"breakevenVRP"`
	// Caveat is the mandatory, non-omittable honesty line.
	Caveat string `json:"caveat"`
}

// Assess compares a market implied vol against the vol forecast. marketIV and
// vrp are decimal annualized vols.
func Assess(marketIV float64, e Expectation, vrp float64) Edge {
	ed := Edge{
		MarketIV: marketIV, ExpectedVol: e.Expected, VRP: vrp,
		FairIV:        e.Expected + vrp,
		FairIVIfRight: e.IfRight + vrp,
		FairIVIfWrong: e.IfWrong + vrp,
		BreakevenVRP:  marketIV - e.Expected,
		Caveat:        "A vol-regime hit rate is not a measured profit. The validated claim is only that the UPPER/LOWER-half call lands 69.7-76.0% of the time; the vol LEVELS, the variance-premium assumption, and this verdict are all layered on top of it and none of them was backtested. Options add costs this comparison ignores entirely: bid-ask spread, early assignment on American contracts, skew (one implied vol does not describe a whole surface), dividends, and the fact that a short-vol position's losses are unbounded while its gains are capped.",
	}
	ed.EdgeVol = marketIV - ed.FairIV
	switch {
	case ed.EdgeVol > InLineBand:
		ed.Verdict = "iv-rich"
		ed.Expression = "market is charging more for volatility than this symbol's history says it will deliver — the forecast-consistent expression is SHORT volatility (defined-risk: credit spread or iron condor, never a naked straddle)"
	case ed.EdgeVol < -InLineBand:
		ed.Verdict = "iv-cheap"
		ed.Expression = "market is charging less for volatility than this symbol's history says it will deliver — the forecast-consistent expression is LONG volatility (debit spread or straddle, sized for total loss)"
	default:
		ed.Verdict = "in-line"
		ed.Expression = "market implied and forecast vol agree within the honest error band — no vol trade is indicated"
	}
	// Robustness: does the verdict hold in BOTH branches of the regime call?
	right := marketIV - ed.FairIVIfRight
	wrong := marketIV - ed.FairIVIfWrong
	ed.Robust = math.Abs(right) > InLineBand && math.Abs(wrong) > InLineBand &&
		math.Signbit(right) == math.Signbit(wrong)
	if ed.Robust {
		ed.RobustNote = "holds either way: the verdict is the same whether the regime call lands or fails, so it rests on the mispricing rather than on the forecast"
	} else {
		ed.RobustNote = "conditional: the verdict flips (or goes in-line) if the regime call is wrong — and at this conviction it is wrong roughly " +
			pctWrong(e.Accuracy) + " of the time"
	}
	return ed
}

func pctWrong(acc float64) string {
	p := int(math.Round((1 - acc) * 100))
	switch {
	case p <= 0:
		return "0%"
	default:
		return itoa(p) + "%"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Trade is the straddle economics behind a verdict: what the market's implied
// vol prices the at-the-money straddle at, versus what the forecast prices it
// at, and the difference in dollars per contract.
type Trade struct {
	MarketStraddle Straddle `json:"marketStraddle"`
	ModelStraddle  Straddle `json:"modelStraddle"`
	// EdgePerContract is (market price - model price) x 100 shares: positive
	// means the market straddle is dearer than the forecast justifies (the
	// seller's edge), negative means the reverse. GROSS of every cost.
	EdgePerContract float64 `json:"edgePerContract"`
	// MarketBreakevenPct / ForecastMovePct are the move the market is pricing
	// versus the move the forecast implies over the same horizon — the same
	// comparison in the units a trader thinks in.
	MarketBreakevenPct float64 `json:"marketBreakevenPct"`
	ForecastMovePct    float64 `json:"forecastMovePct"`
	Caveat             string  `json:"caveat"`
}

// StraddleEdge values the at-the-money straddle at both vols. in.Vol is
// ignored; marketIV and modelVol are used in its place.
func StraddleEdge(in Inputs, marketIV, modelVol float64) Trade {
	mIn, fIn := in, in
	mIn.Vol, fIn.Vol = marketIV, modelVol
	m, f := PriceStraddle(mIn), PriceStraddle(fIn)
	t := Trade{MarketStraddle: m, ModelStraddle: f,
		EdgePerContract:    (m.Price - f.Price) * 100,
		MarketBreakevenPct: m.BreakevenMovePct,
		Caveat:             "GROSS of costs and computed at a single at-the-money implied vol. A real straddle pays the bid-ask on two legs, faces a skewed surface (puts usually imply more than calls), and — if American — early assignment. Treat the dollar number as the size of the theoretical gap, not as expected profit.",
	}
	if in.T > 0 {
		// One-standard-deviation move over the horizon under the forecast.
		t.ForecastMovePct = modelVol * math.Sqrt(in.T)
	}
	return t
}
