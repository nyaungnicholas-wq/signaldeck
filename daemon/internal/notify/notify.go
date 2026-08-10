// Package notify fans daemon notifications out BEYOND the local Mac (Stage 3
// alert delivery). Five optional, env-configured outbound transports:
//
//   - Discord   — SIGNALDECK_DISCORD_WEBHOOK (webhook URL; JSON {content})
//   - Telegram  — SIGNALDECK_TELEGRAM_BOT_TOKEN + SIGNALDECK_TELEGRAM_CHAT_ID
//     (Bot API sendMessage)
//   - Webhook   — SIGNALDECK_WEBHOOK_URL (generic POST JSON {title,body,kind,ts})
//   - Slack     — SIGNALDECK_SLACK_WEBHOOK (incoming webhook; JSON {text} —
//     the generic webhook's {title,body,kind,ts} is a 400 invalid_payload there)
//   - SMTP      — SIGNALDECK_SMTP_HOST + _FROM + _TO (plus optional _PORT,
//     _USER, _PASS); see slack_smtp.go
//
// Design contract (mirrors the fleet's graceful-degradation doctrine):
// every delivery attempt has a 5s timeout and exactly 1 retry; a transport
// that still fails records a dq event + a redacted per-transport error and
// NEVER blocks or fails the caller. Secrets (webhook URLs, bot token) are
// redacted from every log line, dq detail, and status string. With no
// transport configured, Send is a pure no-op. Email is no longer absent: the
// SMTP transport (slack_smtp.go) takes the credentials it always needed as
// named env vars, is allowed ONE attempt rather than two, and records delivery
// through the same status + dq path as every other transport. The whole
// fan-out is capped by sendBudget so no transport can hold the watchdog.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// attemptTimeout bounds ONE delivery attempt; retries+1 attempts total.
const attemptTimeout = 5 * time.Second

// retries is how many extra attempts follow a failed first attempt.
const retries = 1

// Character caps per transport (hard platform limits are 2000 / 4096; we clip
// under them so a huge batched body can never turn into a delivery failure).
const (
	discordMaxChars  = 1900
	telegramMaxChars = 3900
)

// Transport names (stable identifiers used in status + dq details).
const (
	TransportDiscord  = "discord"
	TransportTelegram = "telegram"
	TransportWebhook  = "webhook"
)

// defaultTelegramAPI is the Bot API host; overridable for tests.
const defaultTelegramAPI = "https://api.telegram.org"

// Message is one outbound notification. It doubles as the generic-webhook
// JSON payload shape: {title, body, kind, ts}.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Kind  string `json:"kind"` // e.g. "alerts", "watchdog"
	Ts    int64  `json:"ts"`   // unix seconds
}

// text renders the message for chat-style transports (Discord/Telegram).
func (m Message) text() string {
	if m.Body == "" {
		return m.Title
	}
	return m.Title + "\n" + m.Body
}

// DQ is the slice of the store the notifier needs to record delivery
// failures as data-quality events (satisfied by *store.Store).
type DQ interface {
	InsertDQ(ctx context.Context, ev md.DQEvent) error
}

// TransportStatus is the honest per-transport delivery record surfaced by
// /api/notify-status: configured or not, last successful delivery, and the
// last (secret-redacted) error.
type TransportStatus struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	LastOK      int64  `json:"lastOk,omitempty"`      // unix seconds of last 2xx delivery
	LastError   string `json:"lastError,omitempty"`   // redacted; empty when never failed
	LastErrorTs int64  `json:"lastErrorTs,omitempty"` // unix seconds of last failure
}

