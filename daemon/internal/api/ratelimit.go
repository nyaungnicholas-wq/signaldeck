package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is an in-memory token-bucket limiter keyed per client, with two
// tiers: reads (GET/HEAD) and writes (POST + all /api/ai/*). Buckets are
// evicted lazily once they've been idle long enough to be full again.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time

	readRPS, readBurst   float64
	writeRPS, writeBurst float64
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter builds the limiter; rps/burst <= 0 fall back to defaults
// (reads 10 req/s burst 30; writes 2 req/s burst 5).
func newRateLimiter(rps, burst int) *rateLimiter {
	rl := &rateLimiter{
		buckets:   map[string]*bucket{},
		lastSweep: time.Now(),
		readRPS:   10, readBurst: 30,
		writeRPS: 2, writeBurst: 5,
	}
	if rps > 0 {
		rl.readRPS = float64(rps)
		rl.writeRPS = float64(rps) / 5
		if rl.writeRPS < 1 {
			rl.writeRPS = 1
		}
	}
	if burst > 0 {
		rl.readBurst = float64(burst)
		rl.writeBurst = float64(burst) / 6
		if rl.writeBurst < 2 {
			rl.writeBurst = 2
		}
	}
	return rl
}

// allow consumes one token from the client's bucket for the given tier.
func (rl *rateLimiter) allow(key string, write bool) bool {
	rps, burst := rl.readRPS, rl.readBurst
	tier := "r:"
	if write {
		rps, burst = rl.writeRPS, rl.writeBurst
		tier = "w:"
	}
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if now.Sub(rl.lastSweep) > 5*time.Minute {
		rl.sweep(now)
	}
	b, ok := rl.buckets[tier+key]
	if !ok {
		b = &bucket{tokens: burst, last: now}
		rl.buckets[tier+key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rps
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets idle long enough to be full again (called under mu).
func (rl *rateLimiter) sweep(now time.Time) {
	rl.lastSweep = now
	for k, b := range rl.buckets {
		if now.Sub(b.last) > 2*time.Minute {
			delete(rl.buckets, k)
		}
	}
}

// clientKey identifies the caller for rate limiting: session user id first,
// then bearer token, then remote IP (X-Forwarded-For only behind a trusted
// proxy, and only its first — client — hop).
func (d Deps) clientKey(r *http.Request, uid int64) string {
	if uid != 0 {
		return "u:" + itoa(uid)
	}
	if tokenEqual(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), d.Cfg.APIToken) {
		return "tok"
	}
	if d.Cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return "ip:" + ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
