// LAYER 1 tests — identity and transport. Each one asserts a refusal, because
// the only interesting property of an auth layer is what it will not do.
package mcp

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef"

func newAuth(t *testing.T, opts Options) *authenticator {
	t.Helper()
	if opts.Secret == "" {
		opts.Secret = testSecret
	}
	return newAuthenticator(opts, fakeTokenEqual, time.Now)
}

func TestNonLoopbackBindRefusesAnonymousAccess(t *testing.T) {
	a := newAuth(t, Options{ReachablePrivately: false})
	r := httptest.NewRequest("POST", "https://example.com/mcp", nil)
	// tlsOK=true: even with the transport requirement satisfied, no credential
	// means no access off loopback.
	if _, err := a.Authenticate(r, true); err == nil {
		t.Fatal("anonymous access was permitted on a reachable bind")
	}
}

func TestLoopbackAnonymousIsPermittedButScoped(t *testing.T) {
	a := newAuth(t, Options{ReachablePrivately: true, AnonymousScopes: []string{ScopeMethodology}})
	r := httptest.NewRequest("POST", "http://127.0.0.1:8322/mcp", nil)
	cl, err := a.Authenticate(r, false)
	if err != nil {
		t.Fatalf("loopback anonymous refused: %v", err)
	}
	if !cl.HasScope(ScopeMethodology) {
		t.Fatal("configured anonymous scope was not granted")
	}
	if cl.HasScope(ScopeVerdicts) {
		t.Fatal("anonymous client received a scope that was not configured — default-deny broken")
	}
}

