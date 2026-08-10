// DRAFT — SWARM 5b / BLOCKED-5. NOT applied to the source tree.
//
// TARGET FILE: daemon/internal/notify/slack_smtp.go
// HOW TO APPLY: copy this file to that path verbatim (it is a NEW file — nothing
// is overwritten), then apply drafts/patches/notify-slack-smtp.patch for the
// edits to the existing files it depends on, then:
//     cd daemon && go build ./internal/... && go test ./internal/notify/
// Order does not matter; the package does not compile with only one half.
//
// VERIFIED: built, vetted, `go test -race` green in a scratch copy of the module
// under the system temp on 2026-08-06. daemon/ was never written to.

package notify

// SLACK + SMTP — the two remote transports this daemon did not have.
//
// WHY THIS FILE EXISTS. Measured 2026-08-06 against the LIVE daemon:
//
//	GET http://127.0.0.1:8322/api/notify-status
//	  windows  configured:true   "best-effort, delivery not tracked
//	                              (a daemon in a service session has no desktop)"
//	  discord  configured:false
//	  telegram configured:false
//	  webhook  configured:false
//
// Every remote transport is unconfigured, so the ONLY channel carrying alerts
// on this box is a PowerShell toast whose own doc comment (local.go) says
// delivery is not tracked. That is exactly the failure local.go was written to
// end — "a destination that only LOOKS real is worse than an absent one" — one
// level up: the fleet can go unhealthy with nobody told, which is how the
// SQLITE_BUSY storm and the stale-worker weeks stayed silent.
//
// Two gaps, not one:
//
//   - Slack. The generic webhook (SIGNALDECK_WEBHOOK_URL) posts
//     {title,body,kind,ts}; a Slack incoming webhook answers that with HTTP 400
//     invalid_payload, because Slack wants {"text":…}. Discord's {"content":…}
//     is already TransportDiscord. So Slack is a payload SHAPE and nothing
//     else: it reuses attempt/post/recordOK/recordErr unchanged, which is what
//     makes it auditable for free (Status() row + notify_failed dq event +
//     redaction, all inherited).
//   - SMTP. notify.go's package comment and /api/notify-status both said email
//     was absent "because it needs SMTP credentials or a provider account".
//     It still needs them — they are now named env vars instead of a missing
//     feature. Mail matters because a chat webhook dies with the account that
//     owns it, and because it is the one channel an operator still has at 3am.
//
// CONTRACT (identical to the existing transports, and load-bearing):
// context-cancellable, bounded, never blocking the caller, never fatal, and
// every outcome — success or failure — lands in Status() and, on failure, in a
// notify_failed dq event with secrets redacted.
//
// SMTP gets ONE attempt, not the 1+retry the HTTP transports get: a retry
// doubles the worst case for the transport most likely to be slow, and a
// duplicate alert email is worse than a late one. See sendBudget for how the
// whole fan-out is kept away from the watchdog's tick.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// Transport names (stable identifiers used in status + dq details).
const (
	TransportSlack = "slack"
	TransportSMTP  = "smtp"
)

// slackMaxChars clips the chat text. Slack's top-level `text` tolerates far
// more, but 3000 is the per-block ceiling once a workspace routes the message
// through Block Kit, and a clipped alert delivers where an oversized one 400s.
const slackMaxChars = 3000

// smtpTimeout bounds the WHOLE SMTP conversation — dial, TLS handshake, auth,
// DATA and QUIT — not one command. It is deliberately larger than the 5s HTTP
// attemptTimeout: a real MX handshake plus TLS plus AUTH routinely exceeds 5s,
// and the single-attempt rule means this is also the transport's worst case.
const smtpTimeout = 15 * time.Second

// sendBudget caps the TOTAL time Send may spend across all transports.
//
// Delivery is sequential, so every transport added extends the worst case:
// 3 HTTP transports x 2 attempts x 5s was 30s, and slack (2x5s) + smtp (15s)
// would take it to 55s. The alert sweep ticks every 5m (alerts.Runner.Interval)
// and the watchdog every 10m (health.Watchdog.Interval) — neither may be held
// for a minute by one wedged notification host, because a watchdog that is
// itself blocked on notifying is a watchdog that has stopped watching.
//
// 45s bounds the fan-out while still EXCEEDING the pre-existing 30s worst case,
// so no transport that delivered before this change can be starved by it.
//
// ponytail: sequential + one budget. Per-transport goroutines would remove the
// starvation risk entirely, but they would also make concurrent InsertDQ
// writers out of the failure path — and this database has a measured
// SQLITE_BUSY history. Revisit only if a transport is observed starving.
const sendBudget = 45 * time.Second

// smtpImplicitTLSPort is the submission port that speaks TLS from byte one
// (SMTPS). Every other port is dialed in the clear and upgraded with STARTTLS.
const smtpImplicitTLSPort = "465"

// defaultSMTPPort is the RFC 6409 submission port, used when
// SIGNALDECK_SMTP_PORT is unset.
const defaultSMTPPort = "587"

// smtpSubjectMax caps the Subject header. Alert titles are machine-built from
// worker names and upstream error strings; an unbounded one produces a header
// that some MTAs reject outright.
const smtpSubjectMax = 200

// loadExtEnv fills the Slack/SMTP configuration from the environment. Called
// by NewFromEnv (config.Load exports daemon/.env into the process env first).
// Only env var NAMES ever appear in source; the values go straight into the
// struct and are redacted on the way out — see Redact and extSecrets.
func (n *Notifier) loadExtEnv() {
	n.SlackWebhook = envTrim("SIGNALDECK_SLACK_WEBHOOK")
	n.SMTPHost = envTrim("SIGNALDECK_SMTP_HOST")
	n.SMTPPort = envTrim("SIGNALDECK_SMTP_PORT")
	n.SMTPUser = envTrim("SIGNALDECK_SMTP_USER")
	n.SMTPPass = envTrim("SIGNALDECK_SMTP_PASS")
	n.SMTPFrom = envTrim("SIGNALDECK_SMTP_FROM")
	n.SMTPTo = envTrim("SIGNALDECK_SMTP_TO")
}

