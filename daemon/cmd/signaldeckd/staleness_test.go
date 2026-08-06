package main

import (
	"context"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

type stalenessFakePlain struct {
	interval time.Duration
}

func (f *stalenessFakePlain) Name() string                { return "stalenessFakePlain" }
func (f *stalenessFakePlain) Interval() time.Duration    { return f.interval }
func (f *stalenessFakePlain) Run(ctx context.Context) (string, error) { return "", nil }

type stalenessFakeScheduled struct {
	interval time.Duration
	nextFire func(last, now time.Time) time.Time
}

func (f *stalenessFakeScheduled) Name() string                { return "stalenessFakeScheduled" }
func (f *stalenessFakeScheduled) Interval() time.Duration    { return f.interval }
func (f *stalenessFakeScheduled) Run(ctx context.Context) (string, error) { return "", nil }
func (f *stalenessFakeScheduled) NextFire(last, now time.Time) time.Time {
	if f.nextFire != nil {
		return f.nextFire(last, now)
	}
	return time.Time{}
}

func TestStalenessInterval(t *testing.T) {
	// Case 1: Plain worker returns its interval
	t.Run("plain worker", func(t *testing.T) {
		w := &stalenessFakePlain{interval: 30 * time.Minute}
		got := stalenessInterval(w)
		if got != 30*time.Minute {
			t.Errorf("plain worker: got %v, want 30m", got)
		}
	})

	// Case 2: Weekly scheduled worker - gap between fires is 168h (7 days)
	// This is the false-positive case: a weekly worker judged at its poll interval would be flagged as stale.
	t.Run("weekly scheduled worker", func(t *testing.T) {
		w := &stalenessFakeScheduled{
			interval: 30 * time.Minute,
			nextFire: func(last, now time.Time) time.Time {
				if last.IsZero() {
					return now.Add(7 * 24 * time.Hour)
				}
				return last.Add(7 * 24 * time.Hour)
			},
		}
		got := stalenessInterval(w)
		if got != 7*24*time.Hour {
			t.Errorf("weekly worker judged at its poll interval is the false-positive this guards; got %v, want 168h", got)
		}
	})

	// Case 3: Scheduled worker whose NextFire returns zero -> falls back to Interval
	t.Run("scheduled worker NextFire returns zero", func(t *testing.T) {
		w := &stalenessFakeScheduled{
			interval: 15 * time.Minute,
			nextFire: func(last, now time.Time) time.Time {
				return time.Time{}
			},
		}
		got := stalenessInterval(w)
		if got != 15*time.Minute {
			t.Errorf("scheduled worker with NextFire returning zero: got %v, want 15m", got)
		}
	})

	// Case 4: Scheduled worker whose gap is SHORTER than Interval -> returns Interval
	t.Run("scheduled worker gap shorter than interval", func(t *testing.T) {
		w := &stalenessFakeScheduled{
			interval: 1 * time.Hour,
			nextFire: func(last, now time.Time) time.Time {
				if last.IsZero() {
					return now.Add(10 * time.Minute)
				}
				return last.Add(10 * time.Minute)
			},
		}
		got := stalenessInterval(w)
		if got != 1*time.Hour {
			t.Errorf("scheduled worker gap shorter than interval: got %v, want 1h", got)
		}
	})

	// Case 5: Scheduled worker whose second NextFire equals the first -> falls back to Interval
	t.Run("scheduled worker no forward progress", func(t *testing.T) {
		w := &stalenessFakeScheduled{
			interval: 20 * time.Minute,
			nextFire: func(last, now time.Time) time.Time {
				if last.IsZero() {
					return now.Add(30 * time.Minute)
				}
				return last // No forward progress: second equals first
			},
		}
		got := stalenessInterval(w)
		if got != 20*time.Minute {
			t.Errorf("scheduled worker with second NextFire equal to first: got %v, want 20m", got)
		}
	})

	// Case 6: a BROKEN schedule must not buy itself an unbounded silence budget.
	//
	// This reproduces the real 2026-08-02 failure: TradingDayAtET tested
	// OpenForBars (true only 09:45-16:00 ET) against an 18:30 fire time, matched
	// on no day, exhausted its 10-day loop and fell through ~10 days out on every
	// reschedule. stalenessInterval faithfully derived 10 days, StaleWorkers
	// tripled it, and finra-shorts/finra-shortint went silent for four days
	// against a ~30-day alarm threshold. The watchdog was taking its threshold
	// from the schedule it was supposed to police.
	t.Run("pathological schedule is clamped", func(t *testing.T) {
		w := &stalenessFakeScheduled{
			interval: 30 * time.Minute,
			nextFire: func(last, now time.Time) time.Time {
				if last.IsZero() {
					return now.Add(10 * 24 * time.Hour)
				}
				return last.Add(10 * 24 * time.Hour)
			},
		}
		got := stalenessInterval(w)
		if got != maxDerivedCadence {
			t.Errorf("a 10-day derived cadence must clamp to %v, got %v — an unclamped "+
				"derivation hands a broken schedule a 3x-longer silence budget the "+
				"worse it breaks", maxDerivedCadence, got)
		}
	})

	// Case 7: the clamp must not touch the longest LEGITIMATE cadence. Weekly is
	// the real ceiling in this fleet (WeeklyAtET), so 7 days must pass through
	// unmodified — otherwise the clamp re-creates the weekly false positive that
	// stalenessInterval exists to prevent.
	t.Run("legitimate weekly cadence is not clamped", func(t *testing.T) {
		w := &stalenessFakeScheduled{
			interval: 30 * time.Minute,
			nextFire: func(last, now time.Time) time.Time {
				if last.IsZero() {
					return now.Add(7 * 24 * time.Hour)
				}
				return last.Add(7 * 24 * time.Hour)
			},
		}
		if got := stalenessInterval(w); got != 7*24*time.Hour {
			t.Errorf("weekly cadence must survive the clamp intact: got %v, want 168h", got)
		}
		if maxDerivedCadence <= 7*24*time.Hour {
			t.Fatalf("maxDerivedCadence (%v) must exceed the weekly cadence or every "+
				"weekly worker false-positives", maxDerivedCadence)
		}
	})
}

// Ensure the fake types satisfy the required interfaces at compile time.
var (
	_ workers.Worker          = &stalenessFakePlain{}
	_ workers.ScheduledWorker = &stalenessFakeScheduled{}
)