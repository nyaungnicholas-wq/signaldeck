// The /api/ledger/verify endpoint is a public read-only endpoint that returns ledger verification status.
// Prior to the fix, its stored-mode anchor check performed a full prefix scan (COUNT(*) WHERE seq<=s) for each anchor.
// This resulted in O(anchors * rows) time, which became prohibitive on a large ledger (e.g., 506,934 rows and 25 anchors).
// Additionally, the saveLedgerCkpt function would write the checkpoint to the database even when the new checkpoint was
// byte-identical to the existing one, consuming the single writer connection in the worker fleet unnecessarily.
// Together, these two issues caused the verification handler to exceed its 30-second timeout when the worker fleet was
// busy writing identical checkpoints, leading to 503 errors on live chains.
// The fix (a) replaces the per-anchor prefix scans with a single ordered pass to compute all required prefix counts.
// The fix (b) avoids writing the checkpoint when it is unchanged, preserving the writer connection for actual work.

package store

import (
	"context"
	"testing"
)

func TestPrefixCountsForMatchesAPerSeqCountOnEveryShape(t *testing.T) {
	st := ledgerCacheStore(t)
	ctx := context.Background()
	appendLedgerN(t, st, 40, 5000)

	rows, err := st.db.QueryContext(ctx, `SELECT seq FROM prediction_ledger ORDER BY seq`)
	if err != nil {
		t.Fatalf("query seqs: %v", err)
	}
	defer rows.Close()
	var seqs []int64
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan seq: %v", err)
		}
		seqs = append(seqs, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	if len(seqs) < 3 {
		t.Fatalf("need at least 3 rows to delete, got %d", len(seqs))
	}
	mid := len(seqs) / 2
	toDelete := []int64{seqs[mid-1], seqs[mid], seqs[mid+1]}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM prediction_ledger WHERE seq IN (?,?,?)`, toDelete[0], toDelete[1], toDelete[2]); err != nil {
		t.Fatalf("delete middle rows: %v", err)
	}

	rows, err = st.db.QueryContext(ctx, `SELECT seq FROM prediction_ledger ORDER BY seq`)
	if err != nil {
		t.Fatalf("query remaining: %v", err)
	}
	defer rows.Close()
	var remaining []int64
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan remaining: %v", err)
		}
		remaining = append(remaining, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error remaining: %v", err)
	}

	var maxSeq int64
	if len(remaining) > 0 {
		maxSeq = remaining[len(remaining)-1]
	} else {
		maxSeq = 4999
	}
	input := make([]int64, 0, len(remaining)+len(toDelete)+2)
	input = append(input, remaining...)
	input = append(input, toDelete...)
	input = append(input, maxSeq+1000, 0)

	// Reverse the input
	for i, j := 0, len(input)-1; i < j; i, j = i+1, j-1 {
		input[i], input[j] = input[j], input[i]
	}
	// Swap first two if possible
	if len(input) >= 2 {
		input[0], input[1] = input[1], input[0]
	}
	// Append duplicate of first element
	input = append(input, input[0])

	counts, err := st.prefixCountsFor(ctx, input)
	if err != nil {
		t.Fatalf("prefixCountsFor: %v", err)
	}

	seen := make(map[int64]struct{})
	for _, v := range input {
		seen[v] = struct{}{}
	}
	if len(counts) != len(seen) {
		t.Fatalf("prefixCountsFor returned %d entries, want %d for distinct input values", len(counts), len(seen))
	}

	for v := range seen {
		var cnt int64
		err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM prediction_ledger WHERE seq<=?`, v).Scan(&cnt)
		if err != nil {
			t.Fatalf("naive count for seq %d: %v", v, err)
		}
		if got := counts[v]; got != cnt {
			t.Errorf("prefixCountsFor disagrees with per-seq count for seq %d: got %d, want %d; this would cause /api/ledger/verify to return incorrect verification results", v, got, cnt)
		}
	}
}

func TestPrefixCountsForEmptyAndSingle(t *testing.T) {
	st := ledgerCacheStore(t)
	ctx := context.Background()

	counts, err := st.prefixCountsFor(ctx, []int64{})
	if err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("empty input returned non-empty map; /api/ledger/verify would malfunction on empty ledger")
	}

	appendLedgerN(t, st, 1, 9000)
	var seq int64
	err = st.db.QueryRowContext(ctx, `SELECT seq FROM prediction_ledger ORDER BY seq LIMIT 1`).Scan(&seq)
	if err != nil {
		t.Fatalf("query seq: %v", err)
	}
	counts, err = st.prefixCountsFor(ctx, []int64{seq})
	if err != nil {
		t.Fatalf("single seq: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("single seq returned %d entries, want 1; /api/ledger/verify would miscount", len(counts))
	}
	var want int64
	err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM prediction_ledger WHERE seq<=?`, seq).Scan(&want)
	if err != nil {
		t.Fatalf("naive count: %v", err)
	}
	if got := counts[seq]; got != want {
		t.Errorf("prefixCountsFor for single seq %d: got %d, want %d; /api/ledger/verify would return wrong count", seq, got, want)
	}
}

func TestSaveLedgerCkptSkipsAnIdenticalWriteButStillAdvances(t *testing.T) {
	st := ledgerCacheStore(t)
	ctx := context.Background()
	appendLedgerN(t, st, 12, 7000)

	res1, _, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if !res1.Intact {
		t.Fatalf("first verify reported !Intact; /api/ledger/verify would fail on healthy ledger")
	}
	first, err := st.GetMeta(ctx, metaLedgerVerifyCkpt)
	if err != nil {
		t.Fatalf("get first checkpoint: %v", err)
	}
	if first == "" {
		t.Fatalf("first checkpoint empty; /api/ledger/verify would walk full chain every time")
	}

	res2, _, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatalf("second verify: %v", err)
	}
	if !res2.Intact {
		t.Fatalf("second verify on unchanged chain reported !Intact; /api/ledger/verify would fail incorrectly")
	}
	if res2.Count != res1.Count || res2.HeadHash != res1.HeadHash {
		t.Fatalf("second verify changed Count or HeadHash on unchanged chain; /api/ledger/verify would be inconsistent")
	}
	second, err := st.GetMeta(ctx, metaLedgerVerifyCkpt)
	if err != nil {
		t.Fatalf("get second checkpoint: %v", err)
	}
	if second != first {
		t.Fatalf("checkpoint changed on unchanged chain; write-identical guard failed, wasting writer connection and risking 503 on /api/ledger/verify")
	}

	appendLedgerN(t, st, 5, 8000)
	res3, _, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatalf("third verify: %v", err)
	}
	if !res3.Intact {
		t.Fatalf("third verify after growth reported !Intact; /api/ledger/verify would fail on grown ledger")
	}
	if res3.Count <= res2.Count {
		t.Fatalf("checkpoint did not advance after chain growth; /api/ledger/verify would walk full chain every time due to stale checkpoint")
	}
	third, err := st.GetMeta(ctx, metaLedgerVerifyCkpt)
	if err != nil {
		t.Fatalf("get third checkpoint: %v", err)
	}
	if third == second {
		t.Fatalf("checkpoint did not advance after chain growth; write-identical guard incorrectly blocked progress, causing /api/ledger/verify to exceed 30s")
	}
}
