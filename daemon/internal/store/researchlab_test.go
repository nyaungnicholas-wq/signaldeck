package store

import (
	"context"
	"path/filepath"
	"testing"
)

func newRLStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "rl.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// Insert is idempotent (spec-hash PK) and does NOT reset an accrued streak when
// the same hypothesis is re-discovered; the full lifecycle shadow→promoted is
// reflected by status queries + counts.
func TestResearchHypothesisLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newRLStore(t)

	h := HypothesisRow{
		ID: "abc123", Kind: "ablation", Spec: `{"drop":"noise"}`, Description: "drop noise",
		Status: "shadow", DiscoveredAt: 1000, BaseLift: 0.01, DiscLift: 0.05,
		LastLift: 0.05, LastWilson: 0.55, LastN: 300, PassStreak: 1, Evals: 1, UpdatedAt: 1000,
	}
	if err := st.InsertShadowHypothesis(ctx, h); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Re-discovery must NOT overwrite accrued history.
	h2 := h
	h2.PassStreak = 99
	h2.DiscoveredAt = 2000
	if err := st.InsertShadowHypothesis(ctx, h2); err != nil {
		t.Fatalf("re-insert: %v", err)
	}
	got, err := st.HypothesesByStatus(ctx, "shadow", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].PassStreak != 1 || got[0].DiscoveredAt != 1000 {
		t.Fatalf("re-insert clobbered history: %+v", got)
	}

	// Advance the streak to promotion via the eval-update path.
	h.PassStreak = 5
	h.Evals = 5
	h.Status = "promoted"
	h.PromotedAt = 5000
	h.UpdatedAt = 5000
	if err := st.UpdateHypothesisEval(ctx, h); err != nil {
		t.Fatalf("update: %v", err)
	}
	promoted, err := st.HypothesesByStatus(ctx, "promoted", 10)
	if err != nil {
		t.Fatalf("list promoted: %v", err)
	}
	if len(promoted) != 1 || promoted[0].PromotedAt != 5000 {
		t.Fatalf("promotion not reflected: %+v", promoted)
	}
	if s, _ := st.HypothesesByStatus(ctx, "shadow", 10); len(s) != 0 {
		t.Fatalf("hypothesis should have left shadow status, got %d", len(s))
	}

	counts, err := st.HypothesisStatusCounts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts["promoted"] != 1 || counts["shadow"] != 0 {
		t.Fatalf("status counts wrong: %+v", counts)
	}
}
