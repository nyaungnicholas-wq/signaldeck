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
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
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

// detachedBuildTimeout is the hard ceiling on a rebuild that has been DETACHED
// from a request context, and it exists because "detached" had come to mean
// "unbounded".
//
// Every background refresh in this package ran on a bare context.Background().
// A cold build inherits the request context and is bounded by it; a background
// one had no deadline at all. So a build that never returned — a wedged
// connection, a query that will not complete — never ran its
// `defer releaseColdSlot()` and never cleared `rebuilding`. TWO of those exhaust
// maxConcurrentColdBuilds permanently, and from then on every cold build in the
// process fails admission while every warm entry serves its stale copy forever
// with no error and no age. The daemon reports "warmed dashboard + movers
// caches" every 60s throughout (pipeline/cachewarm.go).
//
// Three minutes was roughly four times the slowest build measured when this
// package was written (~45s for /api/predictions/latest, 22-44s for
// /api/track-record). It is ten now: a cold /api/track-record build after a
// restart measured 129.4s on a loaded host (2026-10-01), which three minutes
// cleared by only 1.4x, and since then every COLD build runs detached under
// this ceiling too. Ten minutes keeps the ~4x margin over the slowest build seen,
// so it fires only on a build that is never coming back.
// A var, not a const, ONLY so the regression test can shorten it — a test that
// actually waits out the real ceiling would take ten minutes and would
// therefore never be run.
var detachedBuildTimeout = 10 * time.Minute

// detachedCtx (or detachedFrom, below) is the ONLY way a rebuild in this
// package should leave its request behind: it outlives the caller, and nothing more. Callers must defer
// the cancel — the timeout bounds a wedged build, the cancel releases the timer
// for the overwhelming majority that finish normally.
func detachedCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), detachedBuildTimeout)
}

// noteRebuildFailure logs a background rebuild that did not produce a payload.
// Silence is how the wedge above stayed invisible: the stale copy kept serving,
// so nothing anywhere named the failure. A deadline breach is called out
// separately because it means the build was still running at the ceiling, which
// is a different problem from a query that returned an error.
func noteRebuildFailure(what string, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("cache rebuild hit the detached-build ceiling and was abandoned; "+
			"the stale copy keeps serving and the next stale hit will retry",
			"cache", what, "ceiling", detachedBuildTimeout)
		return
	}
	slog.Warn("cache rebuild failed; serving the stale copy", "cache", what, "err", err)
}

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

// ── cold builds never hold a request past coldServeWait (step 4, 2026-10-01) ──
//
// A cold build used to run INLINE on the first caller's request. Measured after
// a restart: /api/track-record?horizon=1d built for 129.4s, past the 90s
// response write deadline, so the daemon logged 200 and the client got 0 bytes.
// Now every cold build runs DETACHED (bounded by detachedBuildTimeout) and a
// request waits at most coldServeWait for it; after that it gets 503 "warming"
// with Retry-After and the build carries on, so the retry is a cache hit. The
// warmer (WarmCaches) marks its context with waitForBuild and waits the build
// out, which is how one slow build no longer becomes one failed visitor.

// coldServeWait is the longest a REQUEST waits on a cold build: inside the web
// proxy's 60s timeout and the 90s write deadline. A var only so tests can
// shorten it.
var coldServeWait = 20 * time.Second

// errWarming means there is no value yet: the build is still running, or no
// build slot was free (the warmer's next pass builds it). Routes answer it with
// writeWarming.
var errWarming = errors.New("warming")

// uncachedResult is a build's answer for the callers of THIS build only: a build
// returns it, as its error, when its result does not belong under the key it was
// started for (the ledger verify whose chain moved off that key mid-build). A
// cold build hands the payload to its waiters and stores nothing; a refresh
// keeps the entry's previous payload.
type uncachedResult struct{ payload map[string]any }

func (uncachedResult) Error() string { return "a result for its own callers, not for the cache" }

type waitForBuildKey struct{}

