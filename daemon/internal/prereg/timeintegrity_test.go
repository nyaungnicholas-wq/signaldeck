package prereg

import (
	"errors"
	"testing"
	"time"
)

// An append whose Ts cannot be true must be refused, for every kind. These are
// the exact two shapes the live chain already contains or could next contain:
// the zero timestamp seq 19 carries, and a timestamp that does not advance.
func TestCheckAppendable_RefusesImplausibleTimestamps(t *testing.T) {
	now := time.Now()
	prior := []Record{
		{Seq: 1, Ts: now.Add(-48 * time.Hour).Unix(), Kind: "trend21"},
		{Seq: 2, Ts: now.Add(-24 * time.Hour).Unix(), Kind: "vol21"},
	}
	head := prior[len(prior)-1]

	cases := []struct {
		name string
		ts   int64
	}{
		{"zero timestamp, as seq 19 carries", 0},
		{"negative timestamp", -1},
		{"equal to the head record", head.Ts},
		{"before the head record", head.Ts - 1},
		{"before the chain's genesis", prior[0].Ts - 1},
		{"far in the future", now.Add(72 * time.Hour).Unix()},
	}
	for _, c := range cases {
		err := CheckAppendable(prior, Record{Ts: c.ts, Kind: "trend21", SpecJSON: `{}`}, now)
		if !errors.Is(err, ErrTimestampIntegrity) {
			t.Errorf("%s: CheckAppendable(ts=%d) = %v; want refusal", c.name, c.ts, err)
		}
	}

	// A timestamp that genuinely advances is still accepted — the check can
	// only refuse, never invent a reason to block a legitimate registration.
	if err := CheckAppendable(prior, Record{Ts: head.Ts + 1, Kind: "trend21", SpecJSON: `{}`}, now); err != nil {
		t.Errorf("advancing append refused: %v", err)
	}
	// Sub-second position is part of the order, not an escape from it: a
	// candidate in the head's own second is accepted only when it is strictly
	// LATER within that second, and refused when it is equal or earlier.
	subPrior := append(append([]Record{}, prior...), Record{Seq: 3, Ts: head.Ts, TsNanos: 500, Kind: "vol21"})
	if err := CheckAppendable(subPrior, Record{Ts: head.Ts, TsNanos: 501, Kind: "trend21", SpecJSON: `{}`}, now); err != nil {
		t.Errorf("sub-second advancing append refused: %v", err)
	}
	for _, nanos := range []int64{500, 499, 0} {
		err := CheckAppendable(subPrior, Record{Ts: head.Ts, TsNanos: nanos, Kind: "trend21", SpecJSON: `{}`}, now)
		if !errors.Is(err, ErrTimestampIntegrity) {
			t.Errorf("non-advancing sub-second append (nanos=%d) = %v; want refusal", nanos, err)
		}
	}

	// Genesis has nothing to follow, so only the clock bounds apply.
	if err := CheckAppendable(nil, Record{Ts: now.Unix(), Kind: "trend21", SpecJSON: `{}`}, now); err != nil {
		t.Errorf("genesis append refused: %v", err)
	}
}

// Verification reports time breaks independently of hashes, and the known,
// unrepairable break stays visible by name forever.
func TestChainTimeBreaks_ReportsAndNamesKnownBreak(t *testing.T) {
	recs := []Record{
		{Seq: 18, Ts: 1785161981},
		{Seq: 19, Ts: 0},
		{Seq: 20, Ts: 1785161990},
	}
	breaks := ChainTimeBreaks(recs)
	if len(breaks) != 1 || breaks[0].Seq != 19 || !breaks[0].Known {
		t.Fatalf("ChainTimeBreaks = %+v; want the known break at seq 19", breaks)
	}
	if _, ok := KnownTimeBreakSeqs[19]; !ok {
		t.Error("seq 19 must stay listed as a known, disclosed time-integrity break")
	}
	if len(ChainTimeBreaks([]Record{{Seq: 1, Ts: 10}, {Seq: 2, Ts: 20}})) != 0 {
		t.Error("a monotone chain must report no time breaks")
	}
	// An unknown, non-monotone record is reported as a plain (not excused) break.
	got := ChainTimeBreaks([]Record{{Seq: 1, Ts: 20}, {Seq: 2, Ts: 20}})
	if len(got) != 1 || got[0].Seq != 2 || got[0].Known {
		t.Fatalf("non-monotone chain = %+v; want an unknown break at seq 2", got)
	}
}
