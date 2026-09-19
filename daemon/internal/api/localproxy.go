package api

import (
	"net/http"
	"strings"
)

// localProxyAsserted is the ONLY way a request carrying a forwarding header
// can count as local. The loopback-bound private web launcher
// (ops/start-local-workspace.ps1) presents the shared
// SIGNALDECK_LOCAL_PROXY_KEY in X-Signaldeck-Local and the socket itself must
// still be loopback. A browser cannot manufacture the key, the proxy never
// forwards a client-supplied copy of that header, and an unset key matches
// nothing, so the exception cannot be reproduced by deleting X-Forwarded-For
// on a wildcard bind, which is what the previous scheme allowed
// (audit 2026-09-07).
func (d Deps) localProxyAsserted(r *http.Request) bool {
	if d.Cfg.LocalProxyKey == "" {
		return false
	}
	return tokenEqual(r.Header.Get("X-Signaldeck-Local"), d.Cfg.LocalProxyKey) && remoteAddrIsLoopback(r)
}

func remoteAddrIsLoopback(r *http.Request) bool {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.")
}
