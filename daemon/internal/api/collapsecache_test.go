package api

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The cache may only ever make the gate refuse MORE: a pass is recomputed every
// call (so a new collapse refuses at once), a refusal is reused until its TTL.
func TestCollapseCacheNeverCachesAPass(t *testing.T) {
	now := gateNow // inside the graded window, whatever date it opens on
	_, st, d := newTestServer(t, nil)
	path := writeRegistry(t, registryFor(map[string]int{"1d": 3}))
	d.RegistryPath = path
	reg, err := loadRegistry(path)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	d.CollapseCache = &CollapseRefusalCache{TTL: time.Hour}
	ctx := context.Background()
	gate := func() (string, bool, error) {
		return d.collapsedGradingWindowCached(ctx, reg, now)
	}
	for i := 1; i <= 3; i++ {
		seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -i), 300, 180)
	}
	if _, c, err := gate(); err != nil || c {
		t.Fatalf("clean window: collapsed=%v err=%v", c, err)
	}
	// A collapse on a NEW day must refuse at once: the pass above was not cached.
	seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -4), 300, 5)
	reason, c, err := gate()
	if err != nil || !c {
		t.Fatalf("FAIL-OPEN: a cached pass hid a new collapse (collapsed=%v err=%v)", c, err)
	}
	// Within the TTL the refusal is served without touching the store.
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if r2, c2, err := gate(); err != nil || !c2 || r2 != reason {
		t.Fatalf("refusal not served from cache: collapsed=%v err=%v", c2, err)
	}
	// Expired: recomputed, so the closed store now surfaces as an error, not a verdict.
	d.CollapseCache.TTL = 0
	if _, c3, err := gate(); err == nil || c3 {
		t.Fatalf("expired refusal was not recomputed: collapsed=%v err=%v", c3, err)
	}
}
