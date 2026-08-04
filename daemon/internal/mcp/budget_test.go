// LAYER 4 tests — budget and latency.
package mcp

import (
	"context"
	"sort"
	"testing"
	"time"
)

func TestDailyCallBudgetIsEnforcedAndFailsClosed(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true, DailyCallCap: 5},
		stubSource{verdicts: sampleVerdicts()})
	cl := fullClient()
	for i := 0; i < 5; i++ {
		if _, rerr := call(t, s, cl, "explain_methodology",
			map[string]any{"topic": "survivorship"}); rerr != nil {
			t.Fatalf("call %d refused early: %v", i, rerr)
		}
	}
	_, rerr := call(t, s, cl, "explain_methodology", map[string]any{"topic": "survivorship"})
	if rerr == nil {
		t.Fatal("the daily call budget was not enforced")
	}
	if rerr.Code != codeBudgetExceeded {
		t.Fatalf("wrong code %d", rerr.Code)
	}
	d, _ := rerr.Data.(map[string]any)
	if ra, _ := d["retryAfterSeconds"].(int); ra <= 0 {
		t.Fatal("no usable retry-after was returned")
	}
	// A different client is unaffected: the budget is per identity.
	other := &Client{ID: "other", Scopes: allScopes}
	if _, rerr := call(t, s, other, "explain_methodology",
		map[string]any{"topic": "survivorship"}); rerr != nil {
		t.Fatalf("a second client inherited the first's exhaustion: %v", rerr)
	}
}

func TestRateLimitRefusalIsDistinctAndCarriesRetryAfter(t *testing.T) {
	denyAll := func(string, bool) bool { return false }
	s, err := New(Options{Enabled: true, ReachablePrivately: true, Secret: testSecret},
		stubSource{}, denyAll, fakeTokenEqual)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, rerr := call(t, s, fullClient(), "explain_methodology", map[string]any{"topic": "walk_forward"})
	if rerr == nil || rerr.Code != codeRateLimited {
		t.Fatalf("expected a rate-limit refusal, got %v", rerr)
	}
}

func TestNewRefusesToRunWithoutALimiter(t *testing.T) {
	if _, err := New(Options{Enabled: true}, stubSource{}, nil, fakeTokenEqual); err == nil {
		t.Fatal("a server was constructed with no rate limiter")
	}
	if _, err := New(Options{Enabled: true}, stubSource{}, alwaysAllow, nil); err == nil {
		t.Fatal("a server was constructed with no constant-time compare")
	}
}

func TestResultCapBoundsAListAndSaysSo(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true, ResultCap: 2},
		stubSource{verdicts: sampleVerdicts()})
	out, rerr := call(t, s, fullClient(), "list_validated_findings", map[string]any{})
	if rerr != nil {
		t.Fatalf("%v", rerr)
	}
	surv := out["survived"].([]any)
	if len(surv) != 2 {
		t.Fatalf("result cap not applied: %d items", len(surv))
	}
	if _, ok := out["truncated"].(string); !ok {
		t.Fatal("a truncated list was returned without saying it was truncated")
	}
}

// TestLimiterOverheadIsSubMillisecond — the layer must not cost latency. The
// budget path is measured directly, not through JSON, because it is the part
// that runs on every request regardless of the tool.
func TestLimiterOverheadIsSubMillisecond(t *testing.T) {
	b := newBudgetKeeper(Options{DailyCallCap: 1 << 30}, alwaysAllow,
		newAuditor("", time.Now), time.Now)
	const n = 200000
	start := time.Now()
	for i := 0; i < n; i++ {
		b.admit("bench-client")
	}
	per := time.Since(start) / n
	if per > time.Microsecond {
		t.Fatalf("admission costs %v per call, over the 1µs budget", per)
	}
	t.Logf("admission overhead: %v/call over %d calls", per, n)
}

// TestEndToEndP95UnderRateLimiting measures what the requirement actually
// names: p95 of a full tool call, with the limiter and the budget engaged.
func TestEndToEndP95UnderRateLimiting(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true,
		DailyCallCap: 1 << 20, DailyByteCap: 1 << 30},
		stubSource{verdicts: sampleVerdicts()})
	const n = 2000
	lat := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		// A distinct client per call. Two thousand calls a second from ONE
		// identity is an extraction pattern and the server correctly throttles
		// it (TestSymbolEnumerationIsDetectedAndThrottled covers that); what is
		// being measured here is service latency under a fully engaged limiter,
		// which is what a fleet of legitimate clients looks like.
		cl := &Client{ID: "load-" + itoaTest(int64(i)), Scopes: allScopes}
		start := time.Now()
		if _, rerr := call(t, s, cl, "get_regime_verdict", map[string]any{"symbol": "AAPL"}); rerr != nil {
			t.Fatalf("call %d refused: %v", i, rerr)
		}
		lat = append(lat, time.Since(start))
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p50, p95, p99 := lat[n/2], lat[n*95/100], lat[n*99/100]
	t.Logf("get_regime_verdict latency over %d calls: p50=%v p95=%v p99=%v", n, p50, p95, p99)
	if p95 > 200*time.Millisecond {
		t.Fatalf("p95 %v exceeds the 200ms requirement", p95)
	}
}

func TestVerdictsAreServedFromACacheNotPerRequest(t *testing.T) {
	counting := &countingSource{inner: stubSource{verdicts: sampleVerdicts()}}
	s := newTestServer(t, Options{ReachablePrivately: true, DailyCallCap: 1000}, counting)
	for i := 0; i < 50; i++ {
		if _, rerr := call(t, s, fullClient(), "get_regime_verdict",
			map[string]any{"symbol": "AAPL"}); rerr != nil {
			t.Fatalf("%v", rerr)
		}
	}
	if counting.reads != 1 {
		t.Fatalf("the source was read %d times for 50 calls — the cache is not doing its job",
			counting.reads)
	}
}

type countingSource struct {
	inner stubSource
	reads int
}

func (c *countingSource) Verdicts(ctx context.Context) ([]Verdict, error) {
	c.reads++
	return c.inner.Verdicts(ctx)
}
func (c *countingSource) ModelHealth(ctx context.Context, m string) (string, error) {
	return c.inner.ModelHealth(ctx, m)
}
func (c *countingSource) Preregistration(ctx context.Context) (PreregSummary, error) {
	return c.inner.Preregistration(ctx)
}
func (c *countingSource) EarliestGradeableOn(ctx context.Context) (string, bool, error) {
	return c.inner.EarliestGradeableOn(ctx)
}
func (c *countingSource) EarliestVerdictOn(ctx context.Context) (string, bool, error) {
	return c.inner.EarliestVerdictOn(ctx)
}
