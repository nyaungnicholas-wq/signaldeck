// LAYER 1 (transport half) — Streamable HTTP.
//
// One endpoint, POST only. There is no SSE channel because this server never
// initiates a message: every response is the direct answer to a request, so a
// long-lived stream would be a connection to hold open and defend for no
// benefit. GET is answered with 405 rather than an empty stream, so a client
// that expects a stream is told plainly instead of hanging.
//
// The daemon's existing HTTP defenses (Host allowlist, origin allowlist, body
// cap, the shared rate limiter) already wrap this handler — see
// internal/api/mcpmount.go. What this file adds is the MCP-specific part: the
// TLS requirement for a non-loopback bind, credential resolution, and a
// per-request body cap independent of the outer one.
package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// maxRequestBytes caps one JSON-RPC message. The daemon's outer 128 KB body
// cap also applies; this is the inner bound, so a change to either alone
// cannot open the other.
const maxRequestBytes = 64 << 10

// Handler returns the HTTP handler for the MCP endpoint.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.opts.Enabled {
		writeRPCError(w, http.StatusNotFound, codeDisabled,
			"the SignalDeck MCP server is disabled on this daemon")
		return
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		// Session termination. This server is stateless per request, so there
		// is nothing to tear down; answering 204 keeps spec-following clients
		// from treating a clean exit as an error.
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		writeRPCError(w, http.StatusMethodNotAllowed, codeInvalidRequest,
			"this MCP endpoint accepts POST only; it never initiates messages, so there is no stream to open")
		return
	}

	cl, err := s.auth.Authenticate(r, s.tlsOK(r))
	if err != nil {
		ae, ok := err.(authErr)
		status, code := http.StatusUnauthorized, codeUnauthorized
		msg := err.Error()
		if ok {
			status = ae.status
			if status == http.StatusForbidden {
				code = codeForbidden
			}
		}
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="signaldeck-mcp"`)
		}
		s.audit.record(auditEntry{Client: "unauthenticated", Outcome: "auth-refused", At: s.now()})
		writeRPCError(w, status, code, msg)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeRPCError(w, http.StatusRequestEntityTooLarge, codeInvalidRequest,
			"request body exceeds the MCP message cap")
		return
	}
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		// Batching was removed from MCP in this revision, and accepting it
		// would let one request consume N budget units under one admission
		// check. Refused rather than unrolled.
		writeRPCError(w, http.StatusBadRequest, codeInvalidRequest,
			"JSON-RPC batches are not accepted; send one request per message")
		return
	}

	resp, due := s.Handle(r.Context(), cl, body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !due {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if resp.Error != nil {
		if d, ok := resp.Error.Data.(map[string]any); ok {
			if ra, ok := d["retryAfterSeconds"].(int); ok && ra > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(ra))
			}
		}
	}
	// JSON-RPC application errors travel in a 200 body: the HTTP layer
	// succeeded, and a client that reads only the status code should not
	// conclude the transport is broken.
	_ = json.NewEncoder(w).Encode(resp)
}

// tlsOK reports whether the connection is confidential. X-Forwarded-Proto is
// honoured only behind a trusted proxy, because a client can set that header
// itself and would otherwise be asserting its own transport security.
func (s *Server) tlsOK(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.opts.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func writeRPCError(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: code, Message: msg}})
}
