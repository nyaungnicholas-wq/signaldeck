package logrotate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// newTest builds a writer with a tiny byte cap (bypassing the MB-granular
// constructor) so tests don't need to write megabytes.
func newTest(t *testing.T, path string, maxBytes int64, keep int) *Writer {
	t.Helper()
	w, err := New(path, 1, keep)
	if err != nil {
		t.Fatal(err)
	}
	w.maxBytes = maxBytes
	return w
}

func TestRotatesAtCapAndKeepsK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w := newTest(t, path, 100, 2)
	defer w.Close() //nolint:errcheck

	line := bytes.Repeat([]byte("x"), 40) // 3 lines > 100 bytes
	for i := 0; i < 12; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	// Current file must exist and be under the cap.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if st.Size() > 100 {
		t.Errorf("current log %d bytes, want <= 100", st.Size())
	}
	// Exactly keep=2 rotated generations, no .3.
	for _, want := range []string{path + ".1", path + ".2"} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected rotated file %s: %v", want, err)
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Errorf("%s.3 exists — keep=2 not enforced", path)
	}
}

func TestNoRotationUnderCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w := newTest(t, path, 1000, 3)
	defer w.Close() //nolint:errcheck
	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("rotated under cap")
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w := newTest(t, path, 500, 3)
	defer w.Close() //nolint:errcheck
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				fmt.Fprintf(w, "goroutine %d line %d\n", g, i)
			}
		}(g)
	}
	wg.Wait()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current log missing after concurrent writes: %v", err)
	}
}

func TestWriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	w := newTest(t, filepath.Join(dir, "a.log"), 100, 1)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Error("expected error writing after Close")
	}
}
