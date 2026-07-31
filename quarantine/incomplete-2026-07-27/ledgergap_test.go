package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A prediction whose ledger append failed is INVISIBLE to the hash chain: the
// chain over the remaining rows verifies intact, so "intact" and the head hash
// cannot be read as "every prediction is committed". This is the measurement
// that makes the difference publishable.
func TestMeasureLedgerGap_CountsUncommittedPredictionsAndLeavesThemAlone(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Three predictions on the same bar; only two make it into the chain.
	for _, sym := range []int64{1, 2, 3} {
		if err := st.UpsertPrediction(ctx, Prediction{
			SymbolID: sym, Horizon: md.H1d, Ts: 2000, RawProb: 0.5, CalProb: 0.5,
		}); err != nil {
			t.Fatalf("upsert prediction: %v", err)
		}
	}
	for _, sym := range []int64{1, 2} {
		if _, err := st.AppendLedger(ctx, LedgerEntry{
			PredictedAt: 2001, SymbolID: sym, Horizon: md.H1d, BarTs: 2000,
		}); err != nil {
			t.Fatalf("append ledger: %v", err)
		}
	}
	// A prediction BEFORE the chain's genesis bar is out of scope: the ledger
	// did not exist yet, which is a disclosed start date, not a hole.
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: 4, Horizon: md.H1d, Ts: 1000, RawProb: 0.5, CalProb: 0.5,
	}); err != nil {
		t.Fatalf("upsert pre-genesis prediction: %v", err)
	}

	rep, err := st.MeasureLedgerGap(ctx)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if rep.Missing != 1 {
		t.Fatalf("missing = %d, want 1", rep.Missing)
	}
	if rep.InScope != 3 {
		t.Fatalf("inScope = %d, want 3 (the pre-genesis prediction is out of scope)", rep.InScope)
	}
	if rep.GenesisBarTs != 2000 {
		t.Fatalf("genesis = %d, want 2000", rep.GenesisBarTs)
	}

	// The measurement must not repair what it measures.
	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Count != 2 {
		t.Fatalf("ledger rows = %d, want 2 — MeasureLedgerGap must never append", v.Count)
	}
	if !v.Intact {
		t.Fatal("chain should still verify intact — which is exactly why the gap needs publishing")
	}
}

func TestMeasureLedgerGap_EmptyChainClaimsNothing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: 1, Horizon: md.H1d, Ts: 2000, RawProb: 0.5, CalProb: 0.5,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rep, err := st.MeasureLedgerGap(ctx)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	// An empty chain covers no bars, so it is missing nothing — the honest
	// reading is "the ledger starts nowhere", not "one prediction is lost".
	if rep.Missing != 0 || rep.InScope != 0 || rep.GenesisBarTs != 0 {
		t.Fatalf("empty chain report = %+v, want zero", rep)
	}
}

func TestStoredLedgerGap_UnmeasuredIsNotZero(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, ok, err := st.StoredLedgerGap(ctx); err != nil || ok {
		t.Fatalf("want ok=false before any measurement, got ok=%v err=%v", ok, err)
	}
	if err := st.SetJSON(ctx, LedgerGapMetaKey, LedgerGapReport{Missing: 21, InScope: 9}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	rep, ok, err := st.StoredLedgerGap(ctx)
	if err != nil || !ok || rep.Missing != 21 {
		t.Fatalf("stored gap = %+v ok=%v err=%v", rep, ok, err)
	}
}
