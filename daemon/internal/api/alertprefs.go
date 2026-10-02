package api

// Member alert preferences (plan step 5). The daily read is OPT-IN on every
// channel: email needs a verified address and an explicit switch, Telegram an
// explicit link. Its content is the same forecasts every member sees
// (internal/memberdigest), so these routes take on/off switches and nothing
// that could personalise it (TestMemberRoutesTakeNoPersonalInputs).

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/memberdigest"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const telegramLinkTTL = 15 * time.Minute

type alertPrefsView struct {
	EmailDigest       bool `json:"emailDigest"`
	EmailVerified     bool `json:"emailVerified"`
	TelegramLinked    bool `json:"telegramLinked"`
	TelegramAvailable bool `json:"telegramAvailable"`
	MailAvailable     bool `json:"mailAvailable"`
}

func (d Deps) telegramAvailable() bool {
	return d.Notifier != nil && d.Notifier.TelegramToken != ""
}

func (d Deps) writeAlertPrefs(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	p, err := d.St.AlertPrefs(r.Context(), uid)
	if err != nil {
		httpInternal(w, err)
		return
	}
	_, verified, _, err := d.St.AccountEmail(r.Context(), uid)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, alertPrefsView{
		EmailDigest:       p.EmailDigest && verified,
		EmailVerified:     verified,
		TelegramLinked:    p.TelegramChatID != "",
		TelegramAvailable: d.telegramAvailable(),
		MailAvailable:     mailReady(d) && d.publicBase() != "",
	})
}

func (d Deps) alertPrefsGet(w http.ResponseWriter, r *http.Request) { d.writeAlertPrefs(w, r) }

func (d Deps) alertPrefsSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EmailDigest *bool `json:"emailDigest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.EmailDigest == nil {
		httpErr(w, 400, `body must be {"emailDigest": true|false}`)
		return
	}
	uid := userID(r)
	if *body.EmailDigest {
		if _, verified, _, err := d.St.AccountEmail(r.Context(), uid); err != nil {
			httpInternal(w, err)
			return
		} else if !verified {
			httpErr(w, http.StatusConflict, "confirm your email address before turning on the daily email")
			return
		}
	}
	if err := d.St.SetEmailDigest(r.Context(), uid, *body.EmailDigest); err != nil {
		httpInternal(w, err)
		return
	}
	d.writeAlertPrefs(w, r)
}

func (d Deps) alertPrefsTelegramLink(w http.ResponseWriter, r *http.Request) {
	if !d.telegramAvailable() {
		httpErr(w, http.StatusServiceUnavailable, "telegram delivery is not set up on this server")
		return
	}
	exp := time.Now().Add(telegramLinkTTL)
	var code string
	var err error
	for i := 0; i < 3; i++ { // a collision with another user's live code fails the unique index
		code = memberdigest.NewLinkCode()
		if err = d.St.SetTelegramLinkCode(r.Context(), userID(r), code, exp); err == nil {
			break
		}
	}
	if err != nil {
		httpInternal(w, err)
		return
	}
	out := map[string]any{"code": code, "expiresAt": exp.Unix()}
	if bot := strings.TrimPrefix(strings.TrimSpace(os.Getenv("SIGNALDECK_TELEGRAM_BOT_USERNAME")), "@"); bot != "" {
		out["botUsername"] = bot
	}
	writeJSON(w, out)
}

func (d Deps) alertPrefsTelegramUnlink(w http.ResponseWriter, r *http.Request) {
	if err := d.St.UnlinkTelegram(r.Context(), userID(r)); err != nil {
		httpInternal(w, err)
		return
	}
	d.writeAlertPrefs(w, r)
}

// alertsUnsubscribe is the one-click link in every digest email. Anonymous by
// design (the mail client has no session); the token is the authority, and
// all it can do is turn that one user's email digest off.
func (d Deps) alertsUnsubscribe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, err := d.St.RedeemUnsubscribe(r.Context(), strings.TrimSpace(r.URL.Query().Get("token")))
	msg := "You are unsubscribed from the SignalDeck daily email. Nothing more will be sent to this address."
	switch {
	case err == store.ErrTokenInvalid:
		w.WriteHeader(http.StatusBadRequest)
		msg = "This unsubscribe link is invalid or has expired. Turn the daily email off in your SignalDeck settings."
	case err != nil:
		w.WriteHeader(http.StatusServiceUnavailable)
		msg = "The server is busy. Open the link again in a minute; it still works."
	}
	_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><title>SignalDeck</title><p>" + html.EscapeString(msg) + "</p>\n"))
}

func (d Deps) registerAlertPrefs(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alert-prefs", d.alertPrefsGet)
	mux.HandleFunc("POST /api/alert-prefs", d.alertPrefsSet)
	mux.HandleFunc("POST /api/alert-prefs/telegram-link", d.alertPrefsTelegramLink)
	mux.HandleFunc("POST /api/alert-prefs/telegram-unlink", d.alertPrefsTelegramUnlink)
	mux.HandleFunc("GET /api/alerts/unsubscribe", d.alertsUnsubscribe)
}

// PublicBase is the origin emailed links point at ("" = unknown), for the
// member-digest worker outside this package.
func (d Deps) PublicBase() string { return d.publicBase() }
