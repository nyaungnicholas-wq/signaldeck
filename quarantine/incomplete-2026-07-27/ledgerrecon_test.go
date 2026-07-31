package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func reconStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sd.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() }) //nolint:errcheck
	return st
}

func countGapDQ(t *testing.T, st *store.Store) int {
	t.Helper()
	evs, err := st.RecentDQ(context.Background(), 100)
	if err != nil {
		t.Fatalf("recent dq: %v", err)
	}
	n := 0
	for _, e := range evs {
		if e.Kind == ledgerGapDQKind {
			n++
		}
	}
	return n
}

// The worker publishes the hole and raises a dq event only when it MOVES — a
// standing, already-published gap is not news every six hours, while a growing
// one is a live append failure. And it must never close the hole: a late
// append would place a prediction at the wrong position in the chain.
func TestLedgerReconWorker_PublishesGapAndFlagsMovementWithoutBackfilling(t *testing.T) {
	st := reconStore(t)
	ctx := context.Background()
	w := &LedgerReconWorker{St: st, Now: func() time.Time { return time.Unix(5000, 0) }}

	for _, sym := range []int64{1, 2} {
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym, Horizon: md.H1d, Ts: 2000, CalProb: 0.5,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	if _, err := st.AppendLedger(ctx, store.LedgerEntry{
		PredictedAt: 2001, SymbolID: 1, Horizon: md.H1d, BarTs: 2000,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	rep, ok, err := st.StoredLedgerGap(ctx)
	if err != nil || !ok || rep.Missing != 1 {
		t.Fatalf("published gap = %+v ok=%v err=%v, want missing=1", rep, ok, err)
	}
	if n := countGapDQ(t, st); n != 0 {
		t.Fatalf("dq events = %d, want 0 on the first measurement (no movement to report)", n)
	}

	// Unchanged gap: still published, still no event.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if n := countGapDQ(t, st); n != 0 {
		t.Fatalf("dq events = %d, want 0 while the gap stands still", n)
	}

	// A third prediction that never reaches the chain — the hole grows.
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: 3, Horizon: md.H1d, Ts: 2000, CalProb: 0.5,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("third run: %v", err)
	}
	if n := countGapDQ(t, st); n != 1 {
		t.Fatalf("dq events = %d, want 1 when the gap moves", n)
	}
	rep, _, _ = st.StoredLedgerGap(ctx)
	if rep.Missing != 2 {
		t.Fatalf("published gap = %d, want 2", rep.Missing)
	}

	// The chain is untouched: the worker reports, it does not repair.
	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Count != 1 {
		t.Fatalf("ledger rows = %d, want 1 — the worker must never append the missing entries", v.Count)
	}
}
