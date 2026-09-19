package api

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// screenerCache memoizes the global /api/screener rows for a short TTL. The
// screener is user-independent market data over the whole active universe, so
// like the dashboard's global sections it is safe to share across requests.
//
// Why it matters (2026-07-20): even with the batched reads the build touches
// the large cold bars/scores tables, and during the heavy once-daily
// signal-runner universe pass an uncached build gets CPU-starved to tens of
// seconds. Serving from a 45s cache makes repeat loads instant and shields the
// endpoint from those load spikes. The builder runs UNDER the lock so a burst
// of first-hits rebuilds exactly once instead of stampeding the store.
type screenerCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	builtAt time.Time
	rows    []watchRow
}

var screenerCacheG = &screenerCache{ttl: 45 * time.Second}

// sharedScreenerSWR sits in FRONT of screenerCacheG at the route: the memo
// above still coalesces rebuilds, but when its TTL lapses the next visitor no
// longer waits for the rebuild inline (31-49s under load, 2026-09-08) — the
// stale body is served and the rebuild runs in the background. WarmCaches keeps
// the entry hot.
var sharedScreenerSWR = newSWRBodyCache(60 * time.Second)

// sharedSymbolSWR body-caches GET /api/symbol per market|symbol (LRU, 64
// entries). The symbol page fetches it on mount, so a visitor must not be the
// one paying a cold build under worker load.
var sharedSymbolSWR = newSWRBodyCache(60 * time.Second)

// symbolCacheKey normalises the query the handler resolves (market + symbol),
// so ?symbol=spy and ?symbol=SPY share one entry.
func symbolCacheKey(r *http.Request) string {
	q := r.URL.Query()
	return strings.ToUpper(strings.TrimSpace(q.Get("market"))) + "|" + strings.ToUpper(strings.TrimSpace(q.Get("symbol")))
}

// get returns the cached rows when fresh, otherwise rebuilds via build() while
// holding the lock (so concurrent callers wait for and share the one rebuild).
func (c *screenerCache) get(build func() ([]watchRow, error)) ([]watchRow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows != nil && time.Since(c.builtAt) < c.ttl {
		return c.rows, nil
	}
	rows, err := build()
	if err != nil {
		return nil, err
	}
	c.rows, c.builtAt = rows, time.Now()
	return rows, nil
}
