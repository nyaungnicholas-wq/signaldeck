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
	"fmt"
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// sharedDashCache is THE dashboard cache instance: registerDashboard serves
// from it and WarmCaches pre-builds it. (Tests keep injecting their own via
// registerDashboardCache — unaffected.)
var sharedDashCache = newDashCache(dashboardTTL)

// sharedMoversCache is the response cache in front of GET /api/movers, keyed
// by the raw query so parameterized calls cache independently. The warmer
// keeps the default-query entry ("" — what the dashboard's first paint
// requests) hot. Perf wave 2026-07-24: moved from the synchronous respCache to
// the SWR body cache — movers reaches the network for EDGAR mcap, and a TTL
// lapse mid-fetch made a visitor wait ~20s for the inline rebuild.
var sharedMoversCache = newSWRBodyCache(respCacheTTL)

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
	// /api/predictions/latest: the latest-per-symbol self-join over 240k
	// prediction rows (~45s measured) — the SIGNALS hub's first paint.
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		hh := h
		if _, err := sharedPredictionsCache.get(ctx, fmt.Sprintf("%p|%s", d.St, hh),
			func(c context.Context) (map[string]any, error) {
				return d.buildPredictionsLatest(c, hh)
			}); err != nil {
			return err
		}
	}

	// Body-cached slow pages (perf wave 2026-07-24, measured): composite/top
	// 40s, honesty 22.7s, calibration 22.6s, datastats >30s, macro 5.9s. Warm
	// each default-query entry through the same cache the route serves from —
	// on a hot cache this costs a map lookup; when stale, the warmer is the one
	// caller that eats the rebuild.
	warmBody := func(path, key string, c *swrBodyCache, h http.HandlerFunc) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		if err != nil {
			return
		}
		rec := &discardResponseWriter{header: http.Header{}}
		c.serve(key, rec, req, h)
	}
	warmBody("/api/composite/top", "", sharedCompositeSWR, d.compositeTop)
	warmBody("/api/honesty", "", sharedHonestySWR, d.honesty)
	warmBody("/api/calibration", "", sharedCalibrationSWR, d.calibration)
	warmBody("/api/datastats", "datastats", sharedDatastatsSWR, d.datastats)
	warmBody("/api/macro", "macro", sharedMacroSWR, d.macro)
	// /api/xs-factor: recomputes the whole cross-section at read time from ~300
	// trailing daily bars per active symbol, so a cold build must land on the
	// warmer, never on the first visitor. Default query (21d / stocks / 50).
	warmBody("/api/xs-factor", xsFactorWarmKey(d.St), sharedXSFactorSWR, d.xsFactor)
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
