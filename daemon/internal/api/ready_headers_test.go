package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The 503 branch of /api/ready called w.WriteHeader(503) and THEN writeJSON.
// Go ignores header mutations once WriteHeader has run, so writeJSON's
// Content-Type and Cache-Control were both silently dropped: the response went
// out as text/plain (content sniffing) with no Cache-Control, carrying a JSON
// body.
//
// /api/ready is anonymous (security.go alwaysOpen) and is what a deploy script
// and a load balancer poll. A client that parses by content type rejects a
// readiness answer it could have read, and a 503 with no Cache-Control is a
// readiness answer a proxy may hand out after the daemon has recovered.
//
// The 200 paths always used writeJSON and were never affected, which is why
// nothing noticed -- the bug only appears once the daemon is NOT ready.
func TestReady_ServesJSONHeadersOnTheFailurePath(t *testing.T) {
	ctx := context.Background()
	d, st := readyDeps(t)

	// Force the 503 branch the same way the existing failure test does.
	id, err := st.StartWorkerRun(ctx, "outcome-resolver")
	if err != nil {
		t.Fatalf("start worker run: %v", err)
	}
	if err := st.FinishWorkerRun(ctx, id, "error", "boom"); err != nil {
		t.Fatalf("finish worker run: %v", err)
	}

	// Anonymous and authenticated take different writes inside the same branch,
	// so both are checked.
	for _, tc := range []struct {
		name string
		auth bool
	}{
		{"anonymous", false},
		{"authenticated", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/ready", nil)
			if tc.auth {
				r = withUser(r, 1)
			}
			rec := httptest.NewRecorder()
			d.ready(rec, r)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("want 503 (the branch under test), got %d: %s", rec.Code, rec.Body.String())
			}
			// rec.Result(), NOT rec.Header(). ResponseRecorder.Header() returns the
			// LIVE map, so a header written after WriteHeader still appears there and
			// the bug is invisible -- this test passed against the broken code until
			// that was noticed. Result() returns the snapshot taken at WriteHeader,
			// which is what a real client receives.
			res := rec.Result()
			defer res.Body.Close() //nolint:errcheck
			if got := res.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q, want application/json; charset=utf-8 -- the body IS "+
					"JSON (%s), so a client parsing by content type cannot read a readiness answer "+
					"it could have understood", got, rec.Body.String())
			}
			if got := res.Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store -- a cacheable 503 is a readiness "+
					"answer a proxy can keep serving after the daemon has recovered", got)
			}
		})
	}
}
