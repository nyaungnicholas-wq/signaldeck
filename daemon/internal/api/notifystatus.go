// Stage 3 — alert delivery beyond the Mac: GET /api/notify-status surfaces
// the outbound-delivery configuration HONESTLY — which transports are
// configured (never the secrets themselves), the last successful delivery,
// and the last (secret-redacted) error per transport. macOS is listed too:
// always attempted via osascript, but with an explicit note that delivery is
// best-effort and untracked (no receipt exists). Email is intentionally
// absent — it needs SMTP credentials or a provider account (future), and we
// don't pretend otherwise.
package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
)

// notifyTransportRow is one transport's honest status + how to enable it.
type notifyTransportRow struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	Env         string `json:"env"`            // env var(s) that enable this transport
	Note        string `json:"note,omitempty"` // honest caveat (e.g. macOS untracked)
	LastOK      int64  `json:"lastOk,omitempty"`
	LastError   string `json:"lastError,omitempty"` // redacted upstream
	LastErrorTs int64  `json:"lastErrorTs,omitempty"`
}

// notifyEnvHints maps transport name → the env var(s) that enable it.
var notifyEnvHints = map[string]string{
	notify.TransportDiscord:  "SIGNALDECK_DISCORD_WEBHOOK",
	notify.TransportTelegram: "SIGNALDECK_TELEGRAM_BOT_TOKEN + SIGNALDECK_TELEGRAM_CHAT_ID",
	notify.TransportWebhook:  "SIGNALDECK_WEBHOOK_URL",
}

// notifyStatus reports delivery-channel configuration. Works with a nil
// Notifier (tests / minimal wiring): everything remote shows unconfigured.
func (d Deps) notifyStatus(w http.ResponseWriter, r *http.Request) {
	rows := []notifyTransportRow{{
		Name:       "macos",
		Configured: true,
		Env:        "(built-in)",
		Note:       "osascript display notification on the daemon's Mac — best-effort, delivery not tracked",
	}}
	for _, ts := range d.Notifier.Status() { // nil-receiver safe: returns unconfigured rows
		rows = append(rows, notifyTransportRow{
			Name:        ts.Name,
			Configured:  ts.Configured,
			Env:         notifyEnvHints[ts.Name],
			LastOK:      ts.LastOK,
			LastError:   ts.LastError,
			LastErrorTs: ts.LastErrorTs,
		})
	}
	writeJSON(w, map[string]any{
		"transports": rows,
		"email":      "not supported — needs SMTP credentials or a provider account (future)",
		"note":       "remote transports are env-configured in daemon/.env (see .env.example); delivery failures degrade to dq events and never block alerts",
	})
}

// notifyTest sends a real message through every configured transport, so the
// operator can confirm a freshly-set token actually delivers to their phone.
//
// Why this exists: setting SIGNALDECK_TELEGRAM_BOT_TOKEN is the whole job, but
// until an alert happens to fire there is no way to tell a correct token from a
// typo — and the transport is deliberately best-effort, so a bad token fails
// silently into a dq event rather than into the operator's face. One button
// closes that loop.
//
// It is a POST behind the normal auth + CSRF path (never a GET, which a link
// or a prefetch could fire), and it reports per-transport status AFTER the
// attempt so a failure is visible immediately rather than on the next alert.
func (d Deps) notifyTest(w http.ResponseWriter, r *http.Request) {
	if !d.Notifier.Enabled() {
		writeJSON(w, map[string]any{
			"sent": false,
			"reason": "no remote transport is configured — set SIGNALDECK_DISCORD_WEBHOOK, " +
				"SIGNALDECK_TELEGRAM_BOT_TOKEN + SIGNALDECK_TELEGRAM_CHAT_ID, or " +
				"SIGNALDECK_WEBHOOK_URL in daemon/.env and restart the daemon",
			"transports": d.Notifier.ConfiguredNames(),
		})
		return
	}

	d.Notifier.Send(r.Context(), notify.Message{
		Kind:  "test",
		Title: "SignalDeck test notification",
		Body: "If you can read this, remote alert delivery works. Sent from " +
			"/api/notify/test — no market event occurred.",
	})

	// Status is read AFTER the send so lastOk / lastError describe THIS attempt.
	rows := []notifyTransportRow{}
	for _, ts := range d.Notifier.Status() {
		if !ts.Configured {
			continue
		}
		rows = append(rows, notifyTransportRow{
			Name: ts.Name, Configured: true, Env: notifyEnvHints[ts.Name],
			LastOK: ts.LastOK, LastError: ts.LastError, LastErrorTs: ts.LastErrorTs,
		})
	}
	writeJSON(w, map[string]any{
		"sent":       true,
		"transports": rows,
		"howToRead": "A transport whose lastError timestamp is newer than its lastOk did NOT " +
			"deliver this message. Errors are redacted before they leave the daemon, so a bad " +
			"token reads as an auth failure without echoing the token back.",
	})
}

func (d Deps) registerNotify(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/notify-status", d.notifyStatus)
	mux.HandleFunc("POST /api/notify/test", d.notifyTest)
}
