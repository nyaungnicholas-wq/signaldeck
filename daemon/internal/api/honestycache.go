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
	usedSeq uint64 // last read; drives LRU eviction (see lruclock.go)
}

func newRespCache(ttl time.Duration) *respCache {
	return &respCache{ttl: ttl, ent: map[string]respEntry{}}
}

// evictLRULocked bounds the entry map at maxCacheEntries, dropping the least
// recently USED entry first — same contract as the two maps in slowcache.go,
// and simpler here because respCache holds the lock across the build, so no
// entry can ever be mid-build while this runs.
//
// C6 (2026-07-26 review) called this map out as unbounded. It has no live
// instance today — the live route runs on sharedHonestySWR and only tests
// construct a respCache — so this is a latent leak, not a measured one.
// Bounding it now is what stops re-wiring this cache from silently
// reintroducing the 12-KB-per-junk-key growth the reviewer measured on the
// SWR maps.
//
// What this does NOT address, deliberately: serve() still builds under the
// GLOBAL lock, so one slow key blocks every other key's hits. That is the flaw
// that moved /api/honesty to swrBodyCache in the first place. Fixing it here
// would mean reimplementing per-entry coalescing that already exists one file
// over, for a cache nothing in production constructs.
func (c *respCache) evictLRULocked() {
	for len(c.ent) >= maxCacheEntries {
		var oldestKey string
		var oldest uint64
		found := false
		for k, e := range c.ent {
			if !found || e.usedSeq < oldest {
				oldestKey, oldest, found = k, e.usedSeq, true
			}
		}
		if !found {
			return
		}
		delete(c.ent, oldestKey)
	}
}

// serve runs h with a capturing writer unless a fresh entry exists for key.
// The build happens under the lock: concurrent identical requests coalesce
// instead of each hitting the store (the stampede the storm made expensive).
func (c *respCache) serve(key string, w http.ResponseWriter, r *http.Request, h http.HandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.ent[key]; ok && time.Since(e.builtAt) < c.ttl {
		e.usedSeq = lruTick()
		c.ent[key] = e
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
		if _, replacing := c.ent[key]; !replacing {
			c.evictLRULocked()
		}
		now := time.Now()
		c.ent[key] = respEntry{body: rec.buf, builtAt: now, usedSeq: lruTick()}
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
// cache key is the RESOLVED horizon — the only input that changes the payload.
//
// C6: it used to be the raw ?horizon value. Whitelisting the parameter NAME is
// not the fix; the handler resolves anything outside {1h,1d,1w} to 1d, so
// ?horizon=aaa1, ?horizon=aaa2 ... each built the same body under a new key.
// honestyCacheKey normalises to what the handler will actually render.
func (d Deps) registerHonestyCached(mux *http.ServeMux) {
	// Perf wave 2026-07-24: moved from the synchronous respCache (whose TTL
	// lapse made the next visitor rebuild inline, ~22s) to the SWR body cache.
	mux.HandleFunc("GET /api/honesty", func(w http.ResponseWriter, r *http.Request) {
		sharedHonestySWR.serve(honestyCacheKey(r), w, r, d.honesty)
	})
}
