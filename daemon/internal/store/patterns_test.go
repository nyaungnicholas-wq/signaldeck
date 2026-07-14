// CANDLESTICK-PATTERNS wave store tests: pattern_stats upsert idempotence,
// per-symbol read ordering, and symbol isolation. Fixture data only.
package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openPatternsStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "patterns.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestUpsertAndReadPatternStats(t *testing.T) {
	st := openPatternsStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", "stocks", "Apple")

	rows := []PatternStat{
		{SymbolID: sym.ID, Pattern: "bullish_engulfing", Horizon: 5, HitRate: 0.62, MeanFwd: 0.011, N: 40},
		{SymbolID: sym.ID, Pattern: "hammer", Horizon: 5, HitRate: 0.55, MeanFwd: 0.004, N: 22},
	}
	for _, r := range rows {
		if err := st.UpsertPatternStat(ctx, r); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	got, err := st.PatternStatsForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	// Ordered by pattern name: bullish_engulfing before hammer.
	if got[0].Pattern != "bullish_engulfing" || got[1].Pattern != "hammer" {
		t.Fatalf("unexpected order: %q, %q", got[0].Pattern, got[1].Pattern)
	}
	if got[0].HitRate != 0.62 || got[0].N != 40 {
		t.Fatalf("bullish_engulfing round-trip wrong: %+v", got[0])
	}
}

func TestUpsertPatternStatIsIdempotent(t *testing.T) {
	st := openPatternsStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "MSFT", "stocks", "Microsoft")

	base := PatternStat{SymbolID: sym.ID, Pattern: "morning_star", Horizon: 5, HitRate: 0.5, MeanFwd: 0.002, N: 15}
	if err := st.UpsertPatternStat(ctx, base); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// Re-upsert the same key with new values → row replaced, not duplicated.
	base.HitRate, base.MeanFwd, base.N = 0.7, 0.02, 60
	if err := st.UpsertPatternStat(ctx, base); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, err := st.PatternStatsForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row after re-upsert, got %d", len(got))
	}
	if got[0].HitRate != 0.7 || got[0].N != 60 {
		t.Fatalf("upsert did not replace values: %+v", got[0])
	}
}

func TestPatternStatsSymbolIsolation(t *testing.T) {
	st := openPatternsStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "BTC/USD", "crypto", "Bitcoin")
	b, _ := st.UpsertSymbol(ctx, "ETH/USD", "crypto", "Ether")

	if err := st.UpsertPatternStat(ctx, PatternStat{SymbolID: a.ID, Pattern: "hammer", Horizon: 5, HitRate: 0.6, N: 20}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	got, err := st.PatternStatsForSymbol(ctx, b.ID)
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("symbol b should have no pattern stats, got %d", len(got))
	}
}
