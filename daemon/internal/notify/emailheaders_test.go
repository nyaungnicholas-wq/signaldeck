package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSendEmailWithHeaders(t *testing.T) {
	host, port, received := fakeSMTP(t, "ok")
	n := &Notifier{SMTPHost: host, SMTPPort: port, SMTPFrom: "signaldeck@example.test"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := n.SendEmailWithHeaders(ctx, "m@example.test", "daily", "body", map[string]string{
		"List-Unsubscribe":      "<https://sd.example/api/alerts/unsubscribe?token=abc>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(received(), "body") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := received()
	for _, want := range []string{
		"List-Unsubscribe: <https://sd.example/api/alerts/unsubscribe?token=abc>\r\n",
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q:\n%s", want, got)
		}
	}

	// Injection guard: refused, and nothing reaches the server (each case has
	// its own fake, so a send the guard let through would land in it).
	for name, h := range map[string]map[string]string{
		"CRLF in value":   {"List-Unsubscribe": "<https://x>\r\nBcc: victim@example.test"},
		"bare LF":         {"List-Unsubscribe": "<https://x>\nBcc: v@example.test"},
		"reserved name":   {"Bcc": "victim@example.test"},
		"colon in name":   {"X-A:B": "v"},
		"newline in name": {"X-A\r\nBcc": "v"},
	} {
		host, port, received := fakeSMTP(t, "ok")
		n := &Notifier{SMTPHost: host, SMTPPort: port, SMTPFrom: "signaldeck@example.test"}
		if err := n.SendEmailWithHeaders(ctx, "m@example.test", "s", "b", h); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if got := received(); got != "" {
			t.Errorf("%s: a message reached the server:\n%s", name, got)
		}
	}
}
