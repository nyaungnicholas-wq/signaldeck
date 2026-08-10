package marketdata

// SettleDay returns the unit of independent evidence for a RESOLVED outcome row.
//
// TradingDay(ts) folds on the calendar trading day, which is the right unit off a
// 24/7 market but not off a market that closes: a stock prediction issued on
// Friday, Saturday and Sunday resolves against ONE settled move (Friday->Monday),
// yet the calendar fold counts three independent observations. Measured on the
// live corpus 2026-08-08, stock 1d resolved rows folded to 15,862 (symbol,
// trading-day) buckets but only 11,256 (symbol, settled-move) buckets — a 1.41x
// overstatement of effective N, which narrows every published interval by ~19% in
// the direction that flatters the platform.
//
// settleTs is the base bar the row was graded from (prediction_outcomes.settle_ts)
// and is the true key: two predictions share an outcome exactly when they share a
// base bar. It is relabelled through TradingDay rather than used raw so the result
// stays in the SAME numeric space as every other day key in the codebase — a raw
// bar timestamp (~1.7e9) and a day index (~2e4) used as the same map key is a trap
// even when they never collide. The relabelling is lossless: measured, folding on
// TradingDay(settle_ts) yields exactly the same 11,256 buckets as folding on the
// raw settle_ts.
//
// settleTs <= 0 means the settled move is UNKNOWN (the column predates the row and
// the backfill has not reached it, or no bar exists at or before it). An unknown
// settle bar falls back to the calendar day rather than being dropped or treated as
// day zero — the honest degradation store.BackfillSettleTs documents, and it makes
// this function monotonically improve as the backfill drains.
func SettleDay(settleTs, ts int64) int64 {
	if settleTs > 0 {
		return TradingDay(settleTs)
	}
	return TradingDay(ts)
}
