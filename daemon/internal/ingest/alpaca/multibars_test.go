package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// fastBackoff shrinks the 429 back-off (and the page pause) for tests.
func fastBackoff(t *testing.T) {
	t.Helper()
	oldP, oldB := pagePause, backoff429
	pagePause, backoff429 = time.Millisecond, time.Millisecond
	t.Cleanup(func() { pagePause, backoff429 = oldP, oldB })
}

// registerStocks upserts n symbols A..., returns a resolver over their ids.
func registerStocks(t *testing.T, st *store.Store, syms ...string) func(string) (int64, bool) {
	t.Helper()
	ids := map[string]int64{}
	for _, s := range syms {
		sym, err := st.UpsertSymbol(context.Background(), s, md.Stocks, "")
		if err != nil {
			t.Fatalf("upsert %s: %v", s, err)
		}
		ids[s] = sym.ID
	}
	return func(s string) (int64, bool) { id, ok := ids[strings.ToUpper(s)]; return id, ok }
}

// TestBackfillDailyMultiBatchingAndParse: many symbols, small batch size,
// verifies (a) requests are batched (≤batch symbols each), (b) the map-shaped
// response is parsed and upserted per symbol, (c) counts are correct, and
// (d) an unresolved symbol in the response is skipped, not fatal.
func TestBackfillDailyMultiBatchingAndParse(t *testing.T) {
	fastBackoff(t)
	var reqCount int32
	var gotSymbolSets [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stocks/bars" {
			t.Errorf("path = %q, want /stocks/bars", r.URL.Path)
		}
		if got := r.Header.Get("APCA-API-KEY-ID"); got != "k" {
			t.Errorf("key header = %q, want k", got)
		}
		q := r.URL.Query()
		for k, want := range map[string]string{
			"timeframe": "1Day", "adjustment": "split", "feed": "sip", "limit": "10000",
		} {
			if q.Get(k) != want {
				t.Errorf("query %s = %q, want %q", k, q.Get(k), want)
			}
		}
		syms := strings.Split(q.Get("symbols"), ",")
		gotSymbolSets = append(gotSymbolSets, syms)
		atomic.AddInt32(&reqCount, 1)
		// Respond with one bar per requested symbol that we recognize; include
		// an extra unknown symbol "ZZZZ" in the first response to prove skip.
		bars := map[string]any{}
		for _, s := range syms {
			bars[s] = []map[string]any{
				{"t": "2026-06-01T04:00:00Z", "o": 1, "h": 2, "l": 0.5, "c": 1.5, "v": 100},
			}
		}
		if atomic.LoadInt32(&reqCount) == 1 {
			bars["ZZZZ"] = []map[string]any{
				{"t": "2026-06-01T04:00:00Z", "o": 9, "h": 9, "l": 9, "c": 9, "v": 9},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"bars": bars, "next_page_token": nil})
	}))
	defer srv.Close()

	st := openTestStore(t)
	resolve := registerStocks(t, st, "AAA", "BBB", "CCC")
	c := New("k", "s")
	c.BaseData = srv.URL

	counts, err := c.BackfillDailyMulti(context.Background(), st,
		[]string{"AAA", "BBB", "CCC"}, resolve, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BackfillDailyMulti: %v", err)
	}
	// All three resolved symbols got one bar; ZZZZ was skipped (not resolved).
	for _, s := range []string{"AAA", "BBB", "CCC"} {
		if counts[s] != 1 {
			t.Errorf("counts[%s] = %d, want 1", s, counts[s])
		}
	}
	if _, ok := counts["ZZZZ"]; ok {
		t.Errorf("unresolved ZZZZ leaked into counts: %v", counts)
	}
	// Every request must carry ≤ MaxBatchSymbols symbols.
	for _, set := range gotSymbolSets {
		if len(set) > MaxBatchSymbols {
			t.Errorf("batch too large: %d > %d", len(set), MaxBatchSymbols)
		}
	}
	// Bars actually landed in the store.
	for _, s := range []string{"AAA", "BBB", "CCC"} {
		id, _ := resolve(s)
		bars, err := st.Bars(context.Background(), id, md.TF1d, 0, 1<<62, 0)
		if err != nil || len(bars) != 1 {
			t.Fatalf("stored bars for %s = %d (err %v), want 1", s, len(bars), err)
		}
	}
}

