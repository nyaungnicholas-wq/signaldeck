package api

import (
	"net/http"
	"strings"
)

// maxBodyBytes caps every request body. The largest legitimate payload is a
// risk request with a handful of holdings; 128 KB is generous and stops
// JSON-bomb / unbounded-allocation DoS.
const maxBodyBytes = 128 << 10

// csrfHeader is a custom request header the web app sends on every call.
// Requiring it on state-changing methods defeats CSRF: a cross-origin page
// cannot send a custom header without triggering a CORS preflight, and the
// preflight only succeeds for an allowlisted origin — which an attacker page
// is not. A plain <form>/simple-request POST (the text/plain trick that
// bypasses preflight) lacks this header and is rejected.
const csrfHeader = "X-Signaldeck"

// secure wraps the mux with the daemon's HTTP defenses:
//   - Host allowlist (blocks DNS-rebinding: a remote page rebinding its domain
//     to 127.0.0.1 still sends its own Host, which is rejected). An EMPTY
//     allowlist denies everything — only explicitly listed hosts are served;
//   - Origin allowlist with echo-back (no wildcard — only the real web app can
//     read responses cross-origin);
//   - identity resolution (session cookie first, else bearer APIToken → admin);
//   - per-client token-bucket rate limiting (429 + Retry-After);
//   - CSRF guard on non-GET (custom header required);
//   - per-endpoint auth enforcement (see requiresAuth);
//   - a body-size cap on every request.
func (d Deps) secure(next http.Handler) http.Handler {
	allowedOrigins := d.Cfg.WebOrigins
	allowedHosts := d.Cfg.AllowedHosts
	limiter := newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Host allowlist — the request's Host must be one we serve.
		if !hostAllowed(r.Host, allowedHosts) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}

		// 2. CORS: echo the Origin only if it is explicitly allowlisted.
		origin := r.Header.Get("Origin")
		originOK := origin == "" || originAllowed(origin, allowedOrigins)
		if origin != "" && originAllowed(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+csrfHeader+", Authorization")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			// Preflight: 204 only for an allowlisted origin; otherwise the
			// missing ACAO header makes the browser block the real request.
			if originOK {
				w.WriteHeader(http.StatusNoContent)
			} else {
				http.Error(w, "origin not allowed", http.StatusForbidden)
			}
			return
		}

		// 3. Identity: session cookie first; bearer APIToken (when set) is an
		// equivalent alternative for scripts, mapped to the admin user.
		uid := d.resolveUser(r)
		r = withUser(r, uid)

		// 4. Rate limit per client key (user id / token / IP), two tiers.
		writeTier := (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
			strings.HasPrefix(r.URL.Path, "/api/ai/")
		if !limiter.allow(d.clientKey(r, uid), writeTier) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// 5. CSRF: state-changing methods must carry the custom header AND,
		// when an Origin is present, it must be allowlisted. The TradingView
		// webhook is exempt — TradingView's servers cannot send the header;
		// that endpoint is authenticated by its own shared secret instead.
		if r.Method != http.MethodGet && r.Method != http.MethodHead &&
			r.URL.Path != "/api/tv-webhook" {
			if r.Header.Get(csrfHeader) == "" {
				http.Error(w, "missing "+csrfHeader+" header", http.StatusForbidden)
				return
			}
			if origin != "" && !originAllowed(origin, allowedOrigins) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		}

		// 6. Auth enforcement per endpoint.
		if uid == 0 && d.requiresAuth(r.URL.Path) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"authentication required"}` + "\n"))
			return
		}

		// 7. Body-size cap on every request.
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

		next.ServeHTTP(w, r)
	})
}

// requiresAuth reports whether an anonymous request to path must be rejected.
//   - /api/health and /api/auth/* are always open (you must be able to log in);
//   - user-scoped and spend-incurring endpoints always need identity;
//   - the remaining read-only endpoints (shared market data) are public when
//     SIGNALDECK_PUBLIC_READS=true (the localhost-friendly default).
func (d Deps) requiresAuth(path string) bool {
	if path == "/api/health" || strings.HasPrefix(path, "/api/auth/") {
		return false
	}
	// The TradingView webhook is authenticated by its own shared secret, not by
	// a session — it must stay reachable even when SIGNALDECK_PUBLIC_READS=false.
	if path == "/api/tv-webhook" {
		return false
	}
	switch {
	case path == "/api/watchlist",
		path == "/api/subscribe",
		path == "/api/unsubscribe",
		strings.HasPrefix(path, "/api/portfolio"),
		strings.HasPrefix(path, "/api/alerts"),
		path == "/api/ai/chat",
		path == "/api/ai/filing",
		path == "/api/ai/debate",
		// discovery wave (appended): candidate mutations are session-scoped.
		path == "/api/candidates/add",
		path == "/api/candidates/monitor-all",
		path == "/api/candidates/dismiss":
		return true
	}
	return !d.Cfg.PublicReads
}

func hostAllowed(host string, allowed []string) bool {
	if host == "" {
		return false
	}
	for _, a := range allowed {
		if strings.EqualFold(host, a) {
			return true
		}
	}
	return false
}

func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(origin, a) {
			return true
		}
	}
	return false
}
