// ═══ COLD-LOAD PRECOMPUTE WAVE — cache-warmer worker (appended) ══════════════
//
// Keeps the API's shared response caches (dashboard + movers, internal/api
// warm.go) hot on a 60s cadence — matching their TTL — so the first human
// request after a restart (or after a worker sweep holds the write path for
// 30-55s) is served from a cache the worker already rebuilt, never a cold
// build. The warm target is a callback (wired in cmd/signaldeckd) because the
// caches live in the api package and this package must not import it.
package pipeline

import (
	"context"
	"time"
)

// CacheWarmer periodically invokes the injected warm callback.
type CacheWarmer struct {
	// Warm rebuilds the API caches (api.Deps.WarmCaches). nil is safe: the
	// worker reports an honest no-op until the wiring sets it.
	Warm func(ctx context.Context) error
}

func (w *CacheWarmer) Name() string            { return "cache-warmer" }
func (w *CacheWarmer) Interval() time.Duration { return 60 * time.Second }

func (w *CacheWarmer) Run(ctx context.Context) (string, error) {
	if w.Warm == nil {
		return "no warm target wired — skipping", nil
	}
	if err := w.Warm(ctx); err != nil {
		return "", err
	}
	return "warmed dashboard + movers caches", nil
}
