// LAYER 4 — budget, with latency as a hard requirement.
//
// Three bounds, in increasing time horizon:
//
//	per second  — the daemon's EXISTING token bucket (internal/api's
//	              rateLimiter), injected rather than reimplemented so there is
//	              one limiter in this process, not two that drift.
//	per response — a result cap, applied inside each tool, so one call cannot
//	              return the universe.
//	per day     — a rolling aggregate of calls and bytes per client, which is
//	              the bound that actually makes extraction expensive: a
//	              rate limit alone only decides how long the extraction takes.
//
// Latency is a requirement of this layer, not a hope. The limiter is in-memory
// with one mutex and no allocation on the hot path, and verdicts are served
// from a precomputed cache (cache.go) refreshed off-request, so a tool call is
// a map lookup rather than a database query. budget_test.go measures the
// limiter's own overhead and fails if it exceeds a microsecond budget.
package mcp

import (
	"sync"
	"time"
)

const (
	defaultDailyCallCap = 500
	// defaultDailyByteCap bounds total disclosure per client per day. 2 MB of
	// banded verdicts is far more than any advisory use needs and far less
	// than a dataset.
	defaultDailyByteCap = 2 << 20
	defaultResultCap    = 25
	// throttleFactor is what an anomaly flag costs: the client's remaining
	// daily allowance is cut to this fraction of the cap, immediately.
	throttleFactor = 0.25
	// throttleCooldown is how long a flagged client stays throttled with no
	// further anomalies.
	throttleCooldown = 6 * time.Hour
)

// decision is the outcome of an admission check.
type decision struct {
	allowed    bool
	reason     string // rate | daily-calls | daily-bytes
	message    string
	retryAfter int
}

var admitted = decision{allowed: true}

// clientBudget is one client's rolling state. Kept small and pointer-stable so
// the hot path touches one cache line's worth of counters under one mutex.
type clientBudget struct {
	day        int64 // unix day the counters belong to
	calls      int
	bytes      int
	throttled  bool
	throttleAt time.Time
	flags      []string
	lastSeen   time.Time
}

type budgetKeeper struct {
	mu      sync.Mutex
	clients map[string]*clientBudget

	allow func(key string, write bool) bool
	audit *auditor
	now   func() time.Time

	callCap   int
	byteCap   int
	lastSweep time.Time
}

func newBudgetKeeper(opts Options, allow func(string, bool) bool, audit *auditor, now func() time.Time) *budgetKeeper {
	return &budgetKeeper{
		clients:   map[string]*clientBudget{},
		allow:     allow,
		audit:     audit,
		now:       now,
		callCap:   opts.dailyCallCap(),
		byteCap:   opts.dailyByteCap(),
		lastSweep: now(),
	}
}

// admit runs the per-second and per-day checks. It is called before any work
// is done for a request, so an over-budget client cannot make the server think.
//
// Every tool call is charged to the WRITE tier of the shared limiter. An MCP
// call is not a cheap page read: it is an automated agent's request, and the
// write tier's tighter bucket is the honest classification of what it costs.
func (b *budgetKeeper) admit(clientID string) decision {
	if !b.allow("mcp:"+clientID, true) {
		return decision{reason: "rate",
			message:    "rate limit exceeded for this MCP client",
			retryAfter: 1}
	}
	now := b.now()
	day := now.UTC().Unix() / 86400

	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Sub(b.lastSweep) > time.Hour {
		b.sweep(now)
	}
	c := b.clients[clientID]
	if c == nil {
		c = &clientBudget{day: day}
		b.clients[clientID] = c
	}
	if c.day != day {
		// New UTC day: counters reset, but a throttle does NOT — an
		// extraction attempt should not be laundered by midnight.
		c.day, c.calls, c.bytes = day, 0, 0
	}
	if c.throttled && now.Sub(c.throttleAt) > throttleCooldown {
		c.throttled = false
	}
	c.lastSeen = now

	callCap, byteCap := b.callCap, b.byteCap
	if c.throttled {
		callCap = int(float64(callCap) * throttleFactor)
		byteCap = int(float64(byteCap) * throttleFactor)
	}
	if c.calls >= callCap {
		return decision{reason: "daily-calls",
			message: "daily call budget exhausted for this MCP client" + throttleSuffix(c),
			// Seconds until the next UTC day, so a client waits rather than spins.
			retryAfter: secondsToNextDay(now)}
	}
	if c.bytes >= byteCap {
		return decision{reason: "daily-bytes",
			message:    "daily response-volume budget exhausted for this MCP client" + throttleSuffix(c),
			retryAfter: secondsToNextDay(now)}
	}
	c.calls++
	return admitted
}

func throttleSuffix(c *clientBudget) string {
	if !c.throttled {
		return ""
	}
	return " (reduced: this client's usage matched an extraction pattern)"
}

func secondsToNextDay(now time.Time) int {
	u := now.UTC()
	next := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	s := int(next.Sub(u).Seconds())
	if s < 1 {
		return 1
	}
	return s
}

// charge records the bytes actually served.
func (b *budgetKeeper) charge(clientID string, n int) {
	b.mu.Lock()
	if c := b.clients[clientID]; c != nil {
		c.bytes += n
	}
	b.mu.Unlock()
}

// throttle marks a client whose usage matched an extraction pattern. It is
// additive with the daily cap rather than a replacement for it, and it is
// recorded in the audit sink so the decision is reviewable.
func (b *budgetKeeper) throttle(clientID, flag string) {
	now := b.now()
	b.mu.Lock()
	c := b.clients[clientID]
	if c == nil {
		c = &clientBudget{day: now.UTC().Unix() / 86400}
		b.clients[clientID] = c
	}
	c.throttled = true
	c.throttleAt = now
	c.flags = append(c.flags, flag)
	b.mu.Unlock()
	// Every flag is recorded, not only the first: a client that trips a second
	// distinct pattern is telling you something the first entry does not.
	b.audit.record(auditEntry{Client: clientID, Outcome: "auto-throttled", Anomaly: flag, At: now})
}

// state reports a client's counters. Tests and the operator status view only.
func (b *budgetKeeper) state(clientID string) (calls, bytes int, throttled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.clients[clientID]
	if c == nil {
		return 0, 0, false
	}
	return c.calls, c.bytes, c.throttled
}

// sweep drops clients unseen for two days (called under mu). A throttled
// client is kept: forgetting it is how a slow extractor resets its record.
func (b *budgetKeeper) sweep(now time.Time) {
	b.lastSweep = now
	for k, c := range b.clients {
		if !c.throttled && now.Sub(c.lastSeen) > 48*time.Hour {
			delete(b.clients, k)
		}
	}
}
