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
	base := time.Now().UTC().Truncate(24 * time.Hour).Add(-6 * 24 * time.Hour).Add(5 * time.Hour).Unix()
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
		ts := now.Add(-time.Duration(i) * time.Minute).Unix() // recent → immature for 1w
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

// ── storage-permanence wave: compaction, not deletion ───────────────────

// Minute bars past retention must be rolled into hourly bars BEFORE they are
// pruned — pruning without a surviving rollup would be data loss.
func TestDownsamplerCompactsMinutesBeforePruning(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Two full hours of 1m bars ~100 days old (well past the 90d default),
	// aligned to hour boundaries so expectations are exact.
	old := ((time.Now().Add(-100 * 24 * time.Hour).Unix()) / 3600) * 3600
	var bars []md.Bar
	for m := int64(0); m < 120; m++ {
		ts := old + m*60
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1m, Ts: ts,
			Open: float64(100 + m), High: float64(105 + m), Low: float64(95 + m),
			Close: float64(101 + m), Volume: 2,
		})
	}
	// A daily bar in the same ancient range: must survive no matter what.
	bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: old, Open: 1, High: 1, Low: 1, Close: 1})
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	if _, err := (&Downsampler{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	// 1m bars past retention are gone…
	mins, err := st.Bars(ctx, sym.ID, md.TF1m, 0, old+7200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mins) != 0 {
		t.Fatalf("old 1m bars must be pruned, %d remain", len(mins))
	}
	// …but ONLY because their hourly rollup now exists (compaction).
	hours, err := st.Bars(ctx, sym.ID, md.TF1h, old, old+7200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 2 {
		t.Fatalf("want 2 hourly rollup bars covering the pruned minutes, got %d", len(hours))
	}
	// OHLCV of hour 0: first open, max high, min low, last close, sum volume.
	h0 := hours[0]
	if h0.Open != 100 || h0.High != 105+59 || h0.Low != 95 || h0.Close != 101+59 || h0.Volume != 120 {
		t.Fatalf("hour-0 rollup wrong: %+v", h0)
	}
	// Daily bars are NEVER pruned.
	daily, err := st.Bars(ctx, sym.ID, md.TF1d, 0, old+86400, 0)
	if err != nil || len(daily) != 1 {
		t.Fatalf("daily bar must survive retention: err=%v n=%d", err, len(daily))
	}
}

// A pre-existing (source-backfilled) hourly bar in the compacted range is
// authoritative: compaction must fill only the MISSING hours, not overwrite.
func TestDownsamplerCompactionKeepsAuthoritativeHourlyBars(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	old := ((time.Now().Add(-100 * 24 * time.Hour).Unix()) / 3600) * 3600
	if err := st.UpsertBars(ctx, []md.Bar{
		// Source 1h bar for the hour…
		{SymbolID: sym.ID, TF: md.TF1h, Ts: old, Open: 500, High: 500, Low: 500, Close: 500, Volume: 999},
		// …plus a lone 1m bar inside it (a partial-coverage trap: aggregating
		// it would produce a WRONG hourly bar).
		{SymbolID: sym.ID, TF: md.TF1m, Ts: old + 60, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&Downsampler{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	hours, err := st.Bars(ctx, sym.ID, md.TF1h, old, old+3600, 0)
	if err != nil || len(hours) != 1 {
		t.Fatalf("want the 1 hourly bar: err=%v n=%d", err, len(hours))
	}
	if hours[0].Close != 500 || hours[0].Volume != 999 {
		t.Fatalf("compaction overwrote an authoritative source 1h bar: %+v", hours[0])
	}
	mins, err := st.Bars(ctx, sym.ID, md.TF1m, 0, old+3600, 0)
	if err != nil || len(mins) != 0 {
		t.Fatalf("old 1m bar should still be pruned: err=%v n=%d", err, len(mins))
	}
}
