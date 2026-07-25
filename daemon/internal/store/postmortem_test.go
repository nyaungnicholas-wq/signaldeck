package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/postmortem"
)

func newPMStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "pm.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seed a prediction and resolve it with a given forward return.
func seedResolved(t *testing.T, st *Store, sym md.Symbol, h md.Horizon, ts int64, prob, fwd float64, comps string) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: h, Ts: ts, RawProb: prob, CalProb: prob, NUsed: 40, Components: comps,
	}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, h, ts, fwd); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// UnPostmortemedMisses must return only WRONG resolved predictions, exclude
// correct ones and coin-flips, and stop returning a miss once it's postmortem'd.
func TestUnPostmortemedMisses_SelectsWrongOnly(t *testing.T) {
	ctx := context.Background()
	st := newPMStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}

	// wrong: predicted up (0.7), went down (fwd<0)
	seedResolved(t, st, sym, md.H1d, 1000, 0.70, -0.02, `{"PressureScore":0.4}`)
	// correct: predicted up (0.65), went up (fwd>0) → excluded
	seedResolved(t, st, sym, md.H1d, 2000, 0.65, 0.03, `{"PressureScore":0.3}`)
	// wrong: predicted down (0.30), went up (fwd>0)
	seedResolved(t, st, sym, md.H1d, 3000, 0.30, 0.01, `{"PressureScore":-0.4}`)
	// coin-flip: prob exactly 0.5 → never a miss (excluded)
	seedResolved(t, st, sym, md.H1d, 4000, 0.50, -0.01, `{}`)

	misses, err := st.UnPostmortemedMisses(ctx, md.H1d, 100)
	if err != nil {
		t.Fatalf("misses: %v", err)
	}
	if len(misses) != 2 {
		t.Fatalf("want 2 wrong misses, got %d: %+v", len(misses), misses)
	}
	// newest-first: ts 3000 then 1000
	if misses[0].Ts != 3000 || misses[1].Ts != 1000 {
		t.Fatalf("wrong order: %d,%d", misses[0].Ts, misses[1].Ts)
	}
	// components parsed + n_used joined
	if misses[1].Components["PressureScore"] != 0.4 || misses[1].NUsed != 40 {
		t.Fatalf("join wrong: %+v", misses[1])
	}

	// Postmortem the ts=1000 miss; it must drop out of the un-done set.
	rep := postmortem.Classify(postmortem.Case{Prob: 0.7, Up: 0, FwdReturn: -0.02}, postmortem.DefaultThresholds())
	if err := st.InsertPostmortem(ctx, misses[1], rep, 9999); err != nil {
		t.Fatalf("insert pm: %v", err)
	}
	misses2, err := st.UnPostmortemedMisses(ctx, md.H1d, 100)
	if err != nil {
		t.Fatalf("misses2: %v", err)
	}
	if len(misses2) != 1 || misses2[0].Ts != 3000 {
		t.Fatalf("postmortem'd miss should drop out; got %+v", misses2)
	}
}

// InsertPostmortem is idempotent on the (symbol,horizon,ts) PK.
func TestInsertPostmortem_Idempotent(t *testing.T) {
	ctx := context.Background()
	st := newPMStore(t)
	sym, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	seedResolved(t, st, sym, md.H1d, 1000, 0.70, -0.02, `{}`)
	misses, _ := st.UnPostmortemedMisses(ctx, md.H1d, 100)
	rep := postmortem.Classify(postmortem.Case{Prob: 0.7, Up: 0, FwdReturn: -0.02}, postmortem.DefaultThresholds())
	if err := st.InsertPostmortem(ctx, misses[0], rep, 100); err != nil {
		t.Fatalf("insert1: %v", err)
	}
	if err := st.InsertPostmortem(ctx, misses[0], rep, 200); err != nil {
		t.Fatalf("insert2 (should ignore): %v", err)
	}
	recent, err := st.RecentPostmortems(ctx, 100)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("idempotent insert should yield 1 row, got %d", len(recent))
	}
}

// PostmortemClusters aggregates by primary reason with correct shares.
func TestPostmortemClusters(t *testing.T) {
	ctx := context.Background()
	st := newPMStore(t)
	sym, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")

	mk := func(ts int64, primary postmortem.ReasonCode) {
		seedResolved(t, st, sym, md.H1d, ts, 0.70, -0.02, `{}`)
		misses, _ := st.UnPostmortemedMisses(ctx, md.H1d, 100)
		var target MissRow
		for _, m := range misses {
			if m.Ts == ts {
				target = m
			}
		}
		rep := postmortem.Report{Primary: primary, Conviction: 0.2, Magnitude: 0.02,
			Reasons: []postmortem.Reason{{Code: primary, Weight: 1}}}
		if err := st.InsertPostmortem(ctx, target, rep, 100); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	mk(1000, postmortem.ReasonUnexplained)
	mk(2000, postmortem.ReasonUnexplained)
	mk(3000, postmortem.ReasonRegimeShift)

	clusters, total, err := st.PostmortemClusters(ctx, 0)
	if err != nil {
		t.Fatalf("clusters: %v", err)
	}
	if total != 3 {
		t.Fatalf("total=%d want 3", total)
	}
	if len(clusters) != 2 || clusters[0].Code != string(postmortem.ReasonUnexplained) || clusters[0].Count != 2 {
		t.Fatalf("biggest cluster wrong: %+v", clusters)
	}
	if clusters[0].Share < 0.66 || clusters[0].Share > 0.67 {
		t.Fatalf("share wrong: %v", clusters[0].Share)
	}
}
