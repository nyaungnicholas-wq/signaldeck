package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// fakeDQ captures dq events in memory.
type fakeDQ struct {
	mu     sync.Mutex
	events []md.DQEvent
}

func (f *fakeDQ) InsertDQ(_ context.Context, ev md.DQEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeDQ) all() []md.DQEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]md.DQEvent{}, f.events...)
}

// capture records every request body + path a test server sees.
type capture struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
	uas    []string
}

func (c *capture) handler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 1<<16)
		n, _ := r.Body.Read(b)
		c.mu.Lock()
		c.paths = append(c.paths, r.URL.Path)
		c.bodies = append(c.bodies, string(b[:n]))
		c.uas = append(c.uas, r.Header.Get("User-Agent"))
		c.mu.Unlock()
		w.WriteHeader(status)
	}
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func msg() Message {
	return Message{Title: "SignalDeck: 2 new alert(s)", Body: "NVDA breakout\nBTC prediction 0.67", Kind: "alerts", Ts: 1751800000}
}

func TestDiscordPayloadShape(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler(204))
	defer srv.Close()
	n := &Notifier{DiscordWebhook: srv.URL + "/api/webhooks/123/secret-token"}
	n.Send(context.Background(), msg())

	if cap.count() != 1 {
		t.Fatalf("requests = %d, want 1", cap.count())
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(cap.bodies[0]), &body); err != nil {
		t.Fatalf("discord body not JSON: %v (%q)", err, cap.bodies[0])
	}
	if !strings.Contains(body.Content, "2 new alert(s)") || !strings.Contains(body.Content, "NVDA breakout") {
		t.Errorf("discord content missing title/body: %q", body.Content)
	}
	if cap.uas[0] == "" {
		t.Errorf("no declarative User-Agent sent")
	}
	st := n.Status()
	if !st[0].Configured || st[0].LastOK == 0 || st[0].LastError != "" {
		t.Errorf("discord status after success = %+v", st[0])
	}
	// The other transports stay unconfigured and untouched.
	if st[1].Configured || st[2].Configured || st[1].LastOK != 0 {
		t.Errorf("unconfigured transports touched: %+v", st)
	}
}

func TestTelegramPayloadShape(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler(200))
	defer srv.Close()
	n := &Notifier{TelegramToken: "123:ABCsecret", TelegramChatID: "42", TelegramAPIBase: srv.URL}
	n.Send(context.Background(), msg())

	if cap.count() != 1 {
		t.Fatalf("requests = %d, want 1", cap.count())
	}
	if want := "/bot123:ABCsecret/sendMessage"; cap.paths[0] != want {
		t.Errorf("telegram path = %q, want %q", cap.paths[0], want)
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(cap.bodies[0]), &body); err != nil {
		t.Fatalf("telegram body not JSON: %v", err)
	}
	if body.ChatID != "42" || !strings.Contains(body.Text, "BTC prediction 0.67") {
		t.Errorf("telegram payload = %+v", body)
	}
}

func TestTelegramNeedsBothTokenAndChatID(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler(200))
	defer srv.Close()
	n := &Notifier{TelegramToken: "123:ABC", TelegramAPIBase: srv.URL} // chat id missing
	if n.Enabled() {
		t.Errorf("token without chat id must not enable telegram")
	}
	n.Send(context.Background(), msg())
	if cap.count() != 0 {
		t.Errorf("half-configured telegram sent %d request(s)", cap.count())
	}
}

func TestWebhookPayloadShape(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler(200))
	defer srv.Close()
	n := &Notifier{WebhookURL: srv.URL + "/hook"}
	n.Send(context.Background(), msg())

	if cap.count() != 1 {
		t.Fatalf("requests = %d, want 1", cap.count())
	}
	var body Message
	if err := json.Unmarshal([]byte(cap.bodies[0]), &body); err != nil {
		t.Fatalf("webhook body not JSON: %v", err)
	}
	if body.Title == "" || body.Body == "" || body.Kind != "alerts" || body.Ts != 1751800000 {
		t.Errorf("webhook payload shape = %+v, want {title,body,kind,ts}", body)
	}
}

func TestRetryOnceThenSucceed(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		fail := calls == 1
		mu.Unlock()
		if fail {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	dq := &fakeDQ{}
	n := &Notifier{WebhookURL: srv.URL, DQ: dq}
	n.Send(context.Background(), msg())

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("attempts = %d, want 2 (1 retry)", got)
	}
	if len(dq.all()) != 0 {
		t.Errorf("recovered delivery still recorded dq: %+v", dq.all())
	}
	st := n.Status()
	if st[2].LastOK == 0 {
		t.Errorf("webhook LastOK not set after recovery: %+v", st[2])
	}
}

