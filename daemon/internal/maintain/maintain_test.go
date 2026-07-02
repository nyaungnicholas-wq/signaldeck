package maintain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// The regression this locks: US daily bars open at ~05:00 UTC (midnight ET),
// NOT 00:00 UTC. A score stamped between 00:00 UTC and the bar's timestamp
// must still resolve to a ONE-trading-day return. The earlier arithmetic
// target (p.Ts/86400)*86400 + 86400 picked the bar two days out and recorded
// a ~2-day return as a 1d outcome, corrupting the Honesty page.
func TestOutcomeResolverBaseAlignedNotUTCMidnight(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Three consecutive daily bars, each opening at 05:00 UTC, 5+ days ago so
	// the 1d window is fully mature relative to time.Now().
	base := time.Now().UTC().Truncate(24*time.Hour).Add(-6*24*time.Hour).Add(5*time.Hour).Unix()
	bars := []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: base, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: base + 86400, Open: 110, High: 110, Low: 110, Close: 110},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: base + 2*86400, Open: 121, High: 121, Low: 121, Close: 121},
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	// Score stamped 3h BEFORE the middle bar's timestamp — i.e. in the
	// [00:00, 05:00) UTC window on that calendar day. Its base bar is the
	// FIRST bar (100); one trading day forward is the middle bar (110).
	scoreTs := base + 86400 - 3*3600
	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: scoreTs, Score: 0.5,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	outcomes, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].FwdReturn == nil {
		t.Fatalf("want 1 resolved outcome with a return, got %+v", outcomes)
	}
	// Correct 1-day return is 110/100-1 = +0.10. The old 2-day bug produced
	// 121/100-1 = +0.21.
	if got := *outcomes[0].FwdReturn; got < 0.099 || got > 0.101 {
		t.Fatalf("fwd return = %.4f, want ~0.10 (1 trading day); ~0.21 would be the 2-day regression", got)
	}
}

// Per-horizon resolution must not let the large immature 1w backlog starve a
// freshly-mature 1d outcome (they no longer share one ts-ordered queue).
func TestOutcomeResolverPerHorizonNoStarve(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	d0 := now.Add(-4 * 24 * time.Hour).Unix()
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: d0, Close: 100},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: d0 + 86400, Close: 105},
	}); err != nil {
		t.Fatal(err)
	}

	// One mature 1d score (3 days old) plus many young 1w scores whose ts are
	// OLDER (they would sit ahead in a single ts-ordered queue).
	if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: md.H1d, Ts: d0 + 3600, Score: 0.2}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		ts := now.Add(-time.Duration(i)*time.Minute).Unix() // recent → immature for 1w
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: md.H1w, Ts: ts, Score: 0.1}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("mature 1d outcome should resolve despite the 1w backlog; got %d", len(got))
	}
}
