// Package logrotate is a tiny, stdlib-only size-capped log writer. It wraps
// a single file and rotates it in place when it exceeds a byte cap, keeping a
// fixed number of older generations (file.log.1 … file.log.K). Safe for
// concurrent use — every Write is serialized behind one mutex, which is fine
// for log volume.
package logrotate

import (
	"fmt"
	"os"
	"sync"
)

// Defaults used when New is given non-positive values.
const (
	DefaultMaxMB = 20
	DefaultKeep  = 3
)

// Writer is a size-capped, rotating file writer.
type Writer struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
}

// New opens (creating/appending) the log file at path, rotating at maxMB
// megabytes and keeping `keep` rotated generations. Non-positive values fall
// back to the defaults (20 MB, 3 files).
func New(path string, maxMB, keep int) (*Writer, error) {
	if maxMB <= 0 {
		maxMB = DefaultMaxMB
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	w := &Writer{path: path, maxBytes: int64(maxMB) * 1024 * 1024, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

// Write implements io.Writer. If the write would push the file past the cap
// (and the file is non-empty), the file is rotated first, so a single line
// never straddles two generations.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			// Rotation failed (e.g. permissions): keep logging to the current
			// file rather than dropping output.
			fmt.Fprintf(os.Stderr, "logrotate: rotate %s: %v\n", w.path, err)
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate shifts path.(k-1)→path.k … path→path.1 and reopens a fresh file.
// Caller holds the mutex.
func (w *Writer) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	w.f = nil
	// Drop the oldest, shift the rest up.
	os.Remove(fmt.Sprintf("%s.%d", w.path, w.keep)) //nolint:errcheck
	for i := w.keep - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", w.path, i)
		if _, err := os.Stat(from); err == nil {
			os.Rename(from, fmt.Sprintf("%s.%d", w.path, i+1)) //nolint:errcheck
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		// Fall through to reopen anyway — worst case we keep appending.
		fmt.Fprintf(os.Stderr, "logrotate: rename %s: %v\n", w.path, err)
	}
	return w.open()
}

// Close closes the underlying file. Further Writes return os.ErrClosed.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
