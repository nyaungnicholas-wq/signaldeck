// Package mcp serves SignalDeck's methodology and current regime verdicts to
// AI clients over the Model Context Protocol, while making the underlying
// dataset and research machinery non-extractable.
//
// # Why this package is shaped defensively rather than conveniently
//
// SignalDeck was deliberately unpublished: loopback-only, never deployed, and
// an LLM council rejected public exposure because a live money-adjacent
// predictor "reads as compliance-unaware". An MCP endpoint re-opens exactly
// that door, so the door is built to only open in one shape — advisory. The
// shape is ENFORCED here, not requested in a document:
//
//   - No tool returns a trade recommendation, target price, entry/exit, or
//     position size. There is no code path that can emit one, because no
//     response type has a field for one (see allowlist.go).
//   - Every number ships with its conviction band, sample size and caveat.
//     A bare accuracy figure is a bug, and allowlist_test.go fails the build
//     on one.
//   - Every response carries a machine-readable `disclaimer`.
//   - The headline is never "83% accurate" — that is the persistence base
//     rate, which the naive "it continued" answer also scores. The honest and
//     stronger claim is DISCRIMINATION: ~72.9% at low conviction against
//     ~97.6% at very-high, a 24.7pp spread that held across 24 quarters on a
//     survivorship-clean universe of 54,969 observations.
//   - The live directional record (46.7%, negative Brier skill) is
//     retrievable through get_track_record and cannot be hidden — a test
//     asserts it survives even when the store has nothing to say.
//
// # What this design does NOT guarantee
//
// It does not guarantee zero leakage, and claiming otherwise would be the
// dishonest part. Any interface that answers questions about data is a channel
// that leaks it; enough queries reconstruct some of it. What the six layers
// below buy is BOUNDED, AUDITABLE, EXPENSIVE-TO-ABUSE disclosure: verdicts are
// banded rather than continuous, series are structurally unrepresentable,
// enumeration is detected and throttled, and every call is attributable.
// See MCP_SERVER.md for the same statement in prose.
//
// # The six layers (each fails closed, each independently tested)
//
//	1 identity/transport  — auth.go       — signed expiring client keys, TLS
//	                                        off-loopback, loopbackOnly inherited
//	2 authorization       — tools.go      — default-deny scopes, closed tool
//	                                        surface, typed/enum params only
//	3 data boundary       — allowlist.go  — one serialization chokepoint,
//	                                        allowlist not blocklist
//	4 budget + latency    — budget.go     — reuses api's token bucket, adds
//	                                        result caps + a daily aggregate,
//	                                        serves from a precomputed cache
//	5 audit + anomaly     — audit.go      — append-only sink, enumeration and
//	                                        sweep detection, auto-throttle
//	6 provenance          — watermark.go  — per-client, semantically neutral
//
// The package imports neither internal/api nor internal/config: the two
// primitives it reuses from them (the token bucket and the constant-time
// credential compare) are INJECTED as functions by the mounting code in
// internal/api/mcpmount.go. That keeps the reuse real while leaving this
// package testable without an HTTP server or a database.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ProtocolVersion is the MCP revision this server implements.
const ProtocolVersion = "2025-06-18"

// ServerName / ServerVersion identify this server to clients.
const (
	ServerName    = "signaldeck"
	ServerVersion = "0.1.0"
)

