// Response cache for expensive, user-independent read endpoints (2026-07-24
// perf wave).
//
// /api/track-record recomputed its entire 240k-row grade on EVERY request
// (~22s per page load in the pure-Go SQLite driver), /api/regimes rebuilt its
// fleet-wide earnings annotation the same way (~4.6s), and
// /api/predictions/latest re-ran a latest-per-symbol self-join over 240k rows
// (~45s). All three payloads are identical for every user and change only on
// worker cadence (resolver 10m, regime runner 6h), so recomputing per request
// bought nothing but latency.
//
// Same stale-while-revalidate contract as dashCache: fresh → serve; stale →
// serve the stale copy instantly and kick ONE background rebuild; only a cold
// cache (first hit after boot) builds inline. A user never waits behind a
// rebuild except on the very first request after a restart.
//
// Locking: the global mutex guards only the entry map. Cold builds run OUTSIDE
// it with per-entry coalescing — the first version held the global lock across
// a ~40s cold build, which made a cache HIT on one horizon queue behind a cold
// MISS on another. One slow key must never block the others.
package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// ── C6 admission control and entry bounds (2026-07-26 hostile review) ────────
//
// The whole read path shares FOUR connections (store.go:53) and a cold build
// holds one for 22-45s. With the key taken straight from the query string
// (see cachekey.go) five requests carrying five junk parameters took every
// connection and the daemon stopped answering, /api/health included. Nothing
// capped how many cold builds could run at once, and nothing ever deleted an
// entry, so each junk key also leaked its payload for the life of the process.
//
// Whitelisted keys stop an unknown PARAMETER from minting an entry. These two
// bounds stop a flood of otherwise-legal keys from doing the same thing, and
// they apply to every cache in the package — including the routes whose keys
// are still built from raw input, which is why they live here and not in the
// handlers.
const (
	// maxConcurrentColdBuilds leaves half the read pool free no matter what is
	// rebuilding, so hits and health checks keep answering during a cold build.
	maxConcurrentColdBuilds = 2
	// coldBuildWait is how long a caller queues for a slot before being told to
	// retry. A fast 503 is a better answer than a request parked on a
	// connection the rest of the daemon needs.
	coldBuildWait = 5 * time.Second
	// maxCacheEntries bounds each entry map. Far above the legitimate key space
	// of any route here (horizon x market x limit), far below the point where a
	// flood of distinct keys is worth memory.
	maxCacheEntries = 64
)

var coldBuildSlots = make(chan struct{}, maxConcurrentColdBuilds)

// acquireColdSlot queues up to coldBuildWait for permission to run a cold
// build. A caller that gets one MUST releaseColdSlot when the build returns.
func acquireColdSlot(ctx context.Context) bool {
	t := time.NewTimer(coldBuildWait)
	defer t.Stop()
	select {
	case coldBuildSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-t.C:
		return false
	}
}

func releaseColdSlot() { <-coldBuildSlots }

// swrCache caches one built payload per key (e.g. per horizon).
type swrCache struct {
	mu  sync.Mutex
	ttl time.Duration
	ent map[string]*swrEntry
}

type swrEntry struct {
	builtAt    time.Time
	usedSeq    uint64 // last read; drives LRU eviction (see lruclock.go)
	payload    map[string]any
	rebuilding bool          // a stale-refresh goroutine is in flight
	building   chan struct{} // non-nil while a COLD build is in flight; closed on completion
}

