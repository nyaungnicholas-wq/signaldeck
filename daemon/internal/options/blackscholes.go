// Package options is SignalDeck's options pricing surface — the first place
// the platform's ONE validated forecast becomes a tradeable number.
//
// # Why this exists
//
// SignalDeck proved, six independent ways, that 1-day price DIRECTION is a coin
// flip (~52-55% ceiling). It also proved that VOLATILITY REGIME is genuinely
// forecastable: 69.7-76.0% walk-forward, non-overlapping, quarter-clustered
// (internal/volregime). That forecast had no product to act on — a vol call is
// not expressible in shares. Options are the instrument where a view on
// volatility is the position, so this package turns the vol-regime forecast
// into the only comparison that matters: what the market is charging for
// volatility (implied) versus what this symbol's own history says volatility is
// about to do (forecast).
//
// # What is measured and what is assumed
//
// MEASURED here (from the symbol's own daily bars, walk-forward, no lookahead):
// the forward realized volatility that actually followed each regime label, and
// therefore the expected forward vol of a live call — see volforecast.go.
//
// ASSUMED, and labeled as such everywhere it is used: the variance risk
// premium (implied vol structurally exceeds subsequent realized vol). This
// platform has NO options data feed, so the premium cannot be measured here and
// is a caller-supplied parameter. Every verdict states its VRP assumption and
// whether the verdict survives being wrong about it (see edge.go).
//
// The pricing math itself — Black-Scholes-Merton with a continuous dividend
// yield, its Greeks, and the implied-vol inversion — is exact and standard, and
// carries the model's own assumptions (lognormal returns, constant vol,
// European exercise, no early assignment) which are false in the ways every
// options trader already knows. It is a translation layer, not a prediction.
package options

import "math"

// Inputs are one European option's pricing inputs. Vol and the rates are
// annualized decimals (0.35 = 35%); T is in YEARS.
type Inputs struct {
	Spot     float64 `json:"spot"`
	Strike   float64 `json:"strike"`
	T        float64 `json:"t"`        // time to expiry in years
	Rate     float64 `json:"rate"`     // continuously-compounded risk-free rate
	DivYield float64 `json:"divYield"` // continuous dividend yield (q)
	Vol      float64 `json:"vol"`      // annualized volatility (sigma)
}

// Priced is a full valuation: the option's value plus every first- and
// second-order sensitivity, each in the unit a trader actually reads it in.
type Priced struct {
	Price float64 `json:"price"`
	// Delta is dPrice/dSpot (shares per option).
	Delta float64 `json:"delta"`
	// Gamma is dDelta/dSpot (per $1 of spot).
	Gamma float64 `json:"gamma"`
	// Vega is the price change per ONE VOLATILITY POINT (1% absolute vol).
	Vega float64 `json:"vega"`
	// Theta is the price change per CALENDAR DAY (negative for long premium).
	Theta float64 `json:"theta"`
	// Rho is the price change per ONE PERCENTAGE POINT of rate.
	Rho float64 `json:"rho"`
	// D1/D2 are exposed because the intermediate terms are the honest way to
	// show a caller that a result came from the formula, not a black box.
	D1 float64 `json:"d1"`
	D2 float64 `json:"d2"`
	// ProbITM is the risk-neutral probability of finishing in the money —
	// N(d2) for a call, N(-d2) for a put. NOT a real-world probability: it is
	// the pricing measure's, which prices risk, and is systematically wrong as
	// a forecast. Labeled as such wherever it is surfaced.
	ProbITM float64 `json:"probITM"`
}

// Call prices a European call; Put prices a European put.
func Call(in Inputs) Priced { return price(in, true) }

// Put prices a European put.
func Put(in Inputs) Priced { return price(in, false) }

// Price prices either side.
func Price(in Inputs, isCall bool) Priced { return price(in, isCall) }

