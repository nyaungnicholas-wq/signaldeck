package logrotate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReopensAfterRotationLeftNoFile(t *testing.T) {
	// Silent-log-death guard: a rotation whose open() failed used to leave
	// w.f == nil forever, so Write returned os.ErrClosed for the rest of the
	// process lifetime while slog silently discarded the error. Write must now
	// reopen and actually persist the bytes to disk.
	p := filepath.Join(t.TempDir(), "app.log")
	w, err := New(p, 1, 2)
	if err != nil {
		t.Fatalf("New: got %v, want nil", err)
	}
	defer w.Close() //nolint:errcheck // teardown; the assertions below own the failures

	first := []byte("first line\n")
	if n, err := w.Write(first); err != nil || n != len(first) {
		t.Fatalf("initial Write: got (%d, %v), want (%d, nil)", n, err, len(first))
	}

	// Reproduce the exact post-failed-rotation state: file closed, handle nil,
	// size reset, closed flag still false.
	if err := w.f.Close(); err != nil {
		t.Fatalf("precondition close: got %v, want nil", err)
	}
	w.f = nil
	w.size = 0

	second := []byte("second line after rotation\n")
	n, err := w.Write(second)
	if err != nil {
		t.Fatalf("Write after nil handle: got err %v, want nil", err)
	}
	if n != len(second) {
		t.Fatalf("Write after nil handle: got n %d, want %d", n, len(second))
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: got %v, want nil", err)
	}
	if !strings.Contains(string(got), string(second)) {
		t.Fatalf("file missing second payload: got %q, want substring %q", got, second)
	}
}

func TestWriteStaysClosedAfterClose(t *testing.T) {
	// The other half of the fix: the recoverable reopen path must not resurrect
	// a writer the caller deliberately shut down, or Close stops meaning
	// anything and a half-dead writer keeps writing to a file nobody owns.
	p := filepath.Join(t.TempDir(), "app.log")
	w, err := New(p, 1, 2)
	if err != nil {
		t.Fatalf("New: got %v, want nil", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: got %v, want nil", err)
	}

	payload := []byte("must not land\n")
	n, err := w.Write(payload)
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write after Close: got (%d, %v), want (_, %v)", n, err, os.ErrClosed)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("second Close: got %v, want nil", err)
	}
}

func TestRotateSurvivesACloseFailure(t *testing.T) {
	// Silent-log-death guard: when rotate's internal Close fails (e.g. Windows
	// antivirus/indexer file lock), rotate must surface the failure and carry
	// on to a fresh file rather than returning early and leaving w.f pointing
	// at a dead handle that every later Write retries forever.
	p := filepath.Join(t.TempDir(), "app.log")
	w, err := New(p, 1, 2)
	if err != nil {
		t.Fatalf("New: got %v, want nil", err)
	}
	defer w.Close() //nolint:errcheck // teardown; the assertions below own the failures

	// Close the handle out from under rotate(): w.f is a closed *os.File, so
	// rotate's own w.f.Close() errors, but w.f stays non-nil until rotate
	// overwrites it.
	if err := w.f.Close(); err != nil {
		t.Fatalf("precondition close: got %v, want nil", err)
	}
	w.size = w.maxBytes

	payload := []byte("survives a flaky close\n")
	n, err := w.Write(payload)
	if err != nil {
		t.Fatalf("Write through rotate: got (%d, %v), want (%d, nil)", n, err, len(payload))
	}
	if n != len(payload) {
		t.Fatalf("Write through rotate: got n %d, want %d", n, len(payload))
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: got %v, want nil", err)
	}
	if !strings.Contains(string(got), string(payload)) {
		t.Fatalf("file missing payload: got %q, want substring %q", got, payload)
	}
}