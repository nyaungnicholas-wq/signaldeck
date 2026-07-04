package workers

import (
	"context"
	"errors"
	"path/filepath"
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