// Options is the server's configuration. Every field defaults to the closed
// choice: a zero Options serves nothing to nobody.
type Options struct {
	// Enabled gates the whole server. False = every request is refused before
	// any tool is consulted.
	Enabled bool

	// Secret is the HMAC key that signs client credentials. Empty means no
	// credential can ever verify, which is the correct behaviour rather than a
	// misconfiguration to work around: without it only loopback-anonymous
	// access is possible, and only when ReachablePrivately is true.
	Secret string

	// ReachablePrivately mirrors config.ReachablePrivately for the daemon's
	// bind address: loopback AND no reverse tunnel configured. It is the ONLY
	// condition under which an anonymous client is served. A tunnel makes the
	// process public without changing the bind, which is why the bind alone is
	// not consulted here.
	ReachablePrivately bool

	// AuditPath is the append-only JSONL audit sink. Empty = in-memory only
	// (tests); production wiring always sets it.
	AuditPath string

	// DailyCallCap bounds one client's calls per UTC day (0 = default).
	DailyCallCap int

	// DailyByteCap bounds one client's total response volume per UTC day
	// (0 = default). Calls and bytes are bounded separately because a small
	// number of large responses and a large number of small ones are the same
	// extraction with different pacing.
	DailyByteCap int

	// ResultCap bounds how many items any list-bearing response may contain
	// (0 = default). It is the per-response half of the budget.
	ResultCap int

	// AnonymousScopes are granted to a loopback-anonymous caller. Ignored
	// entirely when ReachablePrivately is false.
	AnonymousScopes []string

	// TrustProxy mirrors the daemon's SIGNALDECK_TRUST_PROXY. It is the ONLY
	// condition under which X-Forwarded-Proto is allowed to satisfy the TLS
	// requirement — an untrusted client can set that header itself.
	TrustProxy bool

	// RevokedClients are client ids whose keys are dead regardless of the
	// signature or the expiry they carry. Revocation is by IDENTITY rather
	// than by key string so a leaked key cannot be resurrected by re-minting
	// it, and so an operator can revoke without possessing the key.
	RevokedClients []string
}

func (o Options) dailyCallCap() int {
	if o.DailyCallCap > 0 {
		return o.DailyCallCap
	}
	return defaultDailyCallCap
}

func (o Options) dailyByteCap() int {
	if o.DailyByteCap > 0 {
		return o.DailyByteCap
	}
	return defaultDailyByteCap
}

func (o Options) resultCap() int {
	if o.ResultCap > 0 {
		return o.ResultCap
	}
	return defaultResultCap
}

// Source is the narrow read-only view of the daemon's data this server needs.
// It is an interface rather than *store.Store so the defence layers can be
// tested without a database, and so the set of queries the MCP surface can
// possibly make is visible in one place: three reads, none of them parameterised
// by anything a client controls except a validated symbol.
type Source interface {
	// Verdicts returns every stored structural forecast. The server caches the
	// result and filters in memory — a client's symbol never becomes part of a
	// query.
	Verdicts(ctx context.Context) ([]Verdict, error)
	// ModelHealth returns the stored model-health verdict JSON for a model key
	// ("" when the worker has not written one yet).
	ModelHealth(ctx context.Context, model string) (string, error)
	// Preregistration returns the frozen per-predictor claims plus chain state.
	Preregistration(ctx context.Context) (PreregSummary, error)
	// EarliestGradeableOn is the first date an outstanding structural forecast
	// can actually be graded, DERIVED from the calls on disk rather than
	// declared. It is reported next to the pre-registered FirstGradableOn, never
	// instead of it: the frozen date is a commitment and must not be edited, but
	// it was a forecast about when data would mature, and on 2026-08-04 the two
	// differed by ten days (frozen 2026-08-07, derived 2026-08-17). Serving only
	// the frozen one presented a stale date as current fact.
	// ok=false means nothing structural is outstanding.
	EarliestGradeableOn(ctx context.Context) (date string, ok bool, err error)
}

// Verdict is one symbol's structural regime call, already reduced to the
// fields the MCP surface is allowed to know about. The reduction happens in
// the adapter (storesource.go), so raw store rows never enter this package's
// response path at all.
type Verdict struct {
	Symbol             string
	Kind               string
	Regime             string
	Conviction         float64 // used to derive a BAND; never serialized
	HistoricalAccuracy float64
	HorizonDays        int
	N                  int
	AsOfDay            string // YYYY-MM-DD, deliberately not a timestamp
	Tradeability       string
	EvidenceCaveat     string
	FirstGradableOn    string
}

// PreregSummary is the chain state plus one row per frozen claim.
type PreregSummary struct {
	ChainVerified    bool
	BrokenAtSeq      int64
	RegisteredBefore bool
	FirstGradableOn  string
	Claims           []PreregClaim
}

