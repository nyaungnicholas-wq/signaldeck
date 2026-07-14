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
// carried in the JSON body ("secret") or the ?secret=/?token= query, compared
// in constant time. No secret configured = the endpoint is disabled (fails
// closed) so it can never accept anonymous writes by accident.
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	var p tvPayload
	// A malformed body is not fatal — some users send a bare text alert. We
	// still require the secret, which then must come from the query string.
	_ = json.Unmarshal(body, &p)

	// Secret may live in the body or the query (?secret= / ?token=).
	provided := p.Secret
	if provided == "" {
		provided = r.URL.Query().Get("secret")
	}
	if provided == "" {
		provided = r.URL.Query().Get("token")
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
		httpErr(w, http.StatusForbidden, "invalid secret")
		return
	}

	sig := store.TVSignal{
		Ticker:  strings.TrimSpace(p.Ticker),
		Action:  strings.ToLower(strings.TrimSpace(p.Action)),
		Message: strings.TrimSpace(p.Message),
		Ts:      time.Now().Unix(),
	}
	if p.Price.set {
		v := p.Price.val
		sig.Price = &v
	}
	// Fall back to the raw text as the message when JSON carried none, so a
	// plain-text alert ("BUY NVDA") is still captured usefully.
	if sig.Message == "" && sig.Ticker == "" && sig.Action == "" {
		sig.Message = strings.TrimSpace(string(body))
	}
	sig.SetRaw(string(body))

	id, err := d.St.InsertTVSignal(r.Context(), sig)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
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
