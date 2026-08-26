package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
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
	return d.secureWith(next, newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst))
}

// secureWith is secure with the limiter supplied by the caller, so the MCP
// mount can share the SAME bucket instance rather than getting a second full
// budget by arriving through a different door (see mcpmount.go).
func (d Deps) secureWith(next http.Handler, limiter *rateLimiter) http.Handler {
	allowedOrigins := d.Cfg.WebOrigins
	allowedHosts := d.Cfg.AllowedHosts

	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Host allowlist — the request's Host must be one we serve.
		if !hostAllowed(r.Host, allowedHosts) {
			httpErr(w, http.StatusForbidden, "forbidden host: "+r.Host+" is not in the daemon's allowed-hosts list")
			return
		}

		// 2. CORS: echo the Origin only if it is explicitly allowlisted.
		origin := r.Header.Get("Origin")
		originOK := origin == "" || originAllowed(origin, allowedOrigins)
		if origin != "" && originAllowed(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			// GET/POST/OPTIONS is the whole surface: every mutation is a POST.
			// The Next proxy also exports PUT/DELETE/PATCH, but no daemon route
			// registers them, so advertising them here would promise a method
			// the mux answers with 405.
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
				httpErr(w, http.StatusForbidden, "origin not allowed: "+origin+" is not in the daemon's web-origins list")
			}
			return
		}

		// 3. Identity: session cookie first; bearer APIToken (when set) is an
		// equivalent alternative for scripts, mapped to the admin user.
		uid := d.resolveUser(r)
		r = withUser(r, uid)
		// Hand the identity out to the access log. withUser returns a NEW
		// request, so the log wrapper outside this closure cannot see the
		// context we just built — and resolving a second time out there would
		// mean a second session lookup per request. Resolution also has to stay
		// BELOW the host check so a rejected host costs no database work.
		if sw, ok := w.(*statusWriter); ok {
			sw.uid = uid
		}

		// 4. Rate limit per client key (user id / token / IP), two tiers.
		writeTier := (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
			strings.HasPrefix(r.URL.Path, "/api/ai/")
		if !limiter.allow(d.clientKey(r, uid), writeTier) {
			w.Header().Set("Retry-After", "1")
			httpErr(w, http.StatusTooManyRequests, "rate limit exceeded — retry in a second")
			return
		}

		// 5. CSRF: state-changing methods must carry the custom header AND,
		// when an Origin is present, it must be allowlisted. The TradingView
		// webhook is exempt — TradingView's servers cannot send the header;
		// that endpoint is authenticated by its own shared secret instead.
		if r.Method != http.MethodGet && r.Method != http.MethodHead &&
			r.URL.Path != "/api/tv-webhook" && !mcpExempt(r.URL.Path) {
			if r.Header.Get(csrfHeader) == "" {
				httpErr(w, http.StatusForbidden, "missing "+csrfHeader+" header — every non-GET "+
					"request must carry it; this is the CSRF guard, not a credential problem")
				return
			}
			if origin != "" && !originAllowed(origin, allowedOrigins) {
				httpErr(w, http.StatusForbidden, "origin not allowed: "+origin+
					" is not in the daemon's web-origins list")
				return
			}
		}

		// 6. Auth enforcement per endpoint.
		if uid == 0 && d.requiresAuth(r.URL.Path) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"authentication required"}` + "\n"))
			return
		}

		// 7. Body-size cap on every request.
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

		next.ServeHTTP(w, r)
	})

	return d.withAccessLog(guarded)
}

// withAccessLog records every request that reaches the daemon.
//
// It wraps the guard chain rather than sitting inside it, and that is the whole
// point: the requests worth having a log for are the ones the guards REJECT — a
// 403 from the host allowlist is someone probing the tunnel, a 429 is abuse or
// a runaway client, a 401 is a credential that stopped working. Logging from
// inside secureWith's handler recorded none of them, because every guard
// returns early.
//
// Until this existed there was NO record that a request had ever been served:
// 2.5 MB of daemon log held 5207 "llm call" lines and not one HTTP request, so
// "was anything read while the tunnel was up?" had no answer.
//
// Path only, never r.URL.RawQuery. A misconfigured TradingView alert puts the
// shared secret in the query string (which is why tvWebhook rejects that form
// outright), and an access log is precisely the durable place it must not land.
func (d Deps) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.code,
			"uid", sw.uid,
			"ms", time.Since(start).Milliseconds())
	})
}

// statusWriter records the status code on its way out so the access log can
// report it. WriteHeader may legitimately never be called (an implicit 200 from
// the first Write), which is why code is seeded to 200 rather than 0.
type statusWriter struct {
	http.ResponseWriter
	code    int
	written bool
	uid     int64 // filled by the guard chain once identity is resolved
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.written {
		s.code, s.written = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

// Flush keeps streaming handlers (SSE) working through the wrapper — without
// it the embedded ResponseWriter's Flush is hidden and a stream buffers until
// the handler returns.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requiresAuth reports whether an anonymous request to path must be rejected.
//   - /api/health and /api/auth/* are always open (you must be able to log in);
//   - user-scoped and spend-incurring endpoints always need identity;
//   - the remaining read-only endpoints (shared market data) are public when
//     SIGNALDECK_PUBLIC_READS=true (the localhost-friendly default).
func (d Deps) requiresAuth(path string) bool {
	// /api/health and /api/ready are PROBES: a monitor, a load balancer or a
	// deploy script has to reach them before it holds any credential, which is
	// the whole reason they exist. /api/ready was omitted here and started
	// 401-ing the moment PublicReads closed — a readiness endpoint nothing can
	// probe. Both answer a SUMMARY ONLY to an anonymous caller (see health/
	// ready): the detail behind it — worker names, the build revision, the
	// specific reasons — is for an authenticated operator, not for whoever
	// finds the tunnel.
	if path == "/api/health" || path == "/api/ready" || strings.HasPrefix(path, "/api/auth/") {
		return false
	}
	// The TradingView webhook is authenticated by its own shared secret, not by
	// a session — it must stay reachable even when SIGNALDECK_PUBLIC_READS=false.
	if path == "/api/tv-webhook" {
		return false
	}
	// THE RECEIPTS. /proof is the one page whose entire purpose is to be shown
	// to someone who has no account here, and it is built from exactly these
	// two reads. They were not exempt, so both 401'd anonymously and the page
	// rendered its error state to every visitor it exists for — while its own
	// source comment claimed it "reads only the already-public GET endpoints".
	//
	// Exempted individually rather than by opening SIGNALDECK_PUBLIC_READS.
	// That flag defaults CLOSED here for a measured reason (A9): daemon/.env
	// allowlists a reserved ngrok hostname, so reachablePrivately() is false
	// and flipping the flag would publish EVERY read endpoint the moment the
	// tunnel starts. Publishing the two endpoints that are meant to be public
	// is not the same decision as publishing all of them.
	//
	// Both are safe to serve anonymously on their own terms:
	//   - neither is user-scoped and neither spends LLM budget;
	//   - track-record is served from cache (~1.6ms measured) and already
	//     carries its own gating — it withholds figures rather than inflating
	//     them when the sample is too thin;
	//   - ledger/verify is CPU-bound (~2.5s), and its resource-exhaustion lever
	//     was already closed by A11: ledgerVerifyConcurrency caps concurrent
	//     walks at 2 and ledgerVerifyTimeout bounds each at 30s.
	//
	// Known and accepted: verify may APPEND a signed anchor on a cadence (see
	// maybeAnchor), so this is a public read with a bounded write side effect.
	// The cadence gate, not the auth gate, is what limits it.
	if path == "/api/track-record" || path == "/api/ledger/verify" {
		return false
	}
	// The MCP endpoint authenticates itself, and strictly more tightly than
	// this gate does: a signed, expiring, revocable per-client key, with
	// anonymous access permitted only on a privately-reachable bind. Letting
	// the session gate answer first would 401 a legitimate key holder while
	// adding nothing — see internal/mcp/auth.go.
	if mcpExempt(path) {
		return false
	}
	// Every /api/ai/ route spends real LLM budget, so gate the whole prefix by
	// default rather than by remembering to list each new one. /api/ai/status
	// only reports availability and spends nothing, so it stays public.
	if strings.HasPrefix(path, "/api/ai/") && path != "/api/ai/status" {
		return true
	}
	switch {
	case path == "/api/watchlist",
		path == "/api/subscribe",
		path == "/api/unsubscribe",
		strings.HasPrefix(path, "/api/portfolio"),
		strings.HasPrefix(path, "/api/alerts"),
		// discovery wave (appended): candidate mutations are session-scoped.
		path == "/api/candidates/add",
		path == "/api/candidates/monitor-all",
		path == "/api/candidates/dismiss",
		// An outbound SIDE EFFECT, not a read. It fell through to the
		// PublicReads default below, so on any deployment that deliberately
		// opens public reads -- a supported configuration, see .env.example --
		// an anonymous caller could drive the configured Discord/Telegram/Slack/
		// SMTP transport at the write-tier rate limit. A read flag must not
		// govern something that leaves the machine.
		path == "/api/notify/test":
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
