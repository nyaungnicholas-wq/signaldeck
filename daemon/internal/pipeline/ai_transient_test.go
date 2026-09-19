package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// fakeAnalystLLM is enabled but always fails Complete with a chosen error, so a
// test can pick which failure class the worker has to classify.
type fakeAnalystLLM struct{ err error }

func (f *fakeAnalystLLM) Enabled() bool    { return true }
func (f *fakeAnalystLLM) Model() string    { return "fake" }
func (f *fakeAnalystLLM) Stats() llm.Stats { return llm.Stats{} }
func (f *fakeAnalystLLM) Complete(ctx context.Context, sys string, msgs []llm.Message, maxTokens int) (string, error) {
	return "", f.err
}

func newAnalystTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ai.db"))
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// An exhausted shared provider pool must NOT fail /api/ready. Readiness answers
// "can this daemon serve CORRECT answers", and "no brief this hour" is a correct
// answer, not a wrong one. Filed as a hard error this was the only reason
// /api/ready answered 503 fleet-wide on 2026-09-10.
func TestAnalystWorker_TransientProviderFailureIsDegradedNotError(t *testing.T) {
	st := newAnalystTestStore(t)
	w := &AnalystWorker{St: st, LLM: &fakeAnalystLLM{err: llm.ErrTransient}}

	detail, err := w.Run(context.Background())
	if err == nil || !errors.Is(err, workers.ErrDegraded) {
		t.Fatalf("transient provider failure: err = %v, want it to wrap workers.ErrDegraded", err)
	}
	if detail == "" {
		t.Errorf("a degraded run must still say what happened; detail was empty")
	}
}

// The guard that stops the degraded branch from swallowing real bugs: without
// it every permanent fault would read as "provider busy, resuming next pass"
// and the worker would look healthy forever.
func TestAnalystWorker_RealFailureStillErrors(t *testing.T) {
	st := newAnalystTestStore(t)
	boom := errors.New("provider returned malformed json")
	w := &AnalystWorker{St: st, LLM: &fakeAnalystLLM{err: boom}}

	_, err := w.Run(context.Background())
	if err == nil {
		t.Fatalf("genuine provider fault: err = nil, want an error")
	}
	if errors.Is(err, workers.ErrDegraded) {
		t.Fatalf("genuine provider fault must still fail the readiness probe; got degraded: %v", err)
	}
}
