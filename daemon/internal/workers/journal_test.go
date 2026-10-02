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

func (f *flakyWriter) FinishWorkerRunAt(_ context.Context, id int64, status, _ string, _ int64) error {
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

// slowWriter implements storeWriter with an artificial delay in FinishWorkerRunAt.
// It records the finishedAt value it was handed, allowing tests to verify
// that the timestamp reflects when the run *ended*, not when the journal
// drain goroutine processed the update.
// This reproduces a scenario where a backed-up journal queue could cause
// finished_at timestamps to be significantly inflated, leading to misinterpretations
// of worker run durations.
type slowWriter struct {
	mu       sync.Mutex
	nextID   int64
	finished map[int64]int64 // runID -> finishedAt
}

func (s *slowWriter) StartWorkerRun(_ context.Context, _ string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	if s.finished == nil {
		s.finished = make(map[int64]int64)
	}
	return s.nextID, nil
}

func (s *slowWriter) FinishWorkerRunAt(_ context.Context, id int64, _, _ string, finishedAt int64) error {
	time.Sleep(150 * time.Millisecond) // Simulate network/database latency or queue backlog
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished[id] = finishedAt
	return nil
}

func (s *slowWriter) got(id int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished[id]
}

// TestJournalStampsFinishedAtWhenTheRunEnded ensures that the finished_at timestamp
// recorded by the journal reflects the exact time the worker run ended,
// not the potentially much later time when the journal's drain goroutine
// processes the update. Historically, finished_at was stamped inside the
// store update, which, due to a busy journal queue, could be minutes behind.
// This inflation, observed on 2026-09-20 (p50 302s, max 2611s), caused
// 30m0s prediction-runner timeouts to appear as 59-73 minute runs, leading
// to misread capacity trends and false machine sleep investigations.
// The slowWriter here reproduces the backlog, ensuring this test fails if
// the timestamp is ever taken at drain time again.
func TestJournalStampsFinishedAtWhenTheRunEnded(t *testing.T) {
	w := &slowWriter{}
	j := newRunJournal(w)
	go j.drain()

	id := j.open(context.Background(), "prediction-runner")
	if id == 0 {
		t.Fatal("expected a non-zero run ID")
	}

	ended := time.Now().Unix() // The instant the run "returned"

	j.submit(journalOp{
		worker:     "prediction-runner",
		runID:      id,
		status:     "ok",
		detail:     "wrote 42 predictions",
		finishedAt: ended,
	})

	j.close(10 * time.Second) // Give drain goroutine time to process

	gotFinishedAt := w.got(id)

	if gotFinishedAt != ended {
		diff := gotFinishedAt - ended
		t.Fatalf("finished_at was stamped at drain time rather than when the run ended; "+
			"this makes every recorded duration the run plus the journal backlog. "+
			"got: %d, want: %d, diff: %d", gotFinishedAt, ended, diff)
	}
}
