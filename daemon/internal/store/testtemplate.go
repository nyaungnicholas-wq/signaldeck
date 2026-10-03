package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// seedFromTemplate: under `go test`, a path that does not exist yet starts as
// a byte copy of a schema-complete database built once per test binary.
// Building the schema from scratch costs ~1.5-2s under -race (modernc's pure-Go
// SQLite is race-instrumented line by line) against ~0.35s to reopen a built
// one, and nearly every test opens its own store -- that was most of
// internal/api's ~28 CI minutes. Open then runs exactly the reopen path
// production takes on every restart (schema IF NOT EXISTS, idempotent migrate,
// verifySchema), so a template that drifted from the schema still fails loudly.
// Existing files and relative paths are never touched, and any failure here
// falls back to a fresh build.
func seedFromTemplate(path string) {
	if !testing.Testing() {
		return
	}
	if !filepath.IsAbs(path) {
		return
	}
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return
	}
	tmplOnce.Do(buildTemplate)
	if tmplErr != nil || len(tmplDB) == 0 {
		return
	}
	_ = writeNew(path, tmplDB)
}

var (
	tmplOnce sync.Once
	tmplDB   []byte
	tmplErr  error
)

func buildTemplate() {
	dir, err := os.MkdirTemp("", "signaldeck-store-template-")
	if err != nil {
		tmplErr = err
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	p := filepath.Join(dir, "template.db")
	st, err := open(p) // open, not Open: Open would recurse into this
	if err != nil {
		tmplErr = err
		return
	}
	if err := st.Close(); err != nil {
		tmplErr = err
		return
	}
	tmplDB, tmplErr = os.ReadFile(p)
	if tmplErr != nil {
		return
	}
	// Close checkpoints with TRUNCATE, so the WAL is gone or empty. If pages were
	// left in it, the main file alone is not the database: don't seed.
	if fi, err := os.Stat(p + "-wal"); err == nil && fi.Size() > 0 {
		tmplDB, tmplErr = nil, errors.New("store template: WAL was not checkpointed")
	}
}

// ponytail: create-then-write, so a second Open racing on the SAME new path
// could read a half-written file. No test does that; write a temp file and
// os.Link it into place if one ever must.
func writeNew(name string, b []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(name)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	return nil
}
