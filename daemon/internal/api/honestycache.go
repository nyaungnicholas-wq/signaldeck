package api

import (
	"net/http"
	"sync"
	"time"
)

// Response cache for the two heaviest INTERACTIVE read paths.
//
// /api/honesty and /api/composite/top each scan tens of thousands of resolved
// outcomes and rebuild the same answer for every caller. They are also the two
// endpoints measured worst under a worker read-storm (61s /api/honesty, 22s
// /api/composite/top on 2026-07-16). The reader-pool split stops batch workers
// from starving them; this stops the endpoints from re-doing identical work,
// and — because the payload is built once under the lock — a burst of callers
// can no longer stampede the store.
//
// The TTL is deliberately short and matches the dashboard's: these surfaces
// summarize resolved history, which changes on worker cadences (10m+), never
// per-request. A stale-by-60s honesty number is not a correctness question; a
// SLOW one is a usability one.
//
// Cached bytes are the fully-rendered JSON body, so a hit costs one write.
// Errors are never cached.

// respCacheTTL mirrors dashboardTTL: one minute of reuse for surfaces whose
// inputs move on multi-minute worker cadences.
const respCacheTTL = 60 * time.Second

// respCache memoizes one handler's rendered body per cache key.
type respCache struct {
	mu  sync.Mutex
	ttl time.Duration
	ent map[string]respEntry
}

type respEntry struct {
	body    []byte
	builtAt time.Time
}

func newRespCache(ttl time.Duration) *respCache {
	return &respCache{ttl: ttl, ent: map[string]respEntry{}}
}

// serve runs h with a capturing writer unless a fresh entry exists for key.
// The build happens under the lock: concurrent identical requests coalesce
// instead of each hitting the store (the stampede the storm made expensive).
func (c *respCache) serve(key string, w http.ResponseWriter, r *http.Request, h http.HandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.ent[key]; ok && time.Since(e.builtAt) < c.ttl {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "hit")
		_, _ = w.Write(e.body)
		return
	}
	rec := &bodyRecorder{ResponseWriter: w}
	h(rec, r)
	// Only successful bodies are cached — an error must not be pinned for a
	// minute, and a partial write is not a valid answer to reuse.
	if rec.status == 0 || rec.status == http.StatusOK {
		c.ent[key] = respEntry{body: rec.buf, builtAt: time.Now()}
	}
}

// bodyRecorder tees the handler's body so it can be cached while still being
// streamed to the caller.
type bodyRecorder struct {
	http.ResponseWriter
	status int
	buf    []byte
}

func (b *bodyRecorder) WriteHeader(code int) {
	b.status = code
	b.ResponseWriter.WriteHeader(code)
}

func (b *bodyRecorder) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	b.buf = append(b.buf, p...)
	return b.ResponseWriter.Write(p)
}

// registerHonestyCached wires GET /api/honesty behind the response cache. The
// cache key is the horizon — the only input that changes the payload.
func (d Deps) registerHonestyCached(mux *http.ServeMux) {
	// Perf wave 2026-07-24: moved from the synchronous respCache (whose TTL
	// lapse made the next visitor rebuild inline, ~22s) to the SWR body cache.
	mux.HandleFunc("GET /api/honesty", func(w http.ResponseWriter, r *http.Request) {
		sharedHonestySWR.serve(r.URL.Query().Get("horizon"), w, r, d.honesty)
	})
}

// registerHonestyCache wires the route against an injected cache (tests pass
// their own instance to drive TTL behavior deterministically).
func (d Deps) registerHonestyCache(mux *http.ServeMux, c *respCache) {
	mux.HandleFunc("GET /api/honesty", func(w http.ResponseWriter, r *http.Request) {
		c.serve(r.URL.Query().Get("horizon"), w, r, d.honesty)
	})
}
