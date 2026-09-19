package marketdata

// dailyStockSettleSecs is the number of seconds after a daily stock bar's
// timestamp (exchange‑local midnight) at which the bar is considered settled.
// 22 hours covers the regular session close at 16:00 ET plus the earliest
// possible early‑close under both EDT and EST.
const dailyStockSettleSecs = 22 * 3600

// dailyCryptoSettleSecs is the number of seconds after a daily crypto bar's
// timestamp (00:00Z) at which the bar is considered settled – a full UTC day.
const dailyCryptoSettleSecs = 24 * 3600

// DailyBarSettled reports whether the daily bar stamped at ts has finished
// forming by wall‑clock now, according to its market.
// The paper book's as‑of clock and the prediction runner previously advanced
// on a daily bar whose close was still the live intraday price (audit 2026-09-07).
func DailyBarSettled(market Market, ts, now int64) bool {
	switch market {
	case Crypto:
		return now >= ts+dailyCryptoSettleSecs
	case Stocks:
		return now >= ts+dailyStockSettleSecs
	default:
		// Conservative bound for unknown markets.
		return now >= ts+dailyCryptoSettleSecs
	}
}

// LatestSettledDaily returns the last bar in bars (ascending by Ts) that is
// settled according to DailyBarSettled, scanning from the end.
// If no bar is settled, the zero Bar and false are returned.
// The paper book's as‑of clock and the prediction runner previously advanced
// on a daily bar whose close was still the live intraday price (audit 2026-09-07).
func LatestSettledDaily(market Market, bars []Bar, now int64) (Bar, bool) {
	for i := len(bars) - 1; i >= 0; i-- {
		if DailyBarSettled(market, bars[i].Ts, now) {
			return bars[i], true
		}
	}
	return Bar{}, false
}
