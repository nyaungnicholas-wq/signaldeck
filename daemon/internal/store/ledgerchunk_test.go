package store

import (
	"context"
	"testing"
)

// verifyChunked runs VerifyLedger with the given chunk size and records the
// size of every read statement it issued.
func verifyChunked(t *testing.T, st *Store, chunk int) (LedgerVerification, []int64) {
	t.Helper()
	prevChunk, prevHook := ledgerVerifyChunk, ledgerChunkRead
	defer func() { ledgerVerifyChunk, ledgerChunkRead = prevChunk, prevHook }()
	var reads []int64
	ledgerVerifyChunk = chunk
	ledgerChunkRead = func(_ string, n int64) { reads = append(reads, n) }
	v, err := st.VerifyLedger(context.Background())
	if err != nil {
		t.Fatalf("verify (chunk %d): %v", chunk, err)
	}
	return v, reads
}

func sameVerification(a, b LedgerVerification) bool {
	if a.Intact != b.Intact || a.Count != b.Count || a.HeadSeq != b.HeadSeq || a.HeadHash != b.HeadHash {
		return false
	}
	if (a.BrokenAtSeq == nil) != (b.BrokenAtSeq == nil) {
		return false
	}
	return a.BrokenAtSeq == nil || *a.BrokenAtSeq == *b.BrokenAtSeq
}

// The chain walk held ONE read snapshot for the whole 626k-row ledger, which
// pins the WAL for as long as it runs (the cache-warmer's ledger-verify ran to
// its 15-minute deadline under the 2026-10-02 boot load). Walked in chunks,
// every read covers at most one chunk and the verdict is exactly the
// single-pass verdict: intact, broken on a chunk boundary, broken inside a
// chunk, and a deleted row whose successor opens a chunk.
func TestChunkedLedgerVerifyEqualsSinglePass(t *testing.T) {
	const rows, chunk = 53, 7
	ctx := context.Background()
	cases := []struct {
		name   string
		tamper string
	}{
		{"intact", ""},
		{"break on a chunk boundary", `UPDATE prediction_ledger SET cal_prob=0.99 WHERE seq=21`},
		{"break inside a chunk", `UPDATE prediction_ledger SET raw_prob=0.01 WHERE seq=30`},
		{"deleted row", `DELETE FROM prediction_ledger WHERE seq=14`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := ledgerCacheStore(t)
			appendLedgerN(t, st, rows, 1000)
			if c.tamper != "" {
				if _, err := st.w.ExecContext(ctx, c.tamper); err != nil {
					t.Fatal(err)
				}
			}
			want, _ := verifyChunked(t, st, 1<<30) // the single pass
			got, reads := verifyChunked(t, st, chunk)
			if !sameVerification(got, want) {
				t.Fatalf("chunked verify %+v (broken %v), single pass %+v (broken %v)",
					got, got.BrokenAtSeq, want, want.BrokenAtSeq)
			}
			if c.tamper != "" && want.Intact {
				t.Fatal("the tamper went unnoticed by the single pass; the case tests nothing")
			}
			var total int64
			for _, n := range reads {
				if n > chunk {
					t.Fatalf("one read statement covered %d rows, want at most %d (reads %v)", n, chunk, reads)
				}
				total += n
			}
			if total != want.Count || len(reads) < 2 {
				t.Fatalf("reads %v cover %d rows in %d statements, want %d rows over several", reads, total, len(reads), want.Count)
			}
		})
	}
}

// The cached path reads in chunks too: the prefix count behind the checkpoint
// and the suffix walk. Its answer must equal a full walk.
func TestChunkedLedgerVerifyCachedEqualsFull(t *testing.T) {
	st := ledgerCacheStore(t)
	ctx := context.Background()
	appendLedgerN(t, st, 40, 1000)
	if _, _, err := st.VerifyLedgerCached(ctx); err != nil { // checkpoint at 40
		t.Fatal(err)
	}
	appendLedgerN(t, st, 25, 2000)

	prevChunk, prevHook := ledgerVerifyChunk, ledgerChunkRead
	defer func() { ledgerVerifyChunk, ledgerChunkRead = prevChunk, prevHook }()
	const chunk = 6
	ledgerVerifyChunk = chunk
	var counts, walks []int64
	ledgerChunkRead = func(kind string, n int64) {
		if kind == "count" {
			counts = append(counts, n)
		} else {
			walks = append(walks, n)
		}
	}
	got, fullWalk, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledgerChunkRead = nil
	want, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fullWalk || !sameVerification(got, want) || !got.Intact || got.Count != 65 {
		t.Fatalf("cached %+v (full walk %v), full %+v", got, fullWalk, want)
	}
	for _, n := range append(append([]int64(nil), counts...), walks...) {
		if n > chunk {
			t.Fatalf("a read covered %d, want at most %d (counts %v, walks %v)", n, chunk, counts, walks)
		}
	}
	if len(counts) < 2 || len(walks) < 2 {
		t.Fatalf("expected chunked count and walk reads, got counts %v walks %v", counts, walks)
	}
}
