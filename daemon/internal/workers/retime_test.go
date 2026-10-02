package workers

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// dynWorker's interval is `first` until it has run once, then `after`: the
// shape of the storage governor, whose interval shrinks when a pass leaves the
// WAL large.
type dynWorker struct {
	name         string
	first, after time.Duration
	runs         atomic.Int32
}

func (w *dynWorker) Name() string { return w.name }
func (w *dynWorker) Interval() time.Duration {
	if w.runs.Load() == 0 {
		return w.first
	}
	return w.after
}
func (w *dynWorker) Run(context.Context) (string, error) { w.runs.Add(1); return "ok", nil }

// A periodic worker's interval must be read again after each run. It was read
// once at boot, so the governor's 10-minute pressure cadence never took effect
// (2026-10-02: no pass for an hour after a 03:36 pass left the WAL at 1.6 GB).
func TestIntervalIsReadAfterEachRun(t *testing.T) {
	cases := []struct {
		name         string
		first, after time.Duration
		min, max     int32
	}{
		// 200ms then 50ms: ~20 runs in the window; read once, ~6.
		{"dyn-shortens", 200 * time.Millisecond, 50 * time.Millisecond, 12, 1 << 30},
		// 200ms then an hour: exactly one run; read once, ~6.
		{"dyn-lengthens", 200 * time.Millisecond, time.Hour, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &dynWorker{name: c.name, first: c.first, after: c.after}
			r := NewRunner(openTemp(t), w)
			ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
			defer cancel()
			r.loop(ctx, w) // returns when ctx ends
			if n := w.runs.Load(); n < c.min || n > c.max {
				t.Fatalf("%d runs in 1.2s with interval %v then %v, want %d..%d", n, c.first, c.after, c.min, c.max)
			}
			_ = r.FlushRunJournal(context.Background())
		})
	}
}
