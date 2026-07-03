package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// flakySpend is a SpendStore whose backing counter can be frozen (simulating a
// DB outage where increments are lost) and later recovered.
type flakySpend struct {
	n    int
	fail bool
}

func (f *flakySpend) IncrAndGetSpend(ctx context.Context, day string) (int, error) {
	if f.fail {
		return 0, fmt.Errorf("db down")
	}
	f.n++
	return f.n, nil
}

// TestReserveOutageRecoveryKeepsCap: calls served via the in-memory fallback
// during a store outage must not be forgotten when the store recovers
// (regression: the DB counter used to overwrite the in-memory count downward,
// reopening already-spent headroom).
func TestReserveOutageRecoveryKeepsCap(t *testing.T) {
	c := New("k", "https://x/v1", "m", 10).(*httpClient)
	fs := &flakySpend{}
	SetSpendStore(c, fs)
	now := time.Now()

	// 3 persisted calls.
	for i := 0; i < 3; i++ {
		if !c.reserve(context.Background(), now) {
			t.Fatalf("call %d should be allowed", i)
		}
	}
	// Outage: 5 calls served in-memory (store frozen at 3).
	fs.fail = true
	for i := 0; i < 5; i++ {
		if !c.reserve(context.Background(), now) {
			t.Fatalf("outage call %d should be allowed (8 <= cap 10)", i)
		}
	}
	// Recovery: store would return 4; in-memory truth is 8, so next call is 9.
	fs.fail = false
	if !c.reserve(context.Background(), now) {
		t.Fatal("call 9 should be allowed")
	}
	if !c.reserve(context.Background(), now) {
		t.Fatal("call 10 should be allowed")
	}
	if c.reserve(context.Background(), now) {
		t.Fatalf("call 11 must be denied (cap 10); stats=%+v", c.Stats())
	}
	if got := c.Stats().Calls; got < 10 {
		t.Fatalf("Stats().Calls = %d, want >= 10 (must not under-report after recovery)", got)
	}
}
