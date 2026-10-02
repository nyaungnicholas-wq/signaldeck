package store

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The collapse verdict cache (api/collapsecache.go) reuses a verdict until this
// fingerprint moves, so it must move on every entry and exit of the gated row
// set, including a same-count swap, and stay put for writes outside it.
func TestResolvedOutcomeFingerprintTracksTheGatedRowSet(t *testing.T) {
	st := openTemp(t)
	seedForecastFixture(t, st)
	ctx := context.Background()
	since := dayA.Add(-24 * time.Hour)
	fp := func() string {
		t.Helper()
		s, err := st.ResolvedOutcomeFingerprint(ctx, string(md.H1d), since)
		if err != nil {
			t.Fatalf("ResolvedOutcomeFingerprint: %v", err)
		}
		return s
	}
	sym := func(name string) int64 {
		t.Helper()
		s, err := st.UpsertSymbol(ctx, name, md.Stocks, name+" Co")
		if err != nil {
			t.Fatalf("upsert symbol %s: %v", name, err)
		}
		return s.ID
	}
	base := fp()

	// 1. Unresolved row
	e := sym("EEE")
	ts := dayB.Unix() + 12*3600
	if err := st.UpsertPrediction(ctx, Prediction{SymbolID: e, Horizon: md.H1d, Ts: ts, RawProb: 0.6, CalProb: 0.6, NUsed: 3, Components: `{}`}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}
	if got := fp(); got != base {
		t.Errorf("an unresolved prediction moved the fingerprint: got %q, want %q", got, base)
	}

	// 2. Other horizon
	if err := st.UpsertPrediction(ctx, Prediction{SymbolID: e, Horizon: md.H1w, Ts: ts, RawProb: 0.6, CalProb: 0.6, NUsed: 3, Components: `{}`}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}
	if err := st.ResolvePrediction(ctx, e, md.H1w, ts, 0.01); err != nil {
		t.Fatalf("resolve prediction: %v", err)
	}
	if got := fp(); got != base {
		t.Errorf("a 1w resolution moved the 1d fingerprint: got %q, want %q", got, base)
	}

	// 3. Entry
	if err := st.ResolvePrediction(ctx, e, md.H1d, ts, 0.01); err != nil {
		t.Fatalf("resolve prediction: %v", err)
	}
	entered := fp()
	if entered == base {
		t.Fatalf("a new resolution did not move the fingerprint: got %q, want different", entered)
	}

	// 4. Same-count swap
	f := sym("FFF")
	if err := st.UpsertPrediction(ctx, Prediction{SymbolID: f, Horizon: md.H1d, Ts: ts, RawProb: 0.6, CalProb: 0.6, NUsed: 3, Components: `{}`}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}
	if _, err := st.w.ExecContext(ctx, `UPDATE prediction_outcomes SET up = NULL WHERE symbol_id = ? AND horizon = '1d' AND ts = ?`, e, ts); err != nil {
		t.Fatalf("update up to null: %v", err)
	}
	if err := st.ResolvePrediction(ctx, f, md.H1d, ts, -0.01); err != nil {
		t.Fatalf("resolve prediction: %v", err)
	}
	swapped := fp()
	if swapped == entered {
		t.Errorf("a same-count swap (one label left, another entered) did not move the fingerprint: got %q, want different", swapped)
	}

	// 5. Exit
	if _, err := st.w.ExecContext(ctx, `UPDATE prediction_outcomes SET up = NULL WHERE symbol_id = ? AND horizon = '1d' AND ts = ?`, f, ts); err != nil {
		t.Fatalf("update up to null: %v", err)
	}
	if got := fp(); got != base {
		t.Errorf("after the swapped-in row left, fingerprint %q != original %q", got, base)
	}
}
