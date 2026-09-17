package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// tokenEqual compares a presented credential to the configured one in constant
// time. Both sides are hashed to fixed length first so neither content nor
// LENGTH differences leak through timing (subtle.ConstantTimeCompare alone
// returns early on length mismatch).
func tokenEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	hg := sha256.Sum256([]byte(got))
	hw := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(hg[:], hw[:]) == 1
}

// dummyHash keeps the unknown-username login path the same cost as a real
// bcrypt comparison (no username-oracle via timing).
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("signaldeck-dummy-password"), bcrypt.DefaultCost)

// sessionCookie is the browser session cookie name.
const sessionCookie = "signaldeck_session"

// sessionTTL is how long a login lasts.
const sessionTTL = 30 * 24 * time.Hour

// userKey is the request-context key carrying the authenticated user id.
type userKey struct{}

// userID returns the authenticated user id for this request (0 = anonymous).
func userID(r *http.Request) int64 {
	if v, ok := r.Context().Value(userKey{}).(int64); ok {
		return v
	}
	return 0
}

// resolveUser identifies the caller: a valid session cookie wins; otherwise a
// matching bearer APIToken (if configured) maps to the admin user (scripts).
// Returns 0 when neither is present/valid.
func (d Deps) resolveUser(r *http.Request) int64 {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if uid, ok, err := d.St.SessionUser(r.Context(), c.Value); err == nil && ok {
			return uid
		}
	}
	if d.Cfg.APIToken != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tokenEqual(got, d.Cfg.APIToken) {
			if id, err := d.St.AdminUserID(r.Context()); err == nil && id != 0 {
				return id
			}
		}
	}
	return 0
}

// newSessionToken returns 32 bytes of crypto/rand as hex. This plaintext is
// handed to the browser in Set-Cookie and to the store, which persists only its
// SHA-256 digest (store.CreateSession) — it is never written to disk.
func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// secureCookie reports whether the session cookie must carry Secure.
//
// The X-Forwarded-Proto branch cannot fire on the deployment that needs it.
// Browsers reach a published SignalDeck through the Next proxy, which forwards
// a fixed header allowlist that does not include X-Forwarded-Proto and must not
// include it: the value would then be whatever the client typed, and a client
// that sends "http" strips Secure off its own session cookie. So the daemon
// sees a plain loopback connection, r.TLS is nil, and on the one deployment
// served over HTTPS the cookie went out without Secure.
//
// PublicSurface answers it instead. It is the operator's STATED intent to
// publish — never derived from a bind address — and a published deployment is
// served over TLS by contract (fly.toml sets force_https). If someone publishes
// over plain HTTP anyway the browser drops the cookie and login visibly fails,
// which is the direction this should fail in.
func (d Deps) secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if d.Cfg.PublicSurface {
		return true
	}
	return d.Cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (d Deps) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   d.secureCookie(r),
	})
}

type credsBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (b *credsBody) validate() string {
	b.Username = strings.TrimSpace(b.Username)
	switch {
	case len(b.Username) < 3 || len(b.Username) > 32:
		return "username must be 3-32 characters"
	case len(b.Password) < 8 || len(b.Password) > 72: // 72 = bcrypt input limit
		return "password must be 8-72 characters"
	}
	for _, r := range b.Username {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.'
		if !ok {
			return "username may only contain letters, digits, . _ -"
		}
	}
	return ""
}

