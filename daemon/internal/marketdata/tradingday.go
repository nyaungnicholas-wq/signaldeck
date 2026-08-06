package marketdata

// ── The unit of independent evidence ──────────────────────────────────────
//
// The platform resamples on DAYS, not rows: the predictor re-scores a symbol
// every 10 minutes, and a 1d/1w label is a property of the day, not of the
// scoring instant. Every published interval rests on that day count, so an
// INFLATED day count narrows intervals in the direction that flatters the
// platform. Getting the day boundary right is therefore a statistical honesty
// concern, not a formatting one.
//
// Most of the codebase folds days with a bare ts/86400 — a UTC-midnight
// boundary. That is correct for crypto, which trades continuously and has no
// session to split. It is NOT correct for US equities, because the US extended
// session closes at 20:00 ET, which is 00:00Z under EDT and 01:00Z under EST:
// the tail of one trading day lands in the NEXT UTC day and is counted as a
// second independent observation.
//
// Measured on the live corpus (data/signaldeck.db, 2026-08-05): stock feature
// rows folded to 16,606 (symbol, UTC-day) buckets but only 15,976 (symbol,
// trading-day) buckets — 630 phantom days, a 3.9% overstatement of effective N.
// 31,139 stock rows sit in UTC hour 0, clustered at :00–:12, which is the
// predictor firing across the boundary rather than genuine 8pm ET prints.
//
// TradingDayOffsetSecs moves the fold boundary off UTC midnight into the gap
// when no US session is running. The US market is dark between the 20:00 ET
// close and the 04:00 ET pre-market open, which is [00:00Z, 08:00Z] under EDT
// and [01:00Z, 09:00Z] under EST — so any boundary in [01:00Z, 08:00Z] splits
// no session in either DST regime. 05:00Z sits in the middle of that safe
// band with three hours of margin on each side, and is simply US Eastern
// standard midnight.
const TradingDayOffsetSecs = 5 * 3600

// SecondsPerDay is one calendar day in seconds — the fold width.
const SecondsPerDay = 86400

// TradingDay maps a unix timestamp to the index of the trading day it belongs
// to. Two timestamps share a value exactly when they are the same independent
// observation, which is the property every day-clustered statistic needs.
//
// This is a pure relabelling of ts/86400 by a constant shift, so it changes
// nothing for data that already sits inside one UTC day — regular-hours rows
// (13:30–21:00Z) group exactly as they did before. It only merges the
// after-close tail back onto the session it belongs to.
//
// Uses FLOOR division, not Go's truncate-toward-zero. For ts below the offset
// the shifted value goes negative, and plain `/` would fold days -1 and 0 onto
// the same index — silently merging two observations into one, the same class
// of bug in the opposite direction. Real timestamps never go there, but
// synthetic fixtures start at ts=0 routinely.
func TradingDay(ts int64) int64 {
	t := ts - TradingDayOffsetSecs
	d := t / SecondsPerDay
	if t < 0 && t%SecondsPerDay != 0 {
		d-- // floor, so the mapping stays monotone across the epoch
	}
	return d
}
