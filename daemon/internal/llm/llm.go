// Package llm is SignalDeck's LLM provider abstraction: an OpenAI-compatible
// chat client (NVIDIA by default, but any OpenAI-compatible base URL works)
// with a hard daily call cap so a bug or loop can never run up an unbounded
// bill. Keys live only here and in config — they never reach the browser or
// any API response.
//
// It supports a POOL of API keys (round-robin with failover) and a small set
// of model TIERS: a fast default model for high-volume workers, an optional
// "deep" model for on-demand reasoning (analyst reports, bull/bear debate,
// scenario narratives), and a "fast" model for the highest-frequency, lowest-
// stakes calls (e.g. per-headline sentiment tagging). Callers that want a tier
// other than the default type-assert to the Tiered interface.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// Client is the minimal LLM interface the agents code against. It is kept
// deliberately small and STABLE — every agent and every test fake implements
// exactly this. Tier-aware capabilities live on the Tiered extension so adding
// them never breaks a fake.
type Client interface {
	// Enabled reports whether at least one key is configured (agents no-op
	// when false).
	Enabled() bool
	// Complete runs a chat completion on the default model. It returns
	// ErrDisabled when no key is set and ErrCapReached when the daily call cap
	// is exhausted.
	Complete(ctx context.Context, sys string, msgs []Message, maxTokens int) (string, error)
	// Model is the configured default model id (for display).
	Model() string
	// Stats returns today's usage for the spend surface.
	Stats() Stats
}

// Tiered is the optional extension the concrete client implements. Callers that
// want the deep reasoning model (debate, scenario, on-demand analyst) do:
//
//	if t, ok := client.(llm.Tiered); ok { t.CompleteWith(ctx, t.DeepModel(), …) }
//	else { client.Complete(ctx, …) }
//
// Keeping this separate from Client means the many test fakes stay valid.
type Tiered interface {
	Client
	// CompleteWith runs a completion on an explicit model id ("" = default).
	CompleteWith(ctx context.Context, model, sys string, msgs []Message, maxTokens int) (string, error)
	// DeepModel is the id used for high-quality on-demand reasoning.
	DeepModel() string
	// FastModel is the id used for the highest-frequency, lowest-stakes calls.
	FastModel() string
	// KeyCount is how many API keys are in the failover pool.
	KeyCount() int
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
	keys                        []string
	baseURL                     string
	model, deepModel, fastModel string
	dailyCap                    int
	http                        *http.Client

	mu     sync.Mutex
	keyIdx int // round-robin cursor into keys
	stats  Stats
	spend  SpendStore // nil = in-memory counter only (tests)
}

// SetSpendStore wires a persistent daily-call counter into a client built by
// New. Safe to call once at startup, before any Complete call.
func SetSpendStore(c Client, s SpendStore) {
	if h, ok := c.(*httpClient); ok {
		h.spend = s
	}
}

// New builds a pooled client. An empty key list yields a disabled client that
// no-ops safely. dailyCap<=0 defaults to 2000. deepModel/fastModel default to
// model when empty. Per-attempt timeouts are enforced via context, so the
// http.Client itself carries no global timeout (a slow deep model must not be
// killed at the same deadline as a fast one).
func New(keys []string, baseURL, model, deepModel, fastModel string, dailyCap int) Client {
	if dailyCap <= 0 {
		dailyCap = 2000
	}
	cleaned := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			cleaned = append(cleaned, k)
		}
	}
	if model == "" {
		model = DefaultModel
	}
	if deepModel == "" {
		deepModel = model
	}
	if fastModel == "" {
		fastModel = model
	}
	return &httpClient{
		keys:      cleaned,
		baseURL:   strings.TrimRight(baseURL, "/"),
		model:     model,
		deepModel: deepModel,
		fastModel: fastModel,
		dailyCap:  dailyCap,
		http:      &http.Client{}, // per-attempt ctx deadline governs timeout
		stats:     Stats{DailyCap: dailyCap},
	}
}