// Notifier is the daemon-wide outbound notifier. Zero-value fields mean
// "transport not configured". Safe for concurrent use (alert-runner and
// watchdog share one instance).
type Notifier struct {
	DiscordWebhook string // Discord webhook URL (secret — always redacted)
	TelegramToken  string // Telegram bot token (secret — always redacted)
	TelegramChatID string // Telegram chat id (numeric or @channel)
	WebhookURL     string // generic webhook URL (treated as secret too)

	// Slack + SMTP (slack_smtp.go). Loaded by NewFromEnv via loadExtEnv.
	SlackWebhook string // Slack incoming-webhook URL (secret — always redacted)
	SMTPHost     string // SIGNALDECK_SMTP_HOST
	SMTPPort     string // SIGNALDECK_SMTP_PORT ("" = 587; "465" = implicit TLS)
	SMTPUser     string // SIGNALDECK_SMTP_USER ("" = no AUTH)
	SMTPPass     string // SIGNALDECK_SMTP_PASS (secret — always redacted)
	SMTPFrom     string // envelope + From: address
	SMTPTo       string // comma-separated recipients

	TelegramAPIBase string        // test hook; "" = https://api.telegram.org
	HTTP            *http.Client  // test hook; nil = default client
	DQ              DQ            // failure sink; nil = log-only
	Now             func() time.Time

	mu     sync.Mutex
	status map[string]*TransportStatus
}

// NewFromEnv builds the notifier from the SIGNALDECK_* env vars (config.Load
// exports daemon/.env into the process env before run() constructs this).
func NewFromEnv(dq DQ) *Notifier {
	n := &Notifier{
		DiscordWebhook: strings.TrimSpace(os.Getenv("SIGNALDECK_DISCORD_WEBHOOK")),
		TelegramToken:  strings.TrimSpace(os.Getenv("SIGNALDECK_TELEGRAM_BOT_TOKEN")),
		TelegramChatID: strings.TrimSpace(os.Getenv("SIGNALDECK_TELEGRAM_CHAT_ID")),
		WebhookURL:     strings.TrimSpace(os.Getenv("SIGNALDECK_WEBHOOK_URL")),
		DQ:             dq,
	}
	n.loadExtEnv() // slack + smtp, same env-name-only rule (slack_smtp.go)
	return n
}

func (n *Notifier) discordOn() bool  { return n.DiscordWebhook != "" }
func (n *Notifier) telegramOn() bool { return n.TelegramToken != "" && n.TelegramChatID != "" }
func (n *Notifier) webhookOn() bool  { return n.WebhookURL != "" }

// Enabled reports whether ANY remote transport is configured.
func (n *Notifier) Enabled() bool {
	if n == nil {
		return false
	}
	return n.discordOn() || n.telegramOn() || n.webhookOn() || n.extOn()
}

// ConfiguredNames lists the configured transports in stable order.
func (n *Notifier) ConfiguredNames() []string {
	if n == nil {
		return nil
	}
	var out []string
	if n.discordOn() {
		out = append(out, TransportDiscord)
	}
	if n.telegramOn() {
		out = append(out, TransportTelegram)
	}
	if n.webhookOn() {
		out = append(out, TransportWebhook)
	}
	if n.slackOn() {
		out = append(out, TransportSlack)
	}
	if n.smtpOn() {
		out = append(out, TransportSMTP)
	}
	return out
}

// Send delivers msg to every configured transport, best-effort and
// sequentially, under a hard sendBudget for the WHOLE fan-out (callers are
// periodic workers, never request handlers). It never returns an error and
// never panics: failures become dq events + status entries.
func (n *Notifier) Send(ctx context.Context, m Message) {
	if n == nil || !n.Enabled() {
		return
	}
	// Cap the whole fan-out. Sequential delivery means every transport added
	// extends the worst case, and the watchdog must never be held by a wedged
	// notification host — see sendBudget (slack_smtp.go).
	ctx, cancel := context.WithTimeout(ctx, sendBudget)
	defer cancel()
	if m.Ts == 0 {
		m.Ts = n.now().Unix()
	}
	if n.discordOn() {
		payload, _ := json.Marshal(map[string]string{"content": clip(m.text(), discordMaxChars)})
		n.attempt(ctx, TransportDiscord, n.DiscordWebhook, payload)
	}
	if n.telegramOn() {
		payload, _ := json.Marshal(map[string]string{
			"chat_id": n.TelegramChatID,
			"text":    clip(m.text(), telegramMaxChars),
		})
		base := n.TelegramAPIBase
		if base == "" {
			base = defaultTelegramAPI
		}
		url := strings.TrimSuffix(base, "/") + "/bot" + n.TelegramToken + "/sendMessage"
		n.attempt(ctx, TransportTelegram, url, payload)
	}
	if n.webhookOn() {
		payload, _ := json.Marshal(m)
		n.attempt(ctx, TransportWebhook, n.WebhookURL, payload)
	}
	n.sendExt(ctx, m) // slack + smtp (slack_smtp.go)
}

