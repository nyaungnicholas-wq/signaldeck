// TradingView webhook signal source. This is the ONE legitimate TradingView
// integration: TradingView's servers PUSH Pine Script alert events to us (we
// never pull their licensed market data, which has no public API and forbids
// redistribution). A Pine alert's webhook URL points at POST /api/tv-webhook
// and its message body is user-defined JSON.
//
// AUTH MODEL (why this endpoint is special): TradingView's servers cannot send
// a session cookie or the X-Signaldeck CSRF header, so this is the only
// state-changing endpoint exempted from the CSRF-header gate (see security.go).
// It is instead authenticated by a shared secret (SIGNALDECK_TV_WEBHOOK_SECRET)
// carried in the JSON body ("secret") or the X-Signaldeck-TV-Secret header,
// compared in constant time. No secret configured = the endpoint is disabled
// (fails closed) so it can never accept anonymous writes by accident.
//
// The ?secret= / ?token= QUERY form was removed 2026-07-25 and is now an
// outright reject (hostile review C8): a URL is the one part of a request that
// everything on the path writes down — the ngrok tunnel, any reverse proxy,
// browser history, referrer headers — so a long-lived shared credential placed
// there is published to every one of those logs. The parameter is rejected even
// when the body would authenticate, because silently ignoring it would let a
// misconfigured alert keep leaking the secret while appearing to work.
//
// The persisted payload is redacted before insert for the same reason: `raw` is
// stored for provenance and used to be stored verbatim, which put a working
// credential in all 162 live tv_signals rows of a world-readable database.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// tvSecretHeader is the out-of-body channel for the shared secret. A header is
// safe where a query parameter is not: it is not part of the URL, so it is not
// what proxies and tunnels log.
const tvSecretHeader = "X-Signaldeck-TV-Secret"

// tvSecretQueryParams are the query parameters that used to carry the secret.
// Their presence is now a hard reject rather than a fallback.
var tvSecretQueryParams = [...]string{"secret", "token"}

// tvPayload is the recommended alert JSON. Every field is optional except that
// the secret must match; unknown fields are ignored. price tolerates a JSON
// number or a quoted string (TradingView placeholder substitution can produce
// either depending on how the user templates it).
type tvPayload struct {
	Secret  string     `json:"secret"`
	Ticker  string     `json:"ticker"`
	Action  string     `json:"action"`
	Price   jsonNumber `json:"price"`
	Message string     `json:"message"`
}

// tvWebhook ingests one TradingView alert. Always returns quickly with a small
// JSON ack (TradingView retries on non-2xx, so we only 4xx on a genuine reject).
func (d Deps) tvWebhook(w http.ResponseWriter, r *http.Request) {
	secret := d.Cfg.TVWebhookSecret
	if secret == "" {
		httpErr(w, http.StatusServiceUnavailable, "tv webhook disabled: set SIGNALDECK_TV_WEBHOOK_SECRET")
		return
	}

	// A secret in the URL has already been written to every log on the path by
	// the time we see it, so reject before doing any work — and reject on
	// PRESENCE, not on value, so the misconfiguration surfaces instead of
	// silently succeeding through the body.
	q := r.URL.Query()
	for _, name := range tvSecretQueryParams {
		if q.Has(name) {
			httpErr(w, http.StatusBadRequest, "the ?"+name+"= query form of the webhook secret is not accepted: "+
				"a URL is recorded by tunnels, proxies and access logs. Send the secret in the JSON body "+
				`("secret") or the `+tvSecretHeader+" header, and rotate SIGNALDECK_TV_WEBHOOK_SECRET if it "+
				"has been used in a URL.")
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	var p tvPayload
	// A malformed body is not fatal — some users send a bare text alert. We
	// still require the secret, which then must come from the header.
	_ = json.Unmarshal(body, &p)

	// Secret may live in the body or the header.
	provided := p.Secret
	if provided == "" {
		provided = r.Header.Get(tvSecretHeader)
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
		httpErr(w, http.StatusForbidden, "invalid secret")
		return
	}

	// Everything below this line is persisted, so the credential comes out
	// first — of the raw payload AND of the message, which the fallback below
	// fills from that same raw payload.
	safe := redactTVSecret(string(body), secret)

	sig := store.TVSignal{
		Ticker:  redactTVSecret(strings.TrimSpace(p.Ticker), secret),
		Action:  redactTVSecret(strings.ToLower(strings.TrimSpace(p.Action)), secret),
		Message: redactTVSecret(strings.TrimSpace(p.Message), secret),
		Ts:      time.Now().Unix(),
	}
	if p.Price.set {
		v := p.Price.val
		sig.Price = &v
	}
	// Fall back to the raw text as the message when JSON carried none, so a
	// plain-text alert ("BUY NVDA") is still captured usefully.
	if sig.Message == "" && sig.Ticker == "" && sig.Action == "" {
		sig.Message = strings.TrimSpace(safe)
	}
	sig.SetRaw(safe)

	id, err := d.St.InsertTVSignal(r.Context(), sig)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

// redactTVSecret removes the shared secret from anything about to be persisted.
//
// It works on the literal value rather than on known field names deliberately:
// the secret is whatever the operator configured, and it can arrive in a shape
// this handler does not model (a "passphrase" key, a templated message, the
// bare text of a non-JSON alert). Matching the value catches every one of them,
// and nothing else in a payload can collide with a random shared secret.
func redactTVSecret(s, secret string) string {
	if s == "" || secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// tvSignalsList returns received TradingView signals, newest first.
// Query: ?unseen=1 filters unread; ?limit= caps rows (default/max 100).
func (d Deps) tvSignalsList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	unseen := r.URL.Query().Get("unseen") == "1"
	rows, err := d.St.TVSignals(r.Context(), unseen, limit)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, rows)
}

func (d Deps) registerTVWebhook(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/tv-webhook", d.tvWebhook)
	mux.HandleFunc("GET /api/tv-signals", d.tvSignalsList)
}

// jsonNumber accepts a JSON number OR a quoted numeric string and records
// whether a usable value was present.
type jsonNumber struct {
	val float64
	set bool
}

func (n *jsonNumber) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // tolerate a non-numeric price rather than reject the alert
	}
	n.val, n.set = v, true
	return nil
}