// PreregClaim is one predictor's frozen, hashed commitment. TopBandClaim is
// named for what it is — the highest per-band claimed accuracy in the frozen
// table — rather than "headline accuracy", which would invite exactly the
// population-average reading this platform spends its documentation refusing.
type PreregClaim struct {
	Kind         string
	Question     string
	Baseline     string
	HorizonDays  int
	TopBandClaim float64
	RegisteredOn string
	SpecHash     string
}

// Server is the MCP server. It is safe for concurrent use.
type Server struct {
	opts   Options
	src    Source
	auth   *authenticator
	budget *budgetKeeper
	audit  *auditor
	cache  *verdictCache
	now    func() time.Time

	// tokenEqual is the daemon's constant-time credential compare, injected
	// from internal/api so there is exactly one implementation of it.
	tokenEqual func(got, want string) bool
}

// New builds a server. allow is the daemon's existing per-client token bucket
// (internal/api's rateLimiter.allow) and tokenEqual its constant-time compare;
// both are injected rather than reimplemented. Either being nil is a
// programming error and is refused rather than silently degraded — a limiter
// that defaults to "allow" is not a limiter.
func New(opts Options, src Source, allow func(key string, write bool) bool,
	tokenEqual func(got, want string) bool) (*Server, error) {
	if src == nil {
		return nil, errors.New("mcp: nil Source")
	}
	if allow == nil {
		return nil, errors.New("mcp: nil rate limiter — refusing to run unlimited")
	}
	if tokenEqual == nil {
		return nil, errors.New("mcp: nil tokenEqual — refusing to compare credentials naively")
	}
	now := time.Now
	s := &Server{
		opts:       opts,
		src:        src,
		now:        now,
		tokenEqual: tokenEqual,
	}
	s.auth = newAuthenticator(opts, tokenEqual, now)
	s.audit = newAuditor(opts.AuditPath, now)
	s.budget = newBudgetKeeper(opts, allow, s.audit, now)
	s.cache = newVerdictCache(src, now)
	return s, nil
}

// SetClock replaces the server's clock. Tests only; production never calls it.
func (s *Server) SetClock(f func() time.Time) {
	s.now = f
	s.auth.now = f
	s.audit.now = f
	s.budget.now = f
	s.cache.now = f
}

// ── JSON-RPC 2.0 envelope ───────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC + MCP error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	// Application codes. Distinct so a client can tell "you asked wrongly"
	// from "you asked too much" from "you may not ask this".
	codeUnauthorized   = -32001
	codeForbidden      = -32002
	codeRateLimited    = -32003
	codeBudgetExceeded = -32004
	codeDisabled       = -32005
)

func errResp(id json.RawMessage, code int, msg string, data any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}}
}

func okResp(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// ── request handling ────────────────────────────────────────────────────────

// Handle executes one JSON-RPC request for an already-authenticated client and
// returns the response, or ok=false for a notification (no reply is due).
//
// Every path through this function that reaches a tool passes through, in
// order: enabled check → scope check (layer 2) → budget (layer 4) → tool →
// allowlist serialization (layer 3) → watermark (layer 6) → audit (layer 5).
// The order matters: a client that is over budget must not be able to make the
// server do work, and nothing reaches the wire without passing the allowlist.
func (s *Server) Handle(ctx context.Context, cl *Client, raw []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return errResp(nil, codeParse, "malformed JSON-RPC request", nil), true
	}
	isNotification := len(req.ID) == 0
	if req.JSONRPC != "2.0" {
		if isNotification {
			return rpcResponse{}, false
		}
		return errResp(req.ID, codeInvalidRequest, `jsonrpc must be "2.0"`, nil), true
	}
	if !s.opts.Enabled {
		if isNotification {
			return rpcResponse{}, false
		}
		return errResp(req.ID, codeDisabled, "the SignalDeck MCP server is disabled on this daemon", nil), true
	}

	switch req.Method {
	case "notifications/initialized", "notifications/cancelled":
		return rpcResponse{}, false
	case "initialize":
		return okResp(req.ID, s.initializeResult()), true
	case "ping":
		return okResp(req.ID, map[string]any{}), true
	case "tools/list":
		return okResp(req.ID, map[string]any{"tools": toolDescriptors(cl)}), true
	case "tools/call":
		if isNotification {
			// A tool call with no id cannot be answered and would be an
			// unattributable, unbilled invocation. Refuse to run it.
			return rpcResponse{}, false
		}
		return s.handleToolCall(ctx, cl, req), true
	default:
		if isNotification {
			return rpcResponse{}, false
		}
		return errResp(req.ID, codeMethodNotFound,
			"unsupported method "+req.Method+"; this server implements initialize, ping, tools/list and tools/call only", nil), true
	}
}

