// Options surfaces: the first place SignalDeck's ONE validated forecast (the
// volatility regime) becomes a tradeable number.
//
//	GET /api/options/price     — pure Black-Scholes-Merton calculator + Greeks
//	                             (+ implied vol when a market price is supplied)
//	GET /api/options/vol-edge  — this symbol's vol forecast vs a market implied
//	                             vol: fair IV, verdict, robustness, straddle gap
//
// Both are stateless reads. The pricing route computes from its query string
// alone; the edge route reads only stored daily bars. Every payload carries the
// same honesty discipline as the rest of the platform: what was measured, what
// is assumed, and what would have to be true for the verdict to be wrong.
package api

import (
	"math"
	"net/http"
	"strconv"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/options"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

// volHorizonDays is the vol-regime forecast's horizon in TRADING days, and
// horizonCalendarDays its rough calendar equivalent — the expiry at which the
// forecast and the option describe the same window.
const (
	volHorizonDays      = 63
	horizonCalendarDays = 91
	// horizonTolerance is how far an option's expiry may sit from the forecast
	// horizon before the payload says the comparison is stretched.
	horizonTolerance = 30
	// optionBars caps the daily-bar load for the edge route.
	optionBars = 2600
)

func (d Deps) registerOptions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/options/price", d.optionsPrice)
	mux.HandleFunc("GET /api/options/vol-edge", d.optionsVolEdge)
}

// qfloat reads a float query param, falling back to def when absent or unparseable.
func qfloat(r *http.Request, key string, def float64) float64 {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return def
	}
	return f
}

// optionsPrice is the calculator: price + Greeks for one European contract, and
// the implied vol when the caller supplies a market price.
//
//	?spot= &strike= &days= (calendar days to expiry) &vol= (decimal, 0.35=35%)
//	&rate= &divYield= &type=call|put|straddle &price= (optional market quote)
func (d Deps) optionsPrice(w http.ResponseWriter, r *http.Request) {
	in := options.Inputs{
		Spot:     qfloat(r, "spot", 0),
		Strike:   qfloat(r, "strike", 0),
		T:        qfloat(r, "days", 30) / 365,
		Rate:     qfloat(r, "rate", 0.04),
		DivYield: qfloat(r, "divYield", 0),
		Vol:      qfloat(r, "vol", 0.30),
	}
	if in.Spot <= 0 || in.Strike <= 0 {
		httpErr(w, 400, "need spot>0 and strike>0")
		return
	}
	kind := r.URL.Query().Get("type")
	if kind != "put" && kind != "straddle" {
		kind = "call"
	}
	out := map[string]any{
		"inputs":      in,
		"type":        kind,
		"assumptions": "Black-Scholes-Merton: European exercise, lognormal returns, ONE constant volatility, continuous dividend yield, no early assignment and no transaction costs. Real listed options violate every one of these to some degree — most visibly the single-vol assumption (a real surface is skewed across strikes) and American early exercise. Greeks are exact for the model, not for the market.",
	}
	switch kind {
	case "straddle":
		st := options.PriceStraddle(in)
		out["straddle"] = st
		if q := qfloat(r, "price", 0); q > 0 {
			if iv, ok := options.ImpliedVolStraddle(q, in); ok {
				out["impliedVol"] = iv
			} else {
				out["impliedVolNote"] = ivRefusalNote
			}
		}
	default:
		p := options.Price(in, kind == "call")
		out["priced"] = p
		out["probITMNote"] = "probITM is the RISK-NEUTRAL probability of finishing in the money. It is a pricing quantity, not a forecast — it embeds the market's risk pricing and is systematically not the real-world odds."
		if q := qfloat(r, "price", 0); q > 0 {
			if iv, ok := options.ImpliedVol(q, in, kind == "call"); ok {
				out["impliedVol"] = iv
			} else {
				out["impliedVolNote"] = ivRefusalNote
			}
		}
	}
	writeJSON(w, out)
}

const ivRefusalNote = "no implied vol is recoverable from that quote: it is outside the no-arbitrage bounds, or the contract is so deep in/out of the money that its price barely moves with volatility (near-zero vega), so many different vols reproduce it. Returning a number here would look like a measurement and would not be one."

