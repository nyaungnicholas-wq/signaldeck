package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// jsonResp builds a minimal OpenAI-compatible completion body.
func jsonResp(content string) string {
	return `{"choices":[{"message":{"content":` + strconv.Quote(content) +
		`}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`
}

func TestDisabledClientNoOps(t *testing.T) {
	c := New(nil, "https://x/v1", "m", "", "", 10)
	if c.Enabled() {
		t.Fatal("empty key list must be disabled")
	}
	if _, err := c.Complete(context.Background(), "s", []Message{{Role: "user", Content: "hi"}}, 16); err != ErrDisabled {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
	// Whitespace-only keys are filtered → still disabled.
	if New([]string{"", "  "}, "https://x/v1", "m", "", "", 10).Enabled() {
		t.Fatal("whitespace keys must be filtered to disabled")
	}
}

func TestComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer auth")
		}
		fmt.Fprint(w, jsonResp("hello"))
	}))
	defer srv.Close()

	c := New([]string{"k"}, srv.URL, "m", "", "", 10)
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

func TestTieredDefaults(t *testing.T) {
	c := New([]string{"a", "b", "a"}, "https://x/v1", "m", "", "", 10) // dup filtered → 2
	tc, ok := c.(Tiered)
	if !ok {
		t.Fatal("concrete client must implement Tiered")
	}
	if tc.KeyCount() != 2 {
		t.Fatalf("KeyCount = %d, want 2 (dup filtered)", tc.KeyCount())
	}
	if tc.DeepModel() != "m" || tc.FastModel() != "m" {
		t.Fatalf("empty deep/fast must default to model; got deep=%q fast=%q", tc.DeepModel(), tc.FastModel())
	}
	c2 := New([]string{"a"}, "https://x/v1", "", "", "", 10) // empty model → DefaultModel
	if c2.Model() != DefaultModel {
		t.Fatalf("empty model must default to %q, got %q", DefaultModel, c2.Model())
	}
}

// TestKeyPoolRoundRobin: successive calls rotate through the key pool.
func TestKeyPoolRoundRobin(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		mu.Unlock()
		fmt.Fprint(w, jsonResp("ok"))
	}))
	defer srv.Close()

	c := New([]string{"k1", "k2", "k3"}, srv.URL, "m", "", "", 100)
	for i := 0; i < 3; i++ {
		if _, err := c.Complete(context.Background(), "", []Message{{Role: "user", Content: "x"}}, 8); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if len(seen) != 3 || seen[0] != "k1" || seen[1] != "k2" || seen[2] != "k3" {
		t.Fatalf("round-robin failed, saw %v", seen)
	}
}

// TestFailoverOnServerError: a 500 on the first key fails over to the next key,
// the logical call succeeds, and the daily cap is consumed only ONCE.
func TestFailoverOnServerError(t *testing.T) {
	var mu sync.Mutex
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		if n == 1 {
			http.Error(w, `{"error":{"message":"overloaded"}}`, 500)
			return
		}
		fmt.Fprint(w, jsonResp("recovered"))
	}))
	defer srv.Close()

	c := New([]string{"bad", "good"}, srv.URL, "m", "", "", 100)
	out, err := c.Complete(context.Background(), "", []Message{{Role: "user", Content: "x"}}, 8)
	if err != nil {
		t.Fatalf("failover should succeed, got %v", err)
	}
	if out != "recovered" {
		t.Fatalf("got %q, want recovered", out)
	}
	if hits != 2 {
		t.Fatalf("expected 2 attempts (500 then 200), got %d", hits)
	}
	if c.Stats().Calls != 1 {
		t.Fatalf("failover retry must NOT double-count the cap: Calls=%d want 1", c.Stats().Calls)
	}
}

// TestNoFailoverOnBadRequest: a 400 is not retryable — no second key is tried.
func TestNoFailoverOnBadRequest(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, `{"error":{"message":"bad model"}}`, 400)
	}))
	defer srv.Close()

	c := New([]string{"k1", "k2"}, srv.URL, "m", "", "", 100)
	if _, err := c.Complete(context.Background(), "", []Message{{Role: "user", Content: "x"}}, 8); err == nil {
		t.Fatal("400 must return an error")
	}
	if hits != 1 {
		t.Fatalf("400 must not fail over: hits=%d want 1", hits)
	}
}

// TestReasoningContentFallback: when content is empty but reasoning_content is
// present (a reasoning model that spent its budget thinking), the reasoning is
// returned rather than an empty string.
func TestReasoningContentFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"","reasoning_content":"the answer"}}]}`)
	}))
	defer srv.Close()

	c := New([]string{"k"}, srv.URL, "m", "", "", 10)
	out, err := c.Complete(context.Background(), "", []Message{{Role: "user", Content: "x"}}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if out != "the answer" {
		t.Fatalf("reasoning fallback failed, got %q", out)
	}
}

func TestExtractAnswer(t *testing.T) {
	cases := []struct{ content, reasoning, want string }{
		{"final answer", "scratch", "final answer"},
		{"", "reasoning only", "reasoning only"},
		{"<think>hidden</think>visible", "", "visible"},
		{"  spaced  ", "", "spaced"},
		{"", "<think>t</think>fallback", "fallback"},
	}
	for i, tc := range cases {
		if got := extractAnswer(tc.content, tc.reasoning); got != tc.want {
			t.Errorf("case %d: extractAnswer(%q,%q)=%q want %q", i, tc.content, tc.reasoning, got, tc.want)
		}
	}
}

// TestCompleteWithModel: CompleteWith targets an explicit model id.
func TestCompleteWithModel(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = jsonDecode(r, &body)
		gotModel = body.Model
		fmt.Fprint(w, jsonResp("ok"))
	}))
	defer srv.Close()

	c := New([]string{"k"}, srv.URL, "default-m", "deep-m", "", 10).(Tiered)
	if _, err := c.CompleteWith(context.Background(), c.DeepModel(), "", []Message{{Role: "user", Content: "x"}}, 8); err != nil {
		t.Fatal(err)
	}
	if gotModel != "deep-m" {
		t.Fatalf("CompleteWith used model %q, want deep-m", gotModel)
	}
}

func TestDailyCapEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, jsonResp("ok"))
	}))
	defer srv.Close()

	c := New([]string{"k"}, srv.URL, "m", "", "", 2) // cap = 2
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
	if !strings.Contains(got, "[redacted]") || strings.Contains(got, "nvapi-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("key not redacted: %q", got)
	}
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
	c := New([]string{"k"}, "https://x/v1", "m", "", "", 10).(*httpClient)
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

// jsonDecode is a tiny helper to read a JSON request body in tests.
func jsonDecode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
