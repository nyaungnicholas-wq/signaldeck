// DRAFT — SWARM 5b / BLOCKED-5. NOT applied to the source tree.
//
// TARGET FILE: daemon/internal/notify/slack_smtp_test.go
// HOW TO APPLY: copy this file to that path verbatim (it is a NEW file — nothing
// is overwritten), then apply drafts/patches/notify-slack-smtp.patch for the
// edits to the existing files it depends on, then:
//     cd daemon && go build ./internal/... && go test ./internal/notify/
// Order does not matter; the package does not compile with only one half.
//
// VERIFIED: built, vetted, `go test -race` green in a scratch copy of the module
// under the system temp on 2026-08-06. daemon/ was never written to.

package notify

// Tests for the Slack + SMTP transports (slack_smtp.go).
//
// What these pin, in the order that matters:
//   1. an outcome is RECORDED — Status() + a notify_failed dq event — because
//      an alert transport whose failures are invisible is the bug this whole
//      package exists to fix;
//   2. secrets never leave, even inside an error string (the Slack webhook URL
//      IS the credential, and net/http puts the URL in every timeout error);
//   3. Send never blocks the caller, on any outcome — the watchdog calls it;
//   4. untrusted alert text cannot inject a mail header.
//
// fakeDQ, msg() and the rest live in notify_test.go; same package, reused.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// statusOf returns the Status() row for one transport.
func statusOf(t *testing.T, n *Notifier, name string) TransportStatus {
	t.Helper()
	for _, s := range n.Status() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no status row for transport %q", name)
	return TransportStatus{}
}

// sendBounded runs Send and fails the test if it does not return. Send is
// called from the watchdog and the alert sweep; blocking is the one failure
// mode that takes the fleet's supervision down with it.
func sendBounded(t *testing.T, n *Notifier, ctx context.Context, m Message) time.Duration {
	t.Helper()
	start := time.Now()
	done := make(chan struct{})
	go func() { defer close(done); n.Send(ctx, m) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Send blocked — it must never hold its caller")
	}
	return time.Since(start)
}

func TestSlackTransportOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		hang      bool
		clientTO  time.Duration
		wantOK    bool
		wantErrIn string // "" with wantOK=false means "any error, unspecified"
		wantHits  int    // requests the server should see (attempt = 1 + 1 retry)
	}{
		{name: "success", status: http.StatusOK, clientTO: 5 * time.Second, wantOK: true, wantHits: 1},
		{name: "non-2xx", status: http.StatusInternalServerError, clientTO: 5 * time.Second, wantErrIn: "http 500", wantHits: 2},
		{name: "timeout", hang: true, clientTO: 100 * time.Millisecond, wantHits: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(b))
				mu.Unlock()
				if tc.hang {
					// The client gives up first; unblock on its disconnect so
					// srv.Close() is never the thing that waits.
					select {
					case <-r.Context().Done():
					case <-time.After(10 * time.Second):
					}
					return
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			dq := &fakeDQ{}
			n := &Notifier{SlackWebhook: srv.URL, DQ: dq, HTTP: &http.Client{Timeout: tc.clientTO}}
			if !n.Enabled() {
				t.Fatal("a set SlackWebhook must make the notifier Enabled")
			}
			sendBounded(t, n, context.Background(), msg())

			st := statusOf(t, n, TransportSlack)
			if !st.Configured {
				t.Error("slack row must report configured")
			}
			mu.Lock()
			got := append([]string{}, bodies...)
			mu.Unlock()
			if len(got) != tc.wantHits {
				t.Errorf("server saw %d requests, want %d", len(got), tc.wantHits)
			}

			if tc.wantOK {
				if st.LastOK == 0 {
					t.Error("success must record LastOK")
				}
				if st.LastError != "" {
					t.Errorf("success recorded an error: %q", st.LastError)
				}
				if evs := dq.all(); len(evs) != 0 {
					t.Errorf("success must record no dq event, got %v", evs)
				}
				var payload map[string]string
				if err := json.Unmarshal([]byte(got[0]), &payload); err != nil {
					t.Fatalf("payload is not JSON: %v (%q)", err, got[0])
				}
				// Slack's incoming webhook answers {title,body,kind,ts} with
				// 400 invalid_payload — the shape IS the transport.
				if len(payload) != 1 || payload["text"] == "" {
					t.Fatalf(`slack payload must be exactly {"text":…}, got %v`, payload)
				}
				if !strings.Contains(payload["text"], "2 new alert(s)") ||
					!strings.Contains(payload["text"], "NVDA breakout") {
					t.Errorf("payload text lost the message: %q", payload["text"])
				}
				return
			}

			if st.LastOK != 0 {
				t.Error("a failed delivery must not record LastOK")
			}
			if st.LastError == "" {
				t.Fatal("a failed delivery must record LastError")
			}
			if tc.wantErrIn != "" && !strings.Contains(st.LastError, tc.wantErrIn) {
				t.Errorf("LastError = %q, want it to contain %q", st.LastError, tc.wantErrIn)
			}
			evs := dq.all()
			if len(evs) != 1 {
				t.Fatalf("want exactly 1 dq event, got %d: %v", len(evs), evs)
			}
			if evs[0].Kind != "notify_failed" || !strings.Contains(evs[0].Detail, "transport="+TransportSlack) {
				t.Errorf("dq event = %+v, want kind notify_failed for transport=slack", evs[0])
			}
			// The webhook URL is the credential. net/http embeds the request
			// URL in transport errors, so this is a live leak path, not a
			// theoretical one.
			if strings.Contains(st.LastError, srv.URL) || strings.Contains(evs[0].Detail, srv.URL) {
				t.Errorf("webhook URL leaked: status=%q dq=%q", st.LastError, evs[0].Detail)
			}
		})
	}
}

