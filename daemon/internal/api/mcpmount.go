// Mounting for the MCP server (internal/mcp).
//
// This file is the seam between the daemon's HTTP defenses and the MCP
// package, and it exists so that internal/mcp does NOT import internal/api.
// The two primitives the MCP server reuses — the per-client token bucket and
// the constant-time credential compare — are handed to it as functions here,
// so there is exactly one implementation of each in the process and no import
// cycle between the package that mounts and the package that serves.
//
// The endpoint sits behind secure(), so it inherits the Host allowlist, the
// origin allowlist, the 128 KB body cap and the shared rate limiter. Two
// explicit exemptions are made in security.go, both because MCP is
// authenticated by its own bearer key and never by a session cookie:
//
//   - CSRF: an MCP client cannot send the X-Signaldeck header, and CSRF is
//     structurally inapplicable to an endpoint that ignores cookies entirely —
//     a browser's ambient credentials buy an attacker nothing here.
//   - requiresAuth: /mcp does its own strictly stronger authentication, and
//     letting the session gate answer first would 401 a legitimate key holder.
package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/mcp"
)

// mcpPath is the single MCP endpoint.
const mcpPath = "/mcp"

// registerMCP mounts the MCP server when it is enabled. It is deliberately
// silent and inert when disabled: no route, no handler, nothing listening.
//
// The rate limiter passed in is the SAME limiter instance the rest of the API
// uses, so an MCP client and an HTTP client cannot each get a full budget by
// arriving through different doors.
func (d Deps) registerMCP(mux *http.ServeMux, limiter *rateLimiter) *mcp.Server {
	if !d.Cfg.MCPEnabled {
		return nil
	}
	srv, err := mcp.New(mcp.Options{
		Enabled:            true,
		Secret:             d.Cfg.MCPSecret,
		ReachablePrivately: d.Cfg.ReachablePrivately(),
		TrustProxy:         d.Cfg.TrustProxy,
		AuditPath:          d.Cfg.MCPAuditPath,
		DailyCallCap:       d.Cfg.MCPDailyCalls,
		RevokedClients:     d.Cfg.MCPRevoked,
		// An anonymous loopback caller — the operator's own Claude Code or
		// Codex session — gets the full advisory surface. Off loopback there
		// is no anonymous caller at all, so this list is unreachable.
		AnonymousScopes: []string{mcp.ScopeMethodology, mcp.ScopeVerdicts, mcp.ScopeRecord},
	}, mcp.StoreSource{St: d.St}, limiter.allow, tokenEqual)
	if err != nil {
		// A server that cannot be constructed is not mounted. Refusing to
		// serve is the correct response to a misconfiguration on a surface
		// whose whole purpose is to be careful.
		return nil
	}
	h := srv.Handler()
	mux.Handle("POST "+mcpPath, h)
	mux.Handle("DELETE "+mcpPath, h)
	mux.Handle("GET "+mcpPath, h)
	return srv
}

// mcpExempt reports whether a path is the MCP endpoint, for the two guards in
// security.go that must step aside for it.
func mcpExempt(path string) bool { return path == mcpPath }
