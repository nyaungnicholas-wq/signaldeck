package sentiment

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// fakeClient is a recording llm.Client stand-in. It captures the system prompt
// and messages of the last Complete call and returns a canned reply (or a
// canned error). enabled controls the disabled short-circuit path; errN, when
// >0, makes the errN-th call (1-indexed) return errReply instead of reply.
type fakeClient struct {
	enabled bool
	reply   string

	err error // returned by every Complete call when non-nil

	calls   int
	gotSys  string
	gotMsgs []llm.Message
}

func (f *fakeClient) Enabled() bool    { return f.enabled }
func (f *fakeClient) Model() string    { return "fake" }
func (f *fakeClient) Stats() llm.Stats { return llm.Stats{} }
func (f *fakeClient) Complete(ctx context.Context, sys string, msgs []llm.Message, maxTokens int) (string, error) {
	f.calls++
	f.gotSys = sys
	f.gotMsgs = msgs
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

// openStore spins up a fresh on-disk sqlite store in a temp dir.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestTag(t *testing.T) {
	cases := []struct {
		name         string
		enabled      bool
		reply        string
		wantSent     string
		wantScore    float64
		wantRational string
		wantCalls    int
	}{
		{
			name:         "clean bullish json",
			enabled:      true,
			reply:        `{"sentiment":"bullish","score":0.8,"rationale":"beat earnings by a wide margin"}`,
			wantSent:     "bullish",
			wantScore:    0.8,
			wantRational: "beat earnings by a wide margin",
			wantCalls:    1,
		},
		{
			name:      "score above one is clamped",
			enabled:   true,
			reply:     `{"sentiment":"bullish","score":4.2,"rationale":"huge"}`,
			wantSent:  "bullish",
			wantScore: 1,
			wantCalls: 1,
		},
		{
			name:      "score below minus one is clamped",
			enabled:   true,
			reply:     `{"sentiment":"bearish","score":-9,"rationale":"bankruptcy filed"}`,
			wantSent:  "bearish",
			wantScore: -1,
			wantCalls: 1,
		},
		{
			name:      "neutral zeroes the score",
			enabled:   true,
			reply:     `{"sentiment":"neutral","score":0.5,"rationale":"unclear"}`,
			wantSent:  "neutral",
			wantScore: 0,
			wantCalls: 1,
		},
		{
			name:      "unknown label degrades to neutral",
			enabled:   true,
			reply:     `{"sentiment":"MOON","score":0.9,"rationale":"hype"}`,
			wantSent:  "neutral",
			wantScore: 0,
			wantCalls: 1,
		},
		{
			name:      "prose-wrapped json is extracted",
			enabled:   true,
			reply:     "Sure! Here you go:\n{\"sentiment\":\"bearish\",\"score\":-0.4,\"rationale\":\"guidance cut\"}\nHope that helps.",
			wantSent:  "bearish",
			wantScore: -0.4,
			wantCalls: 1,
		},
		{
			name:      "malformed json falls back to neutral not error",
			enabled:   true,
			reply:     `{"sentiment":"bullish", score: not json`,
			wantSent:  "neutral",
			wantScore: 0,
			wantCalls: 1,
		},
		{
			name:      "no json at all falls back to neutral",
			enabled:   true,
			reply:     "I cannot help with that.",
			wantSent:  "neutral",
			wantScore: 0,
			wantCalls: 1,
		},
		{
			name:      "disabled client is a no-op unrated",
			enabled:   false,
			reply:     `{"sentiment":"bullish","score":1,"rationale":"x"}`,
			wantSent:  "unrated",
			wantScore: 0,
			wantCalls: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{enabled: tc.enabled, reply: tc.reply}
			got, err := Tag(context.Background(), fc, "ACME beats and raises")
			if err != nil {
				t.Fatalf("Tag returned error: %v", err)
			}
			if got.Sentiment != tc.wantSent {
				t.Errorf("sentiment = %q, want %q", got.Sentiment, tc.wantSent)
			}
			if got.Score != tc.wantScore {
				t.Errorf("score = %v, want %v", got.Score, tc.wantScore)
			}
			if tc.wantRational != "" && got.Rationale != tc.wantRational {
				t.Errorf("rationale = %q, want %q", got.Rationale, tc.wantRational)
			}
			if fc.calls != tc.wantCalls {
				t.Errorf("llm calls = %d, want %d", fc.calls, tc.wantCalls)
			}
		})
	}
}

// TestTagPromptShape verifies the charter goes in the SYSTEM slot and the
// headline is carried in a USER message framed as untrusted data.
func TestTagPromptShape(t *testing.T) {
	fc := &fakeClient{enabled: true, reply: `{"sentiment":"neutral","score":0,"rationale":"ok"}`}
	const headline = "Regulators open probe into ACME accounting"
	if _, err := Tag(context.Background(), fc, headline); err != nil {
		t.Fatalf("Tag: %v", err)
	}
	if fc.gotSys != Charter {
		t.Errorf("system prompt was not the Charter verbatim")
	}
	if len(fc.gotMsgs) != 1 {
		t.Fatalf("want exactly 1 user message, got %d", len(fc.gotMsgs))
	}
	m := fc.gotMsgs[0]
	if m.Role != "user" {
		t.Errorf("message role = %q, want user", m.Role)
	}
	if !strings.Contains(m.Content, headline) {
		t.Errorf("user message does not contain the headline")
	}
	if !strings.Contains(strings.ToLower(m.Content), "untrusted") {
		t.Errorf("user message does not frame the headline as untrusted data")
	}
}

