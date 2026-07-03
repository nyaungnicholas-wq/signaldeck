package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestLabeledFeaturesBySymbol_ResolvedOnlyAndScoped verifies the per-symbol
// labeled join: only RESOLVED, non-voided outcomes for the REQUESTED symbol +
// horizon are returned — the prequential no-leakage guarantee at the symbol
// level (an unresolved prediction is never a training example, and one symbol's
// history never leaks into another's model).
func TestLabeledFeaturesBySymbol_ResolvedOnlyAndScoped(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	aapl, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	// SPY: two predictions at ts 1000 (resolved up) and 2000 (UNRESOLVED).
	mustPredict(t, st, spy.ID, md.H1d, 1000, 0.7, map[string]float64{"pressure_score": 0.4, "pred_raw": 0.7})
	mustPredict(t, st, spy.ID, md.H1d, 2000, 0.3, map[string]float64{"pressure_score": -0.2, "pred_raw": 0.3})
	if err := st.ResolvePrediction(ctx, spy.ID, md.H1d, 1000, 0.02); err != nil {
		t.Fatal(err)
	}

	// AAPL: one resolved prediction at ts 1000 — must NOT appear in SPY's set.
	mustPredict(t, st, aapl.ID, md.H1d, 1000, 0.6, map[string]float64{"pressure_score": 0.1, "pred_raw": 0.6})
	if err := st.ResolvePrediction(ctx, aapl.ID, md.H1d, 1000, -0.01); err != nil {
		t.Fatal(err)
	}

	// SPY: a resolved 1w prediction — must NOT appear in the H1d query.
	mustPredict(t, st, spy.ID, md.H1w, 1000, 0.55, map[string]float64{"pressure_score": 0.05, "pred_raw": 0.55})
	if err := st.ResolvePrediction(ctx, spy.ID, md.H1w, 1000, 0.03); err != nil {
		t.Fatal(err)
	}

	got, err := st.LabeledFeaturesBySymbol(ctx, spy.ID, md.H1d, 100)
	if err != nil {
		t.Fatalf("labeled by symbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly SPY's 1 resolved H1d example, got %d", len(got))
	}
	lf := got[0]
	if lf.SymbolID != spy.ID || lf.Ts != 1000 || lf.Up != 1 {
		t.Fatalf("bad row: %+v", lf)
	}
	if lf.Vec["pred_raw"] != 0.7 {
		t.Fatalf("vector not preserved: %+v", lf.Vec)
	}

	// AAPL sees exactly its own one row (Up=0 from the -0.01 return).
	aaplGot, err := st.LabeledFeaturesBySymbol(ctx, aapl.ID, md.H1d, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(aaplGot) != 1 || aaplGot[0].Up != 0 {
		t.Fatalf("AAPL scoping broken: %+v", aaplGot)
	}
}

// TestSymbolModel_UpsertRoundtrip verifies the cheap idempotent upsert + fetch.
func TestSymbolModel_UpsertRoundtrip(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	// Absent before any write.
	if _, ok, err := st.SymbolModel(ctx, sym.ID, md.H1d); err != nil || ok {
		t.Fatalf("expected no model yet: ok=%v err=%v", ok, err)
	}

	row := SymbolModelRow{
		SymbolID: sym.ID, Horizon: string(md.H1d),
		Weights:     `{"pressure":0.6,"forecast":0.4}`,
		Calibration: `{"kx":[0.2,0.8],"ky":[0.3,0.7],"fitted":true}`,
		Skill:       `{"pressure":{"hitRate":0.6,"ic":0.1,"n":50,"hasHR":true,"hasIC":true}}`,
		Personality: "Momentum-led.",
		NSamples:    50, Tier: "personal", UpdatedTs: 111,
	}
	if err := st.UpsertSymbolModel(ctx, row); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := st.SymbolModel(ctx, sym.ID, md.H1d)
	if err != nil || !ok {
		t.Fatalf("fetch: ok=%v err=%v", ok, err)
	}
	if got.Tier != "personal" || got.NSamples != 50 || got.Personality != "Momentum-led." {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.Weights != row.Weights || got.Calibration != row.Calibration {
		t.Fatalf("blob columns not preserved: %+v", got)
	}

	// Upsert again with new values → in-place replace (idempotent on the key).
	row.Tier = "regime"
	row.NSamples = 20
	row.UpdatedTs = 222
	if err := st.UpsertSymbolModel(ctx, row); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got2, _, _ := st.SymbolModel(ctx, sym.ID, md.H1d)
	if got2.Tier != "regime" || got2.NSamples != 20 || got2.UpdatedTs != 222 {
		t.Fatalf("in-place replace failed: %+v", got2)
	}

	// A different horizon is a separate row.
	if _, ok, _ := st.SymbolModel(ctx, sym.ID, md.H1w); ok {
		t.Fatal("H1w must be a separate (still-absent) row")
	}
}

// mustPredict stores a prediction + its feature vector at ts.
func mustPredict(t *testing.T, st *Store, symbolID int64, h md.Horizon, ts int64, cal float64, vec map[string]float64) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: symbolID, Horizon: h, Ts: ts, RawProb: vec["pred_raw"], CalProb: cal, NUsed: 1, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertFeatures(ctx, symbolID, h, ts, 1, vec); err != nil {
		t.Fatal(err)
	}
}