// fakeSMTP answers exactly one connection and returns the DATA it received.
//
// mode:
//
//	ok      — full 220/250/354/250/221 conversation, no STARTTLS advertised
//	reject  — 550 on MAIL FROM
//	silent  — accepts the TCP connection and then says NOTHING, which is the
//	          wedge a context-less smtp.SendMail would sit on until the OS
//	          gave up
func fakeSMTP(t *testing.T, mode string) (host, port string, received func() string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var mu sync.Mutex
	var body bytes.Buffer

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		say := func(s string) { _, _ = io.WriteString(conn, s) }
		if mode == "silent" {
			<-time.After(20 * time.Second)
			return
		}
		br := bufio.NewReader(conn)
		say("220 mock ESMTP\r\n")
		inData := false
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					say("250 queued\r\n")
					continue
				}
				mu.Lock()
				body.WriteString(line)
				mu.Unlock()
				continue
			}
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			switch strings.ToUpper(f[0]) {
			case "EHLO", "HELO":
				say("250-mock\r\n250 8BITMIME\r\n") // deliberately no STARTTLS
			case "MAIL":
				if mode == "reject" {
					say("550 sender rejected\r\n")
					continue
				}
				say("250 ok\r\n")
			case "RCPT":
				say("250 ok\r\n")
			case "DATA":
				say("354 go ahead\r\n")
				inData = true
			case "QUIT":
				say("221 bye\r\n")
				return
			default:
				say("250 ok\r\n")
			}
		}
	}()

	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	return h, p, func() string { mu.Lock(); defer mu.Unlock(); return body.String() }
}

// injectingMsg carries the hostile shape real alert titles have: machine-built
// text containing an upstream string. A bare CRLF in a Subject is mail-header
// injection.
func injectingMsg() Message {
	return Message{
		Title: "SignalDeck: 2 stale worker(s)\r\nBcc: attacker@example.test",
		Body:  "signalbt-weekly\nweekly-report",
		Kind:  "watchdog",
	}
}

func TestSMTPTransportOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		ctxTO     time.Duration
		wantOK    bool
		wantErrIn string
		maxWall   time.Duration
	}{
		{name: "success", mode: "ok", ctxTO: 20 * time.Second, wantOK: true, maxWall: 10 * time.Second},
		{name: "server rejects sender", mode: "reject", ctxTO: 20 * time.Second, wantErrIn: "550", maxWall: 10 * time.Second},
		{name: "peer never speaks", mode: "silent", ctxTO: 300 * time.Millisecond, maxWall: 5 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port, received := fakeSMTP(t, tc.mode)
			dq := &fakeDQ{}
			n := &Notifier{
				SMTPHost: host,
				SMTPPort: port,
				SMTPFrom: "signaldeck@example.test",
				// The empty middle field pins smtpTo's blank-dropping: a
				// trailing comma must not become an empty RCPT TO that fails
				// the whole delivery.
				SMTPTo: "ops@example.test, , second@example.test",
				DQ:     dq,
			}
			if !n.smtpOn() || !n.Enabled() {
				t.Fatal("host+from+to must make the smtp transport configured")
			}
			ctx, cancel := context.WithTimeout(context.Background(), tc.ctxTO)
			defer cancel()
			wall := sendBounded(t, n, ctx, injectingMsg())
			if wall > tc.maxWall {
				t.Errorf("Send took %s, want under %s — cancellation is not reaching net/smtp", wall, tc.maxWall)
			}

			st := statusOf(t, n, TransportSMTP)
			if !st.Configured {
				t.Error("smtp row must report configured")
			}

			if !tc.wantOK {
				if st.LastOK != 0 {
					t.Error("a failed delivery must not record LastOK")
				}
				if st.LastError == "" {
					t.Fatal("a failed delivery must record LastError")
				}
				if tc.wantErrIn != "" && !strings.Contains(st.LastError, tc.wantErrIn) {
					t.Errorf("LastError = %q, want it to contain %q", st.LastError, tc.wantErrIn)
				}
				evs := dq.all()
				if len(evs) != 1 {
					t.Fatalf("want exactly 1 dq event, got %d: %v", len(evs), evs)
				}
				if evs[0].Kind != "notify_failed" || !strings.Contains(evs[0].Detail, "transport="+TransportSMTP) {
					t.Errorf("dq event = %+v, want kind notify_failed for transport=smtp", evs[0])
				}
				return
			}

			if st.LastOK == 0 {
				t.Error("success must record LastOK")
			}
			if st.LastError != "" {
				t.Errorf("success recorded an error: %q", st.LastError)
			}
			if evs := dq.all(); len(evs) != 0 {
				t.Errorf("success must record no dq event, got %v", evs)
			}

			data := received()
			if !strings.Contains(data, "To: ops@example.test, second@example.test\r\n") {
				t.Errorf("recipient header wrong (blank entry not dropped?):\n%s", data)
			}
			if !strings.Contains(data, "Subject: SignalDeck: 2 stale worker(s) Bcc: attacker@example.test\r\n") {
				t.Errorf("subject not folded onto one line:\n%s", data)
			}
			for _, line := range strings.Split(data, "\r\n") {
				if strings.HasPrefix(line, "Bcc:") {
					t.Fatalf("HEADER INJECTION: a Bcc header was synthesised from the alert title:\n%s", data)
				}
			}
			if !strings.Contains(data, "signalbt-weekly") || !strings.Contains(data, "weekly-report") {
				t.Errorf("body lost the alert text:\n%s", data)
			}
		})
	}
}

// TestSMTPRefusesPlaintextCredentials pins the one thing that must never
// degrade gracefully: with SIGNALDECK_SMTP_USER set and a server that does not
// offer STARTTLS, the password is not sent. A skipped alert is recoverable; a
// leaked SMTP password is not.
func TestSMTPRefusesPlaintextCredentials(t *testing.T) {
	host, port, received := fakeSMTP(t, "ok")
	dq := &fakeDQ{}
	n := &Notifier{
		SMTPHost: host, SMTPPort: port,
		SMTPUser: "ops@example.test", SMTPPass: "hunter2-not-a-real-secret",
		SMTPFrom: "signaldeck@example.test", SMTPTo: "ops@example.test",
		DQ: dq,
	}
	sendBounded(t, n, context.Background(), msg())

	st := statusOf(t, n, TransportSMTP)
	if st.LastOK != 0 {
		t.Fatal("must not report a delivery it refused to make")
	}
	if !strings.Contains(st.LastError, "unencrypted") {
		t.Errorf("LastError = %q, want the plaintext-credential refusal", st.LastError)
	}
	if strings.Contains(st.LastError, n.SMTPPass) {
		t.Error("SMTP password leaked into the status error")
	}
	if data := received(); strings.Contains(data, n.SMTPPass) {
		t.Error("SMTP password reached the wire")
	}
	for _, ev := range dq.all() {
		if strings.Contains(ev.Detail, n.SMTPPass) {
			t.Error("SMTP password leaked into a dq event")
		}
	}
}

// TestExtTransportsUnconfiguredAreNoOp keeps the honest-no-op contract: with
// nothing set, Send touches nothing and Status reports both new rows as
// unconfigured rather than silently absent.
func TestExtTransportsUnconfiguredAreNoOp(t *testing.T) {
	dq := &fakeDQ{}
	n := &Notifier{DQ: dq}
	if n.Enabled() {
		t.Fatal("an empty notifier must not report Enabled")
	}
	sendBounded(t, n, context.Background(), msg())
	if evs := dq.all(); len(evs) != 0 {
		t.Errorf("a no-op Send must record nothing, got %v", evs)
	}
	for _, name := range []string{TransportSlack, TransportSMTP} {
		st := statusOf(t, n, name)
		if st.Configured || st.LastOK != 0 || st.LastError != "" {
			t.Errorf("%s row should be an empty unconfigured row, got %+v", name, st)
		}
	}
}