// TestTagPropagatesLLMError confirms a genuine llm failure surfaces as an error
// (only parse failures degrade to neutral).
func TestTagPropagatesLLMError(t *testing.T) {
	fc := &fakeClient{enabled: true, err: llm.ErrCapReached}
	_, err := Tag(context.Background(), fc, "anything")
	if !errors.Is(err, llm.ErrCapReached) {
		t.Fatalf("want ErrCapReached, got %v", err)
	}
}

// seedUnrated inserts a symbol and one unrated headline, returning the news id.
func seedUnrated(t *testing.T, st *store.Store, headline string) string {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	id := "art-" + headline[:min(4, len(headline))]
	if err := st.InsertNews(ctx, store.NewsItem{
		ID:       id,
		SymbolID: sym.ID,
		Ts:       1_700_000_000,
		Headline: headline,
		Source:   "test",
	}); err != nil {
		t.Fatalf("insert news: %v", err)
	}
	return id
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestRunOnceRatesSeededRow rates one seeded unrated row and confirms the tag
// is persisted and the row leaves the unrated queue.
func TestRunOnceRatesSeededRow(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	id := seedUnrated(t, st, "ACME lands record cloud contract")

	fc := &fakeClient{enabled: true, reply: `{"sentiment":"bullish","score":0.7,"rationale":"record contract"}`}
	n, err := RunOnce(ctx, fc, st, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("rated = %d, want 1", n)
	}

	// The queue is now empty.
	pending, err := st.UnratedNews(ctx, 10)
	if err != nil {
		t.Fatalf("UnratedNews: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("still %d unrated rows, want 0", len(pending))
	}

	// The persisted tag matches.
	news, err := st.RecentNews(ctx, 10)
	if err != nil {
		t.Fatalf("RecentNews: %v", err)
	}
	if len(news) != 1 || news[0].ID != id {
		t.Fatalf("want 1 news row %q, got %+v", id, news)
	}
	if news[0].Sentiment != "bullish" || news[0].Score != 0.7 || news[0].Rationale != "record contract" {
		t.Errorf("persisted tag = %+v", news[0])
	}
}

// capClient errors with ErrCapReached from the Nth call onward.
type capClient struct {
	capAfter int // succeed for the first capAfter calls, then ErrCapReached
	calls    int
}

func (c *capClient) Enabled() bool    { return true }
func (c *capClient) Model() string    { return "cap" }
func (c *capClient) Stats() llm.Stats { return llm.Stats{} }
func (c *capClient) Complete(ctx context.Context, sys string, msgs []llm.Message, maxTokens int) (string, error) {
	c.calls++
	if c.calls > c.capAfter {
		return "", llm.ErrCapReached
	}
	return `{"sentiment":"neutral","score":0,"rationale":"ok"}`, nil
}

// TestRunOnceStopsOnCap confirms hitting the daily cap ends the run gracefully:
// the rows rated before the cap are counted and no error is returned.
func TestRunOnceStopsOnCap(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	for _, h := range []string{"aaaa headline one", "bbbb headline two", "cccc headline three"} {
		seedUnrated(t, st, h)
	}

	cc := &capClient{capAfter: 2}
	n, err := RunOnce(ctx, cc, st, 10)
	if err != nil {
		t.Fatalf("RunOnce should not error on cap, got %v", err)
	}
	if n != 2 {
		t.Fatalf("rated = %d, want 2 (cap hit on 3rd)", n)
	}
	// One row remains unrated.
	pending, err := st.UnratedNews(ctx, 10)
	if err != nil {
		t.Fatalf("UnratedNews: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("want 1 unrated row remaining, got %d", len(pending))
	}
}

// TestRunOnceRespectsContext confirms a cancelled context stops the loop.
func TestRunOnceRespectsContext(t *testing.T) {
	st := openStore(t)
	seedUnrated(t, st, "dddd headline")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fc := &fakeClient{enabled: true, reply: `{"sentiment":"neutral","score":0,"rationale":"x"}`}
	n, err := RunOnce(ctx, fc, st, 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if n != 0 {
		t.Errorf("rated = %d, want 0", n)
	}
	if fc.calls != 0 {
		t.Errorf("llm was called %d times despite cancelled ctx", fc.calls)
	}
}

// TestRunOnceEmptyQueue is a trivial no-work pass.
func TestRunOnceEmptyQueue(t *testing.T) {
	st := openStore(t)
	fc := &fakeClient{enabled: true, reply: `{"sentiment":"neutral","score":0,"rationale":"x"}`}
	n, err := RunOnce(context.Background(), fc, st, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("rated = %d, want 0", n)
	}
}