// waitForBuild marks ctx as the warmer's: a cold get under it waits for the
// build to finish (or ctx to end) instead of giving up after coldServeWait, and
// an entry loaded from disk is refreshed INLINE, awaited, never in a goroutine.
//
// The inline refresh is the point (review #8, 2026-10-01). Fired as goroutines,
// the warmer's refreshes of the persisted track-record entries took BOTH cold
// slots for a whole build (129s measured) within milliseconds of a boot, the
// warmer moved straight on, and every cold build behind them, attribution
// included, waited coldBuildWait and ended "warming". Awaited, the warmer holds
// at most one slot at a time and the other stays free for visitors.
//
// ONLY disk-loaded entries, which exist once per boot. An entry this process
// built and that has merely gone stale keeps the stale-while-revalidate
// contract for the warmer too: served at once, refreshed detached. Awaiting
// every stale key serially (2m TTLs make nearly every key stale each pass) put
// a pass at minutes against the worker's 15-minute run deadline.
func waitForBuild(ctx context.Context) context.Context {
	return context.WithValue(ctx, waitForBuildKey{}, true)
}

// isWarmer reports whether ctx was marked by waitForBuild.
func isWarmer(ctx context.Context) bool { return ctx.Value(waitForBuildKey{}) != nil }

// coldDeadline is what a caller of a cold build waits on: nil (never fires) for
// the warmer, coldServeWait for a request. stop releases the timer.
func coldDeadline(ctx context.Context) (<-chan time.Time, func() bool) {
	if isWarmer(ctx) {
		return nil, func() bool { return false }
	}
	t := time.NewTimer(coldServeWait)
	return t.C, t.Stop
}

// writeWarming is the answer to errWarming: 503, Retry-After 30, and the body
// {"error":"warming"} the web client waits out (lib/api.ts untilWarm).
func writeWarming(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "30")
	httpErr(w, http.StatusServiceUnavailable, "warming")
}

// httpCacheErr answers a failed swrCache.get: warming is a 503 the client
// retries, anything else the opaque 500.
func httpCacheErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errWarming) {
		writeWarming(w)
		return
	}
	httpInternal(w, err)
}

// detachedFrom is detachedCtx for a build started on a caller's behalf: it
// keeps the caller's context VALUES (who is asking, as the inline build saw
// them) but not its cancellation, and gets the detached ceiling.
func detachedFrom(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), detachedBuildTimeout)
}

// recoverBuild turns a panicking detached build into an error. Inline, a panic
// reached withRecover and became a 500; in a goroutine it would kill the daemon.
func recoverBuild(what string, err *error) {
	if r := recover(); r != nil {
		slog.Error("cache build panicked", "cache", what, "panic", r, "stack", string(debug.Stack()))
		*err = fmt.Errorf("cache build panicked: %v", r)
	}
}

// swrCache caches one built payload per key (e.g. per horizon).
type swrCache struct {
	mu  sync.Mutex
	ttl time.Duration
	// maxStale > 0 makes this a PROOF cache (the ledger verify, review #6): its
	// payload is a claim, so how long a copy may stand in is bounded. A payload
	// is never served once maxStale has passed since its builtAt, which is the
	// END of its build: up to maxStale plus one build time after the state the
	// build read. Stale-while-revalidate still serves a copy up to that old to the
	// first reader after a gap with no reads; only reads every TTL keep a served
	// copy within one TTL plus one rebuild. A rebuild that RAN and failed evicts
	// the payload, so the next reader gets the real error. A refresh that never
	// ran (no cold build slot, or the verify semaphore full) keeps the copy, and
	// so does one whose result belonged to another key (uncachedResult). Each
	// keeps the copy's ORIGINAL builtAt, so maxStale from that build alone bounds
	// those. 0 serves the stale copy until a rebuild succeeds, as every other
	// cache here does.
	maxStale time.Duration
	ent      map[string]*swrEntry
	created  time.Time // the boot, for the package's shared caches: see fromDiskMaxStale
}

// fromDiskMaxStale bounds how long a payload persisted by a PREVIOUS process
// stands in for this one's (review #7): only until its first refresh lands, and
// never past this long after the cache was created, which for every persisted
// cache (package vars) is the boot. A key whose refreshes keep failing past it
// answers warming or the build's error, never the previous process's answer.
// That bounds a same-build database restore to the same window.
const fromDiskMaxStale = 10 * time.Minute

