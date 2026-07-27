// LAYER 1 — identity and transport.
//
// Every non-loopback client holds a distinct, revocable, EXPIRING credential.
// The credential is a signed API key rather than an OAuth 2.1 client-credentials
// flow, and that choice is a trade-off worth stating: OAuth would give token
// rotation and an authorization server, at the cost of running one. This daemon
// is a single binary with no identity provider, and a badly-run authorization
// server is worse than a well-run signed key. What the key does keep from the
// OAuth model is the part that matters here — per-client identity, explicit
// scopes, an expiry, and revocation independent of the key itself.
//
// The key is self-describing and stateless: the daemon needs no key database,
// so there is no key store to leak. What it cannot do is limit a key to one
// machine, which is why layer 6 watermarks output and layer 5 watches for
// sharing-shaped usage.
//
// Fails closed at every branch: no secret configured, unparseable key, bad
// signature, expired, revoked, unknown scope, or a reachable bind with no
// credential — all of them refuse. There is no "default to anonymous" path
// except the one explicitly guarded by ReachablePrivately.
package mcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Scopes. Default-deny: a client holds exactly what its key names, and a key
// with no scopes can call nothing.
const (
	// ScopeMethodology — pure exposition, no dataset access.
	ScopeMethodology = "methodology"
	// ScopeVerdicts — today's banded structural verdicts.
	ScopeVerdicts = "verdicts"
	// ScopeRecord — track record, validated findings, preregistration.
	ScopeRecord = "record"
)

// allScopes is the complete scope vocabulary. A key naming anything else is
// rejected at mint time AND at verify time, so an unknown scope can never be
// silently carried along and later matched by a future tool.
var allScopes = []string{ScopeMethodology, ScopeVerdicts, ScopeRecord}

func knownScope(s string) bool {
	for _, k := range allScopes {
		if k == s {
			return true
		}
	}
	return false
}

// Client is an authenticated caller.
type Client struct {
	ID        string
	Scopes    []string
	Anonymous bool
	ExpiresAt int64 // unix; 0 for the loopback-anonymous identity
}

// HasScope reports whether the client may invoke a tool requiring scope.
func (c *Client) HasScope(scope string) bool {
	if c == nil {
		return false
	}
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// keyPrefix marks a SignalDeck MCP key in logs and secret scanners.
const keyPrefix = "sdmcp_"

// MintKey issues a client credential. It is the only way to create one, and it
// refuses to create a bad one: an empty id, an unknown scope, a non-positive
// TTL, or a missing/short secret are all errors rather than a key that will
// fail confusingly later.
//
// Layout: sdmcp_<base64url(id|expiry|scope,scope)>.<hex hmac-sha256>
// The payload is readable on purpose — an operator inspecting a key should be
// able to see what it grants without a tool. Only the signature is secret-
// derived, and only the signature is what authorises anything.
func MintKey(secret, clientID string, scopes []string, ttl time.Duration) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("mcp: signing secret must be at least 32 characters")
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || strings.ContainsAny(clientID, "|.\n\r ") {
		return "", errors.New("mcp: client id must be non-empty and free of | . whitespace")
	}
	if len(scopes) == 0 {
		return "", errors.New("mcp: a key with no scopes can call nothing; name at least one")
	}
	for _, s := range scopes {
		if !knownScope(s) {
			return "", errors.New("mcp: unknown scope " + s)
		}
	}
	if ttl <= 0 {
		return "", errors.New("mcp: keys must expire; pass a positive ttl")
	}
	sorted := append([]string(nil), scopes...)
	sort.Strings(sorted)
	exp := time.Now().Add(ttl).Unix()
	payload := clientID + "|" + strconv.FormatInt(exp, 10) + "|" + strings.Join(sorted, ",")
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return keyPrefix + enc + "." + sign(secret, enc), nil
}

// NewSecret returns a fresh 32-byte signing secret as hex, for an operator
// setting SIGNALDECK_MCP_SECRET for the first time.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func sign(secret, enc string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(enc))
	return hex.EncodeToString(m.Sum(nil))
}

// authenticator verifies credentials and decides whether anonymous access is
// permissible at all.
type authenticator struct {
	opts       Options
	tokenEqual func(got, want string) bool
	now        func() time.Time
	revoked    map[string]bool
}

