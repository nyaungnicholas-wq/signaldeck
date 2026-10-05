package api

// Sign in with Google (2026-09-30). The sign-up and sign-in pages render
// Google's own button (Google Identity Services, popup mode). When a visitor
// picks an account, Google hands the PAGE a signed ID token; the page posts it
// here, and nothing in it is trusted until it verifies against Google's
// published keys. No password and no confirmation email: Google has already
// verified the address (email_verified), which is the point of the button.
//
// Verification is stdlib RS256, small enough to read in one sitting. The
// algorithm is pinned to RS256 before any key is touched, so "none" and
// HS256-keyed-with-the-public-key never reach a verifier; the key is chosen by
// kid from Google's JWKS; issuer, audience (our client ID) and expiry are
// checked only after the signature holds.
//
// Accounts. An address with no account becomes a new MEMBER, never an admin,
// under the same gates as email sign-up (open sign-up, per-network limit, an
// admin must already exist). A verified account signs straight in. An
// unconfirmed account is CLAIMED: Google's word proves the inbox, so the
// password a possible squatter chose is replaced and their sessions end. The
// admin account never signs in this way; its password stays its only key.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

var (
	googleCertsURL = "https://www.googleapis.com/oauth2/v3/certs" // test seam
	googleKeys     = &jwksCache{}
	googleLimiter  = newWindowLimiter(30, time.Hour) // Google sign-ins per client
)

var (
	errGoogleToken       = errors.New("google ID token rejected")
	errGoogleUnavailable = errors.New("google signing keys unavailable")
)

// ── token verification ─────────────────────────────────────────────────────

// jwksCache holds Google's signing keys. Google rotates them every few days
// with an overlap, so an unknown kid triggers a refetch, at most every 10s.
type jwksCache struct {
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
	fetched time.Time
}

func (c *jwksCache) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.keys[kid]
	if ok && time.Now().Before(c.expires) {
		return k, nil
	}
	if time.Since(c.fetched) >= 10*time.Second {
		c.fetched = time.Now()
		keys, err := fetchGoogleKeys(ctx)
		if err != nil {
			if ok {
				// A known Google key past our cache time still only verifies
				// what Google signed; exp is checked regardless.
				return k, nil
			}
			return nil, fmt.Errorf("%w: %v", errGoogleUnavailable, err)
		}
		c.keys, c.expires = keys, time.Now().Add(time.Hour)
		k, ok = keys[kid]
	}
	switch {
	case ok:
		return k, nil
	case c.keys == nil:
		return nil, errGoogleUnavailable // never fetched, and throttled
	default:
		return nil, errGoogleToken
	}
}

func fetchGoogleKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleCertsURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&doc); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if k.Kty != "RSA" || k.Kid == "" || errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < 2048 || pub.E < 3 {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("no usable RSA keys")
	}
	return keys, nil
}

// googleClaims is the part of a Google ID token this server reads. Aud is a
// plain string: Google issues single-audience ID tokens, and a token with an
// audience array fails to decode and is refused.
type googleClaims struct {
	Iss           string `json:"iss"`
	Aud           string `json:"aud"`
	Exp           int64  `json:"exp"`
	Iat           int64  `json:"iat"`
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"`
	// Hd is set only on Google Workspace accounts: the organisation's domain.
	Hd string `json:"hd"`
}

