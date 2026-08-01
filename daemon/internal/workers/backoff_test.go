package workers

import (
	"testing"
	"time"
)

// A long-running worker (Interval()==0) is restarted by loop() after a cooldown.
// That cooldown was a FIXED 5 seconds with no cap and no jitter, and the comment
// above it claimed "its own internals do finer backoff" -- streamer.go contains
// no backoff whatsoever, so nothing anywhere widened the interval.
//
// Measured during the 2026-07-31 audit: with tickstream down, crypto-live
// produced roughly sixty worker_runs rows in sixty-eight seconds and would have
// continued at that rate indefinitely. Against a market-data vendor rather than
// a local service, that is how an outage turns into a rate-limit or an IP ban --
// the client hammers hardest at exactly the moment the provider is least able to
// answer.
//
// These assert RANGES, not exact values. An earlier draft of this test demanded
// both jitter and streamBackoff(40) == streamBackoff(60); those are mutually
// exclusive, and it also asserted strict monotonicity across attempts, which two
// overlapping jittered ranges violate at random. Randomised policies have to be
// tested by their bounds.
const (
	backoffFloor = time.Second
	backoffCap   = 2 * time.Minute
)

// sample returns the min and max of many draws at one attempt number.
func sample(attempt int) (lo, hi time.Duration) {
	lo = time.Duration(1<<62 - 1)
	for i := 0; i < 200; i++ {
		d := streamBackoff(attempt)
		if d < lo {
			lo = d
		}
		if d > hi {
			hi = d
		}
	}
	return lo, hi
}

func TestStreamBackoffStaysWithinBounds(t *testing.T) {
	for _, attempt := range []int{0, 1, 3, 6, 12, 40, 60, 1000} {
		lo, hi := sample(attempt)
		if lo < backoffFloor {
			t.Errorf("attempt %d can return %v, below the %v floor: an outage must not be "+
				"met with an effectively instant redial", attempt, lo, backoffFloor)
		}
		if hi > backoffCap {
			t.Errorf("attempt %d can return %v, above the %v cap: a recovered provider must "+
				"be retried promptly, not hours later", attempt, hi, backoffCap)
		}
	}
}

// It must actually escalate. If attempt 6 can still be as short as attempt 0,
// this is a fixed cooldown wearing a backoff costume -- the original bug.
func TestStreamBackoffEscalates(t *testing.T) {
	_, hi0 := sample(0)
	lo6, _ := sample(6)
	if lo6 <= hi0 {
		t.Errorf("attempt 6 can be as short as %v while attempt 0 reaches %v -- failures are "+
			"not lengthening the wait", lo6, hi0)
	}
}

// And it must settle. Once capped, further failures must not push the interval
// toward infinity, or a long outage means the stream never returns on its own.
//
// Asserted against the cap's FIXED band, not against another sample's extremes.
// The first version compared sample(60)'s maximum to sample(40)'s and failed
// roughly half the time for no reason: once both attempts are capped they draw
// from the identical distribution, so whichever sample happens to contain the
// larger draw is a coin flip. Comparing two random samples to each other is not
// a property test, it is a race.
func TestStreamBackoffCapHolds(t *testing.T) {
	for _, attempt := range []int{40, 60, 1000} {
		lo, hi := sample(attempt)
		if lo < backoffCap/2 || hi > backoffCap {
			t.Errorf("attempt %d spans [%v,%v]; once capped every draw must sit inside "+
				"[%v,%v] -- outside that band the cap is not holding and a long outage "+
				"pushes the retry interval away from any bound",
				attempt, lo, hi, backoffCap/2, backoffCap)
		}
	}
}

// Jitter: if every stream worker uses the identical schedule they all redial in
// the same instant after a shared outage -- the thundering herd a backoff is
// half-meant to prevent.
func TestStreamBackoffIsJittered(t *testing.T) {
	lo, hi := sample(5)
	if lo == hi {
		t.Errorf("200 draws at attempt 5 all returned %v: with no jitter, every reconnecting "+
			"worker redials in the same instant after a shared outage", lo)
	}
}

// The reset matters as much as the growth: a stream that held for hours and then
// dropped must be retried quickly, not at the interval its last bad day ended on.
func TestStreamBackoffStartsShort(t *testing.T) {
	_, hi0 := sample(0)
	if hi0 > 10*time.Second {
		t.Errorf("attempt 0 can wait %v; a first retry after a healthy session must be prompt", hi0)
	}
}
