package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// stubSource stands in for the store. It carries exactly what a real store
// would hand the adapter, so a test exercising the layers is exercising the
// same values production does.
type stubSource struct {
	verdicts []Verdict
	health   map[string]string
	prereg   PreregSummary
	err      error
	// gradeableOn is the DERIVED first-gradable date. Empty means "nothing
	// outstanding", which is the honest default for a stub: a test that has not
	// said when its forecasts mature must not have a date invented for it.
	gradeableOn string
	// verdictOn is the DERIVED first date a VERDICT can exist (block gate plus
	// resolution). Empty means "no benchmark-eligible row yet".
	verdictOn string
}

func (s stubSource) Verdicts(context.Context) ([]Verdict, error) {
	return s.verdicts, s.err
}
func (s stubSource) ModelHealth(_ context.Context, model string) (string, error) {
	return s.health[model], nil
}
func (s stubSource) Preregistration(context.Context) (PreregSummary, error) {
	return s.prereg, s.err
}
func (s stubSource) EarliestGradeableOn(context.Context) (string, bool, error) {
	return s.gradeableOn, s.gradeableOn != "", s.err
}
func (s stubSource) EarliestVerdictOn(context.Context) (string, bool, error) {
	return s.verdictOn, s.verdictOn != "", s.err
}

func sampleVerdicts() []Verdict {
	return []Verdict{
		{
			Symbol: "AAPL", Kind: "trend21", Regime: "uptrend", Conviction: 0.93,
			HistoricalAccuracy: 0.972, HorizonDays: 21, N: 54969, AsOfDay: "2026-07-27",
			Tradeability:    structregime.TradeabilityFor("trend21", 0.93),
			EvidenceCaveat:  structregime.EvidenceCaveatText(),
			FirstGradableOn: structregime.FirstGradableOnDate(),
		},
		{
			Symbol: "AAPL", Kind: "vol21", Regime: "calm", Conviction: 0.42,
			HistoricalAccuracy: 0.558, HorizonDays: 21, N: 12000, AsOfDay: "2026-07-27",
			EvidenceCaveat:  structregime.EvidenceCaveatText(),
			FirstGradableOn: structregime.FirstGradableOnDate(),
		},
	}
}

// alwaysAllow is a limiter stand-in for tests that are not about rate limiting.
func alwaysAllow(string, bool) bool { return true }

// realLimiter is the same token-bucket algorithm the daemon injects
// (internal/api's rateLimiter.allow), so a latency measurement taken through it
// exercises the real admission path — the mutex, the per-client map, the refill
// arithmetic. The refill rate is set high enough that the measurement is not
// dominated by 429s: what is being measured is the cost of limiting, not the
// cost of being limited.
func realLimiter(rps, burst float64) func(string, bool) bool {
	type bucket struct {
		tokens float64
		last   time.Time
	}
	var mu sync.Mutex
	buckets := map[string]*bucket{}
	return func(key string, write bool) bool {
		tier := "r:"
		if write {
			tier = "w:"
		}
		now := time.Now()
		mu.Lock()
		defer mu.Unlock()
		b := buckets[tier+key]
		if b == nil {
			b = &bucket{tokens: burst, last: now}
			buckets[tier+key] = b
		}
		b.tokens += now.Sub(b.last).Seconds() * rps
		if b.tokens > burst {
			b.tokens = burst
		}
		b.last = now
		if b.tokens < 1 {
			return false
		}
		b.tokens--
		return true
	}
}

// fakeTokenEqual mirrors internal/api's constant-time compare closely enough
// for the layers under test; the production wiring injects the real one.
func fakeTokenEqual(got, want string) bool {
	return got != "" && want != "" && got == want
}

func newTestServer(t *testing.T, opts Options, src Source) *Server {
	t.Helper()
	if opts.Secret == "" {
		opts.Secret = "0123456789abcdef0123456789abcdef0123456789abcdef"
	}
	opts.Enabled = true
	s, err := New(opts, src, alwaysAllow, fakeTokenEqual)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func fullClient() *Client {
	return &Client{ID: "test-client", Scopes: []string{ScopeMethodology, ScopeVerdicts, ScopeRecord}}
}

// call invokes one tool through the FULL request path — scopes, budget,
// parameter validation, allowlist and watermark — and returns the structured
// result, or the JSON-RPC error.
func call(t *testing.T, s *Server, cl *Client, tool string, args map[string]any) (map[string]any, *rpcError) {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": json.RawMessage(argsJSON)},
	}
	raw, _ := json.Marshal(req)
	resp, due := s.Handle(context.Background(), cl, raw)
	if !due {
		t.Fatalf("no response due for a tools/call")
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %T", resp.Result)
	}
	sc, ok := m["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structuredContent in result")
	}
	return sc, nil
}
