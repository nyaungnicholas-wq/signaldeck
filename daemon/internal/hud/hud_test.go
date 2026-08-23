package hud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

func TestDownBackoffEscalatesAndCaps(t *testing.T) {
	if got := downBackoff(0); got != 0 {
		t.Errorf("downBackoff(0) = %v, want 0 — a healthy HUD must not be skipped", got)
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute,
		8 * time.Minute, 16 * time.Minute, 30 * time.Minute}
	for i, w := range want {
		if got := downBackoff(i + 1); got != w {
			t.Errorf("downBackoff(%d) = %v, want %v", i+1, got, w)
		}
	}
	// The cap holds: a HUD that comes back is still noticed within half an hour.
	for _, misses := range []int{7, 20, 1000} {
		if got := downBackoff(misses); got != 30*time.Minute {
			t.Errorf("downBackoff(%d) = %v, want the 30m cap", misses, got)
		}
	}
}

// deadURL returns a URL nothing is listening on.
func deadURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	u := srv.URL
	srv.Close()
	return u
}

// An unreachable HUD is DEGRADED, not an error: it means an optional external
// process is off, which is the normal state on a machine running only
// SignalDeck. Filing it as an error kept the fleet permanently red — 258 error
// rows a day from one dependency that was simply not started.
func TestUnreachableHudIsDegradedThenBacksOff(t *testing.T) {
	s := New(nil, deadURL(t))
	ctx := context.Background()

	_, err := s.Run(ctx)
	if err == nil {
		t.Fatal("first unreachable poll returned no error; the outage must be recorded once")
	}
	if !errors.Is(err, workers.ErrDegraded) {
		t.Fatalf("error %v does not wrap ErrDegraded, so it lands as a hard failure", err)
	}
	if s.misses != 1 {
		t.Fatalf("misses = %d after one failure, want 1", s.misses)
	}

	// Immediately inside the backoff window: no re-dial and no log line -- but
	// still DEGRADED, not ok.
	//
	// This asserted err == nil, i.e. a skip filed as status="ok". lastSuccess is
	// `WHERE status='ok'`, so every skipped poll refreshed it and staleness could
	// never fire: a HUD dead for a month read green in both surfaces, with the
	// truth only in a detail string nothing aggregates. "Quiet" has to mean no
	// re-dial and no log spam -- both still asserted below -- and cannot mean
	// recording a run that delivered nothing as a success.
	before := s.misses
	detail, err := s.Run(ctx)
	if !errors.Is(err, workers.ErrDegraded) {
		t.Fatalf("poll inside the backoff window returned %v; a skip must still be degraded", err)
	}
	if s.misses != before {
		t.Fatalf("misses moved from %d to %d during a skipped poll — it re-dialled", before, s.misses)
	}
	if !strings.Contains(err.Error(), "down") {
		t.Errorf("skip status %q does not say the HUD is down (detail %q)", err, detail)
	}
}

// Recovery clears the counter, so a later outage is reported afresh instead of
// being swallowed by a stale backoff.
func TestRecoveryResetsBackoff(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":1}`))
	}))
	defer srv.Close()

	s := New(st, srv.URL)
	s.misses, s.retryAt = 4, time.Now().Add(-time.Hour) // was down, window has passed

	detail, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("reachable HUD returned %v", err)
	}
	if s.misses != 0 {
		t.Fatalf("misses = %d after a successful sync, want 0", s.misses)
	}
	if !strings.Contains(detail, "back after 4 misses") {
		t.Errorf("detail %q does not report the recovery", detail)
	}
}
