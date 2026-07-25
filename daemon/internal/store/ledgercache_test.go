package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func ledgerCacheStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "ledgercache.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func appendLedgerN(t *testing.T, st *Store, n int, from int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := st.AppendLedger(context.Background(), LedgerEntry{
			PredictedAt: from + int64(i), SymbolID: 1, Horizon: md.H1d,
			BarTs: from + int64(i), RawProb: 0.6, CalProb: 0.55,
			FeatureHash: "fh", ModelVersion: 1,
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

// Incremental verify must equal the full walk on a grown chain, checkpoint
// after checkpoint, and only walk the suffix once a checkpoint exists.
func TestVerifyLedgerCachedIncrementalEqualsFull(t *testing.T) {
	st := ledgerCacheStore(t)
	ctx := context.Background()

	appendLedgerN(t, st, 10, 1000)
	res1, full1, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatalf("verify1: %v", err)
	}
	if !full1 {
		t.Fatal("first verify must be the full walk (no checkpoint yet)")
	}
	want, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if res1.Intact != want.Intact || res1.Count != want.Count || res1.HeadHash != want.HeadHash {
		t.Fatalf("cached %+v != full %+v", res1, want)
	}

	// Grow the chain; the second cached verify must be a suffix walk with the
	// same answer as a fresh full walk.
	appendLedgerN(t, st, 7, 2000)
	res2, full2, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatalf("verify2: %v", err)
	}
	if full2 {
		t.Fatal("grown chain with intact checkpoint must verify incrementally")
	}
	want2, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatalf("full2: %v", err)
	}
	if res2.Intact != want2.Intact || res2.Count != want2.Count || res2.HeadHash != want2.HeadHash {
		t.Fatalf("incremental %+v != full %+v", res2, want2)
	}
	if res2.Count != 17 {
		t.Fatalf("count = %d, want 17", res2.Count)
	}

	// No growth: still incremental, still identical.
	res3, full3, err := st.VerifyLedgerCached(ctx)
	if err != nil || full3 {
		t.Fatalf("verify3 full=%v err=%v — an unchanged chain must stay on the fast path", full3, err)
	}
	if res3.Count != 17 || !res3.Intact {
		t.Fatalf("verify3 = %+v", res3)
	}
}

// A "smart" mid-chain tamper — rewrite one row and re-chain every downstream
// hash consistently — passes a naive full walk, but the checkpoint anchor
// catches it: the checkpoint row's hash no longer matches the recorded one, so
// the cached verify reports intact=false.
func TestVerifyLedgerCachedTamperInMiddleDetected(t *testing.T) {
	st := ledgerCacheStore(t)
	ctx := context.Background()
	appendLedgerN(t, st, 8, 1000)
	if _, _, err := st.VerifyLedgerCached(ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	// Tamper seq 4's payload, then re-chain 4..8 consistently.
	rows, err := st.db.QueryContext(ctx, `
		SELECT seq, predicted_at, symbol_id, horizon, bar_ts, raw_prob, cal_prob,
		       feature_hash, model_version, prev_hash, entry_hash
		FROM prediction_ledger ORDER BY seq ASC`)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	var entries []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		var hz string
		if err := rows.Scan(&e.Seq, &e.PredictedAt, &e.SymbolID, &hz, &e.BarTs,
			&e.RawProb, &e.CalProb, &e.FeatureHash, &e.ModelVersion,
			&e.PrevHash, &e.EntryHash); err != nil {
			t.Fatalf("scan: %v", err)
		}
		e.Horizon = md.Horizon(hz)
		entries = append(entries, e)
	}
	_ = rows.Close()
	running := entries[2].EntryHash // hash of seq 3
	for i := 3; i < len(entries); i++ {
		e := entries[i]
		if e.Seq == 4 {
			e.CalProb = 0.99 // the tamper
		}
		e.PrevHash = running
		e.EntryHash = hashEntry(running, e)
		running = e.EntryHash
		if _, err := st.w.ExecContext(ctx, `
			UPDATE prediction_ledger SET cal_prob=?, prev_hash=?, entry_hash=? WHERE seq=?`,
			e.CalProb, e.PrevHash, e.EntryHash, e.Seq); err != nil {
			t.Fatalf("tamper seq %d: %v", e.Seq, err)
		}
	}
	// Sanity: the rewritten chain IS internally consistent — a naive full walk
	// alone would pass it.
	if full, err := st.VerifyLedger(ctx); err != nil || !full.Intact {
		t.Fatalf("test setup: consistent rewrite should pass the naive walk (intact=%v err=%v)", full.Intact, err)
	}

	res, fullWalk, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !fullWalk {
		t.Fatal("anchor mismatch must trigger the full walk")
	}
	if res.Intact {
		t.Fatal("tampered chain must report intact=false (checkpoint anchor mismatch)")
	}
	if res.BrokenAtSeq == nil {
		t.Fatal("tamper must carry a brokenAtSeq")
	}

	// The tampered state must never become the new checkpoint.
	raw, err := st.GetMeta(ctx, metaLedgerVerifyCkpt)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	var ck ledgerCkpt
	if err := json.Unmarshal([]byte(raw), &ck); err != nil {
		t.Fatalf("ckpt json: %v", err)
	}
	if ck.Hash == running {
		t.Fatal("checkpoint must not advance onto the tampered chain")
	}
}

// An inconsistent tamper INSIDE the suffix (after the checkpoint) is caught by
// the incremental walk itself.
func TestVerifyLedgerCachedSuffixBreakDetected(t *testing.T) {
	st := ledgerCacheStore(t)
	ctx := context.Background()
	appendLedgerN(t, st, 5, 1000)
	if _, _, err := st.VerifyLedgerCached(ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	appendLedgerN(t, st, 5, 2000)
	if _, err := st.w.ExecContext(ctx,
		`UPDATE prediction_ledger SET cal_prob=0.99 WHERE seq=8`); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	res, fullWalk, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if fullWalk {
		t.Fatal("suffix tamper is caught on the incremental path")
	}
	if res.Intact || res.BrokenAtSeq == nil || *res.BrokenAtSeq != 8 {
		t.Fatalf("want broken at seq 8, got %+v", res)
	}
}