func envTrim(name string) string { return strings.TrimSpace(os.Getenv(name)) }

func (n *Notifier) slackOn() bool { return n.SlackWebhook != "" }

// smtpOn requires a host, an envelope sender and at least one recipient. A
// half-configured mail transport must report unconfigured rather than fail on
// every alert: /api/notify-status is the operator's only view of this.
func (n *Notifier) smtpOn() bool {
	return n.SMTPHost != "" && n.SMTPFrom != "" && len(n.smtpTo()) > 0
}

// extOn reports whether either transport added by this file is configured.
func (n *Notifier) extOn() bool { return n.slackOn() || n.smtpOn() }

// extSecrets are the secrets this file adds to Redact's sweep. The Slack
// webhook URL is itself the credential; SMTPPass obviously is.
func (n *Notifier) extSecrets() []string { return []string{n.SlackWebhook, n.SMTPPass} }

// sendExt is the single hook Send calls; keeping the dispatch here keeps the
// change to notify.go to one line per concern.
func (n *Notifier) sendExt(ctx context.Context, m Message) {
	if n.slackOn() {
		payload, _ := json.Marshal(map[string]string{"text": clip(m.text(), slackMaxChars)})
		n.attempt(ctx, TransportSlack, n.SlackWebhook, payload)
	}
	if n.smtpOn() {
		n.sendSMTP(ctx, m)
	}
}

// sendSMTP performs the single attempt and records the outcome. It mirrors
// attempt() for the non-HTTP transport: same status map, same dq event, same
// redaction, so /api/notify-status tells the truth about mail too.
func (n *Notifier) sendSMTP(ctx context.Context, m Message) {
	if err := n.smtpDeliver(ctx, m); err != nil {
		n.recordErr(ctx, TransportSMTP, err)
		return
	}
	n.recordOK(TransportSMTP)
}

// smtpDeliver runs one bounded, cancellable SMTP conversation.
//
// smtp.SendMail is NOT used, for one reason: it takes no context. Every command
// in net/smtp is a blocking read on the socket, so a peer that accepts the
// connection and then says nothing would hold the caller until the OS gave up —
// which for the watchdog means the fleet stops being watched. Closing the
// socket is the only way to interrupt net/smtp, so cancellation is wired to
// Close via context.AfterFunc and an absolute deadline backs it up.
func (n *Notifier) smtpDeliver(ctx context.Context, m Message) error {
	rcpts := n.smtpTo()
	if len(rcpts) == 0 {
		return errors.New("no recipients configured")
	}
	ctx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()

	host, port := n.SMTPHost, n.smtpPort()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	defer raw.Close() //nolint:errcheck // best-effort teardown of a doomed conn
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}

	conn := raw
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if port == smtpImplicitTLSPort {
		tc := tls.Client(raw, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = tc
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // Quit already closed it on the happy path

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if n.SMTPUser != "" {
		// smtp.PlainAuth refuses this itself; refusing here too keeps the
		// reason in OUR words (a stdlib error string would name the host) and
		// makes the invariant testable without a live server.
		if _, encrypted := c.TLSConnectionState(); !encrypted {
			return errors.New("refusing to send SMTP credentials over an unencrypted connection")
		}
		if err := c.Auth(smtp.PlainAuth("", n.SMTPUser, n.SMTPPass, host)); err != nil {
			return err
		}
	}
	// Envelope addresses are CRLF-validated by net/smtp itself (validateLine),
	// so a poisoned SIGNALDECK_SMTP_FROM cannot smuggle a command here.
	if err := c.Mail(n.SMTPFrom); err != nil {
		return err
	}
	for _, to := range rcpts {
		if err := c.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(n.smtpBody(m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// smtpBody renders the RFC 5322 message. c.Data() returns a textproto
// DotWriter, so dot-stuffing and line endings in the BODY are the stdlib's
// problem; the HEADERS are ours.
//
// headerSafe is not cosmetic. Alert titles carry worker names, DQ details and
// upstream error strings — the same untrusted material that turned into a
// PowerShell injection in local.go. A bare CRLF in a Subject is header
// injection: it appends attacker-chosen headers (Bcc:) to the message.
func (n *Notifier) smtpBody(m Message) []byte {
	body := m.Body
	if body == "" {
		body = m.Title
	}
	var b strings.Builder
	b.WriteString("From: " + headerSafe(n.SMTPFrom) + "\r\n")
	b.WriteString("To: " + headerSafe(strings.Join(n.smtpTo(), ", ")) + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", clip(headerSafe(m.Title), smtpSubjectMax)) + "\r\n")
	b.WriteString("Date: " + n.now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return []byte(b.String())
}

// headerSafe folds CR and LF to spaces so no value can start a new header
// line. CRLF is listed first because strings.Replacer tries patterns in order
// at each position — a pair must collapse to ONE space, not two.
func headerSafe(s string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
}

func (n *Notifier) smtpPort() string {
	if n.SMTPPort == "" {
		return defaultSMTPPort
	}
	return n.SMTPPort
}

// smtpTo splits SIGNALDECK_SMTP_TO on commas, dropping blanks so a trailing
// comma cannot produce an empty RCPT TO that fails the whole delivery.
func (n *Notifier) smtpTo() []string {
	var out []string
	for _, s := range strings.Split(n.SMTPTo, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
