package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// These pin Sign in with Google: a token is trusted only when Google's key
// signed it for THIS client, recently, for a verified address; accounts are
// created as members, claim unconfirmed squats, and never reach the admin.
// They also pin the Gmail-only rule on email sign-up.

const testGoogleClient = "test-client.apps.googleusercontent.com"

var testGoogleKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

// withGoogleKeys serves k as Google's JWKS under kid "k1" and resets the key
// cache and the Google limiter for the test.
func withGoogleKeys(t *testing.T, k *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(k.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	}))
	t.Cleanup(jwks.Close)
	oldURL, oldKeys, oldLim := googleCertsURL, googleKeys, googleLimiter
	googleCertsURL, googleKeys, googleLimiter = jwks.URL, &jwksCache{}, newWindowLimiter(30, time.Hour)
	t.Cleanup(func() { googleCertsURL, googleKeys, googleLimiter = oldURL, oldKeys, oldLim })
	return jwks
}

// mintGoogle signs claims with k the way Google signs an ID token.
func mintGoogle(t *testing.T, k *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(header) + "." + enc(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func goodHeader() map[string]any { return map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"} }

func goodClaims(email string) map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": testGoogleClient, "sub": "1234567890",
		"email": email, "email_verified": true, "iat": now, "exp": now + 3600,
	}
}

func TestVerifyGoogleIDToken(t *testing.T) {
	k := testGoogleKey()
	withGoogleKeys(t, k)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	with := func(edit func(m map[string]any)) map[string]any {
		c := goodClaims("pat@gmail.com")
		edit(c)
		return c
	}
	good := mintGoogle(t, k, goodHeader(), goodClaims("pat@gmail.com"))
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(
		`{"iss":"https://accounts.google.com","aud":"`+testGoogleClient+`","sub":"1","email":"admin@gmail.com","email_verified":true,"exp":9999999999}`)) + "." + parts[2]

	cases := []struct {
		name string
		tok  string
		ok   bool
	}{
		{"valid", good, true},
		{"email_verified as the string true", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { m["email_verified"] = "true" })), true},
		{"issuer without scheme", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { m["iss"] = "accounts.google.com" })), true},
		{"another site's client ID", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { m["aud"] = "evil.apps.googleusercontent.com" })), false},
		{"audience array", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { m["aud"] = []string{testGoogleClient} })), false},
		{"expired", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { m["exp"] = time.Now().Unix() - 120 })), false},
		{"issued in the future", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { m["iat"] = time.Now().Unix() + 3600 })), false},
		{"not Google", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { m["iss"] = "https://evil.example" })), false},
		{"unverified email", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { m["email_verified"] = false })), false},
		{"no email", mintGoogle(t, k, goodHeader(), with(func(m map[string]any) { delete(m, "email") })), false},
		{"signed by someone else's key", mintGoogle(t, other, goodHeader(), goodClaims("pat@gmail.com")), false},
		{"payload swapped after signing", tampered, false},
		{"alg none", base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"k1"}`)) + "." + parts[1] + ".", false},
		{"alg HS256", base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","kid":"k1"}`)) + "." + parts[1] + "." + parts[2], false},
		{"header names another algorithm", mintGoogle(t, k, map[string]any{"alg": "RS512", "kid": "k1"}, goodClaims("pat@gmail.com")), false},
		{"unknown kid", mintGoogle(t, k, map[string]any{"alg": "RS256", "kid": "nope"}, goodClaims("pat@gmail.com")), false},
		{"two segments", parts[0] + "." + parts[1], false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := verifyGoogleIDToken(context.Background(), tc.tok, testGoogleClient, time.Now())
			if tc.ok {
				if err != nil || c.Email != "pat@gmail.com" {
					t.Fatalf("want accepted, got %v (%+v)", err, c)
				}
				return
			}
			if !errors.Is(err, errGoogleToken) {
				t.Fatalf("want errGoogleToken, got %v", err)
			}
		})
	}
}

