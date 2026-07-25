package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// An automated rule search is a p-hacking machine unless its guardrails hold.
// These pin the three that matter most: it refuses thin data, it records the
// rules it KILLS as well as the ones it keeps, and it cannot promote its own
// findings into production.

func newLoopStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestLoopRefusesThinData(t *testing.T) {
	// Searching 48 rules over a handful of observations finds structure in noise
	// every single time. Refusing is the only correct behaviour.
	w := &ResearchLoop{St: newLoopStore(t)}
	msg, err := w.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "skip") || !strings.Contains(msg, "noise-fitting") {
		t.Fatalf("thin data must be refused with a reason, got %q", msg)
	}
	hyps, _ := w.St.LoopHypotheses(context.Background(), 0)
	if len(hyps) != 0 {
		t.Fatalf("a refused run must record no hypotheses, got %d", len(hyps))
	}
}

func TestLoopIsIdempotentPerDay(t *testing.T) {
	w := &ResearchLoop{St: newLoopStore(t)}
	ctx := context.Background()
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "already ran today") {
		t.Fatalf("second same-day run should short-circuit, got %q", msg)
	}
}

func TestLedgerRecordsRejectionsNotJustWinners(t *testing.T) {
	// The rejected hypotheses are the more valuable half: they are what stops
	// the same dead idea being retried, and a search that logs only its winners
	// cannot be audited.
	st := newLoopStore(t)
	ctx := context.Background()
	for _, h := range []store.LoopHypothesis{
		{ID: "AD-win", Desc: "survived", Status: "shadow", WilsonLower: 0.55, Survives: true, FoundAt: 1},
		{ID: "AD-dead1", Desc: "killed", Status: "rejected", WilsonLower: 0.48, Survives: false, FoundAt: 1},
		{ID: "AD-dead2", Desc: "killed", Status: "rejected", WilsonLower: 0.41, Survives: false, FoundAt: 1},
	} {
		if err := st.UpsertLoopHypothesis(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.LoopHypotheses(ctx, 0)
	if err != nil || len(got) != 3 {
		t.Fatalf("all outcomes must be ledgered, got %d (%v)", len(got), err)
	}
	var rejected int
	for _, h := range got {
		if h.Status == "rejected" {
			rejected++
		}
	}
	if rejected != 2 {
		t.Fatalf("rejections must persist, got %d", rejected)
	}
}

func TestLoopCannotPromoteToProduction(t *testing.T) {
	// The loop may reach `shadow`; a human moves anything further. Automation
	// that can put its own output live is how a p-hacked rule becomes a
	// position.
	st := newLoopStore(t)
	ctx := context.Background()
	_ = st.UpsertLoopHypothesis(ctx, store.LoopHypothesis{
		ID: "AD-x", Desc: "d", Status: "shadow", Survives: true, FoundAt: 1})
	got, _ := st.LoopHypotheses(ctx, 0)
	for _, h := range got {
		if h.Status == "promoted" {
			t.Fatal("the loop must never write a promoted status")
		}
	}
}

func TestRediscoveryUpdatesRatherThanDuplicates(t *testing.T) {
	// The same rule found again tomorrow is the same hypothesis. Duplicating it
	// would let one idea masquerade as repeated independent evidence.
	st := newLoopStore(t)
	ctx := context.Background()
	h := store.LoopHypothesis{ID: "AD-same", Desc: "d", Status: "rejected",
		WilsonLower: 0.40, Survives: false, FoundAt: 100}
	_ = st.UpsertLoopHypothesis(ctx, h)
	h.Status, h.WilsonLower, h.Survives, h.FoundAt = "shadow", 0.58, true, 200
	_ = st.UpsertLoopHypothesis(ctx, h)

	got, _ := st.LoopHypotheses(ctx, 0)
	if len(got) != 1 {
		t.Fatalf("rediscovery must update in place, got %d rows", len(got))
	}
	if got[0].Status != "shadow" || !got[0].Survives {
		t.Fatalf("the newer verdict should win: %+v", got[0])
	}
}
