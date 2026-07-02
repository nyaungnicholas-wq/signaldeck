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
//     to 127.0.0.1 still sends its own Host, which is rejected);
//   - Origin allowlist with echo-back (no wildcard — only the real web app can
//     read responses cross-origin);
//   - CSRF guard on non-GET (custom header required);
//   - optional bearer-token auth (enabled by setting SIGNALDECK_API_TOKEN;
//     off by default for localhost, on for remote exposure);
//   - a body-size cap on every request.
func (d Deps) secure(next http.Handler) http.Handler {
	allowedOrigins := d.Cfg.WebOrigins
	allowedHosts := d.Cfg.AllowedHosts
	token := d.Cfg.APIToken

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

		// 3. Optional bearer token (remote-exposure guard; off when unset).
		if token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got != token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// 4. CSRF: state-changing methods must carry the custom header AND,
		// when an Origin is present, it must be allowlisted.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get(csrfHeader) == "" {
				http.Error(w, "missing "+csrfHeader+" header", http.StatusForbidden)
				return
			}
			if origin != "" && !originAllowed(origin, allowedOrigins) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		}

		// 5. Body-size cap on every request.
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

		next.ServeHTTP(w, r)
	})
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
