package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
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

// secureCookie reports whether the request arrived over TLS (directly, or via
// a trusted proxy's X-Forwarded-Proto).
func (d Deps) secureCookie(r *http.Request) bool {
	if r.TLS != nil {
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
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
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
		httpErr(w, 500, err.Error())
		return
	} else if exists {
		httpErr(w, 409, "username taken")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	n, err := d.St.CountUsers(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	uid, err := d.St.CreateUser(r.Context(), body.Username, string(hash), n == 0)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	d.startSession(w, r, uid, body.Username, n == 0)
}

// authLogin verifies credentials and issues a session cookie.
func (d Deps) authLogin(w http.ResponseWriter, r *http.Request) {
	var body credsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	u, ok, err := d.St.GetUserByName(r.Context(), strings.TrimSpace(body.Username))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// Constant-shape failure path: run bcrypt either way.
	hash := u.PassHash
	if !ok {
		hash = string(dummyHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil || !ok {
		httpErr(w, 401, "invalid username or password")
		return
	}
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
		httpErr(w, 500, err.Error())
		return
	}
	d.setSessionCookie(w, r, token, int(sessionTTL.Seconds()))
	writeJSON(w, map[string]any{"id": uid, "username": username, "isAdmin": isAdmin})
}

// authLogout deletes the session and clears the cookie.
func (d Deps) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = d.St.DeleteSession(r.Context(), c.Value)
	}
	d.setSessionCookie(w, r, "", -1)
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
