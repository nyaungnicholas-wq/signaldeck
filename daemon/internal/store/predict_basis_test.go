package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestPredictionWeightsAndBasisRoundTrip pins the leg-audit wave's new state.
// components records what each leg SAID; weights/basis record how much each was
// BELIEVED and which tier decided that. Without them a static-prior blend and a
// skill-weighted blend are indistinguishable after the fact.
func TestPredictionWeightsAndBasisRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "BASIS", md.Stocks, "seed")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	want := Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1785000000,
		RawProb: 0.61, CalProb: 0.55, NUsed: 3,
		Components: `{"PressureScore":0.2}`,
		Weights:    `{"pressure":0.4,"gbm":0.6}`,
		Basis:      "regime",
	}
	if err := st.UpsertPrediction(ctx, want); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}

	var gotW, gotB, gotC string
	if err := st.db.QueryRowContext(ctx,
		`SELECT components, weights, basis FROM predictions WHERE symbol_id=? AND horizon=? AND ts=?`,
		sym.ID, string(md.H1d), want.Ts).Scan(&gotC, &gotW, &gotB); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if gotW != want.Weights {
		t.Errorf("weights = %q, want %q", gotW, want.Weights)
	}
	if gotB != want.Basis {
		t.Errorf("basis = %q, want %q", gotB, want.Basis)
	}
	if gotC != want.Components {
		t.Errorf("components = %q, want %q", gotC, want.Components)
	}
}

// TestPredictionBasisDefaultsToNotRecorded: rows written before this wave (and
// any writer that omits the fields) must read as "not recorded", never as
// "equal weights" — an empty map is a claim, absence is not.
func TestPredictionBasisDefaultsToNotRecorded(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "OLDROW", md.Stocks, "seed")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Write through the pre-wave column list, as an older binary would.
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO predictions (symbol_id, horizon, ts, raw_prob, cal_prob, n_used, components)
		VALUES (?,?,?,?,?,?,?)`, sym.ID, string(md.H1d), 1785000001, 0.5, 0.5, 1, "{}"); err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	var w, b string
	if err := st.db.QueryRowContext(ctx,
		`SELECT weights, basis FROM predictions WHERE symbol_id=? AND ts=?`,
		sym.ID, 1785000001).Scan(&w, &b); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if w != "" || b != "" {
		t.Errorf("legacy row got weights=%q basis=%q, want both empty (not recorded)", w, b)
	}
}