func price(in Inputs, isCall bool) Priced {
	s, k := in.Spot, in.Strike
	if s <= 0 || k <= 0 {
		return Priced{}
	}
	// Expired or degenerate-vol contracts have no optionality left: the honest
	// value is the (discounted forward) intrinsic, and the Greeks are the
	// step-function limits, not NaNs from dividing by zero.
	if in.T <= 0 || in.Vol <= 0 {
		return atLimit(in, isCall)
	}
	sqT := in.Vol * math.Sqrt(in.T)
	d1 := (math.Log(s/k) + (in.Rate-in.DivYield+in.Vol*in.Vol/2)*in.T) / sqT
	d2 := d1 - sqT
	dfQ := math.Exp(-in.DivYield * in.T)
	dfR := math.Exp(-in.Rate * in.T)
	pdf := normPDF(d1)

	p := Priced{D1: d1, D2: d2}
	p.Gamma = dfQ * pdf / (s * sqT)
	p.Vega = s * dfQ * pdf * math.Sqrt(in.T) / 100 // per 1 vol POINT
	if isCall {
		p.Price = s*dfQ*normCDF(d1) - k*dfR*normCDF(d2)
		p.Delta = dfQ * normCDF(d1)
		p.Theta = (-s*dfQ*pdf*in.Vol/(2*math.Sqrt(in.T)) -
			in.Rate*k*dfR*normCDF(d2) + in.DivYield*s*dfQ*normCDF(d1)) / 365
		p.Rho = k * in.T * dfR * normCDF(d2) / 100
		p.ProbITM = normCDF(d2)
	} else {
		p.Price = k*dfR*normCDF(-d2) - s*dfQ*normCDF(-d1)
		p.Delta = dfQ * (normCDF(d1) - 1)
		p.Theta = (-s*dfQ*pdf*in.Vol/(2*math.Sqrt(in.T)) +
			in.Rate*k*dfR*normCDF(-d2) - in.DivYield*s*dfQ*normCDF(-d1)) / 365
		p.Rho = -k * in.T * dfR * normCDF(-d2) / 100
		p.ProbITM = normCDF(-d2)
	}
	return p
}

// atLimit values a contract with no remaining optionality (T<=0 or vol<=0):
// the discounted forward intrinsic. Delta is the step function (1/0 for a call,
// -1/0 for a put, discounted); every convexity Greek is exactly zero.
func atLimit(in Inputs, isCall bool) Priced {
	dfQ, dfR := math.Exp(-in.DivYield*in.T), math.Exp(-in.Rate*in.T)
	if in.T <= 0 {
		dfQ, dfR = 1, 1
	}
	fwd := in.Spot * dfQ
	strikePV := in.Strike * dfR
	p := Priced{}
	if isCall {
		p.Price = math.Max(0, fwd-strikePV)
		if fwd > strikePV {
			p.Delta, p.ProbITM = dfQ, 1
		}
	} else {
		p.Price = math.Max(0, strikePV-fwd)
		if strikePV > fwd {
			p.Delta, p.ProbITM = -dfQ, 1
		}
	}
	return p
}

// Straddle is the at-the-money volatility trade: long a call and a put at the
// same strike. Its value is almost purely a function of volatility, which makes
// it the cleanest expression of a vol-regime forecast.
type Straddle struct {
	Call  Priced  `json:"call"`
	Put   Priced  `json:"put"`
	Price float64 `json:"price"`
	// BreakevenMovePct is how far spot must travel by expiry, in either
	// direction, for a long straddle to break even.
	BreakevenMovePct float64 `json:"breakevenMovePct"`
	// UpperBreakeven / LowerBreakeven are those points in price terms.
	UpperBreakeven float64 `json:"upperBreakeven"`
	LowerBreakeven float64 `json:"lowerBreakeven"`
	// Vega / Theta are the position's (per vol point / per day).
	Vega  float64 `json:"vega"`
	Theta float64 `json:"theta"`
	// NetDelta is the residual directional exposure — near zero at the money,
	// and the number that says how much of this is NOT a pure vol trade.
	NetDelta float64 `json:"netDelta"`
}

// PriceStraddle values the straddle at the given strike and vol.
func PriceStraddle(in Inputs) Straddle {
	c, p := Call(in), Put(in)
	st := Straddle{Call: c, Put: p, Price: c.Price + p.Price,
		Vega: c.Vega + p.Vega, Theta: c.Theta + p.Theta, NetDelta: c.Delta + p.Delta}
	if in.Spot > 0 {
		st.BreakevenMovePct = st.Price / in.Spot
	}
	st.UpperBreakeven = in.Strike + st.Price
	st.LowerBreakeven = in.Strike - st.Price
	return st
}

// ── normal distribution (dependency-free) ──

func normCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

func normPDF(x float64) float64 { return math.Exp(-x*x/2) / math.Sqrt(2*math.Pi) }
