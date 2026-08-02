package workers

import (
	"context"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
)

// schedStub is a plain Worker (no calendar opinion).
type schedStub struct{ iv time.Duration }

func (s schedStub) Name() string                        { return "sched-stub" }
func (s schedStub) Interval() time.Duration             { return s.iv }
func (s schedStub) Run(context.Context) (string, error) { return "", nil }

// schedStubCal is a ScheduledWorker returning whatever the test plants.
type schedStubCal struct {
	schedStub
	next time.Time
}

func (s schedStubCal) NextFire(_, _ time.Time) time.Time { return s.next }

func TestDailyAtET(t *testing.T) {
	loc := marketcal.Loc()
	// 10:00 ET → the 18:30 slot is later today.
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	got := DailyAtET(now, 18, 30)
	if !got.Equal(time.Date(2026, 8, 3, 18, 30, 0, 0, loc)) {
		t.Fatalf("same-day slot: got %s", got)
	}
	// 19:00 ET → the 18:30 slot has passed, roll to tomorrow.
	now = time.Date(2026, 8, 3, 19, 0, 0, 0, loc)
	got = DailyAtET(now, 18, 30)
	if !got.Equal(time.Date(2026, 8, 4, 18, 30, 0, 0, loc)) {
		t.Fatalf("next-day roll: got %s", got)
	}
	// Exactly at the slot counts as passed — a schedule must always advance,
	// or scheduledLoop re-fires the run it just finished.
	now = time.Date(2026, 8, 3, 18, 30, 0, 0, loc)
	if got = DailyAtET(now, 18, 30); !got.After(now) {
		t.Fatalf("boundary must advance: got %s", got)
	}
}

func TestWeeklyAtET(t *testing.T) {
	loc := marketcal.Loc()
	// Monday → next Saturday 09:00.
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, loc) // 2026-08-03 is a Monday
	if now.Weekday() != time.Monday {
		t.Fatalf("fixture drift: %s is %s", now, now.Weekday())
	}
	got := WeeklyAtET(now, time.Saturday, 9, 0)
	if got.Weekday() != time.Saturday || got.Hour() != 9 {
		t.Fatalf("got %s (%s)", got, got.Weekday())
	}
	if d := got.Sub(now); d <= 0 || d > 7*24*time.Hour {
		t.Fatalf("must be within the next week, got %s", d)
	}
	// Called ON the target weekday after the hour, it rolls a full week.
	sat := time.Date(2026, 8, 8, 10, 0, 0, 0, loc) // Saturday, past 09:00
	got = WeeklyAtET(sat, time.Saturday, 9, 0)
	if got.Weekday() != time.Saturday || !got.After(sat) {
		t.Fatalf("same-weekday roll: got %s", got)
	}
}

func TestTradingDayAtETSkipsWeekend(t *testing.T) {
	loc := marketcal.Loc()
	// Friday evening past the slot → must land Monday, not Saturday.
	fri := time.Date(2026, 8, 7, 20, 0, 0, 0, loc)
	if fri.Weekday() != time.Friday {
		t.Fatalf("fixture drift: %s", fri.Weekday())
	}
	got := TradingDayAtET(fri, 18, 30)
	if wd := got.Weekday(); wd == time.Saturday || wd == time.Sunday {
		t.Fatalf("landed on a closed day: %s (%s)", got, wd)
	}
}

func TestBackoffAfter(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	base, max := 12*time.Hour, 7*24*time.Hour

	if got := BackoffAfter(now, 0, base, max).Sub(now); got != base {
		t.Fatalf("healthy: want %s, got %s", base, got)
	}
	if got := BackoffAfter(now, 1, base, max).Sub(now); got != 2*base {
		t.Fatalf("one failure: want %s, got %s", 2*base, got)
	}
	if got := BackoffAfter(now, 3, base, max).Sub(now); got != 8*base {
		t.Fatalf("three failures: want %s, got %s", 8*base, got)
	}
	// Must clamp, and must stay clamped no matter how long the source is dead.
	for _, n := range []int{5, 50, 5000} {
		if got := BackoffAfter(now, n, base, max).Sub(now); got != max {
			t.Fatalf("clamp at %d failures: want %s, got %s", n, max, got)
		}
	}
}

func TestNextFireFor(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	// A plain Worker has no calendar opinion.
	if _, ok := nextFireFor(schedStub{iv: time.Hour}, time.Time{}, now); ok {
		t.Fatal("plain worker must decline")
	}
	// A ScheduledWorker returning the zero time declines too.
	if _, ok := nextFireFor(schedStubCal{}, time.Time{}, now); ok {
		t.Fatal("zero NextFire must decline")
	}
	// A future instant passes through unchanged.
	want := now.Add(3 * time.Hour)
	got, ok := nextFireFor(schedStubCal{next: want}, time.Time{}, now)
	if !ok || !got.Equal(want) {
		t.Fatalf("future instant: ok=%v got=%s", ok, got)
	}
	// A past instant is clamped forward — a NextFire stuck in the past must not
	// be able to hot-loop the runner.
	got, ok = nextFireFor(schedStubCal{next: now.Add(-time.Hour)}, time.Time{}, now)
	if !ok {
		t.Fatal("past instant must still schedule")
	}
	if d := got.Sub(now); d < minScheduledGap {
		t.Fatalf("past instant not clamped: %s", d)
	}
}
