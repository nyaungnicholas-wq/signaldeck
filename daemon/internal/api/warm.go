// ═══ COLD-LOAD PRECOMPUTE WAVE — cache warmer (appended) ═════════════════════
//
// Measured problem (2026-07-18): the first GET /api/dashboard and /api/movers
// after a daemon restart took 30-55s while the regime sweep held the write
// path — then 3ms once the 60s response cache was hot. The caches were only
// ever filled BY a request, so the first visitor always paid the cold price.
//
// Fix: the process-wide dashboard cache and a response cache for /api/movers
// live here as shared instances the handlers register against, and WarmCaches
// rebuilds both exactly the way the handlers would (same builders, same cache
// entries). The cache-warmer worker (internal/pipeline/cachewarm.go, 60s
// cadence, wired in cmd/signaldeckd) calls WarmCaches so the caches are
// always hot when a human arrives — including the first tick after boot.
package api

import (
	"context"
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// sharedDashCache is THE dashboard cache instance: registerDashboard serves
// from it and WarmCaches pre-builds it. (Tests keep injecting their own via
// registerDashboardCache — unaffected.)
var sharedDashCache = newDashCache(dashboardTTL)

// sharedMoversCache is the response cache in front of GET /api/movers — the
// same respCache machinery /api/honesty uses, keyed by the raw query so
// parameterized calls cache independently. The warmer keeps the default-query
// entry ("" — what the dashboard's first paint requests) hot.
var sharedMoversCache = newRespCache(respCacheTTL)

// WarmCaches rebuilds the shared dashboard cache and the default /api/movers
// response-cache entry the way the handlers would. Safe to call on any
// cadence: a fresh cache entry short-circuits, so a warm pass on a hot cache
// costs two map lookups.
func (d Deps) WarmCaches(ctx context.Context) error {
	if _, err := sharedDashCache.get(ctx, d); err != nil {
		return err
	}
	// Movers: drive the real handler through the shared response cache with a
	// discarded body — identical build path, identical cache key ("").
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/api/movers", nil)
	if err != nil {
		return err
	}
	rec := &discardResponseWriter{header: http.Header{}}
	sharedMoversCache.serve("", rec, req, d.movers)

	// Perf wave 2026-07-24: /api/track-record's full grade costs ~22-44s in the
	// pure-Go driver and /api/regimes ~1.5-4.6s — neither may ever build on a
	// user's request path. The SWR cache serves stale instantly and rebuilds in
	// the background, so on a hot cache each warm pass costs two map lookups;
	// when stale, the warmer (not a visitor) becomes the one caller that kicks
	// the rebuild.
	for _, h := range []md.Horizon{md.H1d, md.H1w, md.H1h} {
		hh := h
		if _, err := sharedTrackCache.get(ctx, string(hh),
			func(c context.Context) (map[string]any, error) {
				return d.buildTrackRecord(c, hh)
			}); err != nil {
			return err
		}
	}
	if _, err := sharedRegimesCache.get(ctx, "regimes", d.buildStructuralRegimes); err != nil {
		return err
	}
	return nil
}

// discardResponseWriter satisfies http.ResponseWriter for warm-up builds: the
// respCache tees the body into its entry; the live byte stream goes nowhere.
type discardResponseWriter struct {
	header http.Header
	status int
}

func (d *discardResponseWriter) Header() http.Header         { return d.header }
func (d *discardResponseWriter) WriteHeader(code int)        { d.status = code }
func (d *discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