// optionsVolEdge compares a market implied vol against what this symbol's own
// history says volatility is about to do.
//
//	?symbol= &market= &iv= (decimal market implied vol, optional)
//	&days= (calendar days to expiry, default ~the forecast horizon)
//	&strike= (default ATM) &vrp= (assumed variance risk premium, default 0.02)
//	&rate= (default: latest FRED effective fed funds, else 0.04)
func (d Deps) optionsVolEdge(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	ctx := r.Context()
	bars, err := d.St.LastBars(ctx, s.ID, md.TF1d, optionBars)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if len(bars) == 0 {
		httpErr(w, 404, "no daily bars for symbol")
		return
	}
	ts := make([]int64, 0, len(bars))
	closes := make([]float64, 0, len(bars))
	for _, b := range bars {
		if b.Close > 0 {
			ts = append(ts, b.Ts)
			closes = append(closes, b.Close)
		}
	}
	rets := make([]float64, 0, len(closes))
	retTs := make([]int64, 0, len(closes))
	for i := 1; i < len(closes); i++ {
		rets = append(rets, closes[i]/closes[i-1]-1)
		retTs = append(retTs, ts[i])
	}
	spot := closes[len(closes)-1]

	out := map[string]any{
		"symbol":     s.Symbol,
		"market":     string(s.Market),
		"spot":       spot,
		"asOf":       bars[len(bars)-1].Ts,
		"what":       "The volatility-regime forecast (the platform's one validated edge: 69.7-76.0% walk-forward on the upper/lower-half call) expressed as a volatility LEVEL, and compared against a market implied vol you supply.",
		"whyNoChain": "SignalDeck has NO options data feed — it cannot see a chain, a quote, or a real implied vol. The market IV is a number you type in from your own broker. Nothing on this page is scraped, inferred, or invented from equity data.",
	}

	// The live regime call. An absent forecast is an honest absence (thin
	// history or the split-corruption guard), never a default.
	f, ok := volregime.Predict(rets)
	if !ok {
		out["gate"] = "no vol-regime forecast for this symbol: fewer than 230 clean daily returns, or the window contains a wild move consistent with an unadjusted split. No forecast is better than a corrupted one."
		writeJSON(w, out)
		return
	}
	out["forecast"] = f

	st, ok := options.Stats(volregime.History(retTs, rets, volHorizonDays), rets, volHorizonDays)
	if !ok {
		out["gate"] = "not enough independent walk-forward windows on this symbol to measure what volatility ACTUALLY did after each regime, so the regime call cannot be turned into a vol level here."
		writeJSON(w, out)
		return
	}
	out["volStats"] = st

	exp, ok := options.Expect(f, st)
	out["expectation"] = exp
	if !ok {
		out["gate"] = "this call sits in the conviction band where the predictor has NO measured edge (accuracy ~0.55). Mixing the two outcomes at that accuracy just reproduces the symbol's average vol — that is the null, not a forecast, so no verdict is issued."
		writeJSON(w, out)
		return
	}

	// Pricing context: expiry, strike, rate.
	days := qfloat(r, "days", horizonCalendarDays)
	if days <= 0 {
		days = horizonCalendarDays
	}
	strike := qfloat(r, "strike", spot)
	rate, rateSrc := qfloat(r, "rate", math.NaN()), "caller-supplied"
	if math.IsNaN(rate) {
		rate, rateSrc = 0.04, "default 4% — no FRED fed-funds observation stored"
		if p, ok, err := d.St.LatestMacro(ctx, "DFF"); err == nil && ok {
			rate, rateSrc = p.Value/100, "FRED effective fed funds (DFF), the platform's stored short-rate proxy"
		}
	}
	in := options.Inputs{Spot: spot, Strike: strike, T: days / 365, Rate: rate,
		DivYield: qfloat(r, "divYield", 0)}
	out["pricingInputs"] = in
	out["rateSource"] = rateSrc
	if math.Abs(days-horizonCalendarDays) > horizonTolerance {
		out["horizonMismatch"] = "the forecast covers the next 63 TRADING days (~91 calendar days); this expiry is " +
			strconv.Itoa(int(days)) + " calendar days out. The comparison is stretched — an implied vol for a different window is answering a different question, and the accuracy tiers were never measured at that horizon."
	}

	// The verdict needs a market implied vol; without one the forecast still
	// stands on its own and is returned alone.
	ivStr := r.URL.Query().Get("iv")
	if ivStr == "" {
		out["next"] = "supply &iv= (your broker's implied vol for this expiry, as a decimal — 0.35 for 35%) to get the rich/cheap verdict and the straddle gap."
		writeJSON(w, out)
		return
	}
	iv := qfloat(r, "iv", 0)
	if iv <= 0 || iv > 5 {
		httpErr(w, 400, "iv must be a decimal annualized vol in (0,5] — 0.35 means 35%")
		return
	}
	vrp := qfloat(r, "vrp", options.DefaultVRP)
	edge := options.Assess(iv, exp, vrp)
	out["edge"] = edge
	out["trade"] = options.StraddleEdge(in, iv, exp.Expected)
	out["vrpNote"] = "The variance risk premium is ASSUMED, not measured: with no options feed this platform cannot observe implied vol history and therefore cannot estimate the premium from its own data. The default 2 vol points is a conservative index-like figure; single-name premia are smaller, vary by regime, and are sometimes negative. edge.breakevenVRP is the premium at which this verdict becomes in-line — if the true premium is on the other side of it, the verdict is an artifact of the assumption."
	writeJSON(w, out)
}
