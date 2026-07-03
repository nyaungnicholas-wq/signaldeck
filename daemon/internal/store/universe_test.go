package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func openUniverseStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestStreamDailyDistinction: a streamed symbol and a daily-only universe
// symbol are counted in the right buckets, and the stream flag round-trips.
func TestStreamDailyDistinction(t *testing.T) {
	st := openUniverseStore(t)
	ctx := context.Background()

	// A streamed hot-set symbol (subscribe path marks stream=1).
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert SPY: %v", err)
	}
	if err := st.SetSymbolStream(ctx, spy.ID, true); err != nil {
		t.Fatalf("set stream: %v", err)
	}

	// A broad daily-only universe symbol (never streamed).
	msft, err := st.UpsertDailyUniverseSymbol(ctx, "MSFT", "Microsoft")
	if err != nil {
		t.Fatalf("upsert MSFT: %v", err)
	}
	if msft.Stream {
		t.Fatalf("daily-only symbol should not be streamed")
	}
	if msft.Name != "Microsoft" {
		t.Fatalf("name = %q, want Microsoft", msft.Name)
	}

	streamed, err := st.StreamedSymbolCount(ctx)
	if err != nil || streamed != 1 {
		t.Fatalf("streamed count = %d (err %v), want 1", streamed, err)
	}
	daily, err := st.DailyUniverseCount(ctx)
	if err != nil || daily != 1 {
		t.Fatalf("daily count = %d (err %v), want 1", daily, err)
	}

	// Read-back preserves the flags.
	got, err := st.GetSymbol(ctx, "SPY", md.Stocks)
	if err != nil || !got.Stream || !got.Active {
		t.Fatalf("SPY read-back = %+v (err %v)", got, err)
	}
	got, err = st.GetSymbol(ctx, "MSFT", md.Stocks)
	if err != nil || got.Stream {
		t.Fatalf("MSFT read-back = %+v (err %v), want stream=false", got, err)
	}
}

// TestUpsertDailyUniverseNeverDemotes: registering an already-streamed symbol
// through the universe path must NOT pull it off the stream.
func TestUpsertDailyUniverseNeverDemotes(t *testing.T) {
	st := openUniverseStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.SetSymbolStream(ctx, sym.ID, true); err != nil {
		t.Fatalf("set stream: %v", err)
	}
	// Re-register via the daily-universe path (as a re-seed would).
	got, err := st.UpsertDailyUniverseSymbol(ctx, "AAPL", "Apple Inc.")
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if !got.Stream {
		t.Fatalf("universe re-register demoted a streamed symbol: %+v", got)
	}
	if n, _ := st.StreamedSymbolCount(ctx); n != 1 {
		t.Fatalf("streamed count = %d, want 1 (still streamed)", n)
	}
}

// TestActiveStockSymbolsFilter: the streamed filter partitions active stocks.
func TestActiveStockSymbolsFilter(t *testing.T) {
	st := openUniverseStore(t)
	ctx := context.Background()
	spy, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, spy.ID, true)
	_, _ = st.UpsertDailyUniverseSymbol(ctx, "MSFT", "")
	_, _ = st.UpsertDailyUniverseSymbol(ctx, "GOOG", "")
	// A crypto symbol must never appear in the stock lists.
	_, _ = st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")

	yes, no := true, false
	streamed, err := st.ActiveStockSymbols(ctx, &yes)
	if err != nil || len(streamed) != 1 || streamed[0].Symbol != "SPY" {
		t.Fatalf("streamed stocks = %+v (err %v)", streamed, err)
	}
	dailyOnly, err := st.ActiveStockSymbols(ctx, &no)
	if err != nil || len(dailyOnly) != 2 {
		t.Fatalf("daily-only stocks = %+v (err %v), want 2", dailyOnly, err)
	}
	all, err := st.ActiveStockSymbols(ctx, nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("all stocks = %+v (err %v), want 3", all, err)
	}
}
