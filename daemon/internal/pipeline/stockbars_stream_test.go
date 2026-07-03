package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestStockBarsCoversOnlyStreamedHotSet: the daily top-up worker must refresh
// ONLY streamed (hot-set) stocks — the broad daily-only universe is the
// universe-poller's job (via the efficient multi-symbol endpoint).
func TestStockBarsCoversOnlyStreamedHotSet(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	// 1 streamed hot stock, 2 daily-only universe stocks, 1 crypto.
	spy, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, spy.ID, true)
	_, _ = st.UpsertDailyUniverseSymbol(ctx, "MSFT", "")
	_, _ = st.UpsertDailyUniverseSymbol(ctx, "GOOG", "")
	_, _ = st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")

	var mu sync.Mutex
	var fetched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Single-symbol path: /stocks/{symbol}/bars
		// r.URL.Path == "/stocks/SPY/bars"
		parts := r.URL.Path
		mu.Lock()
		fetched = append(fetched, parts)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"bars": []any{}, "next_page_token": nil})
	}))
	defer srv.Close()

	ac := alpaca.New("k", "s")
	ac.BaseData = srv.URL
	w := &StockBars{St: st, Alpaca: ac}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if detail != "topped up daily bars for 1 streamed stocks" {
		t.Fatalf("detail = %q, want 1 streamed stock", detail)
	}
	sort.Strings(fetched)
	if len(fetched) != 1 || fetched[0] != "/stocks/SPY/bars" {
		t.Fatalf("fetched = %v, want only /stocks/SPY/bars", fetched)
	}
}
