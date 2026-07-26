package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newRLWorkerStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "rlw.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seed a labeled example: a feature vector + a resolved outcome so it joins into
// LabeledFeatures. up derives from fwd sign inside ResolvePrediction.
func seedRLLabeled(t *testing.T, st *store.Store, sym md.Symbol, ts int64, vec map[string]float64, fwd float64) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, RawProb: 0.5, CalProb: 0.5, NUsed: 40, Components: "{}",
	}); err != nil {
		t.Fatalf("upsert pred: %v", err)
	}
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, ts, 3, vec); err != nil {
		t.Fatalf("insert features: %v", err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, fwd); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// Honest empty state: too little labeled data ⇒ NO EDGE DETECTED, no hypotheses.
func TestResearchLabWorker_NoEdgeOnThinData(t *testing.T) {
	ctx := context.Background()
	st := newRLWorkerStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	for i := int64(0); i < 10; i++ { // far below MinRows
		fwd := 0.01
		if i%2 == 0 {
			fwd = -0.01
		}
		seedRLLabeled(t, st, sym, 1000+i, map[string]float64{"f1": float64(i), "f2": 1}, fwd)
	}
	w := NewResearchLabWorker(st)
	w.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "NO EDGE DETECTED") {
		t.Fatalf("expected NO EDGE on thin data, got %q", detail)
	}
	counts, _ := st.HypothesisStatusCounts(ctx)
	if len(counts) != 0 {
		t.Fatalf("no hypotheses should exist on thin data: %+v", counts)
	}
}

// On a gradable set the worker runs the loop, reports a baseline, and gates to
// once per UTC day (a same-day second run is a no-op).
func TestResearchLabWorker_RunsAndGatesDaily(t *testing.T) {
	ctx := context.Background()
	st := newRLWorkerStore(t)
	sym, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	// 200 labeled rows with a couple of features; enough to grade a baseline.
	//
	// Spaced 8 days apart on purpose. The evaluator now PURGES training rows
	// whose label resolves inside the test block, and the widest pooled label
	// here is a week — so rows one second apart, as this fixture used to seed
	// them, are all overlap and all purged, and the honest outcome is "not
	// gradable". Non-overlapping spacing is what real walk-forward data has to
	// look like for a grade to mean anything.
	const rowSpacing = 8 * 86400
	for i := int64(0); i < 200; i++ {
		sig := 1.0
		fwd := 0.01
		if (i/4)%2 == 1 {
			sig, fwd = -1.0, -0.01
		}
		noise := float64(i%5) / 5.0
		seedRLLabeled(t, st, sym, 1000+i*rowSpacing, map[string]float64{"signal": sig, "noise": noise}, fwd)
	}
	w := NewResearchLabWorker(st)
	fixed := time.Unix(1_700_000_000, 0)
	w.Now = func() time.Time { return fixed }

	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "baseline lift") {
		t.Fatalf("expected a baseline report, got %q", detail)
	}
	// Same-day second run must be a no-op (daily gate).
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if detail2 != "already ran today" {
		t.Fatalf("daily gate not enforced, got %q", detail2)
	}

	// An advisory feedback signal is written (never null after a real run).
	if fb, _ := st.GetMeta(ctx, "research_feedback:v1"); fb == "" {
		t.Fatal("expected research_feedback meta to be written")
	}
}

// The Bonferroni divisor must GROW night over night. The bug this pins: the
// nightly re-test corrected by the size of the shadow pool, and that pool
// shrinks as members are promoted or rejected — so asking the same question
// again made it easier to answer yes.
func TestResearchLabWorker_DivisorGrowsAcrossNights(t *testing.T) {
	ctx := context.Background()
	st := newRLWorkerStore(t)
	sym, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")
	const rowSpacing = 8 * 86400
	for i := int64(0); i < 200; i++ {
		sig, fwd := 1.0, 0.01
		if (i/4)%2 == 1 {
			sig, fwd = -1.0, -0.01
		}
		seedRLLabeled(t, st, sym, 1000+i*rowSpacing, map[string]float64{
			"signal": sig, "noise": float64(i%5) / 5.0,
		}, fwd)
	}
	w := NewResearchLabWorker(st)

	divisors := make([]int, 0, 4)
	for night := 0; night < 4; night++ {
		day := time.Unix(1_700_000_000+int64(night)*86400, 0)
		w.Now = func() time.Time { return day }
		detail, err := w.Run(ctx)
		if err != nil {
			t.Fatalf("night %d: %v", night+1, err)
		}
		var div, prior int
		if _, err := fmt.Sscanf(detail[strings.Index(detail, "Bonferroni divisor "):],
			"Bonferroni divisor %d = tonight's batch + %d prior looks", &div, &prior); err != nil {
			t.Fatalf("night %d: divisor not reported in %q", night+1, detail)
		}
		if night > 0 && div <= divisors[night-1] {
			t.Fatalf("night %d divisor %d did not exceed night %d's %d",
				night+1, div, night, divisors[night-1])
		}
		divisors = append(divisors, div)
	}

	// And the accumulated count is persisted, not recomputed from a table rows
	// leave when they are promoted or rejected.
	if v, _ := st.GetMeta(ctx, researchLabTestsKey); v == "" || v == "0" {
		t.Errorf("cumulative look counter = %q, want a positive persisted count", v)
	}
}
