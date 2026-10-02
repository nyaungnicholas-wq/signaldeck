package api

// Member alert preferences (plan step 5). The daily read is OPT-IN on every
// channel: email needs a verified address and an explicit switch, Telegram an
// explicit link. Its content is the same forecasts every member sees
// (internal/memberdigest), so these routes take on/off switches and nothing
// that could personalise it (TestMemberRoutesTakeNoPersonalInputs).

import (
	"encoding/json"
	"errors"
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
		MailAvailable:     d.digestMailAvailable(),
	})
}

// digestMailAvailable gates the daily email on SIGNALDECK_PUBLIC_URL, not on
// publicBase(): its quick-tunnel fallback changes on every restart, and a
// digest's links (unsubscribe included) must outlive the tunnel that sent it.
func (d Deps) digestMailAvailable() bool {
	return mailReady(d) && d.Cfg.PublicURL != ""
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

const maxUnsubTokenLen = 128

func unsubPage(w http.ResponseWriter, status int, inner string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\">" +
		"<title>SignalDeck</title>" + inner + "\n"))
}

// alertsUnsubscribeConfirm is the link in every digest email. A GET changes
// nothing: mail scanners (Outlook Safe Links, gateways) fetch every link in a
// message, so a GET that unsubscribed would opt members out unasked. It shows
// one button that POSTs the same token back.
func (d Deps) alertsUnsubscribeConfirm(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSpace(r.URL.Query().Get("token"))
	if tok == "" || len(tok) > maxUnsubTokenLen {
		unsubPage(w, http.StatusBadRequest, "<p>This unsubscribe link is invalid or has expired. "+
			"Turn the daily email off in your SignalDeck settings.</p>")
		return
	}
	unsubPage(w, http.StatusOK, "<p>Stop the SignalDeck daily email to this address?</p>"+
		`<form method="post" action="/api/alerts/unsubscribe">`+
		`<input type="hidden" name="token" value="`+html.EscapeString(tok)+`">`+
		`<button type="submit">Unsubscribe</button></form>`)
}

// alertsUnsubscribe redeems the token: the confirm page's form POST (token in
// the body) or a mail client's RFC 8058 one-click POST (token in the
// List-Unsubscribe URL, body List-Unsubscribe=One-Click). Anonymous and exempt
// from the CSRF header by exact path (security.go): neither sender can set a
// custom header, and the token is the credential. All it can do is turn that
// one user's email digest off.
func (d Deps) alertsUnsubscribe(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSpace(r.PostFormValue("token"))
	if tok == "" {
		tok = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	_, err := d.St.RedeemUnsubscribe(r.Context(), tok)
	msg := "You are unsubscribed from the SignalDeck daily email. Nothing more will be sent to this address."
	status := http.StatusOK
	switch {
	case errors.Is(err, store.ErrTokenInvalid):
		status = http.StatusBadRequest
		msg = "This unsubscribe link is invalid or has expired. Turn the daily email off in your SignalDeck settings."
	case err != nil:
		status = http.StatusServiceUnavailable
		msg = "The server is busy. Open the link again in a minute; it still works."
	}
	unsubPage(w, status, "<p>"+html.EscapeString(msg)+"</p>")
}

func (d Deps) registerAlertPrefs(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alert-prefs", d.alertPrefsGet)
	mux.HandleFunc("POST /api/alert-prefs", d.alertPrefsSet)
	mux.HandleFunc("POST /api/alert-prefs/telegram-link", d.alertPrefsTelegramLink)
	mux.HandleFunc("POST /api/alert-prefs/telegram-unlink", d.alertPrefsTelegramUnlink)
	mux.HandleFunc("GET /api/alerts/unsubscribe", d.alertsUnsubscribeConfirm)
	mux.HandleFunc("POST /api/alerts/unsubscribe", d.alertsUnsubscribe)
}
