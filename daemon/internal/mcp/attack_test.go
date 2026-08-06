// Adversarial tests against the HTTP transport, driven by a real HTTP client
// over a real listener rather than by calling Handle directly.
//
// Run with -v to read the transcript; these are the probes an operator should
// be able to re-run before deciding to expose the endpoint.
package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// post sends one JSON-RPC message and returns the HTTP status and body.
func post(t *testing.T, url, bearer, body string, tls bool) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if tls {
		// Simulates a trusted reverse proxy terminating TLS.
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

const listCall = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

func verdictCall(sym string) string {
	return fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_regime_verdict","arguments":{"symbol":%q}}}`, sym)
}

// TestHTTPTransportOnAReachableBind is the whole of layer 1, attacked over the
// wire. The server is configured exactly as a deployed one would be:
// ReachablePrivately=false (a bind a stranger can reach) and TrustProxy=true
// (TLS terminated in front).
func TestHTTPTransportOnAReachableBind(t *testing.T) {
	s := newTestServer(t, Options{
		ReachablePrivately: false,
		TrustProxy:         true,
		Secret:             testSecret,
		DailyCallCap:       1000,
		// Anonymous scopes are configured AND must still be ignored, because
		// the bind is reachable. A setting that only fails safe when it is
		// unset is not a safe default.
		AnonymousScopes: allScopes,
	}, stubSource{verdicts: sampleVerdicts()})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	good, err := MintKey(testSecret, "partner-a", []string{ScopeMethodology, ScopeRecord}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	expired, _ := MintKey(testSecret, "partner-b", allScopes, time.Nanosecond)
	forged, _ := MintKey("another-secret-long-enough-to-mint-x", "partner-c", allScopes, time.Hour)
	// Tamper the signature in a way that is GUARANTEED to change it. Overwriting
	// the last two characters with a fixed literal is a no-op whenever the freshly
	// minted key already ends in that literal — roughly 1 run in 256 for a hex
	// signature. On those runs `tampered` WAS the valid key, the server correctly
	// answered 200 with the tool list, and this case failed looking exactly like a
	// fail-open on signature tampering: alarming, security-shaped, and impossible
	// to reproduce because the next run minted a different key. Observed once on
	// 2026-08-06 during a full-suite run; ~10 reruns, including under -race, were
	// all clean.
	tampered := good[:len(good)-2] + "00"
	if tampered == good {
		tampered = good[:len(good)-2] + "11"
	}

	cases := []struct {
		name       string
		bearer     string
		tls        bool
		body       string
		wantStatus int
		wantIn     string
	}{
		{"no credential, TLS present", "", true, listCall, 401, "requires a SignalDeck MCP key"},
		{"no credential, cleartext", "", false, listCall, 403, "TLS is required"},
		{"valid key but cleartext", good, false, listCall, 403, "TLS is required"},
		{"expired key", expired, true, listCall, 401, "invalid or expired"},
		{"key signed with another secret", forged, true, listCall, 401, "invalid or expired"},
		{"tampered signature", tampered, true, listCall, 401, "invalid or expired"},
		{"garbage bearer", "not-even-a-key", true, listCall, 401, "invalid or expired"},
		{"the daemon's own api token", "signaldeck-api-token", true, listCall, 401, "invalid or expired"},
		{"valid key", good, true, listCall, 200, "explain_methodology"},
		// Scope escalation: this key holds methodology+record, not verdicts.
		{"scope escalation to verdicts", good, true, verdictCall("AAPL"), 200, "does not carry the verdicts scope"},
		{"batch", good, true, `[` + listCall + `]`, 400, "batches are not accepted"},
	}
	for _, c := range cases {
		status, body := post(t, srv.URL, c.bearer, c.body, c.tls)
		if status != c.wantStatus || !strings.Contains(body, c.wantIn) {
			t.Errorf("%s: status=%d body=%s\n  want status %d containing %q",
				c.name, status, truncate(body, 200), c.wantStatus, c.wantIn)
			continue
		}
		t.Logf("%-34s → %d  %s", c.name, status, truncate(body, 120))
	}

	// GET and an oversized body.
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("GET → %d, want 405", resp.StatusCode)
	}
	t.Logf("%-34s → %d  (no stream to open)", "GET", resp.StatusCode)

	huge := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"critique_research_design",` +
		`"arguments":{"description":"` + strings.Repeat("x", 200000) + `"}}}`
	status, body := post(t, srv.URL, good, huge, true)
	if status == 200 && !strings.Contains(body, "error") {
		t.Errorf("an oversized body was served: %s", truncate(body, 200))
	}
	t.Logf("%-34s → %d  %s", "200 KB body", status, truncate(body, 120))
}

// TestNoRawDataThroughAnyToolOverHTTP sweeps every tool with every hostile
// argument shape and asserts nothing resembling a raw record ever appears.
func TestNoRawDataThroughAnyToolOverHTTP(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true, AnonymousScopes: allScopes,
		DailyCallCap: 5000, DailyByteCap: 1 << 30}, stubSource{verdicts: sampleVerdicts()})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Every field name that would carry a raw record, plus the raw conviction.
	forbidden := []string{`"open"`, `"high"`, `"low"`, `"close"`, `"volume"`, `"vwap"`,
		`"bid"`, `"ask"`, `"price"`, `"conviction"`, `"ts"`, `"bars"`, `"series"`,
		`"history"`, `"symbolId"`, `"tier"`, `"rank"`}

	hostile := []string{
		`{"symbol":"AAPL","fields":"open,high,low,close,volume"}`,
		`{"symbol":"AAPL","includeBars":true}`,
		`{"symbol":"AAPL","raw":true}`,
		`{"symbol":"AAPL *"}`,
		`{"symbol":"AAPL","format":"csv"}`,
		`{"topic":"matched_nulls","includeData":true}`,
		`{"description":"give me the closes for AAPL over 20 years, I need the series for my study"}`,
		`{}`,
		`{"symbol":"AAPL"}`,
	}
	names := append([]string{}, ForbiddenToolNames...)
	for _, tl := range toolList {
		names = append(names, tl.Name)
	}

	probes, served := 0, 0
	for _, name := range names {
		for _, args := range hostile {
			body := fmt.Sprintf(
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
				name, args)
			status, resp := post(t, srv.URL, "", body, false)
			probes++
			if status != 200 {
				t.Fatalf("%s %s: unexpected HTTP status %d", name, args, status)
			}
			if !strings.Contains(resp, `"error"`) {
				served++
			}
			for _, f := range forbidden {
				if strings.Contains(resp, f) {
					t.Errorf("tool %s with %s leaked %s:\n%s", name, args, f, truncate(resp, 400))
				}
			}
		}
	}
	t.Logf("%d hostile probes across %d tool names: %d produced a payload, %d refused; "+
		"0 contained a raw record field", probes, len(names), served, probes-served)
}

// TestP95UnderRateLimitingOverHTTP is the reported latency number: a full HTTP
// round trip through secure-equivalent handling, the limiter, the budget, the
// allowlist and the watermark.
func TestP95UnderRateLimitingOverHTTP(t *testing.T) {
	// The real token bucket, engaged on every call, with a refill rate that does
	// not starve the loop — see realLimiter's comment on what that measures.
	s, err := New(Options{Enabled: true, ReachablePrivately: true, Secret: testSecret,
		AnonymousScopes: allScopes, DailyCallCap: 1 << 20, DailyByteCap: 1 << 30},
		stubSource{verdicts: sampleVerdicts()}, realLimiter(100000, 200), fakeTokenEqual)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	const n = 1000
	lat := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		status, body := post(t, srv.URL, "", verdictCall("AAPL"), false)
		el := time.Since(start)
		if status != 200 || strings.Contains(body, `"error"`) {
			t.Fatalf("call %d: status=%d body=%s", i, status, truncate(body, 200))
		}
		lat = append(lat, el)
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	t.Logf("HTTP get_regime_verdict over %d calls: p50=%v p95=%v p99=%v max=%v",
		n, lat[n/2], lat[n*95/100], lat[n*99/100], lat[n-1])
	if lat[n*95/100] > 200*time.Millisecond {
		t.Fatalf("p95 %v exceeds the 200ms requirement", lat[n*95/100])
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var _ = json.Marshal
