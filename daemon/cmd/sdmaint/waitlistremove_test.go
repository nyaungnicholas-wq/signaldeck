package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func seed(t *testing.T, path string, emails ...string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("seed: open store: %v", err)
	}
	now := time.Now()
	for _, email := range emails {
		if _, err := st.AddWaitlist(context.Background(), email, "web", now); err != nil {
			_ = st.Close()
			t.Fatalf("seed: add waitlist %q: %v", email, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("seed: close store: %v", err)
	}
}

func has(t *testing.T, path, email string) bool {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("has: open store: %v", err)
	}
	defer st.Close() //nolint:errcheck
	var count int
	if err := st.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM waitlist WHERE email = ?", email).Scan(&count); err != nil {
		t.Fatalf("has: query: %v", err)
	}
	return count == 1
}

func TestWaitlistRemoveDryRunDeletesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	seed(t, path, "a@example.com", "b@example.com")

	var buf bytes.Buffer
	err := waitlistRemove([]string{"-db", path, "-email", "  A@Example.COM "}, &buf)
	if err != nil {
		t.Fatalf("waitlistRemove: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "would remove 1 address") {
		t.Errorf("expected 'would remove 1 address' in output, got: %q", out)
	}
	if strings.Contains(out, "a@example.com") {
		t.Errorf("address should not be echoed in output, got: %q", out)
	}
	if !has(t, path, "a@example.com") {
		t.Error("a@example.com should still exist after dry run")
	}
	if !has(t, path, "b@example.com") {
		t.Error("b@example.com should still exist after dry run")
	}
}

func TestWaitlistRemoveApplyRemovesOnlyNamedAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	seed(t, path, "a@example.com", "b@example.com")

	var buf bytes.Buffer
	err := waitlistRemove([]string{"-db", path, "-email", "  A@Example.COM ", "-apply"}, &buf)
	if err != nil {
		t.Fatalf("waitlistRemove: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "removed 1 address") {
		t.Errorf("expected 'removed 1 address' in output, got: %q", out)
	}
	if has(t, path, "a@example.com") {
		t.Error("a@example.com should be removed after apply")
	}
	if !has(t, path, "b@example.com") {
		t.Error("b@example.com should still exist after apply")
	}

	buf.Reset()
	err = waitlistRemove([]string{"-db", path, "-email", "  A@Example.COM ", "-apply"}, &buf)
	if err != nil {
		t.Fatalf("second waitlistRemove: %v", err)
	}
	out = buf.String()
	if !strings.Contains(out, "not on the waitlist") {
		t.Errorf("expected 'not on the waitlist' in output, got: %q", out)
	}
}

func TestWaitlistRemoveRejectsInvalidAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	seed(t, path, "a@example.com")

	badAddresses := []string{"", "not-an-address", "a@example.com\r\nBcc: x@y.z"}
	for _, bad := range badAddresses {
		t.Run(bad, func(t *testing.T) {
			var buf bytes.Buffer
			err := waitlistRemove([]string{"-db", path, "-email", bad, "-apply"}, &buf)
			if err == nil {
				t.Errorf("expected error for invalid address %q, got nil; output: %q", bad, buf.String())
			}
			if !has(t, path, "a@example.com") {
				t.Errorf("a@example.com should still exist after invalid address %q", bad)
			}
		})
	}
}

// A mistyped -db must fail, not create an empty database and confirm the
// address is gone.
func TestWaitlistRemoveRefusesMissingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typo.db")
	var buf bytes.Buffer
	err := waitlistRemove([]string{"-db", path, "-email", "a@example.com", "-apply"}, &buf)
	if err == nil {
		t.Errorf("expected an error for a missing -db, got nil; output: %q", buf.String())
	}
	if strings.Contains(buf.String(), "not on the waitlist") {
		t.Errorf("missing -db reported a false confirmation: %q", buf.String())
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("missing -db %s was created", path)
	}
}