// Default model ids (overridable via config).
//
// EVERY PREVIOUS DEFAULT IS RETIRED. Measured against
// https://integrate.api.nvidia.com/v1 on 2026-08-27: qwen/qwen3.5-122b-a10b,
// nvidia/llama-3.3-nemotron-super-49b-v1.5 and meta/llama-3.1-8b-instruct all
// return HTTP 410 Gone. That is why ai-analyst and sentiment-tagger had been
// erroring `llm: provider error: HTTP 410` on every run -- 31 failures a day --
// and why the sentiment leg carries no measured lift: its tagger never ran.
// The same class of breakage is already recorded in daemon/.env ("2026-07-19:
// fixed dead AI model"), so vendor retirement is recurring, not a one-off.
//
// THE /models CATALOGUE IS NOT EVIDENCE. nvidia/llama-3.1-nemotron-70b-instruct
// and nvidia/mistral-nemo-minitron-8b-8k-instruct are both LISTED there and both
// answer HTTP 404 to an actual completion. Every id below was verified by
// issuing a real chat completion, not by reading the list.
//
// Tiering follows the measurement:
//   - nemotron-3-nano-30b-a3b answered a sentiment classification correctly in
//     ~950ms, so it takes the high-volume default and fast tiers.
//   - nemotron-3-super-120b-a12b is a reasoning model (it emits
//     reasoning_content and needs a generous max_tokens or it truncates
//     mid-thought), which is exactly the on-demand deep tier and exactly wrong
//     for per-headline work.
//   - nemotron-3.5-lightning-30b-a3b was rejected despite its name: 19.8s and
//     still truncated on the same prompt.
const (
	DefaultModel = "nvidia/nemotron-3-nano-30b-a3b"
	DefaultDeep  = "nvidia/nemotron-3-super-120b-a12b"
	DefaultFast  = "nvidia/nemotron-3-nano-30b-a3b"
)

func (c *httpClient) Enabled() bool     { return len(c.keys) > 0 }
func (c *httpClient) Model() string     { return c.model }
func (c *httpClient) DeepModel() string { return c.deepModel }
func (c *httpClient) FastModel() string { return c.fastModel }
func (c *httpClient) KeyCount() int     { return len(c.keys) }

func (c *httpClient) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// nextKey returns the next key in round-robin order (under lock).
func (c *httpClient) nextKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := c.keys[c.keyIdx%len(c.keys)]
	c.keyIdx++
	return k
}

// reserve enforces the daily cap under lock and rolls the counter over at UTC
// midnight. When a SpendStore is wired, the counter is persisted in SQLite so
// restarts can't reset it; otherwise (tests) it falls back to in-memory.
// It returns false when the cap is exhausted for today. Reserved ONCE per
// logical Complete call — failover retries do not each consume the cap.
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
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete runs a chat completion on the default model.
func (c *httpClient) Complete(ctx context.Context, sys string, msgs []Message, maxTokens int) (string, error) {
	return c.CompleteWith(ctx, c.model, sys, msgs, maxTokens)
}

// CompleteWith runs a chat completion on an explicit model id ("" = default),
// rotating through the key pool on retryable failures (429 / 5xx / network /
// timeout). The daily cap is reserved once for the whole call.
func (c *httpClient) CompleteWith(ctx context.Context, model, sys string, msgs []Message, maxTokens int) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	if model == "" {
		model = c.model
	}
	now := time.Now()
	reserved := c.reserve(ctx, now)
	// ATTRIBUTE THE SPEND. The daily counter is a single integer, so when the
	// 2,000-call budget was exhausted on 2026-08-05 there was no way to ask
	// WHICH caller spent it — the scheduled fleet accounts for at most ~121
	// calls/day, and the remaining ~1,880 could only be narrowed to "one of the
	// four request-driven endpoints" by reading code. A budget you cannot
	// attribute is one you cannot manage.
	//
	// The charter already identifies the caller uniquely (every agent has its
	// own constant), so a fingerprint of it needs no signature change and no new
	// interface — and cannot drift out of sync with a hand-maintained registry
	// of caller names. Logged rather than counted in a table on purpose: this
	// answers "who spent it" by grep, and adding a second persisted counter
	// alongside llm_spend risks the two disagreeing about the cap.
	//
	// Refusals are logged too. They are the cheap signal that a caller is still
	// hammering a spent budget, which is exactly what you want to see.
	slog.Info("llm call", "caller", callerFingerprint(sys), "model", model,
		"reserved", reserved, "spendToday", c.Stats().Calls)
	if !reserved {
		return "", ErrCapReached
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	if maxTokens > maxOutputTokens {
		maxTokens = maxOutputTokens
	}

	all := make([]Message, 0, len(msgs)+1)
	if sys != "" {
		all = append(all, Message{Role: "system", Content: sys})
	}
	all = append(all, msgs...)
	// Bound the prompt so the model stays fast + reliable no matter how much
	// the watchlist grows. Agents put their (large) data in the last message,
	// so we trim that one, preserving the system charter intact.
	trimToBudget(all, maxPromptChars)

	body, err := json.Marshal(chatReq{Model: model, Messages: all, MaxTokens: maxTokens, Temperature: 0.2})
	if err != nil {
		return "", err
	}

	perAttempt := c.attemptTimeout(model)
	// Retry transient failures (429 / 5xx / network / timeout / bad-key) up to
	// maxAttempts even on a SINGLE key. A freshly-restarted daemon fires dozens
	// of concurrent LLM calls and the provider occasionally returns a transient
	// error under that burst; a single-attempt client would surface it to the
	// user. With multiple keys each attempt rotates to a fresh key; with one key
	// it retries the same key after a short backoff. Permanent errors (bad
	// request / unknown model) are not retryable and return immediately.
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(i) * 250 * time.Millisecond):
			}
		}
		out, retryable, err := c.attempt(ctx, c.nextKey(), body, perAttempt, now)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("llm: request failed")
	}
	return "", lastErr
}