func TestAnonymousWithNoConfiguredScopesGetsNothing(t *testing.T) {
	a := newAuth(t, Options{ReachablePrivately: true})
	r := httptest.NewRequest("POST", "http://127.0.0.1:8322/mcp", nil)
	cl, err := a.Authenticate(r, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range allScopes {
		if cl.HasScope(s) {
			t.Fatalf("empty anonymous scope list granted %s", s)
		}
	}
}

func TestCleartextNonLoopbackRefusesEvenAValidKey(t *testing.T) {
	a := newAuth(t, Options{ReachablePrivately: false})
	key, err := MintKey(testSecret, "partner-a", []string{ScopeMethodology}, time.Hour)
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	r := httptest.NewRequest("POST", "http://example.com/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+key)
	_, err = a.Authenticate(r, false)
	if err == nil {
		t.Fatal("a credential was accepted over cleartext on a reachable bind")
	}
	if !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("wrong refusal: %v", err)
	}
}

func TestValidKeyCarriesOnlyItsOwnScopes(t *testing.T) {
	a := newAuth(t, Options{ReachablePrivately: false})
	key, _ := MintKey(testSecret, "partner-a", []string{ScopeMethodology, ScopeRecord}, time.Hour)
	cl, err := a.verify(key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if cl.ID != "partner-a" {
		t.Fatalf("client id = %q", cl.ID)
	}
	if !cl.HasScope(ScopeMethodology) || !cl.HasScope(ScopeRecord) {
		t.Fatal("granted scopes missing")
	}
	if cl.HasScope(ScopeVerdicts) {
		t.Fatal("key granted a scope it was not minted with")
	}
}

func TestTamperedKeyIsRefused(t *testing.T) {
	a := newAuth(t, Options{})
	key, _ := MintKey(testSecret, "partner-a", []string{ScopeVerdicts}, time.Hour)
	// Flip one character of the signature.
	i := strings.LastIndex(key, ".")
	sig := []byte(key[i+1:])
	if sig[0] == 'a' {
		sig[0] = 'b'
	} else {
		sig[0] = 'a'
	}
	if _, err := a.verify(key[:i+1] + string(sig)); err == nil {
		t.Fatal("a key with a tampered signature verified")
	}
	// Re-encoding the payload with wider scopes must also fail: the signature
	// covers the payload, so self-promotion is not available.
	forged, _ := MintKey("a-different-secret-that-is-long-enough-x", "partner-a",
		[]string{ScopeVerdicts, ScopeRecord}, time.Hour)
	if _, err := a.verify(forged); err == nil {
		t.Fatal("a key signed with a different secret verified")
	}
}

func TestExpiredKeyIsRefused(t *testing.T) {
	past := func() time.Time { return time.Now().Add(48 * time.Hour) }
	a := newAuthenticator(Options{Secret: testSecret}, fakeTokenEqual, past)
	key, _ := MintKey(testSecret, "partner-a", []string{ScopeVerdicts}, time.Hour)
	if _, err := a.verify(key); err == nil {
		t.Fatal("an expired key verified")
	}
}

func TestRevokedClientIsRefused(t *testing.T) {
	a := newAuth(t, Options{RevokedClients: []string{"partner-a"}})
	key, _ := MintKey(testSecret, "partner-a", []string{ScopeVerdicts}, time.Hour)
	if _, err := a.verify(key); err == nil {
		t.Fatal("a revoked client's key verified")
	}
	// Revocation is by identity, so re-minting does not resurrect it.
	fresh, _ := MintKey(testSecret, "partner-a", []string{ScopeVerdicts}, 72*time.Hour)
	if _, err := a.verify(fresh); err == nil {
		t.Fatal("re-minting resurrected a revoked client")
	}
}

func TestNoSecretMeansNoKeyCanVerify(t *testing.T) {
	a := newAuthenticator(Options{Secret: ""}, fakeTokenEqual, time.Now)
	key, _ := MintKey(testSecret, "partner-a", []string{ScopeVerdicts}, time.Hour)
	if _, err := a.verify(key); err == nil {
		t.Fatal("a key verified against an unconfigured secret")
	}
}

func TestUnknownScopeInAKeyIsDroppedNotCarried(t *testing.T) {
	// Mint by hand so an unknown scope can be embedded — MintKey refuses one.
	payload := "partner-a|" + itoaTest(time.Now().Add(time.Hour).Unix()) + "|admin,methodology"
	enc := b64(payload)
	key := keyPrefix + enc + "." + sign(testSecret, enc)
	a := newAuth(t, Options{})
	cl, err := a.verify(key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(cl.Scopes) != 1 || cl.Scopes[0] != ScopeMethodology {
		t.Fatalf("unknown scope was carried: %v", cl.Scopes)
	}
}

func TestMintKeyRefusesBadInputs(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		id     string
		scopes []string
		ttl    time.Duration
	}{
		{"short secret", "tooshort", "a", []string{ScopeRecord}, time.Hour},
		{"empty id", testSecret, "", []string{ScopeRecord}, time.Hour},
		{"id with separator", testSecret, "a|b", []string{ScopeRecord}, time.Hour},
		{"no scopes", testSecret, "a", nil, time.Hour},
		{"unknown scope", testSecret, "a", []string{"admin"}, time.Hour},
		{"no expiry", testSecret, "a", []string{ScopeRecord}, 0},
	}
	for _, c := range cases {
		if _, err := MintKey(c.secret, c.id, c.scopes, c.ttl); err == nil {
			t.Errorf("%s: MintKey accepted it", c.name)
		}
	}
}

func TestRefusalMessagesDoNotDistinguishFailureModes(t *testing.T) {
	a := newAuth(t, Options{RevokedClients: []string{"revoked-client"}})
	expired := newAuthenticator(Options{Secret: testSecret}, fakeTokenEqual,
		func() time.Time { return time.Now().Add(48 * time.Hour) })

	revokedKey, _ := MintKey(testSecret, "revoked-client", []string{ScopeRecord}, time.Hour)
	goodKey, _ := MintKey(testSecret, "live-client", []string{ScopeRecord}, time.Hour)

	_, errRevoked := a.verify(revokedKey)
	_, errExpired := expired.verify(goodKey)
	_, errBadSig := a.verify(keyPrefix + b64("x|1|record") + ".00")

	if errRevoked.Error() != errExpired.Error() || errExpired.Error() != errBadSig.Error() {
		t.Fatalf("refusals are distinguishable: revoked=%q expired=%q badsig=%q",
			errRevoked, errExpired, errBadSig)
	}
}

func TestDisabledServerRefusesEverythingOverHTTP(t *testing.T) {
	s, err := New(Options{Enabled: false, ReachablePrivately: true},
		stubSource{}, alwaysAllow, fakeTokenEqual)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8322/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("disabled server answered with %d", w.Code)
	}
}

func TestGetIsRefusedRatherThanLeftHanging(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true,
		AnonymousScopes: allScopes}, stubSource{})
	r := httptest.NewRequest("GET", "http://127.0.0.1:8322/mcp", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 405 {
		t.Fatalf("GET answered with %d, want 405", w.Code)
	}
}

func TestBatchesAreRefused(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true,
		AnonymousScopes: allScopes}, stubSource{})
	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`
	r := httptest.NewRequest("POST", "http://127.0.0.1:8322/mcp", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("batch answered with %d, want 400", w.Code)
	}
}

// helpers kept local to the test file.
func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func itoaTest(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
