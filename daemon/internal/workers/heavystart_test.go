package workers

import (
	"context"
	"sync"
	"testing"
	"time"
)

type heavyFake struct {
	fakeWorker
	iv time.Duration
}

func (w heavyFake) Interval() time.Duration { return w.iv }
func (w heavyFake) Heavy() bool             { return true }

// The heavy fleet's first runs spread across heavyStartSpan instead of
// landing in the same boot minute (2026-10-02 03:30: ten of them at once).
func TestHeavyWorkersSpreadAcrossTheBootWindow(t *testing.T) {
	if got := startSpan(fakeWorker{name: "prediction-resolver"}); got != maxStartOffset {
		t.Fatalf("a light worker's start span is %v, want %v", got, maxStartOffset)
	}
	if got := startSpan(heavyFake{fakeWorker: fakeWorker{name: "gbm-trainer"}, iv: time.Hour}); got != heavyStartSpan {
		t.Fatalf("a heavy worker's start span is %v, want %v", got, heavyStartSpan)
	}
	heavy := []string{"gbm-trainer", "per-symbol-learner", "pressure-trainer", "adaptive-weights",
		"model-health", "expectancy-runner", "metalabel-runner", "alpha-trainer", "expectancy-trainer",
		"feature-redundancy-runner", "canary-runner", "smart-money-scorer", "forecast-trainer", "feature-health"}
	seen := map[time.Duration]bool{}
	past, lo, hi := 0, heavyStartSpan, time.Duration(0)
	for _, n := range heavy {
		off := startOffset(n, time.Hour, heavyStartSpan)
		if off < 0 || off >= heavyStartSpan {
			t.Fatalf("%s offset %v outside [0, %v)", n, off, heavyStartSpan)
		}
		if off != startOffset(n, time.Hour, heavyStartSpan) {
			t.Fatalf("%s offset is not deterministic", n)
		}
		seen[off] = true
		if off >= maxStartOffset {
			past++
		}
		lo, hi = min(lo, off), max(hi, off)
	}
	if len(seen) < len(heavy)-1 || past < len(heavy)/2 || hi-lo < 10*time.Minute {
		t.Fatalf("heavy first runs: %d distinct slots of %d, %d past the old %v window, spread %v; want them spread across %v",
			len(seen), len(heavy), past, maxStartOffset, hi-lo, heavyStartSpan)
	}
	// Still bounded by the worker's own interval.
	if got := startOffset("gbm-trainer", 5*time.Second, heavyStartSpan); got >= 5*time.Second {
		t.Fatalf("offset %v >= the 5s interval", got)
	}
}

// Only the first run moves: after it, a heavy worker runs at its own interval.
func TestHeavyStaggerKeepsTheInterval(t *testing.T) {
	const iv = 300 * time.Millisecond
	var mu sync.Mutex
	var at []time.Time
	w := heavyFake{fakeWorker: fakeWorker{name: "heavy-cadence", fn: func(context.Context) (string, error) {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		return "ok", nil
	}}, iv: iv}
	r := NewRunner(openTemp(t), w)
	ctx, cancel := context.WithTimeout(context.Background(), 1600*time.Millisecond)
	defer cancel()
	start := time.Now()
	r.loop(ctx, w)
	_ = r.FlushRunJournal(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(at) < 4 {
		t.Fatalf("%d runs in 1.6s at a %v interval", len(at), iv)
	}
	if first := at[0].Sub(start); first > iv+100*time.Millisecond {
		t.Fatalf("first run after %v; the offset must stay inside the %v interval", first, iv)
	}
	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < iv-100*time.Millisecond || gap > iv+150*time.Millisecond {
			t.Fatalf("gap %d is %v, want the %v interval (runs %v)", i, gap, iv, at)
		}
	}
}
