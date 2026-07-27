package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
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

// ── C6 residual (2026-07-26 hostile review): the two routes the first C6 pass
// missed ────────────────────────────────────────────────────────────────────
//
// /api/composite/top and /api/movers were whitelisted; /api/calibration and
// /api/honesty were not. calibration keyed on r.URL.RawQuery (predict.go:110)
// and honesty keyed on the raw VALUE of ?horizon (honestycache.go:95) — the
// parameter NAME was known, the value was not. Both handlers resolve anything
// outside their horizon set to 1d, so `?horizon=aaa1`, `?horizon=aaa2`, ...
// each rendered the SAME body under a DIFFERENT key: an unbounded supply of
// cold builds, each holding one of the store's four read connections.
//
// These two tests exercise the REGISTERED ROUTE, not the key helper, because
// the defect was in the wiring: a correct key function that the route does not
// call fixes nothing.

// swrBodyEntryCount reads the entry map of a shared body cache. The shared
// caches are process-wide, so these tests assert on the DELTA they cause
// rather than an absolute count.
func swrBodyEntryCount(c *swrBodyCache) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.ent)
}

func newCacheRouteDeps(t *testing.T) Deps {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "cacheroute.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return Deps{St: st}
}

func TestHonestyRoute_JunkHorizonsCannotMintCacheEntries(t *testing.T) {
	d := newCacheRouteDeps(t)
	mux := http.NewServeMux()
	d.registerHonestyCached(mux)

	before := swrBodyEntryCount(sharedHonestySWR)
	// Five junk values is the exact shape of the measured attack: five cold
	// builds took all four read connections and the daemon stopped answering.
	for _, h := range []string{"aaa1", "aaa2", "aaa3", "aaa4", "aaa5"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/honesty?horizon="+h, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("horizon=%s: status %d body %s", h, rec.Code, rec.Body.String())
		}
	}
	// All five resolve to 1d (api.go's honesty handler), which is the body the
	// warmer already built under "": at most ONE new entry may appear, and only
	// if the warmer had not run.
	if got := swrBodyEntryCount(sharedHonestySWR) - before; got > 1 {
		t.Fatalf("five junk horizons minted %d cache entries; every one is a cold build on a read connection", got)
	}
}

func TestCalibrationRoute_JunkQueryCannotMintCacheEntries(t *testing.T) {
	d := newCacheRouteDeps(t)
	mux := http.NewServeMux()
	d.registerPredict(mux)

	before := swrBodyEntryCount(sharedCalibrationSWR)
	for _, q := range []string{
		"?horizon=aaa1",
		"?horizon=aaa2",
		"?zz=1", // an unknown parameter name: the RawQuery key made this a miss too
		"?zz=2",
		"?a=1&b=2&c=3",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/calibration"+q, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s", q, rec.Code, rec.Body.String())
		}
	}
	if got := swrBodyEntryCount(sharedCalibrationSWR) - before; got > 1 {
		t.Fatalf("five junk queries minted %d cache entries; the handler renders one identical 1d body for all of them", got)
	}
}

// The unbounded map the review called out separately: respCache never deleted
// an entry and builds under a GLOBAL lock, so a caller that could reach it with
// distinct keys grew it without limit. There is no production instance today
// (only tests construct a respCache), which makes this a latent defect
// rather than a live one — bound it now so wiring it later cannot reintroduce
// the leak.
func TestRespCacheIsBounded(t *testing.T) {
	c := newRespCache(time.Minute)
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}
	for i := 0; i < maxCacheEntries*3; i++ {
		key := "k" + strconv.Itoa(i)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/honesty?horizon="+key, nil)
		c.serve(key, rec, req, handler)
	}
	c.mu.Lock()
	n := len(c.ent)
	c.mu.Unlock()
	if n > maxCacheEntries {
		t.Fatalf("respCache holds %d entries, cap is %d — the map is unbounded", n, maxCacheEntries)
	}
}

// The safety half of collapsing ?horizon=1d onto the warmer's "" entry. The
// header of cachekey.go states the rule: a key may only merge requests the
// handler renders IDENTICALLY, because a key that merges two different bodies
// serves one caller's answer to another — worse than the DoS being fixed.
// Rendering both spellings and comparing bytes is the only way to know that,
// and it is what fails if someone later makes either handler read the raw
// ?horizon string for anything other than the horizon itself.
func TestDefaultAnd1dRenderTheSameBody(t *testing.T) {
	d := newCacheRouteDeps(t)
	render := func(h http.HandlerFunc, target string) string {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s", target, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	for _, c := range []struct {
		name string
		h    http.HandlerFunc
		path string
	}{
		{"honesty", d.honesty, "/api/honesty"},
		{"calibration", d.calibration, "/api/calibration"},
	} {
		bare := render(c.h, c.path)
		explicit := render(c.h, c.path+"?horizon=1d")
		junk := render(c.h, c.path+"?horizon=aaa1")
		if bare != explicit {
			t.Fatalf("%s: no-query body != ?horizon=1d body; they share cache entry %q", c.name, "")
		}
		if bare != junk {
			t.Fatalf("%s: no-query body != ?horizon=aaa1 body; they share cache entry %q", c.name, "")
		}
	}
}