// evictLRULocked makes room for a new entry by dropping the least recently
// USED idle one — LRU rather than oldest-built so the hot default entry the
// warmer keeps alive survives a flood of one-shot junk keys.
//
// An entry with a build in flight is never evicted: waiters already hold a
// pointer to it, and replacing it would let the next caller start a SECOND
// build for the same key, which is exactly the stampede the single-flight
// exists to prevent. The cold-build ceiling bounds how many entries can be in
// that state at once, so this can never fail to make progress for long.
func (c *swrCache) evictLRULocked() {
	for len(c.ent) >= maxCacheEntries {
		var oldestKey string
		var oldest uint64
		found := false
		for k, e := range c.ent {
			if e.building != nil || e.rebuilding {
				continue
			}
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

func newSWRCache(ttl time.Duration) *swrCache {
	return &swrCache{ttl: ttl, ent: map[string]*swrEntry{}}
}

// get returns the cached payload for key, building it via build when cold and
// revalidating in the background when stale. build must be safe to run off the
// request context — rebuilds are detached so they outlive the caller.
func (c *swrCache) get(ctx context.Context, key string,
	build func(ctx context.Context) (map[string]any, error),
) (map[string]any, error) {
	c.mu.Lock()
	e := c.ent[key]
	if e == nil {
		c.evictLRULocked()
		e = &swrEntry{}
		c.ent[key] = e
	}
	e.usedSeq = lruTick()

	// Warm entry: serve immediately; when stale, kick ONE detached refresh.
	if e.payload != nil {
		p := e.payload
		if time.Since(e.builtAt) >= c.ttl && !e.rebuilding {
			e.rebuilding = true
			go func() {
				// Background refreshes read the same four connections a cold
				// build does, so they queue behind the same ceiling. Losing the
				// slot just abandons this refresh — the stale copy keeps serving
				// and the next stale hit tries again.
				if !acquireColdSlot(context.Background()) {
					c.mu.Lock()
					e.rebuilding = false
					c.mu.Unlock()
					return
				}
				defer releaseColdSlot()

				np, err := build(context.Background())
				c.mu.Lock()
				e.rebuilding = false
				if err == nil {
					e.payload = np
					e.builtAt = time.Now()
				}
				c.mu.Unlock()
			}()
		}
		c.mu.Unlock()
		return p, nil
	}

	// Cold entry: exactly one caller builds; concurrent callers for the SAME
	// key wait on its channel; callers for OTHER keys proceed untouched.
	if e.building != nil {
		ch := e.building
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		p := e.payload
		c.mu.Unlock()
		if p == nil {
			return nil, errors.New("cache build failed; retry")
		}
		return p, nil
	}
	ch := make(chan struct{})
	e.building = ch
	c.mu.Unlock()

	// Admission control before the build, not before the lookup: a hit never
	// touches this, so a queue of cold builds cannot delay a warm read.
	if !acquireColdSlot(ctx) {
		c.mu.Lock()
		e.building = nil
		c.mu.Unlock()
		close(ch)
		return nil, errors.New("cache build capacity exhausted; retry")
	}
	// Deferred, not straight-line: a panicking build already poisons its own
	// entry, but a leaked slot would shrink the ceiling for every cache in the
	// process until a restart.
	defer releaseColdSlot()

	p, err := build(ctx)
	c.mu.Lock()
	e.building = nil
	if err == nil {
		e.payload = p
		e.builtAt = time.Now()
	}
	c.mu.Unlock()
	close(ch)
	return p, err
}

// Shared instances. TTLs sit comfortably under the cadence of the workers that
// change the underlying rows, so staleness is bounded by design:
//   - track-record: outcome resolver runs every 10m → 2m TTL.
//   - regimes: the regime runner writes every 6h → 5m TTL (earnings labels
//     drift by the day, not the minute).
//   - predictions: the prediction runner writes every 10m → 2m TTL.
var (
	sharedTrackCache       = newSWRCache(2 * time.Minute)
	sharedRegimesCache     = newSWRCache(5 * time.Minute)
	sharedPredictionsCache = newSWRCache(2 * time.Minute)
)

// ── body-level SWR cache ─────────────────────────────────────────────────────
//
// Same stale-while-revalidate + per-entry coalescing contract as swrCache, but
// wrapping whole http.HandlerFuncs at the ROUTE, for slow endpoints whose
// handlers render directly (honesty, calibration, datastats, macro). The
// existing respCache in honestycache.go is synchronous: when its 60s TTL
// lapses, the next visitor rebuilds inline — which for /api/honesty meant a
// ~22s page load once a minute. Here the visitor gets the stale body instantly
// and the rebuild happens behind them.

type swrBodyCache struct {
	mu  sync.Mutex
	ttl time.Duration
	ent map[string]*swrBodyEntry
}

type swrBodyEntry struct {
	builtAt    time.Time
	usedSeq    uint64 // last read; drives LRU eviction (see lruclock.go)
	body       []byte
	rebuilding bool
	building   chan struct{}
}

// evictLRULocked bounds the entry map. Same contract as swrCache's: least
// recently USED idle entry first, entries mid-build are untouchable. This map
// is the one the hostile review measured leaking ~12 KB per junk key, because
// /api/composite/top and /api/movers keyed it on the raw query string.
func (c *swrBodyCache) evictLRULocked() {
	for len(c.ent) >= maxCacheEntries {
		var oldestKey string
		var oldest uint64
		found := false
		for k, e := range c.ent {
			if e.building != nil || e.rebuilding {
				continue
			}
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

func newSWRBodyCache(ttl time.Duration) *swrBodyCache {
	return &swrBodyCache{ttl: ttl, ent: map[string]*swrBodyEntry{}}
}

// render runs the handler against a throwaway recorder and returns the body,
// or nil when the handler answered non-200 (errors must never be pinned).
func swrRender(r *http.Request, h http.HandlerFunc) []byte {
	rec := &bodyRecorder{ResponseWriter: &discardResponseWriter{header: http.Header{}}}
	h(rec, r)
	if rec.status == 0 || rec.status == http.StatusOK {
		return rec.buf
	}
	return nil
}

// serve returns the cached body for key, rendering via h when cold and
// revalidating in the background when stale. Background rebuilds re-issue the
// request with a detached context so they outlive the caller.
func (c *swrBodyCache) serve(key string, w http.ResponseWriter, r *http.Request, h http.HandlerFunc) {
	c.mu.Lock()
	e := c.ent[key]
	if e == nil {
		c.evictLRULocked()
		e = &swrBodyEntry{}
		c.ent[key] = e
	}
	e.usedSeq = lruTick()

	if e.body != nil {
		body := e.body
		if time.Since(e.builtAt) >= c.ttl && !e.rebuilding {
			e.rebuilding = true
			bg := r.Clone(context.Background())
			go func() {
				// Same ceiling as a cold build: a background refresh reads the
				// same connections. Losing the slot abandons this refresh and
				// keeps serving the stale body.
				if !acquireColdSlot(context.Background()) {
					c.mu.Lock()
					e.rebuilding = false
					c.mu.Unlock()
					return
				}
				defer releaseColdSlot()

				nb := swrRender(bg, h)
				c.mu.Lock()
				e.rebuilding = false
				if nb != nil {
					e.body = nb
					e.builtAt = time.Now()
				}
				c.mu.Unlock()
			}()
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Cache", "hit")
		_, _ = w.Write(body)
		return
	}

	// Cold: one renderer per key; same-key followers wait, other keys untouched.
	if e.building != nil {
		ch := e.building
		c.mu.Unlock()
		select {
		case <-ch:
		case <-r.Context().Done():
			httpErr(w, 504, "cache build in progress; request canceled")
			return
		}
		c.mu.Lock()
		body := e.body
		c.mu.Unlock()
		if body == nil {
			httpErr(w, 500, "cache build failed; retry")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Cache", "coalesced")
		_, _ = w.Write(body)
		return
	}
	ch := make(chan struct{})
	e.building = ch
	c.mu.Unlock()

	// Admission control before the build, never before the lookup — a cache
	// hit must stay free while cold builds queue.
	if !acquireColdSlot(r.Context()) {
		c.mu.Lock()
		e.building = nil
		c.mu.Unlock()
		close(ch)
		w.Header().Set("Retry-After", "5")
		httpErr(w, 503, "cache build capacity exhausted; retry")
		return
	}
	defer releaseColdSlot() // see swrCache.get: never leak a process-wide slot

	// Build inline for the cold caller, streaming to them directly while a
	// recorder tees the body for the cache.
	rec := &bodyRecorder{ResponseWriter: w}
	h(rec, r)
	c.mu.Lock()
	e.building = nil
	if rec.status == 0 || rec.status == http.StatusOK {
		e.body = rec.buf
		e.builtAt = time.Now()
	}
	c.mu.Unlock()
	close(ch)
}

// Shared body-cache instances for the remaining measured-slow read pages:
// honesty/calibration ~22.7s, datastats >30s (timed out), macro ~5.9s. TTLs
// match the underlying worker cadences (outcomes resolve on 10m cadence;
// datastats counts whole tables — 5m is plenty fresh for an ops page).
var (
	sharedHonestySWR     = newSWRBodyCache(2 * time.Minute)
	sharedCompositeSWR   = newSWRBodyCache(2 * time.Minute)
	sharedCalibrationSWR = newSWRBodyCache(2 * time.Minute)
	sharedDatastatsSWR   = newSWRBodyCache(5 * time.Minute)
	sharedMacroSWR       = newSWRBodyCache(2 * time.Minute)
)
