// Package workers is SignalDeck's in-app agent framework. Each Worker is an
// autonomous unit with a name, a cadence, and a Run method; the Runner
// schedules them, persists every run (status + detail) to worker_runs, and
// recovers from panics — the Agents page in the UI renders exactly this
// table, so what the user sees IS what ran.
package workers

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
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

// Runner schedules workers and records their runs.
type Runner struct {
	st      *store.Store
	workers []Worker
}

// NewRunner builds a runner over the given workers.
func NewRunner(st *store.Store, ws ...Worker) *Runner {
	return &Runner{st: st, workers: ws}
}

// Start launches every worker and blocks until ctx is done and all exit.
func (r *Runner) Start(ctx context.Context) {
	var wg sync.WaitGroup
	for _, w := range r.workers {
		wg.Add(1)
		go func(w Worker) {
			defer wg.Done()
			r.loop(ctx, w)
		}(w)
	}
	wg.Wait()
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
	// Periodic worker: run immediately, then on cadence.
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

// runOnce executes one run with persistence + panic isolation.
func (r *Runner) runOnce(ctx context.Context, w Worker) {
	if ctx.Err() != nil {
		return
	}
	runID, err := r.st.StartWorkerRun(ctx, w.Name())
	if err != nil {
		slog.Error("worker: start record", "worker", w.Name(), "err", err)
	}
	detail, runErr := r.safeRun(ctx, w)
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
		// Persist with a fresh context: the run context may already be dead.
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.st.FinishWorkerRun(fctx, runID, status, clip(detail, 500)); err != nil {
			slog.Error("worker: finish record", "worker", w.Name(), "err", err)
		}
	}
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
