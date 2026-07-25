package datasetver

import (
	"testing"
)

const day = int64(86400)

func rows(n int, closeAt func(i int) float64) []Row {
	out := make([]Row, n)
	for i := 0; i < n; i++ {
		c := closeAt(i)
		out[i] = Row{Ts: int64(i) * day, Open: c - 1, High: c + 1, Low: c - 2, Close: c, Volume: 1000}
	}
	return out
}

func TestHashIsStableAndOrderIndependent(t *testing.T) {
	rs := rows(100, func(i int) float64 { return 100 + float64(i) })
	a := Hash("AAPL", "1d", rs)
	b := Hash("AAPL", "1d", rs)
	if a.Hash != b.Hash {
		t.Fatal("hash is not deterministic across calls")
	}
	// Reversed retrieval order must not change the identity of the data.
	rev := make([]Row, len(rs))
	for i := range rs {
		rev[len(rs)-1-i] = rs[i]
	}
	if c := Hash("AAPL", "1d", rev); c.Hash != a.Hash {
		t.Fatal("hash depends on row order")
	}
	if a.N != 100 || a.FirstTs != 0 || a.LastTs != 99*day {
		t.Fatalf("bounds wrong: %+v", a)
	}
	// Hashing must not mutate the caller's slice.
	if rs[0].Ts != 0 {
		t.Fatal("input slice was reordered")
	}
}

// The point of the whole package: one revised close anywhere must change the
// identity of the dataset.
func TestSingleRevisedCloseChangesHash(t *testing.T) {
	rs := rows(100, func(i int) float64 { return 100 + float64(i) })
	base := Hash("AAPL", "1d", rs)
	rs[42].Close += 0.01
	if got := Hash("AAPL", "1d", rs); got.Hash == base.Hash {
		t.Fatal("a revised close did not change the hash")
	}
}

// Identity is bound to symbol and timeframe: the same numbers under a different
// label are a different dataset.
func TestIdentityIsBoundToSymbolAndTimeframe(t *testing.T) {
	rs := rows(50, func(i int) float64 { return 100 })
	a := Hash("AAPL", "1d", rs)
	if b := Hash("MSFT", "1d", rs); b.Hash == a.Hash {
		t.Fatal("different symbols hashed identically")
	}
	if c := Hash("AAPL", "1h", rs); c.Hash == a.Hash {
		t.Fatal("different timeframes hashed identically")
	}
}

// Float formatting must normalize representation noise but preserve real
// differences.
func TestFormattingNormalizesNoiseNotInformation(t *testing.T) {
	a := Hash("X", "1d", []Row{{Ts: 0, Close: 1.0, Open: 1, High: 1, Low: 1, Volume: 1}})
	b := Hash("X", "1d", []Row{{Ts: 0, Close: 1.00, Open: 1, High: 1, Low: 1, Volume: 1}})
	if a.Hash != b.Hash {
		t.Fatal("1.0 and 1.00 hashed differently")
	}
	c := Hash("X", "1d", []Row{{Ts: 0, Close: 1.000001, Open: 1, High: 1, Low: 1, Volume: 1}})
	if c.Hash == a.Hash {
		t.Fatal("a real difference at the 6th decimal was normalized away")
	}
}

func TestEmptyDatasetHasStableIdentity(t *testing.T) {
	a := Hash("AAPL", "1d", nil)
	b := Hash("AAPL", "1d", []Row{})
	if a.Hash != b.Hash || a.Hash == "" {
		t.Fatalf("empty identity unstable: %q vs %q", a.Hash, b.Hash)
	}
	if c := Hash("MSFT", "1d", nil); c.Hash == a.Hash {
		t.Fatal("empty datasets for different symbols hashed identically")
	}
	if a.N != 0 || a.FirstTs != 0 {
		t.Fatalf("empty version = %+v", a)
	}
}

// Appending new bars is benign; rewriting old ones is not. Compare must tell
// them apart, because they mean opposite things about a stored claim.
func TestCompareDistinguishesExtensionFromRevision(t *testing.T) {
	orig := rows(100, func(i int) float64 { return 100 + float64(i) })
	stored := Hash("AAPL", "1d", orig)

	// Case 1: identical.
	if r := Compare(stored, Hash("AAPL", "1d", orig), stored.Hash); r.Changed {
		t.Fatalf("identical data reported as changed: %+v", r)
	}

	// Case 2: extension — 20 new bars appended, history untouched.
	extended := append(append([]Row(nil), orig...), rows(120, func(i int) float64 { return 100 + float64(i) })[100:]...)
	fresh := Hash("AAPL", "1d", extended)
	overlap := Hash("AAPL", "1d", extended[:100])
	r := Compare(stored, fresh, overlap.Hash)
	if !r.Changed || !r.Extended {
		t.Fatalf("append not recognized as extension: %+v", r)
	}
	if r.RowsBefore != 100 || r.RowsAfter != 120 {
		t.Fatalf("row counts = %d/%d", r.RowsBefore, r.RowsAfter)
	}

	// Case 3: revision — history rewritten inside the original range.
	revised := append([]Row(nil), extended...)
	revised[10].Close *= 1.02
	freshRev := Hash("AAPL", "1d", revised)
	overlapRev := Hash("AAPL", "1d", revised[:100])
	rr := Compare(stored, freshRev, overlapRev.Hash)
	if !rr.Changed || rr.Extended {
		t.Fatalf("rewritten history reported as a benign extension: %+v", rr)
	}
	if rr.Reason == "" {
		t.Fatal("revision produced no reason")
	}
}

// A missing overlap hash must fall back to the CONSERVATIVE verdict: unknown
// provenance is treated as a revision, never as benign.
func TestUnknownOverlapIsTreatedAsRevision(t *testing.T) {
	orig := rows(100, func(i int) float64 { return 100 })
	stored := Hash("AAPL", "1d", orig)
	fresh := Hash("AAPL", "1d", rows(120, func(i int) float64 { return 100 }))
	r := Compare(stored, fresh, "")
	if !r.Changed || r.Extended {
		t.Fatalf("missing overlap must be conservative, got %+v", r)
	}
}

// Duplicate timestamps are a data defect and must be exposed, not collapsed.
func TestDuplicateTimestampsAreExposed(t *testing.T) {
	clean := []Row{{Ts: 0, Close: 100}, {Ts: day, Close: 101}}
	dupe := []Row{{Ts: 0, Close: 100}, {Ts: day, Close: 101}, {Ts: day, Close: 101}}
	if Hash("X", "1d", clean).Hash == Hash("X", "1d", dupe).Hash {
		t.Fatal("a duplicated row was hashed as if it were not there")
	}
	// And the duplicate ordering must itself be stable.
	a := Hash("X", "1d", dupe)
	b := Hash("X", "1d", []Row{{Ts: day, Close: 101}, {Ts: 0, Close: 100}, {Ts: day, Close: 101}})
	if a.Hash != b.Hash {
		t.Fatal("duplicate rows hash unstably depending on input order")
	}
}