// attempt performs a single request against one key. It returns (output, retryable, error).
func (c *httpClient) attempt(ctx context.Context, key string, body []byte, timeout time.Duration, now time.Time) (string, bool, error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		// network error / timeout — retryable on another key
		c.record(0, 0, now, "network")
		return "", true, fmt.Errorf("llm: request: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))

	var cr chatResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		c.record(0, 0, now, "decode")
		// a decode failure is usually a gateway/HTML error page → retryable
		return "", res.StatusCode >= 500 || res.StatusCode == 0, fmt.Errorf("llm: decode (HTTP %d)", res.StatusCode)
	}
	if res.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", res.StatusCode)
		if cr.Error != nil {
			msg = cr.Error.Message
		}
		c.record(0, 0, now, sanitize(msg))
		// 429 (rate limit), 5xx (server), 401/403 (this key may be bad — try
		// another) are all worth a failover; 400/404 (bad request/model) are
		// not. Never surface the provider's raw error verbatim.
		retryable := res.StatusCode == 429 || res.StatusCode >= 500 || res.StatusCode == 401 || res.StatusCode == 403
		return "", retryable, fmt.Errorf("llm: provider error: %s", sanitize(msg))
	}
	c.record(cr.Usage.PromptTokens, cr.Usage.CompletionTokens, now, "")
	if len(cr.Choices) == 0 {
		return "", true, fmt.Errorf("llm: empty response")
	}
	ans := extractAnswer(cr.Choices[0].Message.Content, cr.Choices[0].Message.ReasoningContent)
	if strings.TrimSpace(ans) == "" {
		// A 200 with empty content AND empty reasoning is a transient degenerate
		// response (seen under concurrent load). Retry it — another attempt
		// almost always returns real content — rather than handing the caller
		// an empty string it can't use.
		return "", true, fmt.Errorf("llm: empty content")
	}
	return ans, false, nil
}

// attemptTimeout gives the deep reasoning model a longer per-request budget
// (it emits a hidden reasoning trace and can take ~30s); everything else fails
// over quickly.
func (c *httpClient) attemptTimeout(model string) time.Duration {
	if model == c.deepModel && c.deepModel != c.model {
		return deepTimeout
	}
	return defaultTimeout
}

var thinkTag = regexp.MustCompile(`(?s)<think>.*?</think>`)

// extractAnswer returns the model's final answer. Reasoning models place their
// scratchpad in reasoning_content and the answer in content; when content is
// empty (the model spent its budget thinking) we fall back to the reasoning so
// the caller never gets an empty string. Any inline <think>…</think> block is
// stripped from content.
func extractAnswer(content, reasoning string) string {
	content = strings.TrimSpace(thinkTag.ReplaceAllString(content, ""))
	if content != "" {
		return content
	}
	// content empty → use the reasoning trace as a best-effort answer.
	return strings.TrimSpace(thinkTag.ReplaceAllString(reasoning, ""))
}

// Prompt/output budgets. ~24k input chars ≈ 6k tokens; a filing gets most of
// it, a watchlist digest a fraction. Output is capped generously so the deep
// tier can produce a full report while the fast tier stays tight per its own
// requested maxTokens. Retry/timeout policy:
const (
	maxPromptChars  = 24000
	maxOutputTokens = 2000
	maxAttempts     = 3
	// defaultTimeout: 45s produced a steady trickle of "context deadline
	// exceeded" from the free NVIDIA endpoint on analyst-sized prompts
	// (measured 3 worker errors/24h on the hourly analyst = ~12% of runs, all
	// three attempts timing out under provider load). 75s clears the observed
	// slow tail while still failing over well inside the hourly cadence.
	defaultTimeout = 75 * time.Second
	deepTimeout    = 100 * time.Second
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

// callerFingerprint identifies which agent made an LLM call, for spend
// attribution, using the charter it passed.
//
// The charter is a per-agent constant, so it is already a unique caller id and
// costs nothing to derive one from. The alternative — threading an explicit
// caller name through Complete/CompleteWith and every call site — is a wider
// change whose registry would then need to be kept in sync by hand, and a
// caller that forgot to update it would be attributed to whoever it copied.
//
// The first line is used because every charter in this tree opens by naming the
// role ("You are a markets analyst writing a SHORT, factual company snapshot").
// Truncated because the point is to tell callers APART in a log, not to
// reproduce the prompt.
func callerFingerprint(sys string) string {
	const max = 60
	s := strings.TrimSpace(sys)
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "(no charter)"
	}
	if len(s) > max {
		return s[:max]
	}
	return s
}
