package workers

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestIntervalsIsSafeAgainstConcurrentAdd pins the lock on Runner.Intervals and
// Runner.Add.
//
// The wiring makes this overlap real rather than theoretical: in
// cmd/signaldeckd/run.go the API server is started by `api.Serve(ctx, deps)`
// (line ~655) BEFORE the final `runner.Add(fleet...)` (line ~662), and Deps
// carries `WorkerIntervals: runner.Intervals`. So a /api/fleet-health request
// arriving in that window calls Intervals while Add is still appending to the
// same slice.
//
// Intervals originally read `workers` unlocked, justified by "appended only
// during startup wiring, read-only thereafter". That invariant is false for
// exactly the window above, and `go test -race` over the existing suite did NOT
// catch it because nothing exercised startup concurrency. This test is that
// exercise: run it under -race and it fails if either lock is removed.
func TestIntervalsIsSafeAgainstConcurrentAdd(t *testing.T) {
	r := NewRunner(nil)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader: what the API handler does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for name, iv := range r.Intervals() {
				if name == "" || iv < 0 {
					t.Errorf("Intervals returned a malformed entry: %q=%v", name, iv)
					return
				}
			}
		}
	}()

	// Writer: what run.go does after the server is already serving.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r.Add(fakeWorker{
				name: "w" + string(rune('a'+i%26)),
				fn:   func(context.Context) (string, error) { return "", nil },
			})
		}
		close(stop)
	}()

	wg.Wait()

	// Sanity: the reader must be able to observe the fully-registered fleet, or
	// the lock is holding something back rather than merely serializing it.
	got := r.Intervals()
	if len(got) == 0 {
		t.Fatal("Intervals returned nothing after 200 Adds")
	}
	for name, iv := range got {
		if iv != time.Hour {
			t.Fatalf("%s: expected the declared 1h interval, got %v", name, iv)
		}
	}
}
