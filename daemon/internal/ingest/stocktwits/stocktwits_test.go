package stocktwits

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// streamFixture mirrors the live NVDA stream shape (verified 2026-07-10):
// entities.sentiment is null for untagged messages.
const streamFixture = `{
  "symbol": {"id": 1, "symbol": "NVDA"},
  "messages": [
    {"id": 100, "body": "up", "entities": {"sentiment": {"basic": "Bullish"}}},
    {"id": 99,  "body": "down", "entities": {"sentiment": {"basic": "Bearish"}}},
    {"id": 98,  "body": "meh", "entities": {"sentiment": null}},
    {"id": 101, "body": "up2", "entities": {"sentiment": {"basic": "Bullish"}}},
    {"id": 97,  "body": "???", "entities": {"sentiment": {"basic": "Sideways"}}}
  ]
}`

func TestTally(t *testing.T) {
	s, err := Tally([]byte(streamFixture))
	if err != nil {
		t.Fatalf("tally: %v", err)
	}
	if s.Bullish != 2 || s.Bearish != 1 || s.Untagged != 2 || s.Total != 5 {
		t.Errorf("tally wrong: %+v", s)
	}
	if s.NewestID != 101 {
		t.Errorf("newest id = %d, want 101", s.NewestID)
	}
}

func TestTallyMalformed(t *testing.T) {
	if _, err := Tally([]byte("<html>not json</html>")); err == nil {
		t.Error("malformed page must error, never a fabricated zero")
	}
}

func TestFetchSymbol(t *testing.T) {
	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA = r.URL.Path, r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/NVDA.json":
			_, _ = w.Write([]byte(streamFixture))
		case "/ZZZZ.json":
			http.Error(w, `{"errors":[{"message":"not found"}]}`, http.StatusNotFound)
		default:
			http.Error(w, "rate limited", http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, UA: "test-ua", MinInterval: time.Millisecond}

	snap, err := c.FetchSymbol(context.Background(), "nvda")
	if err != nil || snap.Total != 5 || snap.Bullish != 2 {
		t.Fatalf("fetch: snap=%+v err=%v", snap, err)
	}
	if gotPath != "/NVDA.json" || gotUA != "test-ua" {
		t.Errorf("request wrong: path=%q ua=%q", gotPath, gotUA)
	}

	if _, err := c.FetchSymbol(context.Background(), "ZZZZ"); !errors.Is(err, ErrUnknown) {
		t.Errorf("404 must map to ErrUnknown, got %v", err)
	}
	if _, err := c.FetchSymbol(context.Background(), "HOT"); err == nil || errors.Is(err, ErrUnknown) {
		t.Errorf("429 must be a real error, got %v", err)
	}
}
