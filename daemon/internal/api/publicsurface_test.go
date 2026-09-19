package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
)

// registeredRoutes scans THIS package's own source for mux registrations.
//
// http.ServeMux exposes no way to enumerate its patterns, and a hand-copied
// list in a test rots the day someone adds a handler -- which is precisely the
// day this test needs to fire. Reading the source keeps the list honest with
// zero maintenance: a new mux.HandleFunc appears here automatically.
func registeredRoutes(t *testing.T) []string {
	t.Helper()
	re := regexp.MustCompile(`mux\.(?:HandleFunc|Handle)\("(?:(?:GET|POST|PUT|DELETE|PATCH) )?(/api/[^"]*)"`)
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			p := m[1]
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) < 100 {
		t.Fatalf("only found %d routes by scanning source; the regex has rotted "+
			"and this test is no longer checking anything", len(out))
	}
	return out
}

// Every entry in publicRoutes must name a route that actually exists.
//
// A typo'd entry is not a harmless no-op: it means the surface we BELIEVE we
// published is not the surface we published. "/api/track_record" would sit in
// the map looking correct while the real /api/track-record answered 401 to
// every anonymous visitor -- the exact class of bug that made /proof render
// its error state to everyone it was built for.
func TestEveryPublicRouteExists(t *testing.T) {
	live := map[string]bool{}
	for _, p := range registeredRoutes(t) {
		live[p] = true
	}
	for p := range publicRoutes {
		if !live[p] {
			t.Errorf("publicRoutes names %q, which no handler registers — "+
				"typo, or a route that was renamed out from under the allowlist", p)
		}
	}
}

// G1: the published product is an information and risk instrument. It must
// never expose execution, money, positions, per-user state, or a
// substantially-raw vendor record -- and the vendor half is a LICENCE
// question, not a privacy one (internal/datalicense classes tvscanner and
// stocktwits Restricted, the Alpaca/Kraken bars Licensed).
//
// Asserted by substring over the live route list rather than by naming paths,
// so a route added later called /api/portfolio/whatever is caught without
// anyone remembering to extend this test.
func TestSensitiveRoutesAreNeverAnonymous(t *testing.T) {
	forbidden := []string{
		// user-scoped / execution / money — G1
		"/api/hud", "/api/paper", "/api/portfolio", "/api/watchlist",
		"/api/alerts", "/api/candidates", "/api/subscribe", "/api/unsubscribe",
		"/api/notify/", "/api/risk", "/api/stress/",
		// LLM budget — no /api/ai/ path is anonymous, status included
		"/api/ai/",
		// vendor data — licence
		"/api/bars", "/api/export/", "/api/snaps", "/api/news",
		"/api/stocktwits", "/api/tv-quote", "/api/tv-rating",
		"/api/chart-overlays",
		// operational detail that health/ready deliberately withhold
		"/api/agents", "/api/fleet-health", "/api/source-health", "/api/datastats",
	}
	d := Deps{Cfg: config.Config{PublicSurface: true, PublicReads: true}}
	for _, p := range registeredRoutes(t) {
		for _, bad := range forbidden {
			if !strings.HasPrefix(p, bad) {
				continue
			}
			if publicRoutes[p] {
				t.Errorf("%q is in publicRoutes but matches the forbidden prefix %q", p, bad)
			}
			// PublicReads is deliberately true here: the allowlist must close
			// this route on its own, not lean on the flag being off.
			if !d.requiresAuth(p) {
				t.Errorf("%q is anonymously readable under PublicSurface (matched %q)", p, bad)
			}
		}
	}
}

// The whole point of the allowlist: an unlisted route is closed even with
// PublicReads=true. Without this, PublicSurface would be decoration.
func TestUnlistedRoutesAreClosedEvenWithPublicReads(t *testing.T) {
	d := Deps{Cfg: config.Config{PublicSurface: true, PublicReads: true}}
	for _, p := range registeredRoutes(t) {
		if publicRoutes[p] || alwaysOpen(p) || strings.HasPrefix(p, "/api/evidence/") {
			continue
		}
		if !d.requiresAuth(p) {
			t.Errorf("%q is anonymous under PublicSurface but is not allowlisted", p)
		}
	}
}

