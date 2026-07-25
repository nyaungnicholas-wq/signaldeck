package workers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openTemp(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// fakeWorker runs the given fn once.
type fakeWorker struct {
	name string
	fn   func(ctx context.Context) (string, error)
}

func (w fakeWorker) Name() string                            { return w.name }
func (w fakeWorker) Interval() time.Duration                 { return time.Hour }
func (w fakeWorker) Run(ctx context.Context) (string, error) { return w.fn(ctx) }

// lastRun fetches the newest persisted run for a worker.
func lastRun(t *testing.T, st *store.Store, name string) (status, detail string, finished bool) {
	t.Helper()
	runs, err := st.RecentWorkerRuns(context.Background(), 50)
	if err != nil {
		t.Fatalf("recent runs: %v", err)
	}
	for _, r := range runs {
		if r.Worker == name {
			return r.Status, r.Detail, r.FinishedAt != nil
		}
	}
	t.Fatalf("no run recorded for %q", name)
	return "", "", false
}

// TestRunOnce_RecordsFinishAfterRunContextExpired is the live-log regression
// ("worker: finish record — context deadline exceeded"): a worker that blows
// its run deadline must STILL get a finished worker_runs row, because the
// finish write uses its own fresh context, never the (dead) run context.
func TestRunOnce_RecordsFinishAfterRunContextExpired(t *testing.T) {
	st := openTemp(t)
	r := NewRunner(st)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	w := fakeWorker{name: "slow-worker", fn: func(ctx context.Context) (string, error) {
		<-ctx.Done() // simulate a run that consumes its entire deadline
		return "", ctx.Err()
	}}
	r.runOnce(ctx, w)

	if ctx.Err() == nil {
		t.Fatal("test setup: run context should be expired")
	}
	status, detail, finished := lastRun(t, st, "slow-worker")
	if !finished {
		t.Fatal("run was never finished — finish record must survive an expired run context")
	}
	// An expiry at the runner-context level is treated as shutdown, not failure.
	if status != "ok" || detail != "stopped (shutdown)" {
		t.Fatalf("status=%q detail=%q, want ok/stopped (shutdown)", status, detail)
	}
}

// TestRunOnce_RecordsErrorOutcome: a plain failure lands as status=error with
// the error text as detail, and the row is closed.
func TestRunOnce_RecordsErrorOutcome(t *testing.T) {
	st := openTemp(t)
	r := NewRunner(st)

	w := fakeWorker{name: "failing-worker", fn: func(ctx context.Context) (string, error) {
		return "", errors.New("edgar: status 403")
	}}
	r.runOnce(context.Background(), w)

	status, detail, finished := lastRun(t, st, "failing-worker")
	if !finished || status != "error" || detail != "edgar: status 403" {
		t.Fatalf("finished=%v status=%q detail=%q, want finished error row", finished, status, detail)
	}
}

// TestRunOnce_RecordsSuccessDetail: happy path sanity.
func TestRunOnce_RecordsSuccessDetail(t *testing.T) {
	st := openTemp(t)
	r := NewRunner(st)

	w := fakeWorker{name: "ok-worker", fn: func(ctx context.Context) (string, error) {
		return "stored 5 holdings", nil
	}}
	r.runOnce(context.Background(), w)

	status, detail, finished := lastRun(t, st, "ok-worker")
	if !finished || status != "ok" || detail != "stored 5 holdings" {
		t.Fatalf("finished=%v status=%q detail=%q, want finished ok row", finished, status, detail)
	}
}

// De-phasing: offsets are deterministic, bounded by both the cap and the
// worker's own interval, and spread distinct workers across the window — the
// fix for the measured boot stampede + permanent phase-lock.
func TestStartOffsetDeterministicAndBounded(t *testing.T) {
	// Deterministic: same name+interval ⇒ same offset, always.
	for i := 0; i < 5; i++ {
		if a, b := startOffset("composite-scorer", time.Hour), startOffset("composite-scorer", time.Hour); a != b {
			t.Fatalf("offset not deterministic: %v vs %v", a, b)
		}
	}
	// Bounded by the cap for long intervals.
	if got := startOffset("gbm-trainer", time.Hour); got < 0 || got >= maxStartOffset {
		t.Errorf("hourly offset %v outside [0, %v)", got, maxStartOffset)
	}
	// Bounded by the INTERVAL for short ones (a 5s worker must not wait 45s).
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("fast-worker-%d", i)
		if got := startOffset(name, 5*time.Second); got >= 5*time.Second {
			t.Fatalf("%s offset %v >= its 5s interval", name, got)
		}
	}
	// De-phasing actually spreads: the real hourly fleet must not collapse
	// onto one slot (the phase-lock this fixes).
	hourly := []string{"gbm-trainer", "pressure-trainer", "research-ledger", "ranking-runner", "expectancy-runner", "scores-compactor", "derived-retention", "storage-governor", "ai-analyst"}
	seen := map[time.Duration]bool{}
	for _, n := range hourly {
		seen[startOffset(n, time.Hour)] = true
	}
	if len(seen) < len(hourly)-1 { // allow at most one collision
		t.Errorf("hourly fleet collapsed onto %d distinct slots (of %d workers) — still phase-locked", len(seen), len(hourly))
	}
}

