// Stage 2 (verdict cards) — tests for the batched VerdictStats read.
// Contracts under test: (1) BATCHING semantics — every requested id appears
// in the result, only requested ids are consulted; (2) LATEST-WINS — a symbol
// with several predictions reports only its newest cal_prob; (3) HONEST
// ABSENCE — no prediction ⇒ CalProb nil (never zero-dressed-as-verdict), no
// symbol_models row ⇒ Tier ""; (4) horizon isolation — 1w rows never leak
// into a 1d read. t.TempDir store only.
package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func openVerdictStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "verdicts.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestVerdictStats(t *testing.T) {
	st := openVerdictStore(t)
	ctx := context.Background()

	full, _ := st.UpsertSymbol(ctx, "FULL", md.Stocks, "Prediction + model")
	predOnly, _ := st.UpsertSymbol(ctx, "PRED", md.Stocks, "Prediction, no model row")
	modelOnly, _ := st.UpsertSymbol(ctx, "MODL", md.Stocks, "Model row, no prediction")
	bare, _ := st.UpsertSymbol(ctx, "BARE", md.Stocks, "Nothing stored")
	decoy, _ := st.UpsertSymbol(ctx, "DECOY", md.Stocks, "Not requested")

	// FULL: two 1d predictions (newest ts=200 must win) + a 1w row that must
	// NOT leak into the 1d read; PRED: one 1d prediction; DECOY must be ignored.
	for _, p := range []Prediction{
		{SymbolID: full.ID, Horizon: md.H1d, Ts: 100, RawProb: 0.50, CalProb: 0.51, NUsed: 2},
		{SymbolID: full.ID, Horizon: md.H1d, Ts: 200, RawProb: 0.60, CalProb: 0.62, NUsed: 4},
		{SymbolID: full.ID, Horizon: md.H1w, Ts: 300, RawProb: 0.90, CalProb: 0.91, NUsed: 5},
		{SymbolID: predOnly.ID, Horizon: md.H1d, Ts: 150, RawProb: 0.40, CalProb: 0.43, NUsed: 3},
		{SymbolID: decoy.ID, Horizon: md.H1d, Ts: 150, RawProb: 0.99, CalProb: 0.99, NUsed: 9},
	} {
		if err := st.UpsertPrediction(ctx, p); err != nil {
			t.Fatalf("seed prediction: %v", err)
		}
	}

	// FULL: personal-tier model; MODL: still-learning global-tier model; and a
	// 1w model row for FULL that must not leak into the 1d read.
	for _, m := range []SymbolModelRow{
		{SymbolID: full.ID, Horizon: string(md.H1d), NSamples: 44, Tier: "personal"},
		{SymbolID: full.ID, Horizon: string(md.H1w), NSamples: 2, Tier: "static"},
		{SymbolID: modelOnly.ID, Horizon: string(md.H1d), NSamples: 12, Tier: "global"},
		{SymbolID: decoy.ID, Horizon: string(md.H1d), NSamples: 99, Tier: "personal"},
	} {
		if err := st.UpsertSymbolModel(ctx, m); err != nil {
			t.Fatalf("seed model: %v", err)
		}
	}

	got, err := st.VerdictStats(ctx, []int64{full.ID, predOnly.ID, modelOnly.ID, bare.ID}, md.H1d)
	if err != nil {
		t.Fatalf("VerdictStats: %v", err)
	}

	// Every requested id present — callers range without existence checks.
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4 (every requested id present)", len(got))
	}
	if _, ok := got[decoy.ID]; ok {
		t.Fatalf("unrequested symbol leaked into the result")
	}

	// FULL — newest 1d prediction wins; 1w rows ignored; personal tier.
	f := got[full.ID]
	if f.CalProb == nil || *f.CalProb != 0.62 || f.NUsed != 4 || f.PredTs != 200 {
		t.Fatalf("FULL = %+v, want newest 1d row (calProb 0.62, nUsed 4, ts 200)", f)
	}
	if f.Tier != "personal" || f.NSamples != 44 {
		t.Fatalf("FULL tier = %q/%d, want personal/44 (1d row, not the 1w static row)", f.Tier, f.NSamples)
	}

	// PRED — prediction present, no model row ⇒ Tier "" (honest absence).
	p := got[predOnly.ID]
	if p.CalProb == nil || *p.CalProb != 0.43 || p.NUsed != 3 {
		t.Fatalf("PRED = %+v, want calProb 0.43 nUsed 3", p)
	}
	if p.Tier != "" || p.NSamples != 0 {
		t.Fatalf("PRED tier = %q/%d, want \"\"/0 (no model row stored)", p.Tier, p.NSamples)
	}

	// MODL — model row but NO prediction ⇒ CalProb nil, never a fake number.
	m := got[modelOnly.ID]
	if m.CalProb != nil || m.NUsed != 0 || m.PredTs != 0 {
		t.Fatalf("MODL = %+v, want nil CalProb (no prediction stored)", m)
	}
	if m.Tier != "global" || m.NSamples != 12 {
		t.Fatalf("MODL tier = %q/%d, want global/12", m.Tier, m.NSamples)
	}

	// BARE — nothing stored anywhere ⇒ all zero values.
	b := got[bare.ID]
	if b.CalProb != nil || b.Tier != "" || b.NSamples != 0 {
		t.Fatalf("BARE = %+v, want zero-value stat", b)
	}

	// Empty input — empty map, no query error.
	empty, err := st.VerdictStats(ctx, nil, md.H1d)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input: got %v, %v — want empty map, nil error", empty, err)
	}
}
