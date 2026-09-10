package marketcal

import "time"

// SessionDate returns midnight in the America/New_York location of t's New York calendar date.
func SessionDate(t time.Time) time.Time {
	y, m, d := t.In(Loc()).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, Loc())
}

// SessionClose returns the NYSE session close instant for t's New York calendar date.
// It returns 16:00 ET, or 13:00 ET on a half‑day, and ok=false when t is not a trading day.
func SessionClose(t time.Time) (time.Time, bool) {
	if !IsTradingDay(t) {
		return time.Time{}, false
	}
	y, m, d := t.In(Loc()).Date()
	closeHour := 16
	if IsHalfDay(t) {
		closeHour = 13
	}
	return time.Date(y, m, d, closeHour, 0, 0, 0, Loc()), true
}

// SessionsClosedSince counts trading days D whose session date is strictly after the bar's session date
// and whose session close is at or equal to now. It walks forward one calendar day at a time,
// stops when the day exceeds now's New York calendar date, caps the result at 60,
// and returns 0 for non‑positive bar timestamps.
func SessionsClosedSince(barTs int64, now time.Time) int {
	if barTs <= 0 {
		return 0
	}
	barDate := SessionDate(time.Unix(barTs, 0))
	nowDate := SessionDate(now)
	count := 0
	for day := barDate.AddDate(0, 0, 1); !day.After(nowDate); day = day.AddDate(0, 0, 1) {
		if close, ok := SessionClose(day); ok && !close.After(now) {
			count++
			if count >= 60 {
				return 60
			}
		}
	}
	return count
}

// DailyBarStale reports true when at least two trading days lie strictly between the bar's New York date
// and now's New York date (today excluded). It iterates at most 60 calendar days and returns false
// for non‑positive bar timestamps.
func DailyBarStale(latestBarTs int64, now time.Time) bool {
	if latestBarTs <= 0 {
		return false
	}
	barDate := SessionDate(time.Unix(latestBarTs, 0))
	nowDate := SessionDate(now)
	count := 0
	for day := barDate.AddDate(0, 0, 1); day.Before(nowDate); day = day.AddDate(0, 0, 1) {
		if IsTradingDay(day) {
			count++
			if count >= 2 {
				return true
			}
		}
	}
	return false
}