type swrEntry struct {
	builtAt    time.Time
	usedSeq    uint64 // last read; drives LRU eviction (see lruclock.go)
	payload    map[string]any
	fromDisk   bool       // payload is the persisted last good one: rebuild on first read
	rebuilding bool       // a stale-refresh goroutine is in flight
	building   *coldBuild // non-nil while a COLD build is in flight
}

// coldBuild is one in-flight cold build; its result is set before done closes.
type coldBuild struct {
	done    chan struct{}
	payload map[string]any
	err     error
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
	return &swrCache{ttl: ttl, ent: map[string]*swrEntry{}, created: time.Now()}
}

// get returns the cached payload for key, building it via build when cold and
// revalidating in the background when stale. build must be safe to run off the
// request context — every build is detached so it outlives the caller.
func (c *swrCache) get(ctx context.Context, key string,
	build func(ctx context.Context) (map[string]any, error),
) (map[string]any, error) {
	return c.getAt(ctx, "", key, build)
}

// getAt is get with the key's last good payload kept in file across restarts
// (cachepersist.go): the first read of the key in this process, with nothing in
// memory, serves the persisted payload as STALE and rebuilds behind it. file ""
// keeps the cache in memory only.
func (c *swrCache) getAt(ctx context.Context, file, key string,
	build func(ctx context.Context) (map[string]any, error),
) (map[string]any, error) {
	c.mu.Lock()
	e := c.ent[key]
	if e == nil {
		c.evictLRULocked()
		e = &swrEntry{}
		if p, at, ok := loadPersistedPayload(file, key); ok {
			e.payload, e.builtAt, e.fromDisk = p, at, true
		}
		c.ent[key] = e
	}
	e.usedSeq = lruTick()

	if e.payload != nil && c.maxStale > 0 && time.Since(e.builtAt) >= c.maxStale {
		e.payload, e.fromDisk = nil, false // a proof this old is not served
	}
	if e.fromDisk && time.Since(c.created) >= fromDiskMaxStale {
		e.payload, e.fromDisk = nil, false // nor is a previous process's, past its window
	}

	// Warm entry: serve immediately; when stale (or loaded from disk), kick ONE
	// refresh: detached, except the warmer's of a disk-loaded entry, which is
	// inline and awaited (see waitForBuild).
	if e.payload != nil {
		p := e.payload
		if (e.fromDisk || time.Since(e.builtAt) >= c.ttl) && !e.rebuilding {
			e.rebuilding = true
			if isWarmer(ctx) && e.fromDisk {
				c.mu.Unlock()
				if err := c.refresh(ctx, e, file, key, build); err != nil {
					return nil, err
				}
				c.mu.Lock()
				p = e.payload
				c.mu.Unlock()
				if p == nil { // the refresh dropped the copy: build cold, once
					return c.getAt(ctx, file, key, build)
				}
				return p, nil
			}
			go c.refresh(context.Background(), e, file, key, build) //nolint:errcheck // logged inside
		}
		c.mu.Unlock()
		return p, nil
	}

	// Too old to serve while its refresh is still running: that refresh fills
	// the entry, and a second build of the same key would only contend with it.
	if e.rebuilding {
		c.mu.Unlock()
		return nil, errWarming
	}

	// Cold entry: exactly one detached build per key; every caller for the SAME
	// key waits on it, callers for OTHER keys proceed untouched.
	b := e.building
	if b == nil {
		b = &coldBuild{done: make(chan struct{})}
		e.building = b
		bctx, cancel := detachedFrom(ctx)
		go c.buildCold(bctx, cancel, e, b, file, key, build)
	}
	c.mu.Unlock()

	deadline, stop := coldDeadline(ctx)
	defer stop()
	select {
	case <-b.done:
		return b.payload, b.err
	case <-deadline:
		return nil, errWarming
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// buildCold runs one cold build under the process-wide ceiling. Admission
// control sits before the build, never before the lookup, so a hit stays free
// while cold builds queue; a build that gets no slot ends as errWarming.
func (c *swrCache) buildCold(bctx context.Context, cancel context.CancelFunc, e *swrEntry, b *coldBuild,
	file, key string, build func(ctx context.Context) (map[string]any, error),
) {
	defer cancel()
	p, err := map[string]any(nil), errWarming
	defer func() {
		var u uncachedResult
		keep := !errors.As(err, &u)
		if !keep {
			p, err = u.payload, nil // this build's callers get it; the entry does not
		}
		c.mu.Lock()
		e.building = nil
		if err == nil && keep {
			e.payload, e.builtAt, e.fromDisk = p, time.Now(), false
		}
		at := e.builtAt
		c.mu.Unlock()
		if err == nil && keep {
			persistPayload(file, key, at, p)
		} else if err != nil && !errors.Is(err, errWarming) {
			noteRebuildFailure("swr:"+key, err)
		}
		b.payload, b.err = p, err
		close(b.done)
	}()
	if !acquireColdSlot(bctx) {
		return
	}
	// Deferred, not straight-line: a leaked slot would shrink the ceiling for
	// every cache in the process until a restart.
	defer releaseColdSlot()
	defer recoverBuild("swr:"+key, &err)
	p, err = build(bctx)
}

// refresh rebuilds a warm entry: in the background (parent Background) for a
// request, inline for the warmer (parent its context, so shutdown ends it).
// Refreshes read the same connections a cold build does, so they queue behind
// the same ceiling. Losing the slot abandons this refresh (errWarming) — the
// stale copy keeps serving and the next stale hit tries again.
func (c *swrCache) refresh(parent context.Context, e *swrEntry, file, key string,
	build func(ctx context.Context) (map[string]any, error),
) error {
	if !acquireColdSlot(parent) {
		c.mu.Lock()
		e.rebuilding = false
		c.mu.Unlock()
		return errWarming
	}
	defer releaseColdSlot()

	bctx, cancel := context.WithTimeout(parent, detachedBuildTimeout)
	defer cancel()
	var np map[string]any
	err := func() (err error) {
		defer recoverBuild("swr:"+key, &err)
		np, err = build(bctx)
		return err
	}()
	if errors.As(err, new(uncachedResult)) { // answers no one here; the entry stands
		c.mu.Lock()
		e.rebuilding = false
		// ...but not for good. A refusal that keeps recurring (a registry that
		// stays unparsed) would otherwise leave the last published payload up
		// indefinitely; past the limit the copy goes and the next read serves
		// the refusal itself.
		if time.Since(e.builtAt) >= uncachedStandLimit {
			e.payload, e.fromDisk = nil, false
		}
		c.mu.Unlock()
		return nil
	}
	if err != nil {
		c.mu.Lock()
		e.rebuilding = false
		// A proof cache drops a claim it just failed to re-prove. A verify that
		// never ran (its semaphore was full) proved nothing either way, and an
		// anonymous /api/ledger/anchors caller can fill that semaphore, so it is
		// not allowed to evict; maxStale still bounds the copy.
		evict := c.maxStale > 0 && !errors.Is(err, errLedgerVerifyBusy)
		if evict {
			e.payload, e.fromDisk = nil, false
		}
		c.mu.Unlock()
		if evict {
			slog.Warn("proof cache rebuild failed; the cached result is dropped and the next read rebuilds",
				"cache", "swr:"+key, "err", err)
		} else {
			noteRebuildFailure("swr:"+key, err)
		}
		return err
	}
	// Persisted while rebuilding is still set, so two refreshes of one key can
	// never race their writes and leave the older payload on disk.
	at := time.Now()
	persistPayload(file, key, at, np)
	c.mu.Lock()
	e.payload, e.builtAt, e.fromDisk, e.rebuilding = np, at, false, false
	c.mu.Unlock()
	return nil
}

// put stores p under key as freshly built, for a build that knows its result
// also answers a key other than the one it was started for (the ledger verify
// that wrote the anchor its own key names, review #10).
func (c *swrCache) put(key string, p map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.ent[key]
	if e == nil {
		c.evictLRULocked()
		e = &swrEntry{}
		c.ent[key] = e
	}
	e.payload, e.builtAt, e.fromDisk, e.usedSeq = p, time.Now(), false, lruTick()
}

// peek returns key's payload while maxStale still allows serving it, and never
// builds or refreshes anything: for a caller that must not start a build (the
// ledger verify whose key read timed out; that cache is never persisted).
func (c *swrCache) peek(key string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.ent[key]
	if e == nil || e.payload == nil || (c.maxStale > 0 && time.Since(e.builtAt) >= c.maxStale) {
		return nil, false
	}
	return e.payload, true
}

// Shared instances. TTLs sit comfortably under the cadence of the workers that
// change the underlying rows, so staleness is bounded by design:
//   - track-record: outcome resolver runs every 10m → 2m TTL.
//   - regimes: the regime runner writes every 6h → 5m TTL (earnings labels
//     drift by the day, not the minute).
//   - predictions: the prediction runner writes every 10m → 2m TTL.
// uncachedStandLimit is how long a warm copy keeps serving while every rebuild
// of it comes back uncached (a refusal for this moment only, e.g. a torn
// registry read). Five track-record TTLs: a torn read heals within one.
const uncachedStandLimit = 10 * time.Minute

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
	mu      sync.Mutex
	ttl     time.Duration
	ent     map[string]*swrBodyEntry
	created time.Time // see swrCache.created
}

type swrBodyEntry struct {
	builtAt    time.Time
	usedSeq    uint64 // last read; drives LRU eviction (see lruclock.go)
	body       []byte
	fromDisk   bool // body is the persisted last good one: rebuild on first read
	rebuilding bool
	building   *bodyBuild // non-nil while a COLD render is in flight
}

// bodyBuild is one in-flight cold render. status 0 = it never ran (no build
// slot); otherwise the handler's status, headers and body, set before done
// closes, which every waiter replays — a 404 reaches its caller as a 404.
type bodyBuild struct {
	done   chan struct{}
	status int
	header http.Header
	body   []byte
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
	return &swrBodyCache{ttl: ttl, ent: map[string]*swrBodyEntry{}, created: time.Now()}
}

// render runs the handler against a throwaway recorder and returns the body,
// or nil when the handler answered non-200 (errors must never be pinned).
func swrRender(r *http.Request, h http.HandlerFunc) (body []byte) {
	// A background render has no withRecover above it: a panic here would end
	// the daemon, so it ends the render instead.
	defer func() {
		if p := recover(); p != nil {
			slog.Error("cache render panicked", "path", r.URL.Path, "panic", p, "stack", string(debug.Stack()))
			body = nil
		}
	}()
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
	c.serveAt("", key, w, r, h)
}

// serveAt is serve with the key's last good body kept in file across restarts
// (cachepersist.go); file "" keeps the cache in memory only.
func (c *swrBodyCache) serveAt(file, key string, w http.ResponseWriter, r *http.Request, h http.HandlerFunc) {
	c.mu.Lock()
	e := c.ent[key]
	if e == nil {
		c.evictLRULocked()
		e = &swrBodyEntry{}
		if body, at, ok := loadPersistedBody(file, key); ok {
			e.body, e.builtAt, e.fromDisk = body, at, true
		}
		c.ent[key] = e
	}
	e.usedSeq = lruTick()
	if e.fromDisk && time.Since(c.created) >= fromDiskMaxStale {
		e.body, e.fromDisk = nil, false // a previous process's body, past its window
	}

	if e.body != nil {
		body := e.body
		if (e.fromDisk || time.Since(e.builtAt) >= c.ttl) && !e.rebuilding {
			e.rebuilding = true
			if isWarmer(r.Context()) && e.fromDisk { // inline and awaited: see waitForBuild
				c.mu.Unlock()
				c.refresh(r.Context(), r, e, file, key, h)
				c.mu.Lock()
				body = e.body
			} else {
				// Cloned now, while the request is still live; the refresh gives
				// it the detached-with-ceiling context.
				go c.refresh(context.Background(), r.Clone(context.Background()), e, file, key, h)
			}
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Cache", "hit")
		_, _ = w.Write(body)
		return
	}

	// No body to serve while a refresh is still running (its disk body just
	// aged out): that refresh fills the entry, and a second render of the same
	// key would only contend with it.
	if e.rebuilding {
		c.mu.Unlock()
		writeWarming(w)
		return
	}

	// Cold: one detached render per key; same-key callers wait on it, other
	// keys untouched.
	b := e.building
	if b == nil {
		b = &bodyBuild{done: make(chan struct{})}
		e.building = b
		bctx, cancel := detachedFrom(r.Context())
		go c.renderCold(r.Clone(bctx), cancel, e, b, file, key, h)
	}
	c.mu.Unlock()

	deadline, stop := coldDeadline(r.Context())
	defer stop()
	select {
	case <-b.done:
	case <-deadline:
		writeWarming(w)
		return
	case <-r.Context().Done():
		httpErr(w, 504, "cache build in progress; request canceled")
		return
	}
	if b.status == 0 {
		writeWarming(w)
		return
	}
	for k, v := range b.header {
		w.Header()[k] = v
	}
	w.Header().Set("X-Cache", "miss")
	w.WriteHeader(b.status)
	_, _ = w.Write(b.body)
}

// refresh re-renders a warm body: in the background (parent Background) for a
// request, inline for the warmer. Same ceiling as a cold build: a refresh reads
// the same connections. Losing the slot, or a render that is not a 200, keeps
// serving the stale body. The render's context has the detached ceiling: a
// handler re-issued here has no client to disconnect and would otherwise have
// nothing at all to stop it.
func (c *swrBodyCache) refresh(parent context.Context, bg *http.Request, e *swrBodyEntry, file, key string, h http.HandlerFunc) {
	if !acquireColdSlot(parent) {
		c.mu.Lock()
		e.rebuilding = false
		c.mu.Unlock()
		return
	}
	defer releaseColdSlot()

	bctx, cancel := context.WithTimeout(parent, detachedBuildTimeout)
	defer cancel()
	nb := swrRender(bg.WithContext(bctx), h)
	if nb == nil {
		c.mu.Lock()
		e.rebuilding = false
		c.mu.Unlock()
		noteRebuildFailure("swrbody:"+key, bctx.Err())
		return
	}
	at := time.Now() // persisted before rebuilding clears: see swrCache.refresh
	persistBody(file, key, at, nb)
	c.mu.Lock()
	e.body, e.builtAt, e.fromDisk, e.rebuilding = nb, at, false, false
	c.mu.Unlock()
}

// renderCold renders one cold body under the process-wide ceiling (admission
// before the build, never before the lookup) and caches it if it is a 200.
func (c *swrBodyCache) renderCold(bg *http.Request, cancel context.CancelFunc, e *swrBodyEntry, b *bodyBuild,
	file, key string, h http.HandlerFunc,
) {
	defer cancel()
	rec := &bodyRecorder{ResponseWriter: &discardResponseWriter{header: http.Header{}}}
	defer func() {
		if p := recover(); p != nil {
			slog.Error("cache render panicked", "cache", "swrbody:"+key, "panic", p, "stack", string(debug.Stack()))
			rec.status, rec.buf = http.StatusInternalServerError, []byte(`{"error":"internal error"}`+"\n")
			rec.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		ok := rec.status == http.StatusOK
		c.mu.Lock()
		e.building = nil
		if ok {
			e.body, e.builtAt, e.fromDisk = rec.buf, time.Now(), false
		}
		at := e.builtAt
		c.mu.Unlock()
		if ok {
			persistBody(file, key, at, rec.buf)
		}
		b.status, b.header, b.body = rec.status, rec.Header(), rec.buf
		close(b.done)
	}()
	if !acquireColdSlot(bg.Context()) {
		return // status 0: every waiter answers "warming"
	}
	defer releaseColdSlot() // see swrCache.buildCold: never leak a process-wide slot
	h(rec, bg)
	if rec.status == 0 {
		rec.status = http.StatusOK // a handler that wrote nothing still answered 200
	}
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
