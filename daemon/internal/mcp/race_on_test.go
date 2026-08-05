//go:build race

package mcp

import "time"

// admissionBudget is the per-call ceiling TestLimiterOverheadIsSubMillisecond
// holds admit() to, relaxed here because the race detector instruments every
// memory access: admission measures 61ns natively and 5.256µs under -race, 86x,
// against the 1µs native budget. CI runs `go test -race ./...`, so the native
// figure cannot be asserted there.
//
// Relaxed rather than skipped. A number measured under the detector says
// nothing about production latency, but the shape of a real regression — a lock
// added to the hot path, an allocation per call — still shows up as orders of
// magnitude, and 100x the native budget catches that while absorbing the
// detector's own cost and the spread between CI runners.
const admissionBudget = 100 * time.Microsecond