// Zero interval (long-running stream workers) must never be delayed.
func TestStartOffsetZeroInterval(t *testing.T) {
	if got := startOffset("crypto-live", 0); got != 0 {
		t.Errorf("long-running worker offset = %v, want 0", got)
	}
}

// quickWorker is a fakeWorker with a tiny interval so its startOffset (bounded
// by the interval) is negligible — shutdown tests need the first run to begin
// almost immediately.
type quickWorker struct{ fakeWorker }

func (w quickWorker) Interval() time.Duration { return 20 * time.Millisecond }

// TestStart_ForceExitsWhenWorkerIgnoresCancellation is the 2026-07-24 hang
// regression: SIGTERM cancelled the fleet context but one worker sat in a
// context-insensitive call, so Start's wg.Wait() parked the daemon forever.
// Start must escalate: after ShutdownGrace it names the stuck worker and
// force-exits instead of hanging.
func TestStart_ForceExitsWhenWorkerIgnoresCancellation(t *testing.T) {
	st := openTemp(t)

	oldGrace, oldExit := ShutdownGrace, forceExit
	t.Cleanup(func() { ShutdownGrace, forceExit = oldGrace, oldExit })
	ShutdownGrace = 100 * time.Millisecond
	exited := make(chan int, 1)
	forceExit = func(code int) { exited <- code; select {} } // park like os.Exit: never returns

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	stuck := quickWorker{fakeWorker{name: "stuck-worker", fn: func(ctx context.Context) (string, error) {
		<-block // ignores ctx — the exact failure mode
		return "", nil
	}}}
	polite := quickWorker{fakeWorker{name: "polite-worker", fn: func(ctx context.Context) (string, error) {
		return "ok", nil
	}}}

	r := NewRunner(st, stuck, polite)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(60 * time.Millisecond); cancel() }()
	go r.Start(ctx)

	select {
	case code := <-exited:
		if code != 1 {
			t.Fatalf("force exit code = %d, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start never escalated to forceExit with a stuck worker")
	}

	names := r.stuckWorkers()
	if len(names) != 1 || !strings.HasPrefix(names[0], "stuck-worker") {
		t.Fatalf("stuckWorkers = %v, want exactly stuck-worker", names)
	}
}

// TestStart_ReturnsCleanlyWhenWorkersDrain: a fleet whose workers all respect
// cancellation must let Start return normally, never touching forceExit.
func TestStart_ReturnsCleanlyWhenWorkersDrain(t *testing.T) {
	st := openTemp(t)

	oldGrace, oldExit := ShutdownGrace, forceExit
	t.Cleanup(func() { ShutdownGrace, forceExit = oldGrace, oldExit })
	ShutdownGrace = 100 * time.Millisecond
	forceExit = func(code int) { t.Errorf("forceExit(%d) called for a clean drain", code) }

	w := quickWorker{fakeWorker{name: "clean-worker", fn: func(ctx context.Context) (string, error) {
		return "ok", nil
	}}}
	r := NewRunner(st, w)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(60 * time.Millisecond); cancel() }()

	done := make(chan struct{})
	go func() { r.Start(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after clean worker drain")
	}
}