// authRegister creates an account. The FIRST registered user becomes admin.
func (d Deps) authRegister(w http.ResponseWriter, r *http.Request) {
	if !d.Cfg.OpenSignup {
		httpErr(w, 403, "registration is closed")
		return
	}
	var body credsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	if msg := body.validate(); msg != "" {
		httpErr(w, 400, msg)
		return
	}
	if _, exists, err := d.St.GetUserByName(r.Context(), body.Username); err != nil {
		httpInternal(w, err)
		return
	} else if exists {
		httpErr(w, 409, "username taken")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		httpInternal(w, err)
		return
	}
	n, err := d.St.CountUsers(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	uid, err := d.St.CreateUser(r.Context(), body.Username, string(hash), n == 0)
	if err != nil {
		httpInternal(w, err)
		return
	}
	d.startSession(w, r, uid, body.Username, n == 0)
}

// loginFailures throttles repeated password failures PER USERNAME.
//
// The request rate limiter alone did not cover this: it keys on client (user
// id / token / IP), so an attacker rotating source addresses got a fresh write
// budget per address, and a single address still had ~170k attempts/day at the
// write tier. bcrypt at cost 10 makes that slow rather than impossible, and
// nothing anywhere counted the failures.
//
// Keyed on the SUBMITTED username whether or not that user exists, so the
// lockout cannot be used to enumerate accounts — an unknown name locks out
// exactly like a real one.
var loginFailures = &failCounter{fails: map[string]*failState{}}

type failCounter struct {
	mu    sync.Mutex
	fails map[string]*failState
}

type failState struct {
	n     int
	until time.Time
	last  time.Time
}

// loginLockoutAfter is how many consecutive failures are tolerated before the
// backoff starts. Five is high enough that a person mistyping a password never
// meets it and low enough that guessing does immediately.
const loginLockoutAfter = 5

// loginLockoutBase is the FIRST wait once the threshold is crossed.
//
// It started at 1s, which measured out to roughly one guess per second — barely
// better than the write-tier request limiter it was meant to reinforce, and the
// message rounded down to the nonsense "try again in 0s". Five seconds is
// invisible to a person who mistyped their password five times and compounds
// immediately against anything guessing.
const loginLockoutBase = 5 * time.Second

// loginLockoutMax caps the backoff. Unbounded growth would let an attacker lock
// a real user out permanently by failing on their behalf — the cap keeps this a
// slowdown rather than a denial of service against the account owner.
const loginLockoutMax = 15 * time.Minute

// retryAfter reports how long the caller must wait, 0 when they may proceed.
func (f *failCounter) retryAfter(key string, now time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep(now)
	st, ok := f.fails[key]
	if !ok || now.After(st.until) {
		return 0
	}
	return st.until.Sub(now)
}

func (f *failCounter) fail(key string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep(now)
	st, ok := f.fails[key]
	if !ok {
		st = &failState{}
		f.fails[key] = st
	}
	st.n++
	st.last = now
	if st.n >= loginLockoutAfter {
		// Double each failure past the threshold: 5s, 10s, 20s … capped.
		backoff := loginLockoutBase << min(st.n-loginLockoutAfter, 20)
		if backoff > loginLockoutMax || backoff <= 0 {
			backoff = loginLockoutMax
		}
		st.until = now.Add(backoff)
	}
}

func (f *failCounter) succeed(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.fails, key)
}

// sweep drops entries idle past the maximum backoff — they can no longer be
// holding anyone out, so keeping them only grows the map. Called under mu.
func (f *failCounter) sweep(now time.Time) {
	for k, st := range f.fails {
		if now.Sub(st.last) > loginLockoutMax {
			delete(f.fails, k)
		}
	}
}