// Google's keys being unreachable is this server's fault, not a bad token: it
// must surface as unavailable (503), never as "could not be verified".
func TestGoogleKeysUnreachableIsUnavailable(t *testing.T) {
	k := testGoogleKey()
	withGoogleKeys(t, k)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer down.Close()
	googleCertsURL = down.URL
	_, err := verifyGoogleIDToken(context.Background(), mintGoogle(t, k, goodHeader(), goodClaims("pat@gmail.com")), testGoogleClient, time.Now())
	if !errors.Is(err, errGoogleUnavailable) {
		t.Fatalf("want errGoogleUnavailable, got %v", err)
	}
}

func TestCanonicalEmail(t *testing.T) {
	for in, want := range map[string]string{
		"first.last@gmail.com":            "firstlast@gmail.com",
		"first.last+promo@googlemail.com": "firstlast@gmail.com",
		"firstlast@gmail.com":             "firstlast@gmail.com",
		"a.b+c+d@gmail.com":               "ab@gmail.com",
	} {
		if got, gmail := canonicalEmail(in); got != want || !gmail {
			t.Errorf("canonicalEmail(%q) = %q, %v; want %q, true", in, got, gmail, want)
		}
	}
	for _, in := range []string{"first.last+x@yahoo.com", "x@gmail.com.evil.example", "+tag@gmail.com", "pat@notgmail.com"} {
		if got, gmail := canonicalEmail(in); gmail || got != in {
			t.Errorf("canonicalEmail(%q) = %q, %v; want unchanged and not Gmail", in, got, gmail)
		}
	}
}

func newGoogleServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store, *mailbox) {
	t.Helper()
	withGoogleKeys(t, testGoogleKey())
	return newPublishedServerWith(t, func(c *config.Config) {
		c.GoogleClientID = testGoogleClient
		if mutate != nil {
			mutate(c)
		}
	}, true)
}

func googleSignIn(t *testing.T, c *http.Client, base, email string) (int, string) {
	t.Helper()
	return acctPost(t, c, base+"/api/auth/google", map[string]string{
		"credential": mintGoogle(t, testGoogleKey(), goodHeader(), goodClaims(email)),
	})
}

func TestGoogleSignInCreatesAVerifiedMember(t *testing.T) {
	srv, st, _ := newGoogleServer(t, nil)
	ctx := context.Background()
	before, _ := st.CountUsers(ctx)

	c := newClient(t)
	code, body := googleSignIn(t, c, srv.URL, "New.Person+promo@googlemail.com")
	if code != 200 || !strings.Contains(body, `"username":"new.person"`) || !strings.Contains(body, `"isAdmin":false`) {
		t.Fatalf("google sign-up: %d %s", code, body)
	}
	resp, err := c.Get(srv.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	if me := drain(t, resp); resp.StatusCode != 200 || !strings.Contains(me, "new.person") {
		t.Fatalf("the new account is not signed in: %d %s", resp.StatusCode, me)
	}
	// One inbox, one account: the folded address, verified, never admin.
	uid, _, verified, found, err := st.AccountByEmail(ctx, "newperson@gmail.com")
	if err != nil || !found || !verified {
		t.Fatalf("account by folded address: found=%v verified=%v err=%v", found, verified, err)
	}
	if u, _, _ := st.GetUserByID(ctx, uid); u.IsAdmin {
		t.Fatal("a Google sign-up became an admin")
	}
	// Signing in again under another spelling of the same inbox reuses it.
	if code, body := googleSignIn(t, newClient(t), srv.URL, "newperson@gmail.com"); code != 200 || !strings.Contains(body, "new.person") {
		t.Fatalf("second sign-in: %d %s", code, body)
	}
	if after, _ := st.CountUsers(ctx); after != before+1 {
		t.Fatalf("users %d -> %d, want exactly one new account", before, after)
	}
}

// Someone registers a victim's address with a password of their own and never
// confirms it. When the real owner signs in with Google, that password must
// stop working and the pending confirmation link must die.
func TestGoogleSignInClaimsAnUnconfirmedSquat(t *testing.T) {
	srv, _, mb := newGoogleServer(t, nil)
	if code, body := signup(t, newClient(t), srv.URL, "squatter", "victim@gmail.com"); code != 200 {
		t.Fatalf("squat signup: %d %s", code, body)
	}
	waitMail(t, mb, 1)
	link := mb.linkToken(t, "/verify")

	if code, body := googleSignIn(t, newClient(t), srv.URL, "victim@gmail.com"); code != 200 {
		t.Fatalf("owner's Google sign-in: %d %s", code, body)
	}
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/login",
		map[string]string{"username": "squatter", "password": "correcthorse1"}); code == 200 {
		t.Fatalf("the squatter's password still signs in: %s", body)
	}
	if code, _ := acctPost(t, newClient(t), srv.URL+"/api/auth/verify", map[string]string{"token": link}); code == 200 {
		t.Fatal("the pending confirmation link still works after the claim")
	}
}

