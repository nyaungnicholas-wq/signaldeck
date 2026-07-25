package api

import (
	"context"
	"errors"
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
