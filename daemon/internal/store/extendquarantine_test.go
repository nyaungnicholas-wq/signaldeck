package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// seedUnmatched writes n label-less rows of a GUARDED kind straight past the
// write guard, mimicking what a binary predating the guard produced.
func seedUnmatched(t *testing.T, st *Store, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	post := NullAmendmentEpoch + 3600
	var ids []int64
	for i := 0; i < n; i++ {
		sym, err := st.UpsertSymbol(ctx, string(rune('A'+i))+"SEED", md.Stocks, "seed")
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		res, err := st.w.ExecContext(ctx, `
			INSERT INTO regime_outcomes (symbol_id, kind, ts, day, horizon_days, regime,
				conviction, historical_accuracy, rank)
			VALUES (?, 'vol21', ?, ?, 21, 'elevated', 0.5, 0.5, 1)`,
			sym.ID, post, post/86400+int64(i))
		if err != nil {
			t.Fatalf("seed row: %v", err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}
	return ids
}

func openQ(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// The whole safety property: an amnesty must enumerate exactly what it forgives.
// If the live unmatched set has moved since the operator reviewed it -- in
// EITHER direction -- the amendment must abort rather than wave through whatever
// happens to be broken at execution time.
func TestExtendNullQuarantineRefusesAnySetMismatch(t *testing.T) {
	ctx := context.Background()
	st := openQ(t)
	if _, _, err := st.FreezeNullQuarantine(ctx, 1); err != nil {
		t.Fatalf("initial freeze: %v", err)
	}
	ids := seedUnmatched(t, st, 3)

	before, _, err := st.NullQuarantineManifest(ctx)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}

	// Naming too few.
	if _, err := st.ExtendNullQuarantine(ctx, 2, ids[:2]); err == nil {
		t.Error("naming 2 of 3 unmatched rows was accepted; a partial enumeration must abort")
	}
	// Naming an id that is not unmatched.
	if _, err := st.ExtendNullQuarantine(ctx, 2, []int64{ids[0], ids[1], 999999}); err == nil {
		t.Error("naming an id outside the live set was accepted")
	}
	// Naming nothing.
	if _, err := st.ExtendNullQuarantine(ctx, 2, nil); err == nil {
		t.Error("an empty amnesty was accepted")
	}

	// Every refusal must have changed NOTHING.
	after, _, err := st.NullQuarantineManifest(ctx)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if after.Digest != before.Digest || after.NRows != before.NRows {
		t.Errorf("a refused amendment still mutated the manifest: %d rows / %s -> %d rows / %s",
			before.NRows, before.Digest[:12], after.NRows, after.Digest[:12])
	}
	if n, _ := st.UnmatchedNullCount(ctx); n != 3 {
		t.Errorf("unmatched count = %d after refusals, want 3 unchanged", n)
	}
}

// The exact set is admitted, the manifest re-freezes over the new membership,
// and the invariant the worker checks is restored.
func TestExtendNullQuarantineAdmitsTheExactSet(t *testing.T) {
	ctx := context.Background()
	st := openQ(t)
	if _, _, err := st.FreezeNullQuarantine(ctx, 1); err != nil {
		t.Fatalf("initial freeze: %v", err)
	}
	ids := seedUnmatched(t, st, 3)
	before, _, _ := st.NullQuarantineManifest(ctx)

	m, err := st.ExtendNullQuarantine(ctx, 42, ids)
	if err != nil {
		t.Fatalf("extend with the exact set: %v", err)
	}
	if m.NRows != before.NRows+3 {
		t.Errorf("manifest covers %d rows, want %d", m.NRows, before.NRows+3)
	}
	if m.Digest == before.Digest {
		t.Error("digest did not change after admitting rows -- the manifest is not covering membership")
	}
	if n, err := st.UnmatchedNullCount(ctx); err != nil || n != 0 {
		t.Errorf("unmatched count = %d (err %v), want 0 -- the worker is still blocked", n, err)
	}
	// And the re-frozen manifest must verify against the table it now describes.
	if _, ok, err := st.VerifyNullQuarantine(ctx); err != nil || !ok {
		t.Errorf("VerifyNullQuarantine failed after extension: ok=%v err=%v", ok, err)
	}
}

// Rows keep their NULL baseline. Amnesty means "excluded from the denominator",
// never "given a label after the fact" -- a persistence label computed once the
// outcome is known is hindsight, not a null.
func TestExtendNullQuarantineRelabelsNothing(t *testing.T) {
	ctx := context.Background()
	st := openQ(t)
	if _, _, err := st.FreezeNullQuarantine(ctx, 1); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	ids := seedUnmatched(t, st, 2)
	if _, err := st.ExtendNullQuarantine(ctx, 7, ids); err != nil {
		t.Fatalf("extend: %v", err)
	}
	for _, id := range ids {
		var label any
		if err := st.db.QueryRowContext(ctx,
			`SELECT naive_label FROM regime_outcomes WHERE id=?`, id).Scan(&label); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if label != nil {
			t.Errorf("row %d was given a naive_label %v by the amnesty; quarantine must never "+
				"backfill a hindsight baseline", id, label)
		}
	}
}
