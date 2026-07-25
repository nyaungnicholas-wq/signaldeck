package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/postmortem"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// End-to-end: seed resolved misses + a correct call, run the worker once, and
// assert it postmortem'd only the misses, is idempotent on re-run, and produced
// a well-formed cluster.
func TestPostmortemWorker_Run(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pmworker.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close() //nolint:errcheck

	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}

	seed := func(ts int64, prob, fwd float64) {
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
			RawProb: prob, CalProb: prob, NUsed: 40, Components: `{"PressureScore":0.2}`,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, fwd); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	seed(1000, 0.70, -0.02) // wrong (up call, went down)
	seed(2000, 0.68, 0.03)  // correct → must NOT be postmortem'd
	seed(3000, 0.28, 0.02)  // wrong (down call, went up)

	w := NewPostmortemWorker(st)
	w.Now = func() time.Time { return time.Unix(5000, 0) } // deterministic clock

	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if detail == "no new misses to postmortem" {
		t.Fatalf("expected misses to be processed, got %q", detail)
	}

	clusters, total, err := st.PostmortemClusters(ctx, 0)
	if err != nil {
		t.Fatalf("clusters: %v", err)
	}
	if total != 2 {
		t.Fatalf("want 2 postmortems (the 2 misses only), got %d", total)
	}
	// no evidence supplied in the seeds ⇒ every miss is honestly Unexplained.
	if len(clusters) != 1 || clusters[0].Code != string(postmortem.ReasonUnexplained) {
		t.Fatalf("expected one unexplained cluster, got %+v", clusters)
	}

	// Idempotent: a second run finds nothing new.
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if detail2 != "no new misses to postmortem" {
		t.Fatalf("second run should be a no-op, got %q", detail2)
	}
	_, total2, _ := st.PostmortemClusters(ctx, 0)
	if total2 != 2 {
		t.Fatalf("idempotent worker should not duplicate; total=%d", total2)
	}
}

// A regime change inside the forward window must be attributed as the primary
// cause (not Unexplained), proving the worker's context assembly wires through.
func TestPostmortemWorker_RegimeAttribution(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pmregime.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close() //nolint:errcheck
	sym, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")

	ts := int64(1000)
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
		RawProb: 0.72, CalProb: 0.72, NUsed: 40, Components: `{"PressureScore":0.4}`,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, -0.03); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Record a regime change inside [ts, ts+86400]: an initial state then a
	// transition (UpsertRegime writes a regime_changes row when the label flips).
	if err := st.UpsertRegime(ctx, sym.ID, ts, "uptrend", 0.5, ""); err != nil {
		t.Fatalf("regime1: %v", err)
	}
	if err := st.UpsertRegime(ctx, sym.ID, ts+3600, "downtrend", 0.5, ""); err != nil {
		t.Fatalf("regime2: %v", err)
	}

	w := NewPostmortemWorker(st)
	w.Now = func() time.Time { return time.Unix(5000, 0) }
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	recent, err := st.RecentPostmortems(ctx, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 1 || recent[0].Primary != string(postmortem.ReasonRegimeShift) {
		t.Fatalf("want regime_shift primary, got %+v", recent)
	}
}
