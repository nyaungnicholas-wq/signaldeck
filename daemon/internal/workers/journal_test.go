package workers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// flakyWriter fails the first n writes of each kind, then succeeds — standing
// in for the single SQLite write connection under fleet-wide contention.
type flakyWriter struct {
	mu        sync.Mutex
	failStart int
	failFin   int
	nextID    int64
	finished  map[int64]string
}

func (f *flakyWriter) StartWorkerRun(_ context.Context, _ string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStart > 0 {
		f.failStart--
		return 0, errors.New("context deadline exceeded")
	}
	f.nextID++
	return f.nextID, nil
}

func (f *flakyWriter) FinishWorkerRun(_ context.Context, id int64, status, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFin > 0 {
		f.failFin--
		return errors.New("database is locked")
	}
	if f.finished == nil {
		f.finished = map[int64]string{}
	}
	f.finished[id] = status
	return nil
}

// TestJournalRetriesUntilWriteLands is the regression for the lost-completion
// bug: the old inline path gave up after one retry, so a busy write connection
// left runs stuck at 'running' forever and under-counted the looks the
// Bonferroni divisor is derived from. The journal must keep trying.
func TestJournalRetriesUntilWriteLands(t *testing.T) {
	old := journalRetryMinFor(t)
	defer old()

	f := &flakyWriter{failStart: 3, failFin: 4}
	j := newRunJournal(f)
	go j.drain()

	id := j.open(context.Background(), "research-loop")
	if id == 0 {
		t.Fatal("open returned 0 despite the writer eventually succeeding")
	}
	j.submit(journalOp{worker: "research-loop", runID: id, status: "ok", detail: "searched a 40-rule grid"})
	j.close(10 * time.Second)

	f.mu.Lock()
	defer f.mu.Unlock()
	if got := f.finished[id]; got != "ok" {
		t.Fatalf("run %d finished as %q, want %q — the completion was lost", id, got, "ok")
	}
}

// journalRetryMinFor shrinks the backoff for the duration of a test.
func journalRetryMinFor(t *testing.T) func() {
	t.Helper()
	prevMin, prevMax := journalRetryMin, journalRetryMax
	journalRetryMin, journalRetryMax = time.Millisecond, 5*time.Millisecond
	return func() { journalRetryMin, journalRetryMax = prevMin, prevMax }
}
