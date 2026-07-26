package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// The SWR cache exists so a 22-second build can never sit between a user and
// the track-record page more than once per process lifetime. These tests pin
// the three behaviours that contract depends on: cold builds inline, fresh
// serves without rebuilding, stale serves the old payload instantly while ONE
// background rebuild refreshes it.

func TestSWRCache_ColdBuildsInline(t *testing.T) {
	c := newSWRCache(time.Minute)
	builds := 0
	got, err := c.get(context.Background(), "k", func(ctx context.Context) (map[string]any, error) {
		builds++
		return map[string]any{"v": 1}, nil
	})
	if err != nil || got["v"] != 1 || builds != 1 {
		t.Fatalf("cold build: got=%v err=%v builds=%d", got, err, builds)
	}
}

func TestSWRCache_FreshServesWithoutRebuild(t *testing.T) {
	c := newSWRCache(time.Minute)
	builds := 0
	build := func(ctx context.Context) (map[string]any, error) {
		builds++
		return map[string]any{"v": builds}, nil
	}
	_, _ = c.get(context.Background(), "k", build)
	got, _ := c.get(context.Background(), "k", build)
	if builds != 1 || got["v"] != 1 {
		t.Fatalf("fresh hit must not rebuild: builds=%d got=%v", builds, got)
	}
}

func TestSWRCache_StaleServesOldAndRevalidatesOnce(t *testing.T) {
	c := newSWRCache(1 * time.Millisecond) // everything is stale immediately
	var mu sync.Mutex
	builds := 0
	release := make(chan struct{})
	build := func(ctx context.Context) (map[string]any, error) {
		mu.Lock()
		builds++
		n := builds
		mu.Unlock()
		if n > 1 {
			<-release // background rebuilds block until released
		}
		return map[string]any{"v": n}, nil
	}
	_, _ = c.get(context.Background(), "k", build) // cold: v=1
	time.Sleep(5 * time.Millisecond)               // entry now stale

	// A burst of stale hits must all serve the OLD payload instantly and spawn
	// exactly ONE background rebuild between them.
	for i := 0; i < 5; i++ {
		got, err := c.get(context.Background(), "k", build)
		if err != nil || got["v"] != 1 {
			t.Fatalf("stale hit %d must serve old payload: got=%v err=%v", i, got, err)
		}
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := builds
		mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if builds != 2 {
		t.Fatalf("stale burst must trigger exactly one revalidation: builds=%d", builds)
	}
}

func TestSWRCache_ErrorIsNotCached(t *testing.T) {
	c := newSWRCache(time.Minute)
	calls := 0
	failing := func(ctx context.Context) (map[string]any, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("boom")
		}
		return map[string]any{"ok": true}, nil
	}
	if _, err := c.get(context.Background(), "k", failing); err == nil {
		t.Fatal("first call should surface the build error")
	}
	got, err := c.get(context.Background(), "k", failing)
	if err != nil || got["ok"] != true {
		t.Fatalf("error must not be pinned: got=%v err=%v", got, err)
	}
}

func TestSWRCache_ColdBuildDoesNotBlockOtherKeys(t *testing.T) {
	// The production regression this pins: the warmer cold-building one horizon
	// (~40s) must not stall a cache HIT on a different, already-warm horizon.
	c := newSWRCache(time.Minute)
	_, _ = c.get(context.Background(), "warm", func(ctx context.Context) (map[string]any, error) {
		return map[string]any{"v": 1}, nil
	})

	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	go func() {
		_, _ = c.get(context.Background(), "cold", func(ctx context.Context) (map[string]any, error) {
			close(slowStarted)
			<-slowRelease
			return map[string]any{"v": 2}, nil
		})
	}()
	<-slowStarted // the cold build for "cold" is now in flight

	done := make(chan map[string]any, 1)
	go func() {
		got, _ := c.get(context.Background(), "warm", func(ctx context.Context) (map[string]any, error) {
			return map[string]any{"v": 99}, nil
		})
		done <- got
	}()
	select {
	case got := <-done:
		if got["v"] != 1 {
			t.Fatalf("warm hit returned wrong payload: %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("warm-key hit blocked behind another key's cold build")
	}
	close(slowRelease)
}

func TestSWRCache_ConcurrentColdCallersCoalesce(t *testing.T) {
	c := newSWRCache(time.Minute)
	var mu sync.Mutex
	builds := 0
	started := make(chan struct{})
	release := make(chan struct{})
	build := func(ctx context.Context) (map[string]any, error) {
		mu.Lock()
		builds++
		mu.Unlock()
		close(started)
		<-release
		return map[string]any{"v": 7}, nil
	}
	go func() { _, _ = c.get(context.Background(), "k", build) }()
	<-started

	// Followers on the same cold key must WAIT for the in-flight build, not
	// start their own.
	var wg sync.WaitGroup
	results := make([]map[string]any, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = c.get(context.Background(), "k", build)
		}(i)
	}
	time.Sleep(20 * time.Millisecond) // let followers reach the wait
	close(release)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if builds != 1 {
		t.Fatalf("concurrent cold callers must coalesce into one build, got %d", builds)
	}
	for i, r := range results {
		if r["v"] != 7 {
			t.Fatalf("follower %d got wrong payload: %v", i, r)
		}
	}
}

// ── C6: the entry maps are bounded and cold builds are admission-controlled ──
//
// Before the 2026-07-26 fix neither cache ever deleted an entry, so every key
// that reached a build pinned its payload (~12 KB) for the life of the
// process, and there was no limit on how many cold builds could be in flight
// at once — five requests with five distinct query strings took all four of
// the store's read connections and the daemon stopped answering, /api/health
// included. These tests pin both bounds.

func TestSWRCache_EntryMapIsBounded(t *testing.T) {
	c := newSWRCache(time.Minute)
	for i := 0; i < maxCacheEntries*3; i++ {
		k := fmt.Sprintf("junk-%d", i)
		_, _ = c.get(context.Background(), k, func(ctx context.Context) (map[string]any, error) {
			return map[string]any{"v": k}, nil
		})
	}
	c.mu.Lock()
	n := len(c.ent)
	c.mu.Unlock()
	if n > maxCacheEntries {
		t.Fatalf("entry map grew to %d entries; it must be capped at %d or a junk-key flood leaks memory permanently", n, maxCacheEntries)
	}
}

func TestSWRCache_EvictionKeepsTheMostRecentlyUsed(t *testing.T) {
	// Eviction must be LRU, not arbitrary: the hot default entry (the one the
	// warmer keeps alive) must survive a flood of one-shot junk keys, or the
	// attack degrades into "evict the warm entry and make the next real
	// visitor pay the cold build".
	c := newSWRCache(time.Minute)
	build := func(v string) func(context.Context) (map[string]any, error) {
		return func(ctx context.Context) (map[string]any, error) { return map[string]any{"v": v}, nil }
	}
	_, _ = c.get(context.Background(), "hot", build("hot"))
	for i := 0; i < maxCacheEntries*2; i++ {
		k := fmt.Sprintf("junk-%d", i)
		_, _ = c.get(context.Background(), k, build(k))
		// Touch the hot key between junk keys, the way a real visitor would.
		_, _ = c.get(context.Background(), "hot", build("REBUILT"))
	}
	got, _ := c.get(context.Background(), "hot", build("REBUILT"))
	if got["v"] != "hot" {
		t.Fatalf("the continuously-used entry was evicted (got %v); LRU eviction must drop the idle junk keys first", got)
	}
}

func TestSWRBodyCache_EntryMapIsBounded(t *testing.T) {
	c := newSWRBodyCache(time.Minute)
	h := func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }
	for i := 0; i < maxCacheEntries*3; i++ {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/composite/top?zz=%d", i), nil)
		c.serve(fmt.Sprintf("junk-%d", i), httptest.NewRecorder(), req, h)
	}
	c.mu.Lock()
	n := len(c.ent)
	c.mu.Unlock()
	if n > maxCacheEntries {
		t.Fatalf("body-cache entry map grew to %d entries; cap is %d", n, maxCacheEntries)
	}
}

