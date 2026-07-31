//go:build !windows

package ledgeranchor

import (
	"os"
	"path/filepath"
	"testing"
)

func assertKeyIsOwnerOnly(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("key perms = %04o, want 0600 — a readable signing key proves nothing", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("key dir perms = %04o, want owner-only", perm)
	}
}

func widenKeyPermissions(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
