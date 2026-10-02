package api

import (
	"context"
	"strings"
	"sync"
	"time"
)

// CollapseVerdictCache memoizes the collapse verdict, pass OR refusal, keyed on
// the identity of the data the gate reads.
//
// It used to cache only a refusal and recompute every pass "so a newly
// collapsed day can never publish late". That kept the gate exact but put its
// whole cost on every /api/accuracy and homepage track-record call: measured on
// the 2026-10-01 snapshot the gate is ~100 ms of a ~106 ms handler, and under
// host CPU starvation (2026-10-02 01:00 and after the 03:30 deploy) the same
// work ran past the web's 6 s fetch timeout, so visitors were told the grader
// was out.
//
// The gate's entire input is the set of gated horizons (gatedHorizons) and, per
// horizon, the resolved rows ForecastDayStats reads. The key is exactly that:
// each horizon with store.ResolvedOutcomeFingerprint over the same rows. A new
// resolution (or a quarantined label) changes the key, so the gate re-runs and
// can refuse on the very next read; nothing ages out on a clock, so there is no
// TTL. The fingerprint is read BEFORE the gate: if rows land in between, the
// verdict is stored under the older key and the next read recomputes.
//
// nil (tests, the publication gate tool) means no caching.
// ponytail: one entry; a per-key map if callers ever pass different registries.
type CollapseVerdictCache struct {
	mu        sync.Mutex
	key       string
	reason    string
	collapsed bool
}

func (d Deps) collapsedGradingWindowCached(ctx context.Context, reg *registryFile, now time.Time) (string, bool, error) {
	c := d.CollapseCache
	hs := gatedHorizons(reg)
	if c == nil || len(hs) == 0 {
		return d.collapsedGradingWindow(ctx, reg, now)
	}
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		fp, err := d.St.ResolvedOutcomeFingerprint(ctx, h, gateSince())
		if err != nil {
			// Unidentifiable data: run the gate itself and cache nothing.
			return d.collapsedGradingWindow(ctx, reg, now)
		}
		parts = append(parts, h+"="+fp)
	}
	key := strings.Join(parts, ";")
	c.mu.Lock()
	if c.key == key {
		reason, collapsed := c.reason, c.collapsed
		c.mu.Unlock()
		return reason, collapsed, nil
	}
	c.mu.Unlock()
	reason, collapsed, err := d.collapsedGradingWindow(ctx, reg, now)
	if err != nil {
		// Not cached: the entry left in place is keyed to its own data.
		return reason, collapsed, err
	}
	c.mu.Lock()
	c.key, c.reason, c.collapsed = key, reason, collapsed
	c.mu.Unlock()
	return reason, collapsed, nil
}
