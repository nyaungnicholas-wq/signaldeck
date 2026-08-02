package workers

import (
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
)

// ScheduledWorker is the optional CALENDAR escape hatch for a worker whose real
// cadence is a publication event, not an interval.
//
// WHY: the Worker interface only exposed Interval(), so every job that is
// actually "daily after the FINRA drop" or "Sunday evening" had to fake it —
// tick fast, then gate internally on a date key and return a skip. Measured
// 2026-08-02: signalbt-weekly and weekly-report each ticked every 30m to do a
// once-per-week job (336 wakeups per useful run), and finra-shorts ticked 4x/day
// against a source that publishes once. The waste is not CPU, it is that the
// worker_runs log — the thing the Agents page and the watchdog read — fills with
// skips, so "this worker last did real work N days ago" becomes unanswerable.
//
// A ScheduledWorker instead returns the WALL-CLOCK instant of its next real run
// and the runner sleeps until exactly that. Interval() is still required: it
// backs the run-timeout calculation (see runTimeoutFor) and is the fallback if
// NextFire declines to schedule.
type ScheduledWorker interface {
	Worker
	// NextFire returns when this worker should next run. `last` is the start of
	// its previous run (zero if it has never run, including across restarts —
	// the runner seeds it from worker_runs). `now` is the current time.
	//
	// Return the zero time to decline and fall back to Interval() — that is the
	// correct answer for "I have no calendar opinion right now", not an error.
	// A returned instant at or before `now` is treated as "run immediately",
	// then clamped to at least minScheduledGap so a buggy implementation
	// cannot hot-loop the fleet.
	NextFire(last, now time.Time) time.Time
}

// minScheduledGap floors the runner's sleep between two scheduled runs. It
// exists only to bound a NextFire that returns the past forever; no honest
// calendar schedule is this tight.
const minScheduledGap = time.Minute

// maxScheduledGap caps how long the runner will sleep in one hop. A dead-source
// backoff can legitimately ask for 7 days (see congress-poller); waking to
// re-evaluate daily keeps a worker responsive to a config or clock change
// without making the schedule itself lie.
const maxScheduledGap = 24 * time.Hour

// nextFireFor resolves a worker's next run instant, applying the guards above.
// The second return is false when the worker has no calendar opinion and the
// caller should use its Interval.
func nextFireFor(w Worker, last, now time.Time) (time.Time, bool) {
	sw, ok := w.(ScheduledWorker)
	if !ok {
		return time.Time{}, false
	}
	next := sw.NextFire(last, now)
	if next.IsZero() {
		return time.Time{}, false
	}
	if !next.After(now) {
		next = now.Add(minScheduledGap)
	}
	return next, true
}

// ── calendar helpers ────────────────────────────────────────────────────────
//
// All of these work in America/New_York (marketcal.Loc), because every calendar
// this daemon cares about — FINRA's short-volume drop, the CFTC COT release, an
// SEC filing deadline, the NY trading week — is published on ET wall clock and
// therefore moves with US daylight saving. Scheduling them in UTC would silently
// shift them by an hour twice a year.

// DailyAtET returns the next occurrence of hour:min ET strictly after `now`.
func DailyAtET(now time.Time, hour, min int) time.Time {
	loc := marketcal.Loc()
	n := now.In(loc)
	fire := time.Date(n.Year(), n.Month(), n.Day(), hour, min, 0, 0, loc)
	if !fire.After(n) {
		fire = fire.AddDate(0, 0, 1)
	}
	return fire
}

// WeeklyAtET returns the next occurrence of `day` at hour:min ET strictly after
// `now`.
func WeeklyAtET(now time.Time, day time.Weekday, hour, min int) time.Time {
	fire := DailyAtET(now, hour, min)
	for fire.Weekday() != day {
		fire = fire.AddDate(0, 0, 1)
	}
	return fire
}

// TradingDayAtET returns the next hour:min ET that falls on a day the US equity
// market was open — the right schedule for anything derived from a session
// (FINRA short volume, consolidated tape summaries). A holiday simply rolls to
// the next open day; the source does not publish for a day that never traded.
func TradingDayAtET(now time.Time, hour, min int) time.Time {
	fire := DailyAtET(now, hour, min)
	for i := 0; i < 10; i++ {
		if marketcal.OpenForBars(fire) {
			return fire
		}
		fire = fire.AddDate(0, 0, 1)
	}
	// Ten consecutive closed days is not a real calendar; fall through rather
	// than loop, and let the worker's own gate refuse the run.
	return fire
}

// BackoffAfter returns an exponentially widening next-fire for a source that is
// failing, doubling `base` per consecutive failure up to `max`. It is the honest
// schedule for a dead upstream: keep a heartbeat so recovery is detected, stop
// paying full freight to re-learn that a 403 is still a 403.
func BackoffAfter(now time.Time, consecutiveFailures int, base, max time.Duration) time.Time {
	if consecutiveFailures <= 0 {
		return now.Add(base)
	}
	d := base
	for i := 0; i < consecutiveFailures && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return now.Add(d)
}
