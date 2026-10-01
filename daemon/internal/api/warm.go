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
	"errors"
	"fmt"
	"log/slog"
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

// WarmCaches rebuilds every shared cache the way the handlers would (same
// builders, same entries, same persisted files). Safe to call on any cadence: a
// fresh entry short-circuits, so a warm pass on hot caches costs map lookups.
//
// NO STEP MAY END THE PASS (step 4, 2026-10-01). Each step logs its own failure
// and the pass goes on. The payload caches' failures come back joined, so the
// worker run still reads as failed when one of them did (as it did when they
// returned early); a body page that will not render is logged only, as before
// it was not reported at all. The early `return err`s this replaces were measured
// aborting the whole pass after a restart when one cold build found no build
// slot ('cache build capacity exhausted', 00:49:33 and 00:49:59), so nothing
// below it was warmed and the first visitor paid a 129.4s /api/track-record
// build that overran the 90s write deadline and delivered 0 bytes.
//
// ORDER IS THE PRIORITY. Member and /proof pages first: track-record (1d is on
// /today's ProofStrip and /proof; 1w and 1h are /proof's horizon picker), then
// /api/regimes (/today, /watchlist, /market/regimes, the member symbol view),
// then /api/ledger/verify (/proof), then the volatility record (/volatility).
// All four are also persisted, so after a restart they serve their last good
// body at once while this pass rebuilds them. Everything else follows, in its
// old order: attribution, then the operator dashboard and the slow pages.
//
// The context is marked waitForBuild: the warmer waits each cold build out,
// where a visitor would be answered "warming" after coldServeWait.
func (d Deps) WarmCaches(ctx context.Context) error {
	ctx = waitForBuild(ctx)
	var errs []error
	note := func(what string, err error) bool {
		if err == nil || ctx.Err() != nil { // a shutdown is not a failed step
			return false
		}
		slog.Warn("cache warm step failed; the pass continues", "cache", what, "err", err)
		return true
	}
	step := func(what string, err error) {
		if note(what, err) {
			errs = append(errs, fmt.Errorf("%s: %w", what, err))
		}
	}
	// warmBody drives the real handler through the cache the route serves from,
	// with a discarded body; anything but a 200 is noted.
	warmBody := func(path, file, key string, c *swrBodyCache, h http.HandlerFunc) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		if err != nil {
			note(path, err)
			return
		}
		rec := &discardResponseWriter{header: http.Header{}}
		c.serveAt(file, key, rec, req, h)
		if rec.status != 0 && rec.status != http.StatusOK {
			note(path, fmt.Errorf("status %d", rec.status))
		}
	}

	for _, h := range []md.Horizon{md.H1d, md.H1w, md.H1h} {
		_, err := d.cachedTrackRecord(ctx, h)
		step("track-record "+string(h), err)
	}
	_, err := d.cachedRegimes(ctx)
	step("regimes", err)
	_, err = d.cachedLedgerVerify(ctx)
	step("ledger-verify", err)
	// /api/vol-forecast/record: ~25s cold; the public /volatility page fetches
	// it server-side under a 15s bound and rendered "not readable" to any
	// visitor who arrived before a human had paid the build.
	warmBody("/api/vol-forecast/record", d.cacheFile(volRecordCacheName), d.St.CacheKey()+"|record", sharedVolRecordSWR, d.volForecastRecord)

	// /api/attribution: the cheapest of the slow builds (~1s per horizon, one
	// ledger grade covers every symbol) and the one the OPERATOR symbol page
	// fetches on mount. It led this list until the member pages above did.
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		hh := h
		_, err := sharedAttributionLiveCache.get(ctx, attributionCacheKey(d.St, hh),
			func(c context.Context) (map[string]any, error) {
				return d.buildAttributionLive(c, hh)
			})
		note("attribution "+string(hh), err)
	}
	_, err = sharedDashCache.get(ctx, d)
	step("dashboard", err)
	// Movers: the default-query entry ("") the dashboard's first paint reads.
	warmBody("/api/movers", "", "", sharedMoversCache, d.movers)
	// /api/predictions/latest: the latest-per-symbol self-join over 240k
	// prediction rows (~45s measured) — the SIGNALS hub's first paint.
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		hh := h
		_, err := sharedPredictionsCache.get(ctx, fmt.Sprintf("%s|%s", d.St.CacheKey(), hh),
			func(c context.Context) (map[string]any, error) {
				return d.buildPredictionsLatest(c, hh)
			})
		step("predictions "+string(hh), err)
	}

	// Body-cached slow pages (perf wave 2026-07-24, measured): composite/top
	// 40s, honesty 22.7s, calibration 22.6s, datastats >30s, macro 5.9s. Warm
	// each default-query entry through the same cache the route serves from —
	// on a hot cache this costs a map lookup; when stale, the warmer is the one
	// caller that eats the rebuild.
	warmBody("/api/composite/top", "", "", sharedCompositeSWR, d.compositeTop)
	warmBody("/api/honesty", "", d.St.CacheKey()+"|", sharedHonestySWR, d.honesty)
	warmBody("/api/calibration", "", d.St.CacheKey()+"|", sharedCalibrationSWR, d.calibration)
	warmBody("/api/datastats", "", "datastats", sharedDatastatsSWR, d.datastats)
	warmBody("/api/macro", "", "macro", sharedMacroSWR, d.macro)
	// The screener and the two flagship paper books: the workspace's first
	// clicks after the dashboard, and the two slowest under worker load.
	warmBody("/api/screener", "", d.St.CacheKey()+"|screener", sharedScreenerSWR, d.screener)
	warmBody("/api/paper?strategy=flagship-1d", "", d.St.CacheKey()+"|paper|strategy=flagship-1d", sharedPaperSWR, d.paper)
	warmBody("/api/paper?strategy=flagship-1w", "", d.St.CacheKey()+"|paper|strategy=flagship-1w", sharedPaperSWR, d.paper)
	// /api/xs-factor: recomputes the whole cross-section at read time from ~300
	// trailing daily bars per active symbol, so a cold build must land on the
	// warmer, never on the first visitor. Default query (21d / stocks / 50).
	warmBody("/api/xs-factor", "", xsFactorWarmKey(d.St), sharedXSFactorSWR, d.xsFactor)
	// /api/research-ledger goes LAST: it is the most expensive build here, so
	// it must not delay the routes above it.
	//
	// Caching it alone was not enough. Measured 2026-09-13 on a freshly
	// deployed daemon: the first call did not return inside 120s, the second
	// took 75s, and only the third was served from cache (1.8ms). SWR protects
	// every visitor EXCEPT the first one after a restart, and on a published
	// deployment that visitor is anonymous and unauthenticated.
	warmBody("/api/research-ledger", "", d.St.CacheKey()+"|research-ledger", sharedResearchLedgerSWR, d.researchLedger)
	return errors.Join(errs...)
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