func TestGoogleSignInNeverReachesTheAdmin(t *testing.T) {
	srv, st, _ := newGoogleServer(t, nil)
	if _, err := st.DB().Exec(`UPDATE users SET email='owner@gmail.com', email_verified=1 WHERE username='owner'`); err != nil {
		t.Fatal(err)
	}
	if code, body := googleSignIn(t, newClient(t), srv.URL, "owner@gmail.com"); code != 403 {
		t.Fatalf("Google sign-in to the admin account: %d %s, want 403", code, body)
	}
}

func TestGoogleSignUpObeysClosedRegistration(t *testing.T) {
	srv, st, _ := newGoogleServer(t, func(c *config.Config) { c.OpenSignup = false })
	if code, body := googleSignIn(t, newClient(t), srv.URL, "stranger@gmail.com"); code != 403 {
		t.Fatalf("Google sign-up with registration closed: %d %s, want 403", code, body)
	}
	// An account that already exists still signs in.
	if _, err := st.CreateVerifiedUser(context.Background(), "existing", "existing@gmail.com", "x"); err != nil {
		t.Fatal(err)
	}
	if code, body := googleSignIn(t, newClient(t), srv.URL, "existing@gmail.com"); code != 200 {
		t.Fatalf("existing member, registration closed: %d %s", code, body)
	}
}

func TestGoogleRejectsAForgedToken(t *testing.T) {
	srv, st, _ := newGoogleServer(t, nil)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := st.CountUsers(context.Background())
	code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/google", map[string]string{
		"credential": mintGoogle(t, other, goodHeader(), goodClaims("forger@gmail.com")),
	})
	if code != 401 {
		t.Fatalf("forged token: %d %s, want 401", code, body)
	}
	if after, _ := st.CountUsers(context.Background()); after != before {
		t.Fatal("a forged token created an account")
	}
}

func TestGoogleSignInOffWithoutAClientID(t *testing.T) {
	srv, _, _ := newPublishedServer(t)
	if code, _ := acctPost(t, newClient(t), srv.URL+"/api/auth/google", map[string]string{"credential": "x.y.z"}); code != 404 {
		t.Fatalf("no client ID configured: %d, want 404", code)
	}
}

func TestEmailSignupTakesGmailOnly(t *testing.T) {
	srv, _, _ := newPublishedServer(t)
	code, body := signup(t, newClient(t), srv.URL, "yahoo", "someone@yahoo.com")
	if code != 400 || !strings.Contains(body, "Gmail") {
		t.Fatalf("non-Gmail sign-up: %d %s, want 400 naming Gmail", code, body)
	}
}

