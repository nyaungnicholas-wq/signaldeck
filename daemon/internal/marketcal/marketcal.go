// Package marketcal is a self-contained NYSE trading calendar: weekends, the
// full-closure federal holidays the exchange observes (with the weekend
// observance rules), and the 1:00pm ET half-days. It exists so the data-quality
// auditor stops flagging "stale" stock feeds on days the market is simply
// closed — the July 4th holiday that prompted this, plus every other closure.
//
// All reasoning happens in America/New_York (the exchange's clock), so EST/EDT
// and early closes are handled correctly. The zone database is embedded via
// time/tzdata so LoadLocation can never fail on a stripped host.
package marketcal

import (
	"sync"
	"time"
	_ "time/tzdata" // embed the tz database so America/New_York always loads
)

var nyLoc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// Degraded fallback: fixed EST. Half-day/holiday DATES are still
		// correct (they're date-only); only the intraday open/close boundary
		// loses DST precision. Never happens with tzdata embedded.
		return time.FixedZone("EST", -5*3600)
	}
	return loc
}()

// Loc returns the exchange location (America/New_York), for callers that need
// to render or reason in market time.
func Loc() *time.Location { return nyLoc }

// OpenForBars reports whether US equity 1-minute bars should be flowing at
// instant t: a regular NYSE session, past a 15-minute post-open grace (bars lag
// the 9:30 open), and before the close — 4:00pm ET normally, 1:00pm ET on a
// half-day. Weekends and full holidays are closed. This is intentionally the
// "should the feed be producing data right now" question, not "is the auction
// technically live".
func OpenForBars(t time.Time) bool {
	et := t.In(nyLoc)
	if wd := et.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	y := et.Year()
	if fullHolidays(y)[ymd(et)] {
		return false
	}
	closeMin := 16 * 60
	if halfDays(y)[ymd(et)] {
		closeMin = 13 * 60
	}
	mins := et.Hour()*60 + et.Minute()
	return mins >= 9*60+45 && mins <= closeMin
}

// IsFullHoliday reports whether the calendar day of t is a full NYSE closure
// (weekend NOT included — use OpenForBars for the full picture).
func IsFullHoliday(t time.Time) bool {
	et := t.In(nyLoc)
	return fullHolidays(et.Year())[ymd(et)]
}

// IsHalfDay reports whether the calendar day of t is a 1:00pm ET early close.
func IsHalfDay(t time.Time) bool {
	et := t.In(nyLoc)
	return halfDays(et.Year())[ymd(et)]
}

// ── internal calendar computation (cached per year) ─────────────────────

var (
	calMu     sync.Mutex
	fullCache = map[int]map[int]bool{}
	halfCache = map[int]map[int]bool{}
)

func fullHolidays(year int) map[int]bool {
	calMu.Lock()
	defer calMu.Unlock()
	if s, ok := fullCache[year]; ok {
		return s
	}
	s := computeFull(year)
	fullCache[year] = s
	return s
}

func halfDays(year int) map[int]bool {
	calMu.Lock()
	defer calMu.Unlock()
	if s, ok := halfCache[year]; ok {
		return s
	}
	s := computeHalf(year, computeFull(year))
	halfCache[year] = s
	return s
}

func computeFull(year int) map[int]bool {
	s := map[int]bool{}
	add := func(t time.Time) { s[ymd(t)] = true }

	// New Year's Day. NYSE does NOT close the preceding Friday when Jan 1 is a
	// Saturday (the well-known exception), so only shift Sunday -> Monday.
	ny := date(year, time.January, 1)
	switch ny.Weekday() {
	case time.Saturday:
		// no observed closure
	case time.Sunday:
		add(ny.AddDate(0, 0, 1))
	default:
		add(ny)
	}
	add(nthWeekday(year, time.January, time.Monday, 3))    // MLK Day
	add(nthWeekday(year, time.February, time.Monday, 3))   // Washington's Birthday
	add(goodFriday(year))                                  // Good Friday
	add(lastWeekday(year, time.May, time.Monday))          // Memorial Day
	if year >= 2022 {                                      // Juneteenth (federal since 2021, NYSE from 2022)
		add(observed(date(year, time.June, 19)))
	}
	add(observed(date(year, time.July, 4)))                // Independence Day
	add(nthWeekday(year, time.September, time.Monday, 1))  // Labor Day
	add(nthWeekday(year, time.November, time.Thursday, 4)) // Thanksgiving
	add(observed(date(year, time.December, 25)))           // Christmas
	return s
}

func computeHalf(year int, full map[int]bool) map[int]bool {
	s := map[int]bool{}
	add := func(t time.Time) {
		if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
			return
		}
		if full[ymd(t)] {
			return // a full holiday this year, not a half day
		}
		s[ymd(t)] = true
	}
	// Day after Thanksgiving (always a half day).
	add(nthWeekday(year, time.November, time.Thursday, 4).AddDate(0, 0, 1))
	// July 3 — half day when it's a trading day before Independence Day.
	add(date(year, time.July, 3))
	// Christmas Eve — half day when it's a trading day.
	add(date(year, time.December, 24))
	return s
}

// observed shifts a fixed-date holiday to the day the exchange actually closes:
// Saturday -> preceding Friday, Sunday -> following Monday.
func observed(t time.Time) time.Time {
	switch t.Weekday() {
	case time.Saturday:
		return t.AddDate(0, 0, -1)
	case time.Sunday:
		return t.AddDate(0, 0, 1)
	default:
		return t
	}
}

// goodFriday is the Friday two days before Easter Sunday (Gregorian, via the
// Anonymous/Meeus-Jones-Butcher algorithm).
func goodFriday(year int) time.Time {
	a := year % 19
	b := year / 100
	c := year % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := ((h + l - 7*m + 114) % 31) + 1
	return date(year, time.Month(month), day).AddDate(0, 0, -2)
}

func nthWeekday(year int, month time.Month, wd time.Weekday, n int) time.Time {
	first := date(year, month, 1)
	offset := (int(wd) - int(first.Weekday()) + 7) % 7
	return first.AddDate(0, 0, offset+(n-1)*7)
}

func lastWeekday(year int, month time.Month, wd time.Weekday) time.Time {
	// Day 0 of the next month == last day of this month.
	last := date(year, month+1, 0)
	offset := (int(last.Weekday()) - int(wd) + 7) % 7
	return last.AddDate(0, 0, -offset)
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, nyLoc)
}

// ymd is a comparable YYYYMMDD key for a day, read in the exchange zone.
func ymd(t time.Time) int {
	et := t.In(nyLoc)
	y, m, d := et.Date()
	return y*10000 + int(m)*100 + d
}
