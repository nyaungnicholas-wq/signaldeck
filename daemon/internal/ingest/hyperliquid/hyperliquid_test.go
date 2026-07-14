package hyperliquid

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// perpFixture mirrors the live metaAndAssetCtxs shape (verified 2026-07-10):
// [ {universe:[…]}, [ctx, ctx, …] ], index-aligned, numbers as strings.
const perpFixture = `[
  {"universe":[{"name":"BTC","szDecimals":5},{"name":"ETH","szDecimals":4},{"name":"BAD","szDecimals":1}]},
  [
    {"funding":"0.0000125","openInterest":"35315.99","markPx":"64135.0","prevDayPx":"63000.0"},
    {"funding":"0.0000125","openInterest":"779258.09","markPx":"1796.8"},
    {"funding":"not-a-number","openInterest":"1","markPx":"1"}
  ]
]`

func TestParsePerps(t *testing.T) {
	stats, skipped, err := ParsePerps([]byte(perpFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 (the malformed BAD asset)", skipped)
	}
	btc, ok := stats["BTC"]
	if !ok || btc.Funding != 0.0000125 || btc.OpenInterest != 35315.99 || btc.MarkPx != 64135.0 {
		t.Errorf("BTC parsed wrong: %+v (ok=%v)", btc, ok)
	}
	if _, ok := stats["ETH"]; !ok {
		t.Error("ETH missing")
	}
	if _, ok := stats["BAD"]; ok {
		t.Error("malformed asset must be skipped, never fabricated")
	}
}

func TestParsePerpsShapeMismatch(t *testing.T) {
	if _, _, err := ParsePerps([]byte(`{"not":"an array"}`)); err == nil {
		t.Error("non-array response must error")
	}
	if _, _, err := ParsePerps([]byte(`[{"universe":[]}]`)); err == nil {
		t.Error("single-element response must error")
	}
}

func TestCoinForSymbol(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"BTC/USD", "BTC", true},
		{"eth/usd", "ETH", true},
		{"AAPL", "", false},
		{"/USD", "", false},
	}
	for _, c := range cases {
		got, ok := CoinForSymbol(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("CoinForSymbol(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFetchPerps(t *testing.T) {
	var gotBody, gotUA, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotUA, gotCT = string(b), r.Header.Get("User-Agent"), r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(perpFixture))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, UA: "test-ua", HTTP: srv.Client()}
	stats, _, err := c.FetchPerps(context.Background())
	if err != nil || len(stats) != 2 {
		t.Fatalf("fetch: stats=%d err=%v", len(stats), err)
	}
	if gotBody != `{"type":"metaAndAssetCtxs"}` || gotUA != "test-ua" || gotCT != "application/json" {
		t.Errorf("request wrong: body=%q ua=%q ct=%q", gotBody, gotUA, gotCT)
	}
}

func TestFetchPerpsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	if _, _, err := c.FetchPerps(context.Background()); err == nil {
		t.Error("non-200 must error")
	}
}