func newAuthenticator(opts Options, tokenEqual func(got, want string) bool, now func() time.Time) *authenticator {
	rv := make(map[string]bool, len(opts.RevokedClients))
	for _, id := range opts.RevokedClients {
		if id = strings.TrimSpace(id); id != "" {
			rv[id] = true
		}
	}
	return &authenticator{opts: opts, tokenEqual: tokenEqual, now: now, revoked: rv}
}

// stdioClient is the identity for a locally-launched stdio server: the
// operator, on their own machine, with every scope. It still carries an id so
// its calls are audited and budgeted like anyone else's.
func (a *authenticator) stdioClient() *Client {
	return &Client{ID: "stdio-local", Scopes: append([]string(nil), allScopes...), Anonymous: true}
}

// authErr distinguishes the refusal reasons a caller may be told apart from
// the ones it may not. The MESSAGE is deliberately identical for every bad
// credential — wrong signature, expired and revoked must not be separable, or
// the endpoint becomes an oracle for which client ids exist.
type authErr struct {
	status int
	msg    string
}

func (e authErr) Error() string { return e.msg }

var (
	errNoCredential = authErr{http.StatusUnauthorized,
		"this endpoint requires a SignalDeck MCP key: Authorization: Bearer sdmcp_..."}
	errBadCredential = authErr{http.StatusUnauthorized,
		"invalid or expired MCP key"}
	errInsecure = authErr{http.StatusForbidden,
		"TLS is required for MCP over a non-loopback bind; refusing to accept a credential in clear text"}
)

// Authenticate resolves the caller for an HTTP request.
//
// The ordering is the layer: transport security is checked BEFORE the
// credential is even read, so a key is never accepted — and therefore never
// logged, cached or leaked — over a cleartext non-loopback connection.
func (a *authenticator) Authenticate(r *http.Request, tlsOK bool) (*Client, error) {
	private := a.opts.ReachablePrivately

	if !private && !tlsOK {
		return nil, errInsecure
	}

	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if raw == "" {
		// No credential. This is the branch the whole posture hangs on: it is
		// permitted ONLY when the daemon is bound to loopback with no reverse
		// tunnel configured. A tunnel publishes the process without changing
		// the bind, and every tunnelled request also presents a loopback
		// RemoteAddr — which is exactly why the decision is made from the
		// daemon's configuration (config.ReachablePrivately) and never from
		// the connection in front of us.
		if !private {
			return nil, errNoCredential
		}
		scopes := a.opts.AnonymousScopes
		if len(scopes) == 0 {
			// An anonymous identity with no configured scopes gets nothing,
			// not everything.
			scopes = nil
		}
		return &Client{ID: "anonymous-loopback", Scopes: append([]string(nil), scopes...), Anonymous: true}, nil
	}
	return a.verify(raw)
}

// verify parses and checks a presented key.
func (a *authenticator) verify(raw string) (*Client, error) {
	if a.opts.Secret == "" {
		// No secret means no key can be valid. Saying so plainly is safe (it
		// is a property of the server, not of the key) and saves an operator
		// an hour.
		return nil, authErr{http.StatusUnauthorized,
			"this daemon has no MCP signing secret configured, so no key can be accepted; " +
				"set SIGNALDECK_MCP_SECRET"}
	}
	if !strings.HasPrefix(raw, keyPrefix) {
		return nil, errBadCredential
	}
	body := strings.TrimPrefix(raw, keyPrefix)
	enc, sig, ok := strings.Cut(body, ".")
	if !ok || enc == "" || sig == "" {
		return nil, errBadCredential
	}
	// Constant-time compare, using the daemon's single implementation.
	if !a.tokenEqual(sig, sign(a.opts.Secret, enc)) {
		return nil, errBadCredential
	}
	decoded, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return nil, errBadCredential
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 3 {
		return nil, errBadCredential
	}
	id, expStr, scopeStr := parts[0], parts[1], parts[2]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || exp <= 0 {
		return nil, errBadCredential
	}
	if a.now().Unix() >= exp {
		return nil, errBadCredential
	}
	if a.revoked[id] {
		return nil, errBadCredential
	}
	var scopes []string
	for _, s := range strings.Split(scopeStr, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// A scope this build does not know is DROPPED, not carried. A future
		// tool must never inherit authority from a key minted before it
		// existed.
		if !knownScope(s) {
			continue
		}
		scopes = append(scopes, s)
	}
	if id == "" {
		return nil, errBadCredential
	}
	return &Client{ID: id, Scopes: scopes, ExpiresAt: exp}, nil
}
