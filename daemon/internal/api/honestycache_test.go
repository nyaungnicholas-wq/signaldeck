package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The response cache must: reuse a fresh body, coalesce identical concurrent
// callers into ONE build, key by horizon, rebuild after the TTL, and NEVER
// cache an error.
func TestRespCache(t *testing.T) {
	builds := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		builds++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"horizon":"` + r.URL.Query().Get("horizon") + `"}`))
	}
	c := newRespCache(time.Minute)
	call := func(horizon string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/honesty?horizon="+horizon, nil)
		c.serve(horizon, rec, req, handler)
		return rec
	}

	// First call builds; second is served from cache.
	if got := call("1d").Body.String(); got != `{"horizon":"1d"}` {
		t.Fatalf("body = %q", got)
	}
	rec := call("1d")
	if builds != 1 {
		t.Errorf("builds = %d, want 1 (second call must hit the cache)", builds)
	}
	if rec.Header().Get("X-Cache") != "hit" {
		t.Error("cached response not marked X-Cache: hit")
	}
	if got := rec.Body.String(); got != `{"horizon":"1d"}` {
		t.Errorf("cached body = %q", got)
	}

	// A different horizon is a different key — it must build.
	call("1w")
	if builds != 2 {
		t.Errorf("builds = %d, want 2 (horizon is the cache key)", builds)
	}

	// Past the TTL, it rebuilds.
	expired := newRespCache(time.Nanosecond)
	builds = 0
	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/honesty?horizon=1d", nil)
	expired.serve("1d", rec2, req, handler)
	time.Sleep(2 * time.Millisecond)
	expired.serve("1d", httptest.NewRecorder(), req, handler)
	if builds != 2 {
		t.Errorf("builds = %d, want 2 (expired entry must rebuild)", builds)
	}
}

// An error response must never be pinned for the TTL — a transient store
// failure would otherwise be served as the answer for a full minute.
func TestRespCacheDoesNotCacheErrors(t *testing.T) {
	builds := 0
	failing := func(w http.ResponseWriter, r *http.Request) {
		builds++
		httpErr(w, 500, "store exploded")
	}
	c := newRespCache(time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/honesty?horizon=1d", nil)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		c.serve("1d", rec, req, failing)
		if rec.Code != 500 {
			t.Fatalf("call %d: status %d, want 500", i, rec.Code)
		}
	}
	if builds != 3 {
		t.Errorf("builds = %d, want 3 — an error was cached", builds)
	}
}
