package envcfg

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestRejectedIsEmptyByDefault verifies a clean daemon reports no rejections.
// if this fails, a daemon with no config mistakes would report phantom errors.
func TestRejectedIsEmptyByDefault(t *testing.T) {
	Reset()
	if got := Rejected(); len(got) != 0 {
		t.Fatalf("expected empty, got %d entries: %v", len(got), got)
	}
	if HasCritical() {
		t.Fatalf("expected HasCritical false, got true")
	}
}

// TestRejectRecordsAllFields verifies every field is preserved on a normal rejection.
// if this fails, operators would see incomplete or wrong rejection details.
func TestRejectRecordsAllFields(t *testing.T) {
	Reset()
	Reject("SIGNALDECK_ANOM_Z", "abc", "not a number", "3")
	got := Rejected()
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	r := got[0]
	if r.Key != "SIGNALDECK_ANOM_Z" {
		t.Errorf("Key: got %q, want %q", r.Key, "SIGNALDECK_ANOM_Z")
	}
	if r.Raw != "abc" {
		t.Errorf("Raw: got %q, want %q", r.Raw, "abc")
	}
	if r.Reason != "not a number" {
		t.Errorf("Reason: got %q, want %q", r.Reason, "not a number")
	}
	if r.Using != "3" {
		t.Errorf("Using: got %q, want %q", r.Using, "3")
	}
	if r.Critical {
		t.Errorf("Critical: got true, want false")
	}
}

// TestDedupedByKey verifies repeated rejections of the same key collapse to one entry.
// if this fails, a single typo would produce unbounded log entries per sweep.
func TestDedupedByKey(t *testing.T) {
	Reset()
	for i, reason := range []string{"bad1", "bad2", "bad3", "bad4", "bad5"} {
		Reject("SIGNALDECK_DUP", "raw", reason, "default")
		_ = i
	}
	got := Rejected()
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Reason != "bad5" {
		t.Errorf("expected last reason %q, got %q", "bad5", got[0].Reason)
	}
}

// TestSortedByKey verifies Rejected() returns entries sorted ascending by Key.
// if this fails, operators would see an inconsistent ordering of rejections.
func TestSortedByKey(t *testing.T) {
	Reset()
	Reject("SIGNALDECK_ZETA", "x", "r", "d")
	Reject("SIGNALDECK_ALPHA", "x", "r", "d")
	Reject("SIGNALDECK_MID", "x", "r", "d")
	got := Rejected()
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	want := []string{"SIGNALDECK_ALPHA", "SIGNALDECK_MID", "SIGNALDECK_ZETA"}
	for i, w := range want {
		if got[i].Key != w {
			t.Errorf("index %d: got %q, want %q", i, got[i].Key, w)
		}
	}
}

// TestCriticalIsTracked verifies critical rejections set the critical flag and HasCritical.
// if this fails, a data-deletion misconfiguration would not be surfaced as critical.
func TestCriticalIsTracked(t *testing.T) {
	t.Run("critical_alone", func(t *testing.T) {
		Reset()
		RejectCritical("SIGNALDECK_RETENTION_DAYS", "999", "out of range", "30")
		got := Rejected()
		if len(got) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(got))
		}
		if !got[0].Critical {
			t.Errorf("expected Critical true")
		}
		if !HasCritical() {
			t.Errorf("expected HasCritical true")
		}
	})
	t.Run("noncritical_alone", func(t *testing.T) {
		Reset()
		Reject("SIGNALDECK_ANOM_Z", "abc", "not a number", "3")
		if HasCritical() {
			t.Errorf("expected HasCritical false")
		}
	})
	t.Run("mixed", func(t *testing.T) {
		Reset()
		Reject("SIGNALDECK_ANOM_Z", "abc", "not a number", "3")
		RejectCritical("SIGNALDECK_RETENTION_DAYS", "999", "out of range", "30")
		if !HasCritical() {
			t.Errorf("expected HasCritical true with mix")
		}
	})
}

// TestSecretValuesAreRedacted verifies secret-bearing keys never leak the raw value.
// if this fails, a rejected token is being echoed into the logs.
func TestSecretValuesAreRedacted(t *testing.T) {
	secretKeys := []string{
		"SIGNALDECK_API_TOKEN",
		"SIGNALDECK_ALPACA_SECRET",
		"MY_PASSWORD",
		"SOME_APIKEY",
		"X_CREDENTIAL",
		"SIGNALDECK_AUTH_HEADER",
		"SERVICE_KEY",
	}
	const secret = "hunter2-SHOULD-NOT-APPEAR"

	Reset()
	for _, k := range secretKeys {
		Reject(k, secret, "bad value", "default")
	}
	// Non-secret key must keep raw verbatim.
	Reject("SIGNALDECK_ANOM_Z", "abc", "not a number", "3")

	got := Rejected()

	// Build a quick lookup for the non-secret assertion.
	var nonSecret *Rejection
	for i := range got {
		if got[i].Key == "SIGNALDECK_ANOM_Z" {
			nonSecret = &got[i]
		}
	}
	if nonSecret == nil {
		t.Fatalf("non-secret entry missing")
	}
	if nonSecret.Raw != "abc" {
		t.Errorf("non-secret raw redacted: got %q, want %q", nonSecret.Raw, "abc")
	}

	for _, r := range got {
		if r.Key == "SIGNALDECK_ANOM_Z" {
			continue
		}
		if r.Raw != "(redacted)" {
			t.Errorf("key %q: Raw got %q, want %q", r.Key, r.Raw, "(redacted)")
		}
		if strings.Contains(r.Key, secret) ||
			strings.Contains(r.Raw, secret) ||
			strings.Contains(r.Reason, secret) ||
			strings.Contains(r.Using, secret) {
			t.Errorf("key %q: secret text leaked into a field", r.Key)
		}
	}
}

// TestConcurrentRejectIsRaceFree verifies concurrent Reject calls don't panic or corrupt state.
// if this fails, concurrent daemon sweeps would race on the rejection registry.
func TestConcurrentRejectIsRaceFree(t *testing.T) {
	Reset()

	const workers = 50
	// Each worker rejects one unique key plus a shared key; distinct keys = workers + 1.
	const sharedKey = "SIGNALDECK_SHARED"
	const distinctCount = workers + 1

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			Reject(sharedKey, "bad", "shared reason", "default")
			// Per-goroutine key: the point is that N distinct keys survive
			// concurrent writes while the SHARED one collapses to a single
			// entry. Hardcoding one name here would assert nothing about
			// either.
			Reject(fmt.Sprintf("SIGNALDECK_WORKER_%02d", n), "bad", "worker reason", "default")
		}(i)
	}
	wg.Wait()

	got := Rejected()
	if len(got) != distinctCount {
		t.Fatalf("expected %d distinct keys, got %d: %v", distinctCount, len(got), got)
	}
}
