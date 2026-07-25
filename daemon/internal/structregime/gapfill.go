package structregime

import "math"

// KindGapFill5 is the gap-fill event forecast: an opening gap expected to fill
// (price revisits the prior close) within 5 sessions of the gap day, inclusive.
//
// NOT EMITTED LIVE — pulled 2026-07-17 by the verification pass. Read why:
//
// The discovery loop measured, over 802k gap events with a matched null, that
// gaps DO fill within 5 sessions at high rates — 84.9%/87.3% (up/down) for
// 0.5-1% gaps, 78.4%/80.3% for 1-2%, 74.3%/72.3% for 2-4% — and that the
// behavior is genuinely gap-specific (skill +8pp to +47pp over no-gap days
// retracing the same distance). That unconditional claim is TRUE, but it is
// only actionable AT THE OPEN of the gap day, because most fills happen on
// day 0. A worker that runs after the gap day closes can only forecast gaps
// that SURVIVED day 0 unfilled, and for those the measured fill rate over the
// remaining window collapses to 55.5%/60.6% (0.5-1%), 52.8%/57.4% (1-2%),
// 51.4%/51.0% (2-4%), 45.1%/43.9% (4-10%) — every bucket below the 70%
// product bar. Emitting the unconditional number against the survivor
// population would be a lie, so the worker emits nothing and clears any
// stale rows. PredictGapFill remains for a future intraday-capable surface
// and reports the honest SURVIVOR-conditional accuracy.
const KindGapFill5 Kind = "gapfill5"

type gapBucket struct {
	lo, hi  float64
	accUp   float64 // fill rate GIVEN the gap survived day 0 unfilled
	accDown float64
	label   string
}

var gapBuckets = []gapBucket{
	{0.005, 0.01, 0.555, 0.606, "small gap (0.5-1%)"},
	{0.01, 0.02, 0.528, 0.574, "medium gap (1-2%)"},
	{0.02, 0.04, 0.514, 0.510, "large gap (2-4%)"},
	{0.04, 0.10, 0.451, 0.439, "outsized gap (4-10%)"},
}

// PredictGapFill forecasts, from a COMPLETED gap-day bar and the prior close,
// whether the still-unfilled gap fills within the remaining 4 sessions.
// HistoricalAccuracy is the measured SURVIVOR-CONDITIONAL rate — every bucket
// sits below 0.70, which is exactly why the live worker does not emit this
// kind. ok=false when there is no qualifying gap or it already filled on the
// gap day (honest absences).
func PredictGapFill(prevClose, open, high, low float64) (Forecast, bool) {
	if prevClose <= 0 || open <= 0 {
		return Forecast{}, false
	}
	gap := open/prevClose - 1
	ag := math.Abs(gap)
	for _, b := range gapBuckets {
		if ag < b.lo || ag >= b.hi {
			continue
		}
		acc, regime := b.accUp, "gap-up fill expected"
		if gap < 0 {
			acc, regime = b.accDown, "gap-down fill expected"
		}
		// already filled on the gap day itself?
		if gap > 0 && low > 0 && low <= prevClose {
			return Forecast{}, false
		}
		if gap < 0 && high > 0 && high >= prevClose {
			return Forecast{}, false
		}
		return Forecast{
			Kind: KindGapFill5, HorizonDays: 5, Regime: regime,
			Conviction:         acc,
			HistoricalAccuracy: acc,
			Tier:               b.label,
			Rank:               gap,
			N:                  0,
		}, true
	}
	return Forecast{}, false
}