// Dots and +tags are one inbox: a second spelling is the taken path (same
// answer, no new account, a notice to the owner), not a second account.
func TestGmailSpellingsShareOneAccount(t *testing.T) {
	srv, st, mb := newPublishedServer(t)
	ctx := context.Background()
	if code, body := signup(t, newClient(t), srv.URL, "firstlast", "First.Last+promo@googlemail.com"); code != 200 {
		t.Fatalf("signup: %d %s", code, body)
	}
	waitMail(t, mb, 1)
	if to := mb.last().to; to != "firstlast@gmail.com" {
		t.Fatalf("confirmation went to %q, want the folded firstlast@gmail.com", to)
	}
	before, _ := st.CountUsers(ctx)
	if code, body := signup(t, newClient(t), srv.URL, "firstlast2", "firstlast@gmail.com"); code != 200 || !strings.Contains(body, verifySent) {
		t.Fatalf("second spelling: %d %s, want the same answer as success", code, body)
	}
	waitMail(t, mb, 2)
	if after, _ := st.CountUsers(ctx); after != before {
		t.Fatal("a second spelling of one Gmail inbox created a second account")
	}
	if m := mb.last(); m.to != "firstlast@gmail.com" || !strings.Contains(m.body, "already exists") {
		t.Fatalf("taken-address notice: to=%q body=%q", m.to, m.body)
	}
}

// Google vouches for an address only when it is Gmail or a Workspace account
// (hd set). A Google account made with any other email proves that inbox only
// once, at Google sign-up, so it must not sign into the account here that
// carries the same address.
func TestGoogleRefusesAddressesGoogleDoesNotVouchFor(t *testing.T) {
	srv, st, _ := newGoogleServer(t, nil)
	ctx := context.Background()
	if _, err := st.CreateVerifiedUser(ctx, "pat", "pat@company.com", "x"); err != nil {
		t.Fatal(err)
	}
	before, _ := st.CountUsers(ctx)
	tok := func(hd string) string {
		c := goodClaims("pat@company.com")
		if hd != "" {
			c["hd"] = hd
		}
		return mintGoogle(t, testGoogleKey(), goodHeader(), c)
	}
	c := newClient(t)
	if code, body := acctPost(t, c, srv.URL+"/api/auth/google", map[string]string{"credential": tok("")}); code != 403 {
		t.Fatalf("non-Gmail, non-Workspace Google account: %d %s, want 403", code, body)
	}
	if resp, err := c.Get(srv.URL + "/api/auth/me"); err != nil || resp.StatusCode == 200 {
		t.Fatal("a Google account Google does not vouch for got a session")
	}
	if after, _ := st.CountUsers(ctx); after != before {
		t.Fatal("a Google account Google does not vouch for created an account")
	}
	// A Workspace account (hd set) is Google's to vouch for.
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/google", map[string]string{"credential": tok("company.com")}); code != 200 || !strings.Contains(body, `"username":"pat"`) {
		t.Fatalf("Workspace account: %d %s, want signed in as pat", code, body)
	}
}

// A squat past its 24h link is dead: Google sign-in deletes it and gives the
// owner a fresh account under a name derived from their own address, instead
// of claiming the squatter's.
func TestGoogleReplacesAStaleSquatInsteadOfClaimingIt(t *testing.T) {
	srv, st, _ := newGoogleServer(t, nil)
	ctx := context.Background()
	squat, err := st.CreateUserWithEmail(ctx, "impersonator", "victim@gmail.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE users SET created_ts=? WHERE id=?`, time.Now().Add(-48*time.Hour).Unix(), squat); err != nil {
		t.Fatal(err)
	}
	code, body := googleSignIn(t, newClient(t), srv.URL, "victim@gmail.com")
	if code != 200 || !strings.Contains(body, `"username":"victim"`) {
		t.Fatalf("owner after a stale squat: %d %s, want a fresh account named victim", code, body)
	}
	if _, found, _ := st.GetUserByName(ctx, "impersonator"); found {
		t.Fatal("the stale squat survived")
	}
}
