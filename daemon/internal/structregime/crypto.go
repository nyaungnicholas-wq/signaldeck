// Crypto regime kinds — the 2026-07-18 crypto discovery loop's two
// ship-with-caveat winners, market-aware variants of the stock predictors.
//
// The SAME predictor arithmetic as trend21/liquidity21 (sign vs SMA200 with
// distance-ranked conviction; dollar-volume rank vs trailing median) — only
// the accuracy tables differ, because the numbers below were MEASURED ON
// CRYPTO: 7 symbols (ADA/BTC/DOGE/ETH/LINK/SOL/XRP vs USD), ~728 daily bars
// each (2024-07→2026-07), strict walk-forward, non-overlapping 21d windows,
// quarter-block-clustered bootstrap CIs.
//
// MEASURED (2026-07-18 loop, daily h=21):
//
//	TREND-vs-SMA200:  all 93.4% CI[0.838,1.000] (n=106, 4 clusters)
//	                  conv>0.5 98.5% CI[0.958,1.000] (n=68)
//	DOLLAR-VOLUME:    all 79.5% CI[0.719,0.877] (n=161, 6 clusters)
//	                  conv>0.5 91.2% CI[0.835,0.988] (n=91)
//	                  conv>0.8 93.5% CI[0.827,1.000] (n=46)
//	                  conv>0.9 96.4% CI[0.870,1.000] (n=28)
//
// MANDATORY caveats (shipped verbatim in /api/regimes):
//   - Only ~2 years of history and 4-6 quarterly clusters back every CI — a
//     single new regime quarter can move them materially. Far smaller n than
//     the ~900-stock / 7.5-year tables behind the stock kinds.
//   - The trend sample is bear-dominated (83% of sampled points below SMA200;
//     label up-rate 0.170) — a genuine bull-flip stress test is absent.
//   - The liquidity predictor agrees with naive persistence on 98% of samples
//     (persistence baseline 78.3%): the skill IS liquidity stickiness, sized
//     by rank extremity — nothing more.
//   - The vol regime did NOT replicate on crypto (h=21 never separates from
//     the majority baseline; h=63 inverts) and is NOT shipped; nor are
//     vol21/vol63/trend63 computed for crypto symbols.
//
// Conservative tier rule (task discipline): at conviction <0.5 each kind
// reports its measured ALL-DECISIONS number, never an extrapolated low band.
// Trend's >0.8/>0.9 tiers measured 100% on only 3 clusters — too thin to
// quote, so every tier at or above 0.5 conservatively reports the >0.5
// cumulative 98.5% instead.
package structregime

// Crypto regime kinds ("-crypto" suffix keeps the stock and crypto accuracy
// tables from ever being conflated on any surface).
const (
	KindTrendCrypto21     Kind = "trend21-crypto"
	KindLiquidityCrypto21 Kind = "liquidity21-crypto"
)

// cryptoAccuracyFor maps (crypto kind, conviction) to the MEASURED crypto
// accuracy per the conservative tier rule above. Monotone non-decreasing in
// conviction by construction.
func cryptoAccuracyFor(k Kind, conv float64) float64 {
	type bands struct{ lo, b50, b80, b90 float64 }
	var t bands
	switch k {
	case KindTrendCrypto21:
		// <0.5 reports the all-decisions 93.4%; >=0.5 reports the measured
		// cumulative 98.5% (the 100% tiers above it rest on 3 clusters — never
		// quoted).
		t = bands{0.934, 0.985, 0.985, 0.985}
	case KindLiquidityCrypto21:
		// <0.5 reports the all-decisions 79.5%; higher tiers are the measured
		// CUMULATIVE tier accuracies (91.2/93.5/96.4).
		t = bands{0.795, 0.912, 0.935, 0.964}
	default:
		return 0.5
	}
	switch {
	case conv >= 0.9:
		return t.b90
	case conv >= 0.8:
		return t.b80
	case conv >= 0.5:
		return t.b50
	default:
		return t.lo
	}
}

// PredictTrendCrypto forecasts whether a CRYPTO symbol stays on its current
// side of the 200-day SMA for the next 21 sessions — identical inputs,
// refusals and arithmetic to PredictTrend; only the kind and the
// crypto-measured accuracy table differ.
func PredictTrendCrypto(closes []float64) (Forecast, bool) {
	f, ok := PredictTrend(closes)
	if !ok {
		return Forecast{}, false
	}
	f.Kind = KindTrendCrypto21
	f.HistoricalAccuracy = cryptoAccuracyFor(KindTrendCrypto21, f.Conviction)
	// PredictTrend already stamped the US-STOCK forward-return disclosure; this
	// is a crypto row, where forward return was never measured, so re-derive it
	// for this kind (empty) rather than inheriting a number from another market.
	f.Tradeability = TradeabilityFor(KindTrendCrypto21, f.Conviction)
	return f, true
}

// PredictLiquidityCrypto forecasts whether a CRYPTO symbol's mean daily
// dollar volume over the next 21 sessions sits above (active) or below
// (quiet) its trailing-200d median — identical to PredictLiquidity with the
// crypto-measured accuracy table.
func PredictLiquidityCrypto(closes, volumes []float64) (Forecast, bool) {
	f, ok := PredictLiquidity(closes, volumes)
	if !ok {
		return Forecast{}, false
	}
	f.Kind = KindLiquidityCrypto21
	f.HistoricalAccuracy = cryptoAccuracyFor(KindLiquidityCrypto21, f.Conviction)
	// Same reason as PredictTrendCrypto: never inherit another market's measured
	// forward return onto a crypto row.
	f.Tradeability = TradeabilityFor(KindLiquidityCrypto21, f.Conviction)
	return f, true
}
