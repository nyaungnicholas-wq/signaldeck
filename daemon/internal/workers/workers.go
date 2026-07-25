// Package workers is SignalDeck's in-app agent framework. Each Worker is an
// autonomous unit with a name, a cadence, and a Run method; the Runner
// schedules them, persists every run (status + detail) to worker_runs, and
// recovers from panics — the Agents page in the UI renders exactly this
// table, so what the user sees IS what ran.
package workers

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Worker is one in-app agent.
type Worker interface {
	// Name is the stable identifier shown on the Agents page.
	Name() string
	// Interval is the cadence between runs (0 = long-running: Run is called
	// once and expected to block until ctx is done, e.g. stream ingestors).
	Interval() time.Duration
	// Run does one unit of work and returns a one-line human detail.
	Run(ctx context.Context) (detail string, err error)
}

// ShutdownGrace is how long Start waits after ctx cancellation for workers to
// drain before force-exiting the process. A worker parked in a
// context-insensitive call (SQLite exec, HTTP with no timeout, a bare channel
// receive) would otherwise hang wg.Wait() forever — observed 2026-07-24 as a
// daemon stuck 30+ min post-SIGTERM until SIGKILL.
var ShutdownGrace = 75 * time.Second

// forceExit is swappable so tests can observe the escalation without dying.
var forceExit = func(code int) { os.Exit(code) }

// Runner schedules workers and records their runs.
type Runner struct {
	st      *store.Store
	workers []Worker

	mu      sync.Mutex
	running map[string]time.Time // worker name → start of in-flight Run
}

// NewRunner builds a runner over the given workers.
func NewRunner(st *store.Store, ws ...Worker) *Runner {
	return &Runner{st: st, workers: ws, running: make(map[string]time.Time)}
}

// Start launches every worker and blocks until ctx is done and all exit —
// or until ShutdownGrace after cancellation, at which point it logs which
// workers are still in-flight and force-exits the process. SQLite (WAL) and
// every worker's persistence are crash-safe, so a hard exit beats a daemon
// parked forever behind a stuck goroutine.
func (r *Runner) Start(ctx context.Context) {
	var wg sync.WaitGroup
	for _, w := range r.workers {
		wg.Add(1)
		go func(w Worker) {
			defer wg.Done()
			r.loop(ctx, w)
		}(w)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
	select {
	case <-done:
	case <-time.After(ShutdownGrace):
		slog.Error("shutdown deadline exceeded — forcing exit",
			"grace", ShutdownGrace, "stuckWorkers", r.stuckWorkers())
		forceExit(1)
	}
}

// stuckWorkers names the workers with a Run still in flight, oldest first.
func (r *Runner) stuckWorkers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	type entry struct {
		name  string
		since time.Time
	}
	entries := make([]entry, 0, len(r.running))
	for name, since := range r.running {
		entries = append(entries, entry{name, since})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].since.Before(entries[j].since) })
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = fmt.Sprintf("%s (running %s)", e.name, time.Since(e.since).Round(time.Second))
	}
	return names
}

func (r *Runner) loop(ctx context.Context, w Worker) {
	iv := w.Interval()
	if iv == 0 {
		// Long-running stream worker: keep it alive, restarting on error
		// with a fixed cooldown (its own internals do finer backoff).
		for ctx.Err() == nil {
			r.runOnce(ctx, w)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
		return
	}
	// Periodic worker: stagger the first run, then hold that phase.
	//
	// WHY (measured 2026-07-16): every periodic worker used to call runOnce
	// immediately at t=0 and then start its ticker, so (a) the whole fleet
	// stampeded on boot and (b) tickers created in the same instant stayed
	// PHASE-LOCKED forever — all hourly workers firing together every hour,
	// all 10-minute workers together every 10 minutes. Observed: 10 heavy
	// workers scanning a 5.5GB database concurrently, starving the API's read
	// pool (a 61s /api/honesty) and denying the WAL checkpoint the reader-free
	// moment a TRUNCATE needs. De-phasing the fleet is the root fix.
	//
	// The offset is DETERMINISTIC (FNV of the worker name, not RNG) so runs
	// stay reproducible and the Agents page cadence is legible: a given worker
	// always occupies the same slot in its interval, and distinct names get
	// distinct slots.
	if off := startOffset(w.Name(), iv); off > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(off):
		}
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	r.runOnce(ctx, w)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.runOnce(ctx, w)
		}
	}
}

// maxStartOffset caps the de-phasing delay: long enough to spread the fleet
// across the heaviest scans, short enough that a fresh daemon is fully warm in
// under a minute.
const maxStartOffset = 45 * time.Second

// startOffset returns a worker's deterministic phase offset, bounded by both
// maxStartOffset and the worker's own interval (a 5s worker must not wait 45s).
// Long-running workers (interval 0) never reach here.
func startOffset(name string, interval time.Duration) time.Duration {
	span := maxStartOffset
	if interval < span {
		span = interval
	}
	if span <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return time.Duration(uint64(h.Sum32()) % uint64(span))
}

// runOnce executes one run with persistence + panic isolation.
func (r *Runner) runOnce(ctx context.Context, w Worker) {
	if ctx.Err() != nil {
		return
	}
	runID, err := r.st.StartWorkerRun(ctx, w.Name())
	if err != nil {
		slog.Error("worker: start record", "worker", w.Name(), "err", err)
	}
	r.mu.Lock()
	r.running[w.Name()] = time.Now()
	r.mu.Unlock()
	detail, runErr := r.safeRun(ctx, w)
	r.mu.Lock()
	delete(r.running, w.Name())
	r.mu.Unlock()
	status := "ok"
	if runErr != nil {
		// A cancellation at shutdown is not a failure worth alarming on.
		if ctx.Err() != nil {
			status, detail = "ok", "stopped (shutdown)"
		} else {
			status, detail = "error", runErr.Error()
			slog.Warn("worker failed", "worker", w.Name(), "err", runErr)
		}
	}
	if runID != 0 {
		r.finishRecord(w.Name(), runID, status, clip(detail, 500))
	}
}

// finishRecord persists the run outcome with its OWN context — never the run
// context, which may already be expired after a slow/timed-out run (a worker
// that blew its deadline must still record that failure). The single SQLite
// write conn can queue for a while when many workers finish together (the
// startup herd), so the deadline is generous and one retry absorbs a
// transient "context deadline exceeded"/busy window.
func (r *Runner) finishRecord(worker string, runID int64, status, detail string) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		lastErr = r.st.FinishWorkerRun(fctx, runID, status, detail)
		cancel()
		if lastErr == nil {
			return
		}
	}
	slog.Error("worker: finish record", "worker", worker, "err", lastErr)
}

func (r *Runner) safeRun(ctx context.Context, w Worker) (detail string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
			slog.Error("worker panicked", "worker", w.Name(), "panic", p, "stack", string(debug.Stack()))
		}
	}()
	return w.Run(ctx)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
