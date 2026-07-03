// Package llm is SignalDeck's LLM provider abstraction: an OpenAI-compatible
// chat client (NVIDIA by default, but any OpenAI-compatible base URL works)
// with a hard daily call cap so a bug or loop can never run up an unbounded
// bill. The key lives only here and in config — it never reaches the browser
// or any API response.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// Client is the minimal LLM interface the agents code against.
type Client interface {
	// Enabled reports whether a key is configured (agents no-op when false).
	Enabled() bool
	// Complete runs a chat completion. It returns ErrDisabled when no key is
	// set and ErrCapReached when the daily call cap is exhausted.
	Complete(ctx context.Context, sys string, msgs []Message, maxTokens int) (string, error)
	// Model is the configured model id (for display).
	Model() string
	// Stats returns today's usage for the spend surface.
	Stats() Stats
}

// Stats is the daily usage snapshot.
type Stats struct {
	Day        string `json:"day"` // YYYY-MM-DD (UTC)
	Calls      int    `json:"calls"`
	DailyCap   int    `json:"dailyCap"`
	PromptTok  int    `json:"promptTokens"`
	OutputTok  int    `json:"outputTokens"`
	LastCallTs int64  `json:"lastCallTs"`
	LastError  string `json:"lastError"`
}

// Sentinel errors.
var (
	ErrDisabled   = fmt.Errorf("llm: no key configured")
	ErrCapReached = fmt.Errorf("llm: daily call cap reached")
)

// SpendStore persists the daily call counter so a daemon restart can't reset
// the spend cap. IncrAndGetSpend atomically increments and returns the count
// for the given UTC day (YYYY-MM-DD).
type SpendStore interface {
	IncrAndGetSpend(ctx context.Context, day string) (int, error)
}

// httpClient is the concrete OpenAI-compatible client.
type httpClient struct {
	key, baseURL, model string
	dailyCap            int
	http                *http.Client

	mu    sync.Mutex
	stats Stats
	spend SpendStore // nil = in-memory counter only (tests)
}

// SetSpendStore wires a persistent daily-call counter into a client built by
// New. Safe to call once at startup, before any Complete call.
func SetSpendStore(c Client, s SpendStore) {
	if h, ok := c.(*httpClient); ok {
		h.spend = s
	}
}

// New builds a client. key=="" yields a disabled client that no-ops safely.
// dailyCap<=0 defaults to 2000.
func New(key, baseURL, model string, dailyCap int) Client {
	if dailyCap <= 0 {
		dailyCap = 2000
	}
	return &httpClient{
		key:      key,
		baseURL:  strings.TrimRight(baseURL, "/"),
		model:    model,
		dailyCap: dailyCap,
		http:     &http.Client{Timeout: 90 * time.Second},
		stats:    Stats{DailyCap: dailyCap},
	}
}

func (c *httpClient) Enabled() bool { return c.key != "" }
func (c *httpClient) Model() string { return c.model }

func (c *httpClient) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// reserve enforces the daily cap under lock and rolls the counter over at UTC
// midnight. When a SpendStore is wired, the counter is persisted in SQLite so
// restarts can't reset it; otherwise (tests) it falls back to in-memory.
// It returns false when the cap is exhausted for today.
func (c *httpClient) reserve(ctx context.Context, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	day := now.UTC().Format("2006-01-02")
	if c.stats.Day != day {
		c.stats = Stats{Day: day, DailyCap: c.dailyCap}
	}
	if c.spend != nil {
		if n, err := c.spend.IncrAndGetSpend(ctx, day); err == nil {
			// Never let the DB counter move the in-memory count DOWN: calls
			// served through the in-memory fallback during a DB outage were
			// never persisted, so after recovery the store lags reality. Max
			// keeps the cap honest across outage/recovery cycles (a slight
			// over-count beats silently reopening already-spent headroom).
			if n < c.stats.Calls+1 {
				n = c.stats.Calls + 1
			}
			c.stats.Calls = n
			return n <= c.dailyCap
		}
		// Persistence failure: fall through to the in-memory counter so a
		// transient DB error can't disable (or unbound) the AI layer.
	}
	if c.stats.Calls >= c.dailyCap {
		return false
	}
	c.stats.Calls++
	return true
}

func (c *httpClient) record(promptTok, outputTok int, now time.Time, errMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.PromptTok += promptTok
	c.stats.OutputTok += outputTok
	c.stats.LastCallTs = now.Unix()
	c.stats.LastError = errMsg
}

type chatReq struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
}

type chatResp struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete runs a chat completion with the given system prompt and messages.
func (c *httpClient) Complete(ctx context.Context, sys string, msgs []Message, maxTokens int) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	now := time.Now()
	if !c.reserve(ctx, now) {
		return "", ErrCapReached
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	all := make([]Message, 0, len(msgs)+1)
	if sys != "" {
		all = append(all, Message{Role: "system", Content: sys})
	}
	all = append(all, msgs...)

	// Bound the prompt so the free-tier model stays fast + reliable no matter
	// how much the watchlist grows — the single most important thing for a
	// public deployment. Agents put their (large) data in the last message,
	// so we trim that one, preserving the system charter intact.
	trimToBudget(all, maxPromptChars)

	if maxTokens > maxOutputTokens {
		maxTokens = maxOutputTokens
	}

	body, err := json.Marshal(chatReq{Model: c.model, Messages: all, MaxTokens: maxTokens, Temperature: 0.2})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		c.record(0, 0, now, "network")
		return "", fmt.Errorf("llm: request: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))

	var cr chatResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		c.record(0, 0, now, "decode")
		return "", fmt.Errorf("llm: decode (HTTP %d)", res.StatusCode)
	}
	if res.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", res.StatusCode)
		if cr.Error != nil {
			msg = cr.Error.Message
		}
		c.record(0, 0, now, sanitize(msg))
		// Never surface the provider's raw error verbatim (it can echo the
		// key in some proxies); a sanitized short form only.
		return "", fmt.Errorf("llm: provider error: %s", sanitize(msg))
	}
	c.record(cr.Usage.PromptTokens, cr.Usage.CompletionTokens, now, "")
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm: empty response")
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), nil
}

// Prompt/output budgets keep every call small enough for the free-tier model
// to answer reliably. ~24k input chars ≈ 6k tokens; a filing gets most of it,
// a watchlist digest uses a fraction. Output is capped so replies stay tight.
const (
	maxPromptChars  = 24000
	maxOutputTokens = 900
)

// trimToBudget shrinks the message list to at most `budget` total content
// chars by truncating the LAST message (where agents place their data),
// leaving the system charter and earlier turns intact.
func trimToBudget(msgs []Message, budget int) {
	if len(msgs) == 0 {
		return
	}
	last := len(msgs) - 1
	other := 0
	for i, m := range msgs {
		if i != last {
			other += len(m.Content)
		}
	}
	room := budget - other
	if room < 500 {
		room = 500 // always leave a little room for the final message
	}
	if len(msgs[last].Content) > room {
		msgs[last].Content = msgs[last].Content[:room] + "\n…[truncated to fit model budget]"
	}
}

// sanitize strips anything key-shaped from an error string before it can be
// stored or returned.
func sanitize(s string) string {
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "nvapi-") || strings.HasPrefix(tok, "sk-") || len(tok) > 40 {
			s = strings.ReplaceAll(s, tok, "[redacted]")
		}
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