// authLogin verifies credentials and issues a session cookie.
func (d Deps) authLogin(w http.ResponseWriter, r *http.Request) {
	var body credsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	name := strings.TrimSpace(body.Username)

	// Check the lockout BEFORE bcrypt: the whole point is to stop spending a
	// ~100ms hash on an attacker, and answering fast here is not an oracle
	// because the lockout key exists for unknown usernames too.
	now := time.Now()
	if wait := loginFailures.retryAfter(strings.ToLower(name), now); wait > 0 {
		// Round UP, and never below a second. Rounding to nearest produced
		// "try again in 0s" for any sub-500ms remainder — an instruction to
		// wait no time at all, on a request that was just refused.
		secs := int((wait + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		httpErr(w, http.StatusTooManyRequests,
			"too many failed sign-in attempts — try again in "+
				(time.Duration(secs)*time.Second).String())
		return
	}

	u, ok, err := d.St.GetUserByName(r.Context(), name)
	if err != nil {
		httpInternal(w, err)
		return
	}
	// Constant-shape failure path: run bcrypt either way.
	hash := u.PassHash
	if !ok {
		hash = string(dummyHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil || !ok {
		loginFailures.fail(strings.ToLower(name), now)
		slog.Warn("login failed", "username", name)
		httpErr(w, 401, "invalid username or password")
		return
	}
	loginFailures.succeed(strings.ToLower(name))
	d.startSession(w, r, u.ID, u.Username, u.IsAdmin)
}

func (d Deps) startSession(w http.ResponseWriter, r *http.Request, uid int64, username string, isAdmin bool) {
	// Prune on every session creation, not just login: it also deletes the
	// pre-digest rows that stored a cookie value verbatim (see
	// store.PruneSessions). Those rows stopped authenticating the moment the
	// hashed lookup shipped, so this is cleanup of a dead credential, not the
	// invalidation itself.
	_ = d.St.PruneSessions(r.Context())
	token, err := newSessionToken()
	if err != nil {
		httpErr(w, 500, "token generation failed")
		return
	}
	// Only the digest of token reaches the database.
	if err := d.St.CreateSession(r.Context(), token, uid, time.Now().Add(sessionTTL).Unix()); err != nil {
		httpInternal(w, err)
		return
	}
	d.setSessionCookie(w, r, token, int(sessionTTL.Seconds()))
	writeJSON(w, map[string]any{"id": uid, "username": username, "isAdmin": isAdmin})
}

// authLogout deletes the session and clears the cookie.
func (d Deps) authLogout(w http.ResponseWriter, r *http.Request) {
	// A FAILED DELETE IS NOT A LOGOUT. Discarding this error cleared the
	// browser's cookie and answered ok:true while the session row kept
	// authenticating for the rest of sessionTTL -- so anyone holding that token
	// was still signed in, and the user had been told they were not. That is the
	// one lie this endpoint must never tell.
	var delErr error
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		delErr = d.St.DeleteSession(r.Context(), c.Value)
	}
	// The cookie is cleared either way: it costs nothing and helps if the row is
	// already gone. The RESPONSE is what must stay honest.
	d.setSessionCookie(w, r, "", -1)
	if delErr != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": "the session could not be revoked server-side and may still be valid; the cookie was cleared in this browser",
		})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// authMe returns the current identity, or 401 when anonymous.
func (d Deps) authMe(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	if uid == 0 {
		httpErr(w, 401, "not signed in")
		return
	}
	u, ok, err := d.St.GetUserByID(r.Context(), uid)
	if err != nil || !ok {
		httpErr(w, 401, "not signed in")
		return
	}
	writeJSON(w, map[string]any{"id": u.ID, "username": u.Username, "isAdmin": u.IsAdmin})
}

func (d Deps) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/register", d.authRegister)
	mux.HandleFunc("POST /api/auth/login", d.authLogin)
	mux.HandleFunc("POST /api/auth/logout", d.authLogout)
	mux.HandleFunc("GET /api/auth/me", d.authMe)
}

// withUser stashes the resolved user id in the request context.
func withUser(r *http.Request, uid int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userKey{}, uid))
}

// requireAdmin refuses any caller who is not the admin account, writing the
// error response itself; a false return means the handler must return at once.
//
// Until now IsAdmin was decorative. It is set at signup (the first account
// created becomes admin), stored on the users row, and handed to the browser
// in the login and /api/auth/me payloads -- and then checked by NOTHING. No
// handler in this daemon consulted it, and the web client only declares it as
// a type field. Every authenticated account therefore had identical authority,
// so "admin" described a badge rather than a permission.
//
// FAILS CLOSED on a database with no admin row. AdminUserID returns (0, nil)
// in that case, and treating "nobody is admin" as "everybody passes" is how a
// gate inverts under exactly the condition that should shut it -- a restored
// or half-migrated database.
//
// 403, not 404: the caller is authenticated, so hiding the route's existence
// buys nothing, and a plain refusal is easier to diagnose than a lie.
func (d Deps) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	uid := userID(r)
	if uid == 0 {
		httpErr(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	admin, err := d.St.AdminUserID(r.Context())
	if err != nil {
		httpInternal(w, err)
		return false
	}
	if admin == 0 || uid != admin {
		httpErr(w, http.StatusForbidden, "admin only")
		return false
	}
	return true
}
