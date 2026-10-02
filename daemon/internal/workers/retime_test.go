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

// fakeTicker records the intervals retime resets it to.
type fakeTicker struct{ resets []time.Duration }

func (f *fakeTicker) Reset(d time.Duration) { f.resets = append(f.resets, d) }

// A periodic worker's interval must be read again after each run. It was read
// once at boot, so the governor's 10-minute pressure cadence never took effect
// (2026-10-02: no pass for an hour after a 03:36 pass left the WAL at 1.6 GB).
func TestRetimeFollowsTheWorkersInterval(t *testing.T) {
	w := &dynWorker{name: "dyn", first: time.Hour, after: 10 * time.Minute}
	tk := &fakeTicker{}
	if got := retime(w, tk, time.Hour); got != time.Hour || len(tk.resets) != 0 {
		t.Fatalf("unchanged interval: got %v, resets %v; want 1h and no reset", got, tk.resets)
	}
	w.runs.Store(1) // the pass ended with pressure
	if got := retime(w, tk, time.Hour); got != 10*time.Minute || len(tk.resets) != 1 || tk.resets[0] != 10*time.Minute {
		t.Fatalf("shortened interval: got %v, resets %v; want 10m and one reset to 10m", got, tk.resets)
	}
	w.after = 0 // a zero interval is not a cadence: keep the current one
	if got := retime(w, tk, 10*time.Minute); got != 10*time.Minute || len(tk.resets) != 1 {
		t.Fatalf("zero interval: got %v, resets %v; want 10m kept", got, tk.resets)
	}
}

// The loop must call retime after each run. Here the interval goes from 200ms
// to an hour after the first run, so the loop can run at most once in 1.5s;
// with the interval read once it runs every 200ms. Only an upper bound is
// asserted: a slow host can only lower the count.
func TestLoopRetimesAfterEachRun(t *testing.T) {
	w := &dynWorker{name: "dyn-lengthens", first: 200 * time.Millisecond, after: time.Hour}
	r := NewRunner(openTemp(t), w)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	r.loop(ctx, w) // returns when ctx ends
	_ = r.FlushRunJournal(context.Background())
	if n := w.runs.Load(); n > 1 {
		t.Fatalf("%d runs in 1.5s after the interval moved to 1h; the loop never re-read it", n)
	}
}