// attempt POSTs payload to url with the 5s-timeout + 1-retry contract and
// records the outcome. All error paths are redacted before they leave.
func (n *Notifier) attempt(ctx context.Context, name, url string, payload []byte) {
	var lastErr error
	for try := 0; try <= retries; try++ {
		err := n.post(ctx, url, payload)
		if err == nil {
			n.recordOK(name)
			return
		}
		lastErr = err
		if ctx.Err() != nil {
			break // daemon shutting down — don't burn the retry
		}
	}
	n.recordErr(ctx, name, lastErr)
}

// post performs one bounded delivery attempt.
func (n *Notifier) post(ctx context.Context, url string, payload []byte) error {
	actx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Declarative UA on every outbound request (same identity the EDGAR
	// client uses: app name + deliverable contact).
	req.Header.Set("User-Agent", edgar.ResolveUA())
	res, err := n.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close() //nolint:errcheck
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("http %d", res.StatusCode)
	}
	return nil
}

// Redact strips every configured secret (webhook URLs, bot token, Slack
// webhook, SMTP password) from s — applied to ALL error strings before they
// reach logs, dq events, or status.
func (n *Notifier) Redact(s string) string {
	secrets := append([]string{n.DiscordWebhook, n.TelegramToken, n.WebhookURL}, n.extSecrets()...)
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}

func (n *Notifier) recordOK(name string) {
	now := n.now().Unix()
	n.mu.Lock()
	st := n.ensureStatusLocked(name)
	st.LastOK = now
	n.mu.Unlock()
	slog.Debug("notify: delivered", "transport", name)
}

// recordErr is the graceful-degradation sink: redacted log + status + dq
// event. Never fatal, never blocks.
func (n *Notifier) recordErr(ctx context.Context, name string, err error) {
	msg := "delivery failed"
	if err != nil {
		msg = n.Redact(err.Error())
	}
	now := n.now().Unix()
	n.mu.Lock()
	st := n.ensureStatusLocked(name)
	st.LastError = msg
	st.LastErrorTs = now
	n.mu.Unlock()
	slog.Warn("notify: delivery failed", "transport", name, "err", msg)
	if n.DQ != nil {
		if dqErr := n.DQ.InsertDQ(ctx, md.DQEvent{
			Ts:     now,
			Kind:   "notify_failed",
			Detail: fmt.Sprintf("transport=%s err=%s", name, msg),
		}); dqErr != nil {
			slog.Warn("notify: record dq failed", "transport", name, "err", dqErr)
		}
	}
}

// Status snapshots every remote transport in stable order (configured
// flag + last delivery/error), for /api/notify-status. Secrets never appear.
func (n *Notifier) Status() []TransportStatus {
	out := []TransportStatus{
		{Name: TransportDiscord},
		{Name: TransportTelegram},
		{Name: TransportWebhook},
		{Name: TransportSlack},
		{Name: TransportSMTP},
	}
	if n == nil {
		return out
	}
	configured := map[string]bool{
		TransportDiscord:  n.discordOn(),
		TransportTelegram: n.telegramOn(),
		TransportWebhook:  n.webhookOn(),
		TransportSlack:    n.slackOn(),
		TransportSMTP:     n.smtpOn(),
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := range out {
		out[i].Configured = configured[out[i].Name]
		if st, ok := n.status[out[i].Name]; ok {
			out[i].LastOK = st.LastOK
			out[i].LastError = st.LastError
			out[i].LastErrorTs = st.LastErrorTs
		}
	}
	return out
}

func (n *Notifier) ensureStatusLocked(name string) *TransportStatus {
	if n.status == nil {
		n.status = map[string]*TransportStatus{}
	}
	st, ok := n.status[name]
	if !ok {
		st = &TransportStatus{Name: name, Configured: true}
		n.status[name] = st
	}
	return st
}

func (n *Notifier) httpClient() *http.Client {
	if n.HTTP != nil {
		return n.HTTP
	}
	return &http.Client{Timeout: attemptTimeout}
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

// clip truncates s to at most max runes, marking the cut with an ellipsis.
func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
