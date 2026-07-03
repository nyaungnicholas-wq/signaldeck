package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// baseCfg is a deterministic config for tests — config.Load() is deliberately
// avoided so ambient env vars / .env files can't leak into assertions.
func baseCfg() config.Config {
	return config.Config{
		WebOrigins:  []string{"http://app.example"},
		OpenSignup:  true,
		PublicReads: true,
	}
}

// newTestServer stands up the real middleware + the wiring-relevant routes
// against a temp store. mutate tweaks cfg before the middleware captures it.
func newTestServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store, Deps) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{
		St:      st,
		Cfg:     baseCfg(),
		Version: "test",
		Started: time.Now(),
		Subscribe: func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error) {
			return st.UpsertSymbol(ctx, symbol, market, "")
		},
	}

	// The Host allowlist must contain the (not yet started) server's address,
	// and secure() captures cfg at wrap time — so allocate the listener first.
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	d.Cfg.AllowedHosts = []string{addr}
	if mutate != nil {
		mutate(&d.Cfg)
	}

	// Same wiring as Serve(): routes behind d.secure.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", d.health)
	d.registerAuth(mux)
	mux.HandleFunc("GET /api/watchlist", d.watchlist)
	mux.HandleFunc("GET /api/trends", d.trends)
	mux.HandleFunc("GET /api/agents", d.agents)
	mux.HandleFunc("POST /api/subscribe", d.subscribe)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st, d
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("jar: %v", err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

// postJSON sends a CSRF-header-carrying JSON POST (like the web app does).
func postJSON(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func drain(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestRegisterLoginMeFlow(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	c := newClient(t)

	// Anonymous /me is 401.
	resp, err := c.Get(srv.URL + "/api/auth/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous me: %d want 401 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// Register: first user becomes admin, session cookie set.
	resp = postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{"username": "alice", "password": "hunter2secret"})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d %s", resp.StatusCode, drain(t, resp))
	}
	var reg map[string]any
	_ = json.Unmarshal([]byte(drain(t, resp)), &reg)
	if reg["username"] != "alice" || reg["isAdmin"] != true {
		t.Fatalf("register payload: %+v", reg)
	}

	// Cookie session now authenticates /me.
	resp, _ = c.Get(srv.URL + "/api/auth/me")
	if resp.StatusCode != 200 || !strings.Contains(drain(t, resp), `"alice"`) {
		t.Fatalf("me after register: %d", resp.StatusCode)
	}

	// Logout, then fresh login with the same credentials.
	resp = postJSON(t, c, srv.URL+"/api/auth/logout", map[string]string{})
	drain(t, resp)
	resp, _ = c.Get(srv.URL + "/api/auth/me")
	if resp.StatusCode != 401 {
		t.Fatalf("me after logout: %d want 401", resp.StatusCode)
	}
	drain(t, resp)

	resp = postJSON(t, c, srv.URL+"/api/auth/login", map[string]string{"username": "alice", "password": "hunter2secret"})
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
	resp, _ = c.Get(srv.URL + "/api/auth/me")
	if resp.StatusCode != 200 {
		t.Fatalf("me after login: %d", resp.StatusCode)
	}
	drain(t, resp)

	// Wrong password is rejected.
	resp = postJSON(t, newClient(t), srv.URL+"/api/auth/login", map[string]string{"username": "alice", "password": "wrong-password"})
	if resp.StatusCode != 401 {
		t.Fatalf("bad login: %d want 401", resp.StatusCode)
	}
	drain(t, resp)
}

func TestRegisterClosedSignup(t *testing.T) {
	srv, st, _ := newTestServer(t, func(c *config.Config) { c.OpenSignup = false })
	// A user already exists (bootstrap admin).
	if _, err := st.CreateUser(context.Background(), "local", "hash", true); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	resp := postJSON(t, newClient(t), srv.URL+"/api/auth/register", map[string]string{"username": "mallory", "password": "password123"})
	if resp.StatusCode != 403 {
		t.Fatalf("closed signup register: %d want 403 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
}

func TestWatchlistAuthAndSubscribeCSRF(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)

	// Anonymous watchlist: 401 even with PublicReads=true (user-scoped).
	anon := newClient(t)
	resp, _ := anon.Get(srv.URL + "/api/watchlist")
	if resp.StatusCode != 401 {
		t.Fatalf("anon watchlist: %d want 401", resp.StatusCode)
	}
	drain(t, resp)

	// Log in.
	c := newClient(t)
	resp = postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{"username": "bob", "password": "password123"})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// Subscribe WITHOUT the CSRF header → 403 from the middleware.
	body, _ := json.Marshal(map[string]string{"symbol": "AAPL", "market": "stocks"})
	req, _ := http.NewRequest("POST", srv.URL+"/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("subscribe no-csrf: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("subscribe without %s: %d want 403", csrfHeader, resp.StatusCode)
	}
	drain(t, resp)

	// With the header it succeeds and lands on the watchlist.
	resp = postJSON(t, c, srv.URL+"/api/subscribe", map[string]string{"symbol": "AAPL", "market": "stocks"})
	if resp.StatusCode != 200 {
		t.Fatalf("subscribe: %d %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
	resp, _ = c.Get(srv.URL + "/api/watchlist")
	if resp.StatusCode != 200 {
		t.Fatalf("watchlist: %d", resp.StatusCode)
	}
	if got := drain(t, resp); !strings.Contains(got, `"AAPL"`) {
		t.Fatalf("watchlist missing AAPL: %s", got)
	}
}

func TestBearerAPITokenMapsToAdmin(t *testing.T) {
	srv, st, _ := newTestServer(t, func(c *config.Config) { c.APIToken = "sekret-token" })
	if _, err := st.CreateUser(context.Background(), "admin", "hash", true); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer sekret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("bearer me: %d want 200", resp.StatusCode)
	}
	if got := drain(t, resp); !strings.Contains(got, `"admin"`) || !strings.Contains(got, `"isAdmin":true`) {
		t.Fatalf("bearer identity: %s", got)
	}

	// Wrong token stays anonymous.
	req, _ = http.NewRequest("GET", srv.URL+"/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("wrong bearer: %d want 401", resp.StatusCode)
	}
	drain(t, resp)
}

func TestRateLimit429WithRetryAfter(t *testing.T) {
	srv, _, _ := newTestServer(t, func(c *config.Config) {
		c.RateRPS = 1
		c.RateBurst = 3
	})
	var got429 bool
	for i := 0; i < 30; i++ {
		resp, err := http.Get(srv.URL + "/api/health")
		if err != nil {
			t.Fatalf("health: %v", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			if ra := resp.Header.Get("Retry-After"); ra == "" {
				t.Fatalf("429 without Retry-After")
			}
			got429 = true
			drain(t, resp)
			break
		}
		drain(t, resp)
	}
	if !got429 {
		t.Fatalf("never rate limited after 30 rapid requests (burst 3)")
	}
}

func TestHostAllowlistRejectsForgedHost(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	req, _ := http.NewRequest("GET", srv.URL+"/api/health", nil)
	req.Host = "evil.example:8322" // DNS-rebinding style forged Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forged host: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("forged host: %d want 403", resp.StatusCode)
	}
	drain(t, resp)

	// The real host still works.
	resp, _ = http.Get(srv.URL + "/api/health")
	if resp.StatusCode != 200 {
		t.Fatalf("real host: %d want 200", resp.StatusCode)
	}
	drain(t, resp)
}

func TestCORSOriginAllowlist(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)

	// Disallowed origin: no ACAO header (browser blocks the read).
	req, _ := http.NewRequest("GET", srv.URL+"/api/health", nil)
	req.Header.Set("Origin", "http://attacker.example")
	resp, _ := http.DefaultClient.Do(req)
	if h := resp.Header.Get("Access-Control-Allow-Origin"); h != "" {
		t.Fatalf("disallowed origin got ACAO=%q", h)
	}
	drain(t, resp)

	// Allowed origin is echoed back (no wildcard).
	req, _ = http.NewRequest("GET", srv.URL+"/api/health", nil)
	req.Header.Set("Origin", "http://app.example")
	resp, _ = http.DefaultClient.Do(req)
	if h := resp.Header.Get("Access-Control-Allow-Origin"); h != "http://app.example" {
		t.Fatalf("allowed origin ACAO=%q want echo", h)
	}
	drain(t, resp)

	// Preflight from a disallowed origin is 403.
	req, _ = http.NewRequest("OPTIONS", srv.URL+"/api/subscribe", nil)
	req.Header.Set("Origin", "http://attacker.example")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("preflight disallowed origin: %d want 403", resp.StatusCode)
	}
	drain(t, resp)
}

func TestBodyCapRejectsOversizedPost(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	big := bytes.Repeat([]byte("a"), maxBodyBytes+4096) // > 128 KB
	req, _ := http.NewRequest("POST", srv.URL+"/api/auth/login", bytes.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("oversized post: %v", err)
	}
	if resp.StatusCode != 400 && resp.StatusCode != 413 {
		t.Fatalf("oversized body: %d want 400/413 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
}

func TestPublicReadsFalseGatesTrends(t *testing.T) {
	srv, _, _ := newTestServer(t, func(c *config.Config) { c.PublicReads = false })

	resp, _ := http.Get(srv.URL + "/api/trends")
	if resp.StatusCode != 401 {
		t.Fatalf("anon trends with PublicReads=false: %d want 401", resp.StatusCode)
	}
	drain(t, resp)

	// Health stays open regardless.
	resp, _ = http.Get(srv.URL + "/api/health")
	if resp.StatusCode != 200 {
		t.Fatalf("health must stay public: %d", resp.StatusCode)
	}
	drain(t, resp)

	// A logged-in user can read trends.
	c := newClient(t)
	resp = postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{"username": "carol", "password": "password123"})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
	resp, _ = c.Get(srv.URL + "/api/trends")
	if resp.StatusCode != 200 {
		t.Fatalf("authed trends: %d want 200", resp.StatusCode)
	}
	drain(t, resp)
}

// sanity: default PublicReads=true serves trends anonymously (localhost mode).
func TestPublicReadsTrueServesTrendsAnonymously(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	resp, _ := http.Get(srv.URL + "/api/trends")
	if resp.StatusCode != 200 {
		t.Fatalf("anon trends default: %d want 200 (%s)", resp.StatusCode, drain(t, resp))
	}
	if got := drain(t, resp); !strings.Contains(got, "tracked") {
		t.Fatalf("trends payload: %s", got)
	}
}