func TestFailureRecordsDQNeverBlocks(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler(500))
	defer srv.Close()
	dq := &fakeDQ{}
	n := &Notifier{WebhookURL: srv.URL, DQ: dq}
	n.Send(context.Background(), msg()) // must return normally

	if cap.count() != 2 {
		t.Fatalf("attempts = %d, want 2 (initial + 1 retry)", cap.count())
	}
	events := dq.all()
	if len(events) != 1 || events[0].Kind != "notify_failed" {
		t.Fatalf("dq events = %+v, want one notify_failed", events)
	}
	if !strings.Contains(events[0].Detail, "transport=webhook") {
		t.Errorf("dq detail missing transport: %q", events[0].Detail)
	}
	st := n.Status()
	if st[2].LastError == "" || st[2].LastErrorTs == 0 {
		t.Errorf("webhook status missing error record: %+v", st[2])
	}
}

func TestRedactionInErrorsAndDQ(t *testing.T) {
	// A server that is immediately closed: the connection error embeds the
	// full request URL — which contains the bot token — so the redaction path
	// is exercised end-to-end.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close()

	dq := &fakeDQ{}
	n := &Notifier{
		TelegramToken:   "999:VERYSECRET",
		TelegramChatID:  "7",
		TelegramAPIBase: base,
		DiscordWebhook:  base + "/api/webhooks/1/hooksecret",
		WebhookURL:      base + "/generic?key=urlsecret",
		DQ:              dq,
	}
	n.Send(context.Background(), msg())

	events := dq.all()
	if len(events) != 3 {
		t.Fatalf("dq events = %d, want 3 (one per failed transport)", len(events))
	}
	for _, ev := range events {
		for _, secret := range []string{"VERYSECRET", "hooksecret", "urlsecret"} {
			if strings.Contains(ev.Detail, secret) {
				t.Errorf("secret %q leaked into dq detail: %q", secret, ev.Detail)
			}
		}
	}
	for _, st := range n.Status() {
		for _, secret := range []string{"VERYSECRET", "hooksecret", "urlsecret"} {
			if strings.Contains(st.LastError, secret) {
				t.Errorf("secret %q leaked into status: %q", secret, st.LastError)
			}
		}
		if st.Configured && st.LastError == "" {
			t.Errorf("configured transport %s missing error after total failure", st.Name)
		}
	}
}

func TestNoConfigIsNoOp(t *testing.T) {
	dq := &fakeDQ{}
	n := &Notifier{DQ: dq}
	if n.Enabled() {
		t.Fatalf("empty notifier reports enabled")
	}
	if names := n.ConfiguredNames(); len(names) != 0 {
		t.Fatalf("ConfiguredNames = %v, want empty", names)
	}
	n.Send(context.Background(), msg())
	if len(dq.all()) != 0 {
		t.Errorf("no-op send recorded dq events: %+v", dq.all())
	}
	for _, st := range n.Status() {
		if st.Configured || st.LastOK != 0 || st.LastError != "" {
			t.Errorf("no-config status not pristine: %+v", st)
		}
	}
	// A nil notifier is also safe.
	var nilN *Notifier
	if nilN.Enabled() {
		t.Errorf("nil notifier enabled")
	}
	nilN.Send(context.Background(), msg())
}

func TestClipCaps(t *testing.T) {
	long := strings.Repeat("x", discordMaxChars+500)
	got := clip(long, discordMaxChars)
	if len([]rune(got)) != discordMaxChars {
		t.Errorf("clip len = %d, want %d", len([]rune(got)), discordMaxChars)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clip missing ellipsis marker")
	}
	if clip("short", 100) != "short" {
		t.Errorf("clip mangled short string")
	}
}

func TestSendFillsZeroTs(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler(200))
	defer srv.Close()
	fixed := time.Unix(1751801234, 0)
	n := &Notifier{WebhookURL: srv.URL, Now: func() time.Time { return fixed }}
	n.Send(context.Background(), Message{Title: "t", Kind: "alerts"})
	var body Message
	if err := json.Unmarshal([]byte(cap.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body.Ts != fixed.Unix() {
		t.Errorf("zero Ts not filled: %d want %d", body.Ts, fixed.Unix())
	}
}
