//go:build windows

package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// unwritableSink returns a path that opens for append but refuses the write:
// another handle holds an exclusive byte-range lock over the whole file, so
// WriteFile fails with ERROR_LOCK_VIOLATION after a successful open.
func unwritableSink(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp_audit.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, ^uint32(0), ^uint32(0), new(windows.Overlapped)); err != nil {
		t.Fatal(err)
	}
	return path
}
