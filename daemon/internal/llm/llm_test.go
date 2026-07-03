package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDisabledClientNoOps(t *testing.T) {
	c := New("", "https://x/v1", "m", 10)
	if c.Enabled() {
		t.Fatal("empty key must be disabled")
	}
	if _, err := c.Complete(context.Background(), "s", []Message{{Role: "user", Content: "hi"}}, 16); err != ErrDisabled {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}

func TestComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer auth")
		}
		_ = json.NewEncoder(w).Encode(chatResp{
			Choices: []struct {
				Message Message `json:"message"`
			}{{Message: Message{Role: "assistant", Content: "hello"}}},
		})
	}))
	defer srv.Close()

	c := New("k", srv.URL, "m", 10)
	out, err := c.Complete(context.Background(), "sys", []Message{{Role: "user", Content: "hi"}}, 16)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Fatalf("got %q", out)
	}
	if c.Stats().Calls != 1 {
		t.Fatalf("calls = %d, want 1", c.Stats().Calls)
	}
}

func TestDailyCapEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(chatResp{Choices: []struct {
			Message Message `json:"message"`
		}{{Message: Message{Content: "ok"}}}})
	}))
	defer srv.Close()

	c := New("k", srv.URL, "m", 2) // cap = 2
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.Complete(ctx, "", []Message{{Role: "user", Content: "x"}}, 8); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if _, err := c.Complete(ctx, "", []Message{{Role: "user", Content: "x"}}, 8); err != ErrCapReached {
		t.Fatalf("3rd call want ErrCapReached, got %v", err)
	}
}

func TestSanitizeRedactsKeyShapedTokens(t *testing.T) {
	got := sanitize("bad key nvapi-abcdefghijklmnopqrstuvwxyz012345 rejected")
	if got == "" || len(got) == 0 {
		t.Fatal("empty")
	}
	if !contains(got, "[redacted]") || contains(got, "nvapi-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("key not redacted: %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestTrimToBudget(t *testing.T) {
	big := make([]byte, 40000)
	for i := range big {
		big[i] = 'x'
	}
	msgs := []Message{{Role: "system", Content: "charter"}, {Role: "user", Content: string(big)}}
	trimToBudget(msgs, maxPromptChars)
	if msgs[0].Content != "charter" {
		t.Fatal("system charter must be preserved")
	}
	total := len(msgs[0].Content) + len(msgs[1].Content)
	if total > maxPromptChars+64 {
		t.Fatalf("total %d exceeds budget %d", total, maxPromptChars)
	}
}
