package marketcal

import (
	"testing"
	"time"
)

func et(t *testing.T, y int, m time.Month, d, hh, mm int) time.Time {
	t.Helper()
	return time.Date(y, m, d, hh, mm, 0, 0, nyLoc)
}

func TestFullHolidays2026(t *testing.T) {
	// (month, day) expected to be full NYSE closures in 2026.
	want := []struct {
		m    time.Month
		d    int
		name string
	}{
		{time.January, 1, "New Year's Day (Thu)"},
		{time.January, 19, "MLK Day (3rd Mon)"},
		{time.February, 16, "Washington's Birthday (3rd Mon)"},
		{time.April, 3, "Good Friday"},
		{time.May, 25, "Memorial Day (last Mon)"},
		{time.June, 19, "Juneteenth (Fri)"},
		{time.July, 3, "Independence Day observed (Jul 4 is Sat)"},
		{time.September, 7, "Labor Day (1st Mon)"},
		{time.November, 26, "Thanksgiving (4th Thu)"},
		{time.December, 25, "Christmas (Fri)"},
	}
	for _, w := range want {
		if !IsFullHoliday(et(t, 2026, w.m, w.d, 12, 0)) {
			t.Errorf("%s: %d-%02d-%02d should be a full holiday", w.name, 2026, int(w.m), w.d)
		}
	}
}

func TestJuly4HolidayNoStale(t *testing.T) {
	// The bug this whole package fixes: 2026-07-03 is the observed Independence
	// Day (Jul 4 is a Saturday). Market is CLOSED all day → never open-for-bars.
	for _, hh := range []int{6, 10, 13, 15, 20} {
		if OpenForBars(et(t, 2026, time.July, 3, hh, 0)) {
			t.Fatalf("2026-07-03 %02d:00 ET: market is a holiday, OpenForBars must be false", hh)
		}
	}
}

func TestRegularSession(t *testing.T) {
	// 2026-07-07 is a normal Tuesday.
	cases := []struct {
		hh, mm int
		open   bool
	}{
		{8, 0, false},   // pre-market
		{9, 30, false},  // at the open, still inside the 15m bar-grace
		{9, 45, true},   // grace elapsed
		{12, 0, true},   // midday
		{15, 59, true},  // just before close
		{16, 30, false}, // after close
	}
	for _, c := range cases {
		if got := OpenForBars(et(t, 2026, time.July, 7, c.hh, c.mm)); got != c.open {
			t.Errorf("2026-07-07 %02d:%02d ET: OpenForBars=%v want %v", c.hh, c.mm, got, c.open)
		}
	}
}

func TestWeekendClosed(t *testing.T) {
	if OpenForBars(et(t, 2026, time.July, 11, 12, 0)) { // Saturday
		t.Fatal("Saturday must be closed")
	}
	if OpenForBars(et(t, 2026, time.July, 12, 12, 0)) { // Sunday
		t.Fatal("Sunday must be closed")
	}
}

func TestHalfDayEarlyClose(t *testing.T) {
	// Day after Thanksgiving 2026 = Nov 27 (Friday) → 1:00pm ET early close.
	if !IsHalfDay(et(t, 2026, time.November, 27, 12, 0)) {
		t.Fatal("2026-11-27 should be a half day")
	}
	if !OpenForBars(et(t, 2026, time.November, 27, 12, 0)) {
		t.Error("half day should still be open at noon")
	}
	if OpenForBars(et(t, 2026, time.November, 27, 13, 30)) {
		t.Error("half day must be closed after 1:00pm ET")
	}
	// A regular Tuesday is open until 4:00pm.
	if !OpenForBars(et(t, 2026, time.July, 7, 15, 0)) {
		t.Error("regular day should be open at 3:00pm ET")
	}
}

func TestNewYearSaturdayException(t *testing.T) {
	// Jan 1 2028 is a Saturday → NYSE does NOT close the preceding Friday
	// (Dec 31 2027), and there's no Jan-1 closure that year on the 1st itself.
	if IsFullHoliday(et(t, 2027, time.December, 31, 12, 0)) {
		t.Error("Dec 31 2027 (Fri before a Saturday New Year) must not be a holiday")
	}
	// Jan 1 2027 is a Friday → observed on the day.
	if !IsFullHoliday(et(t, 2027, time.January, 1, 12, 0)) {
		t.Error("2027-01-01 (Fri) should be New Year's holiday")
	}
}

func TestGoodFridayAcrossYears(t *testing.T) {
	// Independent Easter checks (Good Friday = Easter - 2).
	cases := []struct {
		y    int
		m    time.Month
		d    int
	}{
		{2026, time.April, 3},
		{2025, time.April, 18},
		{2024, time.March, 29},
	}
	for _, c := range cases {
		if !IsFullHoliday(et(t, c.y, c.m, c.d, 12, 0)) {
			t.Errorf("Good Friday %d should be %d-%02d-%02d (holiday)", c.y, c.y, int(c.m), c.d)
		}
	}
}
