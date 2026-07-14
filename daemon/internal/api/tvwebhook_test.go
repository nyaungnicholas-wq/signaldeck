package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newTVServer wires the TradingView routes behind the REAL middleware (own
// harness so this file never collides with parallel edits to api_test.go).
func newTVServer(t *testing.T, secret string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tv_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	d.Cfg.TVWebhookSecret = secret

	mux := http.NewServeMux()
	d.registerTVWebhook(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

// postWebhook sends a raw JSON POST WITHOUT the CSRF header, exactly like
// TradingView's servers do (they cannot set custom headers).
func postWebhook(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func TestTVWebhook(t *testing.T) {
	const secret = "s3cr3t-token"
	srv, st := newTVServer(t, secret)
	ctx := context.Background()

	// A symbol we track, so ticker resolution can be exercised.
	if _, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, ""); err != nil {
		t.Fatalf("seed symbol: %v", err)
	}

	// 1. Missing secret is rejected (403) even though it carries no CSRF header
	//    — proving the endpoint is CSRF-exempt but secret-gated.
	resp := postWebhook(t, srv.URL+"/api/tv-webhook", map[string]any{
		"ticker": "NVDA", "action": "buy",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no secret: %d want 403 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// 2. Wrong secret is rejected.
	resp = postWebhook(t, srv.URL+"/api/tv-webhook", map[string]any{
		"secret": "wrong", "ticker": "NVDA",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong secret: %d want 403", resp.StatusCode)
	}
	drain(t, resp)

	// 3. Valid alert is accepted and persisted, with the ticker resolved.
	resp = postWebhook(t, srv.URL+"/api/tv-webhook", map[string]any{
		"secret": secret, "ticker": "NASDAQ:NVDA", "action": "BUY",
		"price": "123.45", "message": "breakout long",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid alert: %d want 200 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	sigs, err := st.TVSignals(ctx, false, 10)
	if err != nil {
		t.Fatalf("list signals: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("stored %d signals, want 1", len(sigs))
	}
	s := sigs[0]
	if s.Action != "buy" { // normalized to lower-case
		t.Errorf("action=%q want buy", s.Action)
	}
	if s.Price == nil || *s.Price != 123.45 {
		t.Errorf("price=%v want 123.45", s.Price)
	}
	if s.Symbol != "NVDA" { // "NASDAQ:NVDA" resolved to the tracked symbol
		t.Errorf("resolved symbol=%q want NVDA", s.Symbol)
	}

	// 4. The GET listing returns it and never leaks the raw payload field.
	getResp, err := http.Get(srv.URL + "/api/tv-signals")
	if err != nil {
		t.Fatalf("get signals: %v", err)
	}
	body := drain(t, getResp)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get signals: %d (%s)", getResp.StatusCode, body)
	}
	if !bytes.Contains([]byte(body), []byte(`"breakout long"`)) {
		t.Errorf("listing missing message: %s", body)
	}
	if bytes.Contains([]byte(body), []byte("rawJSON")) || bytes.Contains([]byte(body), []byte(`"raw"`)) {
		t.Errorf("listing leaked raw payload: %s", body)
	}
}

func TestTVWebhookDisabledWhenNoSecret(t *testing.T) {
	srv, _ := newTVServer(t, "") // no secret configured
	resp := postWebhook(t, srv.URL+"/api/tv-webhook", map[string]any{"ticker": "NVDA"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled webhook: %d want 503 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
}