// verifyGoogleIDToken checks raw against Google's keys and returns its claims.
// Every rejection is errGoogleToken except Google's keys being unreachable
// (errGoogleUnavailable), which is this server's problem, not the visitor's.
func verifyGoogleIDToken(ctx context.Context, raw, clientID string, now time.Time) (googleClaims, error) {
	var claims googleClaims
	parts := strings.Split(raw, ".")
	if clientID == "" || len(parts) != 3 {
		return claims, errGoogleToken
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if decodeJWTPart(parts[0], &hdr) != nil || hdr.Alg != "RS256" || hdr.Kid == "" {
		return claims, errGoogleToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return claims, errGoogleToken
	}
	key, err := googleKeys.key(ctx, hdr.Kid)
	if err != nil {
		return claims, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return claims, errGoogleToken
	}
	// Google signed it. Now: issued by Google, for THIS site, and still live.
	if decodeJWTPart(parts[1], &claims) != nil {
		return claims, errGoogleToken
	}
	const skew = 60 // seconds of clock disagreement tolerated
	if (claims.Iss != "https://accounts.google.com" && claims.Iss != "accounts.google.com") ||
		claims.Aud != clientID ||
		claims.Exp == 0 || now.Unix() > claims.Exp+skew || claims.Iat > now.Unix()+skew ||
		claims.Sub == "" || claims.Email == "" ||
		(claims.EmailVerified != true && claims.EmailVerified != "true") {
		return claims, errGoogleToken
	}
	return claims, nil
}

func decodeJWTPart(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ── accounts ───────────────────────────────────────────────────────────────

// canonicalEmail folds every Gmail spelling of one inbox to a single address.
// Gmail ignores dots and anything after "+" in the local part, and
// googlemail.com is gmail.com, so without this one inbox could open unlimited
// accounts (a.b@, ab+1@, ab+2@ ...). Mail to the folded form reaches the same
// inbox. Other domains pass through; gmail reports which it was. email must
// already be normalised (lower case, one @).
func canonicalEmail(email string) (canon string, gmail bool) {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return email, false
	}
	local, domain := email[:at], email[at+1:]
	if domain != "gmail.com" && domain != "googlemail.com" {
		return email, false
	}
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	local = strings.ReplaceAll(local, ".", "")
	if local == "" {
		return email, false
	}
	return local + "@gmail.com", true
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return hex.EncodeToString(b)
}

// unusablePassword is the hash for an account that signs in with Google: the
// bcrypt of 32 random bytes nobody ever sees. The owner can still set a real
// password later through "forgot password", which mails the same inbox.
func unusablePassword() (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(randomHex(32)), bcrypt.DefaultCost)
	return string(h), err
}

// googleUsername derives a free username from an address's local part:
// first.last@gmail.com -> first.last, then first.last-2 and so on. Keeps only
// the characters credsBody.validate allows.
func (d Deps) googleUsername(ctx context.Context, email string) (string, error) {
	local := email
	if at := strings.IndexByte(local, '@'); at >= 0 {
		local = local[:at]
	}
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	var b strings.Builder
	for _, r := range local {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	base := b.String()
	if len(base) < 3 {
		base = "member" + base
	}
	if len(base) > 23 {
		base = base[:23] // room for a "-NN" or "-" + 8 hex suffix within 32
	}
	for i := 1; i <= 20; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		if _, taken, err := d.St.GetUserByName(ctx, name); err != nil {
			return "", err
		} else if !taken {
			return name, nil
		}
	}
	return base + "-" + randomHex(4), nil
}

type googleBody struct {
	Credential string `json:"credential"`
}

