package api

import (
	"context"
	"strings"
	"sync"
	"time"
)

// CollapseRefusalCache memoizes a REFUSED collapse verdict, never a pass.
//
// collapsedGradingWindow window-sorts every resolved forecast since the grading
// epoch (~130k rows per weekly horizon, ~1.4s measured 2026-09-30) and the
// homepage calls it on every render. Caching only the refusal keeps the gate's
// fail-closed direction: a stale refusal can only over-refuse, which the gate
// already accepts (it reads a superset of the graded days on purpose), while a
// pass is recomputed every call so a newly collapsed day can never publish late.
// The window starts at a constant epoch, so a refusal clears only by
// re-registration — a code change and restart, which drops this cache anyway.
//
// nil (tests, the publication gate tool) means no caching.
// ponytail: one entry keyed on the registry's horizons; per-key map if callers diverge.
type CollapseRefusalCache struct {
	TTL time.Duration

	mu     sync.Mutex
	key    string
	reason string
	at     time.Time
}

func (d Deps) collapsedGradingWindowCached(ctx context.Context, reg *registryFile, now time.Time) (string, bool, error) {
	c := d.CollapseCache
	if c == nil {
		return d.collapsedGradingWindow(ctx, reg, now)
	}
	var hs []string
	for _, r := range reg.Rows {
		hs = append(hs, r.Predictor)
	}
	key := reg.GradedAt + "|" + strings.Join(hs, ",")
	c.mu.Lock()
	if c.key == key && time.Since(c.at) < c.TTL {
		reason := c.reason
		c.mu.Unlock()
		return reason, true, nil
	}
	c.mu.Unlock()
	reason, collapsed, err := d.collapsedGradingWindow(ctx, reg, now)
	c.mu.Lock()
	if err == nil && collapsed {
		c.key, c.reason, c.at = key, reason, time.Now()
	} else {
		c.key = "" // a pass or an error must not leave an old refusal behind
	}
	c.mu.Unlock()
	return reason, collapsed, err
}
