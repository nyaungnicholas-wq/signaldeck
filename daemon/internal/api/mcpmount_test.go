package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func mcpTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mcpmount.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// The MCP endpoint must be absent unless it is explicitly enabled. A surface
// designed for a third party's AI agent does not get to appear by default.
func TestMCPIsNotMountedUnlessEnabled(t *testing.T) {
	d := Deps{Cfg: config.Config{
		AllowedHosts: []string{"127.0.0.1:8322"},
		PublicReads:  true,
		MCPEnabled:   false,
	}}
	mux := http.NewServeMux()
	limiter := newRateLimiter(0, 0)
	if srv := d.registerMCP(mux, limiter); srv != nil {
		t.Fatal("registerMCP returned a server while disabled")
	}
	h := d.secureWith(mux, limiter)
	r := httptest.NewRequest("POST", "http://127.0.0.1:8322/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Host = "127.0.0.1:8322"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled /mcp answered %d, want 404", w.Code)
	}
}

// When enabled, /mcp must be reachable THROUGH secure() without the CSRF
// header — an MCP client cannot send it, and CSRF is inapplicable to an
// endpoint that ignores cookies entirely. This is the exemption in
// security.go, tested rather than asserted.
func TestMCPPassesSecureWithoutTheCSRFHeader(t *testing.T) {
	// This machine has a reverse-tunnel LaunchAgent, so config.ReachablePrivately
	// is FALSE here and an anonymous MCP caller is refused — which is the whole
	// point of deriving the posture from the tunnel rather than the bind. Assert
	// that first, then clear the signal to exercise the private-bind path.
	t.Setenv("SIGNALDECK_ASSUME_TUNNEL", "true")
	assertAnonymousMCP(t, http.StatusForbidden, "TLS is required")
	t.Setenv("SIGNALDECK_ASSUME_TUNNEL", "false")
	assertAnonymousMCP(t, http.StatusOK, "explain_methodology")
}

func assertAnonymousMCP(t *testing.T, wantStatus int, wantIn string) {
	t.Helper()
	st := mcpTestStore(t)
	d := Deps{St: st, Cfg: config.Config{
		AllowedHosts: []string{"127.0.0.1:8322"},
		PublicReads:  false, // and it must STILL be reachable: /mcp authenticates itself
		MCPEnabled:   true,
		HTTPAddr:     "127.0.0.1:8322",
	}}
	mux := http.NewServeMux()
	limiter := newRateLimiter(0, 0)
	if srv := d.registerMCP(mux, limiter); srv == nil {
		t.Fatal("registerMCP returned nil while enabled")
	}
	h := d.secureWith(mux, limiter)

	r := httptest.NewRequest("POST", "http://127.0.0.1:8322/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Host = "127.0.0.1:8322"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	// Whatever the posture, the request reached the MCP handler rather than
	// being stopped by the CSRF guard or the session gate — a 403 from those
	// would carry their own message, not the MCP server's.
	if w.Code != wantStatus || !strings.Contains(w.Body.String(), wantIn) {
		t.Fatalf("/mcp through secure() answered %d: %s\n  want %d containing %q",
			w.Code, w.Body.String(), wantStatus, wantIn)
	}
	// The Host allowlist still applies — secure() is not bypassed, only the
	// two guards that do not apply.
	r = httptest.NewRequest("POST", "http://evil.example/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Host = "evil.example"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unlisted Host reached /mcp: %d", w.Code)
	}
}