// authGoogle signs a visitor in with a Google ID token, creating a member
// account the first time an address is seen.
func (d Deps) authGoogle(w http.ResponseWriter, r *http.Request) {
	if d.Cfg.GoogleClientID == "" {
		httpErr(w, http.StatusNotFound, "Google sign-in is not set up on this server")
		return
	}
	var body googleBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil || body.Credential == "" {
		httpErr(w, 400, "bad json")
		return
	}
	key := d.clientKey(r, 0)
	if !googleLimiter.allow(acctKey(key)) {
		httpErr(w, http.StatusTooManyRequests, "too many sign-in attempts from this network — try again later")
		return
	}
	vctx, vcancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer vcancel()
	claims, err := verifyGoogleIDToken(vctx, body.Credential, d.Cfg.GoogleClientID, time.Now())
	if errors.Is(err, errGoogleUnavailable) {
		slog.Warn("google sign-in: signing keys unavailable", "err", err)
		httpErr(w, http.StatusServiceUnavailable, "Google sign-in is unavailable right now — try again in a minute")
		return
	} else if err != nil {
		httpErr(w, http.StatusUnauthorized, "Google sign-in could not be verified — try again")
		return
	}
	typed, ok := normaliseEmail(claims.Email)
	if !ok {
		httpErr(w, 400, "your Google account's email address can't be used here")
		return
	}
	email, gmail := canonicalEmail(typed)
	// Google is AUTHORITATIVE for an address only when it is Gmail, or when hd
	// marks a Workspace account. For any other address (a Google account made
	// with a yahoo or company email) email_verified says the inbox was proven
	// once, at Google sign-up, not that this person still controls it; linking
	// it would hand a former holder of the address its account here. Google's
	// own ID-token guidance; review 2026-09-30.
	if !gmail && claims.Hd == "" {
		httpErr(w, http.StatusForbidden, "this Google account doesn't use a Gmail address — sign in with a Gmail account")
		return
	}

	// The key fetch may have spent most of the request's budget; the account
	// writes get their own, so a slow verify cannot fail a write half-way.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	defer cancel()
	// Unconfirmed sign-ups past their link are dead (authRegister purges the
	// same way). Purging first means a stale squat is deleted, not claimed, and
	// the owner gets a fresh account under a name of their own.
	if err := d.St.PurgeStaleUnverified(ctx, time.Now().Add(-verifyTTL)); err != nil {
		slog.Warn("google sign-in: stale unverified purge failed", "err", err)
	}
	uid, username, verified, found, err := d.St.AccountByEmail(ctx, email)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if found {
		u, ok, err := d.St.GetUserByID(ctx, uid)
		if err != nil || !ok {
			httpInternal(w, errors.New("google sign-in: account vanished"))
			return
		}
		if u.IsAdmin {
			httpErr(w, http.StatusForbidden, "this account signs in with its password")
			return
		}
		if !verified {
			hash, err := unusablePassword()
			if err != nil {
				httpInternal(w, err)
				return
			}
			err = d.prioritized(func() error { return d.St.ClaimUnverified(ctx, uid, email, hash) })
			if errors.Is(err, store.ErrClaimRaced) {
				httpErr(w, http.StatusConflict, "your account changed while you were signing in — try again")
				return
			} else if err != nil {
				slog.Warn("google sign-in: claiming an unconfirmed account failed", "uid", uid, "err", err)
				httpErr(w, http.StatusServiceUnavailable, "the server is busy — try again in a minute")
				return
			}
		}
		d.googleSession(ctx, w, r, uid, username)
		return
	}

	// No account yet: a sign-up, behind the same gates as email sign-up,
	// including the bot-check pause (signupOpen) the sign-up page reports.
	if !d.Cfg.OpenSignup {
		httpErr(w, http.StatusForbidden, "registration is closed")
		return
	}
	if !d.signupOpen() {
		httpErr(w, http.StatusServiceUnavailable, signupPaused)
		return
	}
	if !signupLimiter.allow(acctKey(key)) {
		w.Header().Set("Retry-After", "3600")
		httpErr(w, http.StatusTooManyRequests, "too many sign-ups from this network — try again later")
		return
	}
	if n, err := d.St.CountUsers(ctx); err != nil {
		httpInternal(w, err)
		return
	} else if n == 0 {
		// Same rule as authRegister: the operator account is never created
		// from outside, so the first sign-up cannot become the admin.
		httpErr(w, http.StatusForbidden, "no admin account exists; create it on the server itself first")
		return
	}
	username, err = d.googleUsername(ctx, typed)
	if err != nil {
		httpInternal(w, err)
		return
	}
	hash, err := unusablePassword()
	if err != nil {
		httpInternal(w, err)
		return
	}
	err = d.prioritized(func() (err error) {
		uid, err = d.St.CreateVerifiedUser(ctx, username, email, hash)
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			// A concurrent sign-up took the name or the address first.
			httpErr(w, http.StatusConflict, "that account was just created — try signing in again")
		} else {
			slog.Warn("google sign-up: create failed", "err", err)
			httpErr(w, http.StatusServiceUnavailable, "the server is busy — try again in a minute")
		}
		return
	}
	slog.Info("google sign-up", "uid", uid) // the username is derived from the Gmail address
	d.googleSession(ctx, w, r, uid, username)
}

// prioritized runs one account write ahead of the worker fleet. The hold is
// released by defer, so even a panicking write cannot leave the gate stuck
// (a stuck hold would stall every main-writer statement by up to 3s).
func (d Deps) prioritized(write func() error) error {
	defer d.St.Priority()()
	return write()
}

// googleSession signs the account in on a fresh time budget: the account
// exists (or was just claimed) by now, so a slow verify must not cost the
// session that completes it.
func (d Deps) googleSession(ctx context.Context, w http.ResponseWriter, r *http.Request, uid int64, username string) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	d.startSession(w, r.WithContext(sctx), uid, username, false)
}
