// LAYER 4 (latency half) — verdicts are served from a precomputed snapshot,
// never computed per request.
//
// The forecasts this server exposes are written by the daemon's existing
// regime worker every few hours; nothing about them changes between two MCP
// calls a second apart. So a call is a read from an in-memory map behind an
// RWMutex, and the only database work happens on a refresh that at most one
// goroutine performs per TTL.
//
// A stale snapshot is preferred to a slow one AND to an empty one: if the
// refresh fails while a snapshot exists, the existing snapshot is served with
// its own as-of date attached, because the date is what makes staleness
// visible to the caller rather than silent.
package mcp

import (
	"context"
	"strings"
	"sync"
	"time"
)

// verdictTTL is how long a snapshot is served before a refresh is attempted.
// The producing worker runs on a multi-hour cadence, so this is short.
const verdictTTL = 60 * time.Second

type verdictCache struct {
	src Source
	now func() time.Time

	mu       sync.RWMutex
	bySymbol map[string][]Verdict
	builtAt  time.Time
	ok       bool

	refresh sync.Mutex // serialises refreshes; readers never wait on it
}

func newVerdictCache(src Source, now func() time.Time) *verdictCache {
	return &verdictCache{src: src, now: now, bySymbol: map[string][]Verdict{}}
}

// lookup returns one symbol's verdicts. Note what it does NOT offer: no
// listing, no prefix match, no "nearest symbol", no count. The only question
// answerable here is "what is the current verdict for this exact symbol",
// which is the question the tool surface promises and nothing more.
func (c *verdictCache) lookup(ctx context.Context, symbol string) ([]Verdict, error) {
	c.mu.RLock()
	fresh := c.ok && c.now().Sub(c.builtAt) < verdictTTL
	if fresh {
		v := c.bySymbol[symbol]
		c.mu.RUnlock()
		return v, nil
	}
	c.mu.RUnlock()

	if err := c.rebuild(ctx); err != nil {
		c.mu.RLock()
		defer c.mu.RUnlock()
		if c.ok {
			return c.bySymbol[symbol], nil // stale beats nothing; asOfDay says so
		}
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bySymbol[symbol], nil
}

func (c *verdictCache) rebuild(ctx context.Context) error {
	c.refresh.Lock()
	defer c.refresh.Unlock()
	// Another goroutine may have rebuilt while this one waited.
	c.mu.RLock()
	if c.ok && c.now().Sub(c.builtAt) < verdictTTL {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	rows, err := c.src.Verdicts(ctx)
	if err != nil {
		return err
	}
	next := make(map[string][]Verdict, len(rows))
	for _, r := range rows {
		s := strings.ToUpper(r.Symbol)
		next[s] = append(next[s], r)
	}
	c.mu.Lock()
	c.bySymbol, c.builtAt, c.ok = next, c.now(), true
	c.mu.Unlock()
	return nil
}