func (s *Server) initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": ServerName, "version": ServerVersion},
		"instructions": "SignalDeck answers questions about research METHODOLOGY and about today's " +
			"structural regime verdicts. It does not serve market data, price history, or backtests, " +
			"and it never returns a trade recommendation, target, entry, exit or size. Numbers arrive " +
			"banded, with a sample size and a caveat; quote them that way. The platform's live " +
			"directional record is negative-skill and is available through get_track_record — do not " +
			"present this server's output as predictive of price.",
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolCall(ctx context.Context, cl *Client, req rpcRequest) rpcResponse {
	var p toolCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, codeInvalidParams, "params must be an object with name and arguments", nil)
		}
	}
	start := s.now()
	name := p.Name

	t, ok := toolByName[name]
	if !ok {
		// Say nothing about why a name is unknown: "forbidden" and
		// "misspelled" must look identical, or the error becomes a directory
		// of what exists. The full legitimate surface is in tools/list.
		s.audit.record(auditEntry{Client: cl.ID, Tool: name, Outcome: "unknown-tool", At: s.now()})
		return errResp(req.ID, codeMethodNotFound,
			"unknown tool; call tools/list for the complete surface. This server executes no queries, "+
				"code, expressions or file access under any tool name.", nil)
	}

	// LAYER 2 — default-deny scope.
	if !cl.HasScope(t.Scope) {
		s.audit.record(auditEntry{Client: cl.ID, Tool: name, Outcome: "scope-denied", At: s.now()})
		return errResp(req.ID, codeForbidden,
			"your credential does not carry the "+t.Scope+" scope", map[string]any{"requiredScope": t.Scope})
	}

	// LAYER 4 — budget, before any work happens.
	if d := s.budget.admit(cl.ID); !d.allowed {
		s.audit.record(auditEntry{Client: cl.ID, Tool: name, Outcome: "budget-" + d.reason, At: s.now()})
		code := codeRateLimited
		if d.reason != "rate" {
			code = codeBudgetExceeded
		}
		return errResp(req.ID, code, d.message, map[string]any{"retryAfterSeconds": d.retryAfter})
	}

	args, perr := t.Parse(p.Arguments)
	if perr != nil {
		s.audit.record(auditEntry{Client: cl.ID, Tool: name, Outcome: "invalid-params",
			ParamHash: hashParams(p.Arguments), At: s.now()})
		return errResp(req.ID, codeInvalidParams, perr.Error(), nil)
	}

	// LAYER 5 — feed the anomaly detector the shape of the request before
	// serving it, so a sweep is caught on the call that completes it.
	if flag := s.audit.observe(cl.ID, args); flag != "" {
		s.budget.throttle(cl.ID, flag)
	}

	payload, terr := t.Run(ctx, s, cl, args)
	if terr != nil {
		s.audit.record(auditEntry{Client: cl.ID, Tool: name, Outcome: "error",
			ParamHash: hashParams(p.Arguments), At: s.now()})
		return errResp(req.ID, codeInternal, terr.Error(), nil)
	}

	// LAYER 3 — the single serialization chokepoint. Nothing reaches a client
	// without passing it, and it drops rather than passes what it does not
	// recognise.
	clean, dropped := Sanitize(name, payload)
	if len(dropped) > 0 {
		// A drop in production means a tool grew a field nobody allowlisted.
		// The client gets the safe subset; the operator gets told.
		s.audit.record(auditEntry{Client: cl.ID, Tool: name, Outcome: "allowlist-drop",
			Dropped: dropped, At: s.now()})
	}

	// LAYER 6 — provenance. Formatting only; never a number.
	clean = watermark(cl.ID, name, clean)

	body, err := json.Marshal(clean)
	if err != nil {
		return errResp(req.ID, codeInternal, "response serialization failed", nil)
	}
	s.budget.charge(cl.ID, len(body))
	s.audit.record(auditEntry{
		Client: cl.ID, Tool: name, ParamHash: hashParams(p.Arguments),
		Bytes: len(body), Outcome: "ok", At: s.now(),
		LatencyMicros: s.now().Sub(start).Microseconds(),
	})

	return okResp(req.ID, map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(body)}},
		"structuredContent": clean,
		"isError":           false,
	})
}

