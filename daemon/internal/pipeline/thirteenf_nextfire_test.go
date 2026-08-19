package pipeline

import (
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
)

// A healthy worker must keep its pinned 20:00 ET slot and must NOT fire immediately.
func TestThirteenFNextFire_OnScheduleDoesNotCatchUp(t *testing.T) {
	loc := marketcal.Loc()
	w := &ThirteenFPoller{}
	last := time.Date(2026, 8, 11, 20, 0, 0, 0, loc)
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, loc)
	expected := time.Date(2026, 8, 12, 20, 0, 0, 0, loc)
	got := w.NextFire(last, now)
	if !got.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// This is the live 2026-08-13 case, where restart churn across the 20:00 window skipped a day and the old code never retried.
func TestThirteenFNextFire_MissedWindowCatchesUp(t *testing.T) {
	loc := marketcal.Loc()
	w := &ThirteenFPoller{}
	last := time.Date(2026, 8, 11, 17, 0, 0, 0, loc)
	now := time.Date(2026, 8, 13, 1, 0, 0, 0, loc)
	got := w.NextFire(last, now)
	if !got.Equal(now) {
		t.Fatalf("expected %v, got %v", now, got)
	}
}

// A daemon that has never run this worker must not stampede at boot.
func TestThirteenFNextFire_ZeroLastUsesScheduleNotCatchUp(t *testing.T) {
	loc := marketcal.Loc()
	w := &ThirteenFPoller{}
	last := time.Time{}
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, loc)
	expected := time.Date(2026, 8, 12, 20, 0, 0, 0, loc)
	got := w.NextFire(last, now)
	if !got.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// A catch-up must not permanently shift the pinned 20:00 ET slot.
//
// Raised in review: NextFire returns `now` on catch-up, so the recovery run
// happens at restart time (e.g. 21:00) rather than at 20:00 — the worry being
// that the schedule then walks forward every time. It does not. Once the
// catch-up run lands, `last` is recent, the stale branch stops firing, and
// DailyAtET pins the next fire back to 20:00 ET. Only the single recovery run
// is off-hour, which is the intended trade: run the missed day now, then resume
// the schedule that makes consecutive days comparable.
func TestThirteenFNextFire_CatchUpDoesNotDriftTheSchedule(t *testing.T) {
	loc := marketcal.Loc()
	w := &ThirteenFPoller{}

	// Missed window: last ran 08-11 17:00, now 08-12 21:00 (28h) — catch up.
	last := time.Date(2026, 8, 11, 17, 0, 0, 0, loc)
	now := time.Date(2026, 8, 12, 21, 0, 0, 0, loc)
	caught := w.NextFire(last, now)
	if !caught.Equal(now) {
		t.Fatalf("catch-up: expected %v, got %v", now, caught)
	}

	// The catch-up run just happened, so `last` is now that moment. The very
	// next decision must return to the 20:00 slot, NOT keep firing at 21:00.
	next := w.NextFire(now, now)
	want := time.Date(2026, 8, 13, 20, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("after catch-up the schedule must return to 20:00 ET: expected %v, got %v", want, next)
	}
}

// The comparison is strictly greater-than, so exactly-at-threshold is still on schedule.
func TestThirteenFNextFire_BoundaryIsExclusive(t *testing.T) {
	loc := marketcal.Loc()
	w := &ThirteenFPoller{}
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, loc)

	lastExactly26h := now.Add(-26 * time.Hour)
	expected := time.Date(2026, 8, 12, 20, 0, 0, 0, loc)
	got := w.NextFire(lastExactly26h, now)
	if !got.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}

	lastOver26h := now.Add(-26*time.Hour - time.Second)
	got = w.NextFire(lastOver26h, now)
	if !got.Equal(now) {
		t.Fatalf("expected %v, got %v", now, got)
	}
}
