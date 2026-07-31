// Timestamp integrity for the pre-registration chain.
//
// The chain's whole claim is temporal: "these gates were frozen BEFORE the
// outcomes were seen". Until now the chain enforced only rowid ordering and
// hash linkage — neither of which says anything about time. AppendPrereg
// passed the caller's Ts straight into the INSERT, and VerifyPrereg only
// recomputed hashes, so a record could carry any registration time at all and
// nothing in the codebase would notice.
//
// One already did. Seq 19 — the discovery-protocol record that freezes the
// grid's alpha, era floors, blind holdout era, fragility probe and atom
// vocabulary — carries ts = 0 (1970-01-01) and was appended AFTER seq 18
// (ts 1785161981). The record whose entire purpose is to prove a freeze
// happened first bears a provably false registration time.
//
// The chain is append-only, so seq 19 cannot be repaired. The two halves here
// are therefore: refuse the next one at append time, and report the existing
// one at verify time, permanently and by name, so a reader sees
// "hash-intact, time-broken at seq 19" rather than either a silent pass or an
// opaque failure.
//
// Neither half gates a number. Appending can only be REFUSED; verification
// only adds a second, independently reported verdict beside the hash verdict.
package prereg

import (
	"errors"
	"fmt"
	"time"
)

// MaxFutureSkew is how far ahead of the writer's own clock a registration
// timestamp may sit before it is treated as fabricated rather than as clock
// drift. A day is generous for skew and still far too small to backdate a
// freeze past an outcome.
const MaxFutureSkew = 24 * time.Hour

// ErrTimestampIntegrity is returned when a candidate record's Ts is not
// strictly after the head record's, or sits outside a plausible window around
// the store's own clock.
var ErrTimestampIntegrity = errors.New("prereg: candidate record has an implausible registration timestamp")

// TimeBreak is one record whose registration timestamp cannot be true.
type TimeBreak struct {
	// Seq is the offending record; PriorSeq is the record it failed to follow
	// in time (0 when the failure is against the clock rather than the head).
	Seq      int64
	PriorSeq int64
	Detail   string
	// Known marks a break that predates this check and is unrepairable because
	// the chain is append-only. It stays reported forever; it is never excused.
	Known bool
}

func (b TimeBreak) String() string {
	if b.Known {
		return fmt.Sprintf("seq %d: %s (KNOWN, unrepairable — append-only)", b.Seq, b.Detail)
	}
	return fmt.Sprintf("seq %d: %s", b.Seq, b.Detail)
}

// KnownTimeBreakSeqs names every time-integrity break already written to the
// live chain before this check existed. They cannot be rewritten, so they are
// disclosed by name rather than hidden or allowed to fail the chain opaquely.
//
// This list is permanent. Adding to it is not a way to make a new violation
// acceptable — the append check refuses those before they can be written.
var KnownTimeBreakSeqs = map[int64]string{
	19: "discovery-protocol record carries ts=0 (1970-01-01) yet was appended after seq 18 (ts 1785161981); " +
		"its registration time is provably false, so its freeze cannot be evidenced by this chain's timestamps",
}

// CheckAppendable decides whether r's registration time can be true given the
// chain in prior and the writer's own clock. It can only refuse a write.
//
// Two independent conditions, both necessary for "registered before the
// outcomes existed" to mean anything:
//
//   - strictly after the head record — equal or retrograde timestamps make the
//     freeze order unreadable, and rowid order is not evidence of time;
//   - inside a plausible window — at or before now+MaxFutureSkew, and not
//     before the chain's own genesis timestamp (which is also what rules out
//     the zero value that seq 19 carries).
func CheckAppendable(prior []Record, r Record, now time.Time) error {
	if r.Ts <= 0 {
		return fmt.Errorf("%w: ts=%d is not a real registration time", ErrTimestampIntegrity, r.Ts)
	}
	if max := now.Add(MaxFutureSkew).Unix(); r.Ts > max {
		return fmt.Errorf("%w: ts=%d is more than %s ahead of the store's clock (%d)",
			ErrTimestampIntegrity, r.Ts, MaxFutureSkew, now.Unix())
	}
	if len(prior) == 0 {
		return nil // genesis has nothing to follow
	}
	genesis := prior[0]
	head := prior[len(prior)-1]
	if r.Ts < genesis.Ts && genesis.Ts > 0 {
		return fmt.Errorf("%w: ts=%d predates the chain's genesis record (seq %d, ts %d)",
			ErrTimestampIntegrity, r.Ts, genesis.Seq, genesis.Ts)
	}
	// Strictly after the head at INSTANT granularity. Comparing seconds here
	// would make freeze order unprovable for the ordinary case of one registrar
	// pass writing many records inside the same second; comparing instants
	// keeps the rule strict while letting the order actually be recorded.
	if r.Instant() <= head.Instant() {
		return fmt.Errorf("%w: ts=%d.%09d is not strictly after the head record (seq %d, ts %d.%09d)",
			ErrTimestampIntegrity, r.Ts, r.TsNanos, head.Seq, head.Ts, head.TsNanos)
	}
	return nil
}

// ChainTimeBreaks walks recs in seq order and reports every record whose
// registration timestamp cannot be true: non-positive, or not strictly after
// the newest timestamp seen before it. Breaks listed in KnownTimeBreakSeqs are
// reported with Known set — disclosed, not excused.
//
// This is deliberately independent of the hash verdict. A chain can be
// perfectly hash-intact and still make a false claim about WHEN something was
// frozen, and a reader is entitled to see both answers.
func ChainTimeBreaks(recs []Record) []TimeBreak {
	var out []TimeBreak
	var highest int64
	var highestSeq int64
	for _, r := range recs {
		var detail string
		switch {
		case r.Ts <= 0:
			detail = fmt.Sprintf("ts=%d is not a real registration time", r.Ts)
		case highestSeq != 0 && r.Instant() <= highest:
			detail = fmt.Sprintf("ts=%d.%09d is not after the timestamp of seq %d (ts %d)",
				r.Ts, r.TsNanos, highestSeq, highest/1_000_000_000)
		}
		if detail != "" {
			b := TimeBreak{Seq: r.Seq, PriorSeq: highestSeq, Detail: detail}
			if known, ok := KnownTimeBreakSeqs[r.Seq]; ok {
				b.Known, b.Detail = true, known
			}
			out = append(out, b)
		}
		if r.Instant() > highest {
			highest, highestSeq = r.Instant(), r.Seq
		}
	}
	return out
}