// ── shared response furniture ───────────────────────────────────────────────

// disclaimerText is attached to every single response. It is a field rather
// than prose in a doc because a machine consumer needs to be able to find it.
const disclaimerText = "Research output, not investment advice. SignalDeck does not provide " +
	"recommendations, targets, entries, exits or position sizes, and nothing here is a solicitation " +
	"to trade. Accuracy figures are measured hit rates for a stated question and conviction band — " +
	"they are not returns, and at the top trend band accuracy and forward return are INVERTED."

// headlineGuard is the sentence that must accompany any mention of the
// aggregate structural accuracy, so the base rate is never quoted as skill.
const headlineGuard = "Do not quote the 83% aggregate as accuracy or skill: persistence — always " +
	"answering \"the regime continues\" — scores the same. The measured claim is DISCRIMINATION: " +
	"72.9% at low conviction versus 97.6% at very-high, a 24.7pp spread across 24 quarters on a " +
	"survivorship-clean universe of 54,969 independent observations."

// band names the conviction band, which is the unit every number is quoted in.
// A continuous conviction is never serialized: it is a per-symbol real number
// and therefore a far better extraction channel than the band it falls in.
func band(conv float64) string {
	switch {
	case conv >= 0.9:
		return "very-high (>=0.9)"
	case conv >= 0.8:
		return "high (0.8-0.9)"
	case conv >= 0.5:
		return "moderate (0.5-0.8)"
	default:
		return "low (<0.5)"
	}
}

// capList truncates a list to the per-response result cap and reports it,
// because a silently truncated list reads as a complete one.
func capList[T any](in []T, cap int) ([]T, string) {
	if len(in) <= cap {
		return in, ""
	}
	return in[:cap], fmt.Sprintf("truncated to the per-response cap of %d items (%d available); "+
		"this server does not paginate, by design", cap, len(in))
}

var _ = strings.TrimSpace // keep strings imported for the helpers below

// ── stdio transport ─────────────────────────────────────────────────────────

// ServeStdio runs the server over a line-delimited JSON-RPC stream, which is
// how Claude Code and Codex launch a local server. A stdio client is by
// construction the operator on their own machine, so it receives the
// loopback-anonymous identity — but still with explicit scopes, still budgeted,
// and still audited, because "it is only me" is how the interesting bugs stay
// invisible.
func (s *Server) ServeStdio(ctx context.Context, in *json.Decoder, out *json.Encoder) error {
	cl := s.auth.stdioClient()
	var mu sync.Mutex
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var raw json.RawMessage
		if err := in.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return err // clean client exit
			}
			// A syntax error desynchronises a byte stream: there is no
			// dependable way to find where the next message begins, so
			// continuing would mean interpreting arbitrary fragments as
			// requests. Report the parse error and close, rather than guess.
			mu.Lock()
			_ = out.Encode(errResp(nil, codeParse,
				"malformed JSON on the stdio stream; the stream cannot be resynchronised, "+
					"so the connection is closing", nil))
			mu.Unlock()
			return err
		}
		resp, due := s.Handle(ctx, cl, raw)
		if !due {
			continue
		}
		mu.Lock()
		err := out.Encode(resp)
		mu.Unlock()
		if err != nil {
			return err
		}
	}
}
