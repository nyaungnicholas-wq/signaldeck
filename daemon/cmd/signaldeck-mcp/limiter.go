package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"sync"
	"time"
)

// The stdio process has no daemon to borrow from, so it carries the same two
// primitives locally: a token bucket with the daemon's write-tier defaults, and
// the same constant-time compare (hash both sides to a fixed length first, so
// neither content nor LENGTH leaks through timing).

const (
	localRPS   = 2.0
	localBurst = 5.0
)

type localBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newLocalLimiter() func(string, bool) bool {
	b := &localBucket{tokens: localBurst, last: time.Now()}
	return func(string, bool) bool {
		now := time.Now()
		b.mu.Lock()
		defer b.mu.Unlock()
		b.tokens += now.Sub(b.last).Seconds() * localRPS
		if b.tokens > localBurst {
			b.tokens = localBurst
		}
		b.last = now
		if b.tokens < 1 {
			return false
		}
		b.tokens--
		return true
	}
}

func constantTimeEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	hg := sha256.Sum256([]byte(got))
	hw := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(hg[:], hw[:]) == 1
}
