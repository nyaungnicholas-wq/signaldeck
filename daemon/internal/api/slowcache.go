// Response cache for expensive, user-independent read endpoints (2026-07-24
// perf wave).
//
// /api/track-record recomputed its entire 240k-row grade on EVERY request
// (~22s per page load in the pure-Go SQLite driver) and /api/regimes rebuilt
// its fleet-wide earnings annotation the same way (~4.6s). Both payloads are
// identical for every user and change only on worker cadence (resolver 10m,
// regime runner 6h), so recomputing per request bought nothing but latency.
//
// Same stale-while-revalidate contract as dashCache: fresh → serve; stale →
// serve the stale copy instantly and kick ONE background rebuild; only a cold
// cache (first hit after boot) builds inline. A user never waits behind a
// rebuild except on the very first request after a restart.
package api

import (
	"context"
	"sync"
	"time"
)

// swrCache caches one built payload per key (e.g. per horizon).
type swrCache struct {
	mu  sync.Mutex
	ttl time.Duration
	ent map[string]*swrEntry
}

type swrEntry struct {
	builtAt    time.Time
	payload    map[string]any
	rebuilding bool
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
	if e != nil && e.payload != nil {
		p := e.payload
		if time.Since(e.builtAt) >= c.ttl && !e.rebuilding {
			e.rebuilding = true
			go func() {
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
	if e == nil {
		e = &swrEntry{}
		c.ent[key] = e
	}
	defer c.mu.Unlock()
	p, err := build(ctx)
	if err != nil {
		return nil, err
	}
	e.payload = p
	e.builtAt = time.Now()
	return p, nil
}

// Shared instances. TTLs sit comfortably under the cadence of the workers that
// change the underlying rows, so staleness is bounded by design:
//   - track-record: outcome resolver runs every 10m → 2m TTL.
//   - regimes: the regime runner writes every 6h → 5m TTL (earnings labels
//     drift by the day, not the minute).
var (
	sharedTrackCache   = newSWRCache(2 * time.Minute)
	sharedRegimesCache = newSWRCache(5 * time.Minute)
)