func TestSWRBodyCache_JunkQueryFloodIsOneBuildAndOneEntry(t *testing.T) {
	// The measured attack, end to end through the real key function: the
	// review sent /api/composite/top?zz=1, ?zz=2 and so on and each one was a
	// fresh 28-40s inline build on a read connection. Driving the same
	// requests through compositeTopCacheKey must produce exactly ONE build and
	// exactly ONE entry, because none of those parameters is whitelisted.
	c := newSWRBodyCache(time.Minute)
	builds := 0
	h := func(w http.ResponseWriter, r *http.Request) {
		builds++
		_, _ = w.Write([]byte(`{"rows":[]}`))
	}
	for _, q := range []string{
		"/api/composite/top",
		"/api/composite/top?zz=1",
		"/api/composite/top?zz=2",
		"/api/composite/top?a=1&b=2&c=3&d=4&e=5",
		"/api/composite/top?limit=0&horizon=garbage&market=garbage",
	} {
		req := httptest.NewRequest("GET", q, nil)
		c.serve(compositeTopCacheKey(req), httptest.NewRecorder(), req, h)
	}
	c.mu.Lock()
	n := len(c.ent)
	c.mu.Unlock()
	if builds != 1 || n != 1 {
		t.Fatalf("junk-parameter flood caused %d builds across %d entries; want 1 and 1 — each extra build is a read connection held for ~40s", builds, n)
	}
}

func TestColdBuilds_AreCappedGlobally(t *testing.T) {
	// The pool has four read connections. More than maxConcurrentColdBuilds
	// cold builds must never be in flight at once, no matter how many DISTINCT
	// keys arrive — that headroom is what keeps /api/health answering while a
	// slow page rebuilds.
	c := newSWRCache(time.Minute)
	var mu sync.Mutex
	inFlight, peak := 0, 0
	release := make(chan struct{})
	started := make(chan struct{}, 16)
	build := func(ctx context.Context) (map[string]any, error) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		started <- struct{}{}
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
		return map[string]any{"v": 1}, nil
	}

	const callers = 6
	done := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			_, _ = c.get(context.Background(), fmt.Sprintf("cold-%d", i), build)
		}(i)
	}
	// Let every caller reach the admission gate, then read the peak.
	for i := 0; i < maxConcurrentColdBuilds; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("no cold build was admitted")
		}
	}
	time.Sleep(50 * time.Millisecond) // any over-admission would show up here
	mu.Lock()
	p := peak
	mu.Unlock()
	if p > maxConcurrentColdBuilds {
		t.Fatalf("%d cold builds ran concurrently; the ceiling is %d — a burst of distinct keys must not drain the read pool", p, maxConcurrentColdBuilds)
	}
	close(release)
	for i := 0; i < callers; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("callers did not drain after the builds were released")
		}
	}
}

func TestSWRCache_KeysAreIndependent(t *testing.T) {
	c := newSWRCache(time.Minute)
	mk := func(v int) func(context.Context) (map[string]any, error) {
		return func(ctx context.Context) (map[string]any, error) {
			return map[string]any{"v": v}, nil
		}
	}
	a, _ := c.get(context.Background(), "1d", mk(1))
	b, _ := c.get(context.Background(), "1w", mk(2))
	if a["v"] != 1 || b["v"] != 2 {
		t.Fatalf("keys must not share entries: a=%v b=%v", a, b)
	}
}
