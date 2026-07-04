package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// appendN appends n sequential ledger entries with varied field values and
// returns them (with chain fields filled in). Fails the test on any error.
func appendN(t *testing.T, st *Store, n int) []LedgerEntry {
	t.Helper()
	ctx := context.Background()
	out := make([]LedgerEntry, 0, n)
	for i := 0; i < n; i++ {
		h := md.H1d
		if i%2 == 1 {
			h = md.H1w
		}
		e, err := st.AppendLedger(ctx, LedgerEntry{
			PredictedAt:  int64(1_700_000_000 + i),
			SymbolID:     int64(1 + i%3),
			Horizon:      h,
			BarTs:        int64(1_600_000_000 + i*60),
			RawProb:      0.5 + float64(i)*0.001,
			CalProb:      0.4 + float64(i)*0.0007,
			FeatureHash:  HashFeatureVector(map[string]float64{"a": float64(i), "b": -float64(i) * 0.5}),
			ModelVersion: 1,
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		out = append(out, e)
	}
	return out
}

// TestLedger_ChainIntegrityManyAppends: a long run of appends produces a
// strictly linear chain — seq is contiguous from 1, each prev_hash equals the
// previous entry_hash, the genesis prev_hash is "", and VerifyLedger reports
// intact over the whole thing.
func TestLedger_ChainIntegrityManyAppends(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	const n = 250
	entries := appendN(t, st, n)

	if entries[0].PrevHash != "" {
		t.Errorf("genesis prev_hash = %q, want empty", entries[0].PrevHash)
	}
	for i := range entries {
		if entries[i].Seq != int64(i+1) {
			t.Fatalf("entry %d seq = %d, want %d (contiguous autoincrement)", i, entries[i].Seq, i+1)
		}
		if entries[i].EntryHash == "" {
			t.Fatalf("entry %d has empty entry_hash", i)
		}
		if i > 0 && entries[i].PrevHash != entries[i-1].EntryHash {
			t.Fatalf("entry %d prev_hash %q != previous entry_hash %q",
				i, entries[i].PrevHash, entries[i-1].EntryHash)
		}
	}

	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Intact {
		t.Errorf("intact = false, want true")
	}
	if v.Count != n {
		t.Errorf("count = %d, want %d", v.Count, n)
	}
	if v.HeadHash != entries[n-1].EntryHash {
		t.Errorf("head hash = %q, want %q", v.HeadHash, entries[n-1].EntryHash)
	}
	if v.BrokenAtSeq != nil {
		t.Errorf("brokenAtSeq = %d, want nil on intact chain", *v.BrokenAtSeq)
	}

	// LedgerHead must equal the last appended entry.
	head, ok, err := st.LedgerHead(ctx)
	if err != nil || !ok {
		t.Fatalf("head: ok=%v err=%v", ok, err)
	}
	if head.Seq != int64(n) || head.EntryHash != entries[n-1].EntryHash {
		t.Errorf("head seq/hash = %d/%q, want %d/%q", head.Seq, head.EntryHash, n, entries[n-1].EntryHash)
	}
}

// TestLedger_TamperDetectionMidChain: mutating a persisted field of ONE
// historical row breaks the chain, and VerifyLedger reports intact=false at
// exactly that row's seq (the first place the recomputed hash disagrees).
func TestLedger_TamperDetectionMidChain(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	appendN(t, st, 40)

	// Tamper: flip the calibrated probability of seq 17 (deep in the chain).
	// This is exactly the fraud the ledger exists to catch — silently rewriting
	// a past claim. The stored entry_hash is left untouched, so recomputation
	// will disagree at seq 17.
	const tampered = 17
	if _, err := st.w.ExecContext(ctx,
		`UPDATE prediction_ledger SET cal_prob = cal_prob + 0.5 WHERE seq=?`, tampered); err != nil {
		t.Fatal(err)
	}

	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Intact {
		t.Fatal("intact = true after tampering a row; tamper NOT detected")
	}
	if v.BrokenAtSeq == nil {
		t.Fatal("brokenAtSeq = nil, want the tampered seq")
	}
	if *v.BrokenAtSeq != tampered {
		t.Errorf("brokenAtSeq = %d, want %d (the mutated row)", *v.BrokenAtSeq, tampered)
	}
}

// TestLedger_TamperDetectionRewriteHash: even if an attacker recomputes and
// rewrites the tampered row's OWN entry_hash to match its new fields, the chain
// still breaks — because every LATER row's prev_hash still points at the old
// hash. Detection lands at the first row whose prev_hash no longer matches the
// running head (the row AFTER the tampered one).
func TestLedger_TamperDetectionRewriteHash(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	entries := appendN(t, st, 30)

	const tampered = 12
	// Forge the tampered row so ITS entry_hash is internally consistent with a
	// mutated payload — the sophisticated attack.
	forged := entries[tampered-1] // seq==tampered → index tampered-1
	forged.CalProb += 0.3
	forged.EntryHash = hashEntry(forged.PrevHash, forged)
	if _, err := st.w.ExecContext(ctx,
		`UPDATE prediction_ledger SET cal_prob=?, entry_hash=? WHERE seq=?`,
		forged.CalProb, forged.EntryHash, tampered); err != nil {
		t.Fatal(err)
	}

	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Intact {
		t.Fatal("intact = true after forging a row's own hash; chain-linkage tamper NOT detected")
	}
	if v.BrokenAtSeq == nil || *v.BrokenAtSeq != tampered+1 {
		got := int64(-1)
		if v.BrokenAtSeq != nil {
			got = *v.BrokenAtSeq
		}
		t.Errorf("brokenAtSeq = %d, want %d (the row whose prev_hash still points at the old hash)", got, tampered+1)
	}
}

// TestLedger_TamperDetectionDelete: deleting a historical row (append-only
// violation via a raw DELETE) breaks the prev_hash linkage of the next row and
// is detected there.
func TestLedger_TamperDetectionDelete(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	appendN(t, st, 20)

	const deleted = 9
	if _, err := st.w.ExecContext(ctx, `DELETE FROM prediction_ledger WHERE seq=?`, deleted); err != nil {
		t.Fatal(err)
	}
	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Intact {
		t.Fatal("intact = true after deleting a row; gap NOT detected")
	}
	// The next surviving row (seq 10) now has a prev_hash pointing at the
	// deleted row's hash, which is no longer the running head → break at 10.
	if v.BrokenAtSeq == nil || *v.BrokenAtSeq != deleted+1 {
		got := int64(-1)
		if v.BrokenAtSeq != nil {
			got = *v.BrokenAtSeq
		}
		t.Errorf("brokenAtSeq = %d, want %d (row after the deleted gap)", got, deleted+1)
	}
}

// TestLedger_HashDeterminism: the entry hash is a pure function of prev_hash +
// the entry's identity fields — recomputing it (and the feature hash) always
// yields the stored value, and the same logical entry always hashes the same
// regardless of Go map iteration order in the feature vector.
func TestLedger_HashDeterminism(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	vec := map[string]float64{"pressure_score": 0.42, "forecast_prob": 0.61, "n_used": 3, "regime_bull": 1}
	fh := HashFeatureVector(vec)

	e, err := st.AppendLedger(ctx, LedgerEntry{
		PredictedAt: 1_700_000_123, SymbolID: 7, Horizon: md.H1w, BarTs: 1_600_000_000,
		RawProb: 0.55, CalProb: 0.53, FeatureHash: fh, ModelVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Recompute the entry hash from the returned entry — must match exactly.
	if got := hashEntry(e.PrevHash, e); got != e.EntryHash {
		t.Errorf("recomputed entry_hash %q != stored %q", got, e.EntryHash)
	}

	// Feature hash is order-independent: a map built in a different insertion
	// order must produce the identical digest.
	reordered := map[string]float64{}
	reordered["regime_bull"] = 1
	reordered["n_used"] = 3
	reordered["forecast_prob"] = 0.61
	reordered["pressure_score"] = 0.42
	if got := HashFeatureVector(reordered); got != fh {
		t.Errorf("feature hash not order-independent: %q != %q", got, fh)
	}

	// Feature hash is sensitive to value changes (not a constant).
	changed := map[string]float64{"pressure_score": 0.42, "forecast_prob": 0.62, "n_used": 3, "regime_bull": 1}
	if HashFeatureVector(changed) == fh {
		t.Error("feature hash unchanged after a value change — hash is not sensitive")
	}

	// Entry hash is sensitive to prev_hash (chain linkage): same fields, a
	// different prev_hash must yield a different digest.
	if hashEntry("deadbeef", e) == e.EntryHash {
		t.Error("entry hash unchanged when prev_hash changes — chain linkage is not hashed")
	}
}

// TestLedger_AppendOnly_NoUpdatePath asserts the store package exposes NO way to
// update or replace a ledger row: the only exported mutator is AppendLedger,
// which always INSERTs a new row (seq strictly grows) and never touches an
// existing one. Appending an entry with the SAME identity as an earlier one
// still creates a brand-new row rather than overwriting — proving there is no
// upsert/replace semantics.
func TestLedger_AppendOnly_NoUpdatePath(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	base := LedgerEntry{
		PredictedAt: 1_700_000_000, SymbolID: 1, Horizon: md.H1d, BarTs: 1_600_000_000,
		RawProb: 0.5, CalProb: 0.5, FeatureHash: HashFeatureVector(map[string]float64{"a": 1}), ModelVersion: 1,
	}
	e1, err := st.AppendLedger(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	// Append an IDENTICAL-identity entry: an upsert would collapse it; an
	// append-only ledger creates a second row with the next seq.
	e2, err := st.AppendLedger(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Seq <= e1.Seq {
		t.Fatalf("second append seq %d did not grow past %d — looks like an update, not append", e2.Seq, e1.Seq)
	}
	// Its prev_hash must chain to the first row (proving order, not overwrite),
	// and its entry_hash differs (prev_hash is part of the digest).
	if e2.PrevHash != e1.EntryHash {
		t.Errorf("second entry prev_hash %q != first entry_hash %q", e2.PrevHash, e1.EntryHash)
	}
	if e2.EntryHash == e1.EntryHash {
		t.Error("two chained entries share an entry_hash — chain position not committed")
	}

	var count int64
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM prediction_ledger`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("row count = %d, want 2 (append-only: no row was replaced)", count)
	}

	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Intact || v.Count != 2 {
		t.Errorf("verify after identical appends: intact=%v count=%d, want true/2", v.Intact, v.Count)
	}
}

// TestLedger_For_ScopedNewestFirst: LedgerFor returns only the requested
// symbol+horizon, newest-first, honoring the limit.
func TestLedger_LedgerForScoped(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	mk := func(sym int64, h md.Horizon, bar int64) {
		if _, err := st.AppendLedger(ctx, LedgerEntry{
			PredictedAt: bar, SymbolID: sym, Horizon: h, BarTs: bar,
			RawProb: 0.5, CalProb: 0.5, FeatureHash: "x", ModelVersion: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk(1, md.H1d, 100)
	mk(1, md.H1d, 200)
	mk(1, md.H1w, 150) // different horizon — excluded from the 1d query
	mk(2, md.H1d, 300) // different symbol — excluded

	got, err := st.LedgerFor(ctx, 1, md.H1d, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (symbol 1, horizon 1d only)", len(got))
	}
	if got[0].BarTs != 200 || got[1].BarTs != 100 {
		t.Errorf("order = [%d,%d], want newest-first [200,100]", got[0].BarTs, got[1].BarTs)
	}

	// Limit is honored.
	one, err := st.LedgerFor(ctx, 1, md.H1d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].BarTs != 200 {
		t.Errorf("limit=1 → %d rows (bar %d), want 1 row bar 200", len(one), func() int64 {
			if len(one) > 0 {
				return one[0].BarTs
			}
			return -1
		}())
	}
}

// TestLedger_EmptyIsIntact: an empty ledger verifies as trivially intact.
func TestLedger_EmptyIsIntact(t *testing.T) {
	st := openTemp(t)
	v, err := st.VerifyLedger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Intact || v.Count != 0 || v.HeadHash != "" || v.BrokenAtSeq != nil {
		t.Errorf("empty ledger: %+v, want intact/0/empty/nil", v)
	}
	if _, ok, err := st.LedgerHead(context.Background()); ok || err != nil {
		t.Errorf("empty LedgerHead: ok=%v err=%v, want false/nil", ok, err)
	}
}
