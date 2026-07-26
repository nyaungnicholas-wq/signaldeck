package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newTVServer wires the TradingView routes behind the REAL middleware (own
// harness so this file never collides with parallel edits to api_test.go).
// The DB path is returned so a test can read persisted rows off disk, which is
// the only way to see what a webhook actually wrote (the API hides `raw`).
func newTVServer(t *testing.T, secret string) (*httptest.Server, *store.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tv_api.db")
	st, err := store.Open(dbPath)
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
	return srv, st, dbPath
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
	srv, st, _ := newTVServer(t, secret)
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
	srv, _, _ := newTVServer(t, "") // no secret configured
	resp := postWebhook(t, srv.URL+"/api/tv-webhook", map[string]any{"ticker": "NVDA"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled webhook: %d want 503 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
}

// tvStoredText returns every persisted column of tv_signals that can carry
// caller-supplied text, read straight off disk (the API never exposes `raw`).
func tvStoredText(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	rows, err := db.Query(`SELECT COALESCE(raw,''), COALESCE(message,''), COALESCE(ticker,''), COALESCE(action,'') FROM tv_signals`)
	if err != nil {
		t.Fatalf("query tv_signals: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	var out []string
	for rows.Next() {
		var raw, msg, ticker, action string
		if err := rows.Scan(&raw, &msg, &ticker, &action); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, raw, msg, ticker, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestTVWebhookSecretNotPersisted pins the second half of C8: the raw payload
// is kept for provenance and used to be stored VERBATIM, so all 162 live
// tv_signals rows held a working credential in a world-readable database. The
// secret must be stripped BEFORE the insert — including from the plain-text
// fallback path, which copies the whole body into `message`.
func TestTVWebhookSecretNotPersisted(t *testing.T) {
	const secret = "tv-shared-secret-9f2c"
	srv, _, dbPath := newTVServer(t, secret)

	// (a) the normal shape: secret in the JSON body beside a real alert.
	resp := postWebhook(t, srv.URL+"/api/tv-webhook", map[string]any{
		"secret": secret, "ticker": "NVDA", "action": "buy", "message": "breakout",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid alert: %d want 200 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// (b) the fallback shape: nothing but the secret, so the handler copies the
	//     body into `message`. This path leaked the secret into a SECOND column.
	resp = postWebhook(t, srv.URL+"/api/tv-webhook", map[string]any{"secret": secret})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bare alert: %d want 200 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// (c) the secret smuggled into a field we do not model.
	resp = postWebhook(t, srv.URL+"/api/tv-webhook", map[string]any{
		"secret": secret, "ticker": "NVDA", "message": "auth=" + secret,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("smuggled secret: %d want 200 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	for i, col := range tvStoredText(t, dbPath) {
		if strings.Contains(col, secret) {
			t.Fatalf("persisted column %d contains the webhook secret: %q", i, col)
		}
	}
}

// TestTVWebhookRejectsQuerySecret pins the other C8 leak: the endpoint accepted
// ?secret= / ?token=, which puts a long-lived shared credential into every
// tunnel, proxy and access log that records a URL. Presence of the parameter is
// rejected outright rather than ignored — silently falling back to the body
// would let a misconfigured alert keep leaking the secret while still working.
func TestTVWebhookRejectsQuerySecret(t *testing.T) {
	const secret = "tv-shared-secret-9f2c"
	srv, st, _ := newTVServer(t, secret)
	ctx := context.Background()

	for _, param := range []string{"secret", "token"} {
		// The CORRECT secret in the query must not authenticate.
		resp := postWebhook(t, srv.URL+"/api/tv-webhook?"+param+"="+secret,
			map[string]any{"ticker": "NVDA", "action": "buy"})
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("?%s= authenticated the webhook (%s)", param, drain(t, resp))
		}
		drain(t, resp)

		// Even a request that WOULD authenticate via the body is rejected while
		// the parameter is present, so the misconfiguration is visible.
		resp = postWebhook(t, srv.URL+"/api/tv-webhook?"+param+"="+secret,
			map[string]any{"secret": secret, "ticker": "NVDA", "action": "buy"})
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("?%s= present but request still accepted (%s)", param, drain(t, resp))
		}
		drain(t, resp)
	}

	sigs, err := st.TVSignals(ctx, false, 10)
	if err != nil {
		t.Fatalf("list signals: %v", err)
	}
	if len(sigs) != 0 {
		t.Fatalf("query-authenticated webhook persisted %d row(s)", len(sigs))
	}

	// The header form is the supported out-of-body channel and must work.
	b, _ := json.Marshal(map[string]any{"ticker": "NVDA", "action": "buy"})
	req, _ := http.NewRequest("POST", srv.URL+"/api/tv-webhook", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(tvSecretHeader, secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("header auth: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("header auth: %d want 200 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
}