// TestBackfillDailyMultiBatchSize forces batching by requesting more than
// MaxBatchSymbols symbols and asserting the request count == ceil(n/batch).
func TestBackfillDailyMultiBatchSize(t *testing.T) {
	fastBackoff(t)
	n := MaxBatchSymbols*2 + 5 // → 3 batches
	syms := make([]string, n)
	for i := range syms {
		syms[i] = fmt.Sprintf("S%04d", i)
	}
	st := openTestStore(t)
	resolve := registerStocks(t, st, syms...)

	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.Split(r.URL.Query().Get("symbols"), ",")
		if len(got) > MaxBatchSymbols {
			t.Errorf("batch = %d > %d", len(got), MaxBatchSymbols)
		}
		atomic.AddInt32(&reqCount, 1)
		bars := map[string]any{}
		for _, s := range got {
			bars[s] = []map[string]any{{"t": "2026-06-02T04:00:00Z", "o": 1, "h": 1, "l": 1, "c": 1, "v": 1}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"bars": bars, "next_page_token": nil})
	}))
	defer srv.Close()

	c := New("k", "s")
	c.BaseData = srv.URL
	counts, err := c.BackfillDailyMulti(context.Background(), st, syms, resolve, time.Time{})
	if err != nil {
		t.Fatalf("BackfillDailyMulti: %v", err)
	}
	wantReqs := int32((n + MaxBatchSymbols - 1) / MaxBatchSymbols)
	if reqCount != wantReqs {
		t.Errorf("request count = %d, want %d (ceil(%d/%d))", reqCount, wantReqs, n, MaxBatchSymbols)
	}
	if len(counts) != n {
		t.Errorf("symbols with bars = %d, want %d", len(counts), n)
	}
}

// TestBackfillDailyMultiPaging: a batch that spans two pages via
// next_page_token accumulates bars across pages.
func TestBackfillDailyMultiPaging(t *testing.T) {
	fastBackoff(t)
	st := openTestStore(t)
	resolve := registerStocks(t, st, "AAA")
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&reqCount, 1)
		if n == 1 {
			if r.URL.Query().Get("page_token") != "" {
				t.Errorf("first page carried a token")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"bars":            map[string]any{"AAA": []map[string]any{{"t": "2026-06-01T04:00:00Z", "o": 1, "h": 1, "l": 1, "c": 1, "v": 1}}},
				"next_page_token": "TOK2",
			})
			return
		}
		if r.URL.Query().Get("page_token") != "TOK2" {
			t.Errorf("second page token = %q, want TOK2", r.URL.Query().Get("page_token"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bars":            map[string]any{"AAA": []map[string]any{{"t": "2026-06-02T04:00:00Z", "o": 2, "h": 2, "l": 2, "c": 2, "v": 2}}},
			"next_page_token": nil,
		})
	}))
	defer srv.Close()
	c := New("k", "s")
	c.BaseData = srv.URL
	counts, err := c.BackfillDailyMulti(context.Background(), st, []string{"AAA"}, resolve, time.Time{})
	if err != nil {
		t.Fatalf("BackfillDailyMulti: %v", err)
	}
	if counts["AAA"] != 2 {
		t.Errorf("counts[AAA] = %d, want 2 (two pages)", counts["AAA"])
	}
	if reqCount != 2 {
		t.Errorf("request count = %d, want 2", reqCount)
	}
}

// TestBackfillDailyMulti429Backoff: the client retries on 429 then succeeds.
func TestBackfillDailyMulti429Backoff(t *testing.T) {
	fastBackoff(t)
	st := openTestStore(t)
	resolve := registerStocks(t, st, "AAA")
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&reqCount, 1)
		if n <= 2 { // fail the first two attempts with 429
			http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bars":            map[string]any{"AAA": []map[string]any{{"t": "2026-06-01T04:00:00Z", "o": 1, "h": 1, "l": 1, "c": 1, "v": 1}}},
			"next_page_token": nil,
		})
	}))
	defer srv.Close()
	c := New("k", "s")
	c.BaseData = srv.URL
	counts, err := c.BackfillDailyMulti(context.Background(), st, []string{"AAA"}, resolve, time.Time{})
	if err != nil {
		t.Fatalf("expected success after backoff, got %v", err)
	}
	if counts["AAA"] != 1 {
		t.Errorf("counts[AAA] = %d, want 1", counts["AAA"])
	}
	if reqCount != 3 {
		t.Errorf("request count = %d, want 3 (2 x 429 + 1 ok)", reqCount)
	}
}

// TestBackfillDailyMulti429Exhausts: persistent 429 surfaces an error after
// maxBackfillRetries.
func TestBackfillDailyMulti429Exhausts(t *testing.T) {
	fastBackoff(t)
	st := openTestStore(t)
	resolve := registerStocks(t, st, "AAA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New("k", "s")
	c.BaseData = srv.URL
	if _, err := c.BackfillDailyMulti(context.Background(), st, []string{"AAA"}, resolve, time.Time{}); err == nil {
		t.Fatal("want error on persistent 429, got nil")
	}
}
