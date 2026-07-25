// trend63 — the fourth validated structregime kind (2026-07-17 loop, same
// walk-forward discipline as the package doc: non-overlapping windows,
// quarter-block-clustered CIs, ~904 stocks 2019-2026).
//
// TREND (63d): will the stock still be on its current side of the 200-day SMA
// in 63 trading days (one quarter)? Same predictor as trend21 — sign of
// close/SMA200, conviction = trailing-200d percentile of |distance| — only the
// horizon differs.
//
// MEASURED cumulative tiers (2026-07-17 loop):
//
//	all 70.0% · conv>0.5 77.7% · conv>0.8 81.7% · conv>0.9 83.7% [CI 0.789-0.883]
//
// Unlike trend21/liquidity21/vol21, the per-band arithmetic DECOMPOSITION of
// these tiers was not recorded in the loop, so accuracyFor serves the
// CUMULATIVE tier at the call's conviction floor (the volregime precedent).
// That slightly overstates a call sitting at the bottom of its band — the
// /api/regimes caveat says so explicitly rather than inventing band shares.
//
// MEASURED mean forward 63d RETURN at the top band (2026-07-24 re-validation):
// conv>=0.9 returns -1.30% while scoring 83.8% accuracy — the same
// accuracy/return inversion as trend21, larger at the quarterly horizon. Only
// that band's return was reported, so forwardReturnFor refuses the others.
package structregime

// KindTrend63 is the quarterly trend-persistence regime call.
const KindTrend63 Kind = "trend63"

// horizon63 is the trend63 forward window in trading days.
const horizon63 = 63

// PredictTrend63 forecasts whether the stock stays on its current side of the
// 200-day SMA for the next 63 sessions. Identical inputs and refusals to
// PredictTrend (thin history / wild-move windows are honest absences); only
// the horizon and the accuracy table differ.
func PredictTrend63(closes []float64) (Forecast, bool) {
	f, ok := PredictTrend(closes)
	if !ok {
		return Forecast{}, false
	}
	f.Kind = KindTrend63
	f.HorizonDays = horizon63
	f.HistoricalAccuracy = accuracyFor(KindTrend63, f.Conviction)
	f.Tradeability = TradeabilityFor(KindTrend63, f.Conviction)
	return f, true
}
