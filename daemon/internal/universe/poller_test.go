package universe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "poller.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// multiBarsMock returns an httptest server that answers the multi-symbol bars
// endpoint with one daily bar per requested symbol.
func multiBarsMock(t *testing.T, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stocks/bars" {
			t.Errorf("path = %q, want /stocks/bars", r.URL.Path)
		}
		syms := strings.Split(r.URL.Query().Get("symbols"), ",")
		bars := map[string]any{}
		for _, s := range syms {
			if seen != nil {
				*seen = append(*seen, s)
			}
			bars[s] = []map[string]any{
				{"t": "2026-06-30T04:00:00Z", "o": 1, "h": 2, "l": 0.5, "c": 1.5, "v": 100},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"bars": bars, "next_page_token": nil})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mockClient(url string) *alpaca.Client {
	c := alpaca.New("k", "s")
	c.BaseData = url
	return c
}

// TestPollerSkipsWithoutClient: degrades to a skip like NewsFetcher.
func TestPollerSkipsWithoutClient(t *testing.T) {
	p := &Poller{St: openStore(t)}
	detail, err := p.Run(context.Background())
	if err != nil || !strings.Contains(detail, "skipped") {
		t.Fatalf("no-key run: %q %v", detail, err)
	}
}

// TestPollerRefreshesDailyUniverse: the poller pulls daily bars for exactly
// the registered daily-only universe (not the streamed hot set, not crypto).
func TestPollerRefreshesDailyUniverse(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	// Register a broad daily universe + one streamed hot symbol + crypto.
	for _, s := range []string{"AAA", "BBB", "CCC"} {
		if _, err := st.UpsertDailyUniverseSymbol(ctx, s, ""); err != nil {
			t.Fatalf("register %s: %v", s, err)
		}
	}
	hot, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, hot.ID, true)
	_, _ = st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")

	var seen []string
	srv := multiBarsMock(t, &seen)
	p := &Poller{St: st, Alpaca: mockClient(srv.URL), FirstRunDelay: time.Millisecond}

	detail, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "3/3 universe symbols") {
		t.Fatalf("detail = %q, want 3/3 universe symbols", detail)
	}
	// The poller must have requested ONLY the daily-only universe symbols.
	got := map[string]bool{}
	for _, s := range seen {
		got[s] = true
	}
	for _, want := range []string{"AAA", "BBB", "CCC"} {
		if !got[want] {
			t.Errorf("universe symbol %s not fetched (seen=%v)", want, seen)
		}
	}
	if got["SPY"] {
		t.Errorf("poller fetched the STREAMED hot symbol SPY — it must be daily-only only")
	}
	if got["BTC/USD"] {
		t.Errorf("poller fetched a crypto symbol")
	}
	// Bars actually landed for a universe symbol.
	sym, _ := st.GetSymbol(ctx, "AAA", md.Stocks)
	bars, err := st.Bars(ctx, sym.ID, md.TF1d, 0, 1<<62, 0)
	if err != nil || len(bars) != 1 {
		t.Fatalf("AAA daily bars = %d (err %v), want 1", len(bars), err)
	}
}

// TestPollerNoUniverseYet: with no daily-only symbols registered the poller is
// a clean no-op (not an error).
func TestPollerNoUniverseYet(t *testing.T) {
	st := openStore(t)
	srv := multiBarsMock(t, nil)
	p := &Poller{St: st, Alpaca: mockClient(srv.URL), FirstRunDelay: time.Millisecond}
	detail, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "no daily-universe symbols") {
		t.Fatalf("detail = %q", detail)
	}
}

// TestSeedRegistersAndBackfills: Seed registers the curated universe (stream=0)
// and deep-backfills daily bars for each via the mock.
func TestSeedRegistersAndBackfills(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	var seen []string
	srv := multiBarsMock(t, &seen)

	registered, bars, err := Seed(ctx, st, mockClient(srv.URL), 30) // cap 30
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if registered != 30 {
		t.Fatalf("registered = %d, want 30 (== cap)", registered)
	}
	if bars != 30 {
		t.Fatalf("bars = %d, want 30 (one per symbol)", bars)
	}
	// Every registered symbol is active daily-only (stream=0), and the count
	// matches the cap.
	daily, _ := st.DailyUniverseCount(ctx)
	if daily != 30 {
		t.Fatalf("daily universe count = %d, want 30", daily)
	}
	streamed, _ := st.StreamedSymbolCount(ctx)
	if streamed != 0 {
		t.Fatalf("streamed count = %d, want 0 (seed never streams)", streamed)
	}
}

// TestSeedNoKeysRegistersOnly: without a client, Seed still registers the
// universe (for a later poll) but backfills nothing.
func TestSeedNoKeysRegistersOnly(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	registered, bars, err := Seed(ctx, st, nil, 10)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if registered != 10 || bars != 0 {
		t.Fatalf("registered=%d bars=%d, want 10/0", registered, bars)
	}
	daily, _ := st.DailyUniverseCount(ctx)
	if daily != 10 {
		t.Fatalf("daily universe count = %d, want 10", daily)
	}
}

// TestCuratedDedupAndCap: the curated list is deduped and honors the cap.
func TestCuratedDedupAndCap(t *testing.T) {
	full := Curated(0)
	if len(full) < 400 {
		t.Fatalf("curated universe too small: %d", len(full))
	}
	seen := map[string]bool{}
	for _, s := range full {
		if seen[s] {
			t.Fatalf("duplicate ticker in curated list: %s", s)
		}
		if s != strings.ToUpper(s) {
			t.Fatalf("non-upper ticker: %s", s)
		}
		seen[s] = true
	}
	capped := Curated(50)
	if len(capped) != 50 {
		t.Fatalf("capped = %d, want 50", len(capped))
	}
}

// TestSP500NoDuplicates guards the hardcoded list against copy-paste dupes.
func TestSP500NoDuplicates(t *testing.T) {
	seen := map[string]int{}
	for _, s := range SP500 {
		seen[s]++
	}
	for s, n := range seen {
		if n > 1 {
			t.Errorf("duplicate ticker %q appears %d times in SP500", s, n)
		}
	}
	if len(SP500) < 400 {
		t.Fatalf("SP500 shrank unexpectedly: %d tickers", len(SP500))
	}
}

// TestUniverseCapEnv: the cap env override is honored.
func TestUniverseCapEnv(t *testing.T) {
	if UniverseCap() != DefaultUniverseCap {
		t.Fatalf("default cap = %d, want %d", UniverseCap(), DefaultUniverseCap)
	}
	t.Setenv("SIGNALDECK_UNIVERSE_CAP", "42")
	if UniverseCap() != 42 {
		t.Fatalf("env cap = %d, want 42", UniverseCap())
	}
	t.Setenv("SIGNALDECK_UNIVERSE_CAP", "-3") // invalid → default
	if UniverseCap() != DefaultUniverseCap {
		t.Fatalf("invalid cap = %d, want default %d", UniverseCap(), DefaultUniverseCap)
	}
}
