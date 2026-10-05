package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// registerAccountData serves a signed-in account's own data, to take away or
// to erase (2026-10-05 audit, AUD-05: neither existed, so a member had no way
// to see what was held about them or to leave).
func (d Deps) registerAccountData(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/account/export", d.accountExport)
	mux.HandleFunc("POST /api/account/delete", d.accountDelete)
}

func (d Deps) accountExport(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	if uid == 0 {
		httpErr(w, http.StatusUnauthorized, "sign in first")
		return
	}
	data, err := d.St.AccountExport(r.Context(), uid)
	if err != nil {
		httpInternal(w, err)
		return
	}
	data["exportedAt"] = time.Now().UTC().Format(time.RFC3339)
	w.Header().Set("Content-Disposition", `attachment; filename="signaldeck-my-data.json"`)
	writeJSON(w, data)
}

// accountDelete erases the signed-in account after the password confirms it.
// Wrong passwords count on a lockout ladder of their own, reachable only with
// this account's session (see lockKey below).
func (d Deps) accountDelete(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	if uid == 0 {
		httpErr(w, http.StatusUnauthorized, "sign in first")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
		httpErr(w, http.StatusBadRequest, "confirm with your password")
		return
	}
	ctx := r.Context()
	u, ok, err := d.St.GetUserByID(ctx, uid)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if !ok {
		httpErr(w, http.StatusUnauthorized, "sign in first")
		return
	}
	if u.IsAdmin {
		httpErr(w, http.StatusForbidden, store.ErrAdminAccount.Error())
		return
	}
	// Its own ladder, reachable only with this account's session: on the name's
	// ladder an internet attacker who kept the name locked also blocked the
	// member's deletion (the device cookie is scoped to /api/auth; 2026-10-05
	// review S2). '|' never appears in a username.
	lockKey := u.Username + "|delete"
	if wait := loginFailures.retryAfter(lockKey, time.Now()); wait > 0 {
		httpErr(w, http.StatusTooManyRequests, "too many wrong passwords — try again later")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PassHash), []byte(body.Password)) != nil {
		loginFailures.fail(lockKey, time.Now())
		httpErr(w, http.StatusForbidden, "that password is not right")
		return
	}
	// A person is waiting: go ahead of the worker fleet.
	err = d.prioritized(func() error { return d.St.DeleteAccount(ctx, uid, u.Username) })
	if errors.Is(err, store.ErrAdminAccount) {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	} else if errors.Is(err, sql.ErrNoRows) { // already gone, or the id now belongs to someone else
		httpErr(w, http.StatusUnauthorized, "sign in first")
		return
	} else if err != nil {
		httpInternal(w, err)
		return
	}
	loginFailures.succeed(lockKey)
	d.setSessionCookie(w, r, "", -1)
	http.SetCookie(w, &http.Cookie{Name: deviceCookie, Value: "", Path: "/api/auth", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: d.secureCookie(r)})
	writeJSON(w, map[string]string{"status": "deleted"})
}