// The allowlist must actually open what it names, and the probes and login
// path must survive it.
func TestAllowlistedRoutesAreOpen(t *testing.T) {
	d := Deps{Cfg: config.Config{PublicSurface: true, PublicReads: false}}
	for p := range publicRoutes {
		if d.requiresAuth(p) {
			t.Errorf("%q is allowlisted but still requires auth", p)
		}
	}
	for _, p := range []string{"/api/health", "/api/ready", "/api/auth/login", "/api/tv-webhook"} {
		if d.requiresAuth(p) {
			t.Errorf("%q must stay reachable under PublicSurface", p)
		}
	}
}

// PublicSurface is opt-in. With it off, requiresAuth must behave exactly as it
// did before this file existed, or every existing deployment changed posture
// silently.
func TestPublicSurfaceOffChangesNothing(t *testing.T) {
	d := Deps{Cfg: config.Config{PublicSurface: false, PublicReads: true}}
	for _, p := range []string{"/api/dashboard", "/api/companies", "/api/screener", "/api/predictions"} {
		if d.requiresAuth(p) {
			t.Errorf("%q became closed with PublicSurface off — the old denylist behaviour changed", p)
		}
	}
	closed := Deps{Cfg: config.Config{PublicSurface: false, PublicReads: false}}
	if !closed.requiresAuth("/api/dashboard") {
		t.Error("/api/dashboard open with PublicReads=false and PublicSurface off")
	}
}

// The 451 guard must not be defeatable by a proxy that strips X-Forwarded-For.
//
// This is the concrete failure: run the container behind a same-host reverse
// proxy (the shape DEPLOY.md documents and fly.toml recommends), and the
// daemon sees RemoteAddr 127.0.0.1 with no forwarding header. requestIsLoopback
// then returns true for every visitor on the internet, and licensed Alpaca and
// Kraken bars stream out of /api/bars and all three CSV exports.
//
// Before the fix this test served 200 with bar data.
func TestRawExportRefusesOnAPublishedDeploymentDespiteLoopbackRemoteAddr(t *testing.T) {
	d, _ := newExportDeps(t)
	// PUBLISHED: a non-loopback allowlist entry is what tells the daemon it is
	// reachable, and it is the same signal that closes PublicReads/OpenSignup.
	d.Cfg.AllowedHosts = []string{"signaldeck.example"}

	for _, tc := range []struct {
		name string
		req  func() *http.Request
	}{
		{"bare loopback RemoteAddr, no forwarding header", func() *http.Request {
			r := httptest.NewRequest("GET", "/api/export/bars.csv?symbol=AAPL&market=stocks", nil)
			r.RemoteAddr = "127.0.0.1:51234"
			return r
		}},
		{"forged X-Forwarded-For claiming loopback", func() *http.Request {
			r := httptest.NewRequest("GET", "/api/export/bars.csv?symbol=AAPL&market=stocks", nil)
			r.RemoteAddr = "127.0.0.1:51234"
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			return r
		}},
	} {
		rec := httptest.NewRecorder()
		d.exportBars(rec, tc.req())
		if rec.Code != 451 {
			t.Errorf("%s: status %d, want 451 (%d bytes of licensed rows served)",
				tc.name, rec.Code, rec.Body.Len())
		}
	}

	// And the operator's genuinely-private box must still work, or the fix is
	// just an outage wearing a licence argument.
	d.Cfg.AllowedHosts = []string{"127.0.0.1:8322"}
	rec := httptest.NewRecorder()
	local := httptest.NewRequest("GET", "/api/export/bars.csv?symbol=AAPL&market=stocks", nil)
	local.RemoteAddr = "127.0.0.1:51234"
	d.exportBars(rec, local)
	if rec.Code != 200 {
		t.Errorf("private loopback deployment: %d, want 200 — the guard became an outage", rec.Code)
	}
}
