// Package workers provides scheduling helpers with catch‑up logic.
// several workers fire at a fixed ET wall-clock slot (FINRA short interest 18:45 ET,
// CFTC COT Saturday 09:00 ET, Congress 09:00 ET). This host is routinely powered off
// or the daemon stopped at those hours, and a plain "next slot" schedule then silently
// skips the whole period: measured 2026-09-09, the short-interest poller had not run
// since 09-03 and the COT poller since 08-31 while both sources went stale. Each worker
// had grown its own ad-hoc catch-up constant (9 days for COT, 26 hours for short volume)
// or none at all. These helpers give every fixed-slot schedule one rule: if the most
// recent slot at or before `now` came after the last run, the daemon was down when it
// was due, so fire now; otherwise fire at the next slot.

package workers

import (
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
)

// DailyAtETCatchUp returns the next scheduled time for a daily ET slot,
// applying a catch‑up rule: if the worker missed its previous slot
// (last run is before the most recent slot), run now; otherwise run at the
// next slot.
func DailyAtETCatchUp(last, now time.Time, hour, min int) time.Time {
	next := DailyAtET(now, hour, min)
	prev := next.AddDate(0, 0, -1)
	if !last.IsZero() && last.Before(prev) {
		return now
	}
	return next
}

// WeeklyAtETCatchUp returns the next scheduled time for a weekly ET slot,
// applying the same catch‑up rule as DailyAtETCatchUp but with a one‑week
// interval.
func WeeklyAtETCatchUp(last, now time.Time, day time.Weekday, hour, min int) time.Time {
	next := WeeklyAtET(now, day, hour, min)
	prev := next.AddDate(0, 0, -7)
	if !last.IsZero() && last.Before(prev) {
		return now
	}
	return next
}

// TradingDayAtETCatchUp returns the next scheduled time for a trading‑day
// ET slot, applying the catch‑up rule.  The previous slot is found by
// stepping back from the next slot until a trading day is found (max 15
// days to avoid infinite loops).
func TradingDayAtETCatchUp(last, now time.Time, hour, min int) time.Time {
	next := TradingDayAtET(now, hour, min)
	prev := DailyAtET(now, hour, min).AddDate(0, 0, -1)
	for i := 0; i < 15 && !marketcal.IsTradingDay(prev); i++ {
		prev = prev.AddDate(0, 0, -1)
	}
	if !last.IsZero() && last.Before(prev) {
		return now
	}
	return next
}
