// LAYER 5 tests — audit and anomaly detection.
package mcp

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEveryCallIsAudited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_audit.jsonl")
	s := newTestServer(t, Options{ReachablePrivately: true, AuditPath: path},
		stubSource{verdicts: sampleVerdicts()})
	if _, rerr := call(t, s, fullClient(), "get_regime_verdict",
		map[string]any{"symbol": "AAPL"}); rerr != nil {
		t.Fatalf("%v", rerr)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("no audit file: %v", err)
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	var got auditEntry
	for sc.Scan() {
		if err := json.Unmarshal(sc.Bytes(), &got); err != nil {
			t.Fatalf("audit line is not JSON: %v", err)
		}
	}
	if got.Client != "test-client" || got.Tool != "get_regime_verdict" || got.Outcome != "ok" {
		t.Fatalf("audit entry is wrong: %+v", got)
	}
	if got.ParamHash == "" || got.Bytes == 0 {
		t.Fatalf("audit entry lacks a parameter hash or a response size: %+v", got)
	}
}

func TestAuditRecordsTheParameterHashNotTheParameter(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{verdicts: sampleVerdicts()})
	desc := "a study of rolling windows over current constituents, uniquely-worded-marker-42"
	if _, rerr := call(t, s, fullClient(), "critique_research_design",
		map[string]any{"description": desc}); rerr != nil {
		t.Fatalf("%v", rerr)
	}
	blob, _ := json.Marshal(s.audit.entries())
	if strings.Contains(string(blob), "uniquely-worded-marker-42") {
		t.Fatal("the audit sink stored the caller's own text")
	}
}

func TestSymbolEnumerationIsDetectedAndThrottled(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true, DailyCallCap: 1000},
		stubSource{verdicts: sampleVerdicts()})
	cl := fullClient()
	// Deliberately NON-monotonic so this test measures enumeration breadth and
	// not the sweep detector.
	syms := []string{"ZM", "AAPL", "TSLA", "BA", "NVDA", "MSFT", "KO", "IBM", "GE", "F",
		"WMT", "T", "XOM", "CVX", "PG", "JNJ", "V", "MA", "PFE", "DIS", "NKE", "SBUX"}
	for _, sym := range syms {
		call(t, s, cl, "get_regime_verdict", map[string]any{"symbol": sym})
	}
	if _, _, throttled := s.budget.state(cl.ID); !throttled {
		t.Fatal("walking 22 symbols did not trip the enumeration detector")
	}
	if !hasAnomaly(s, "symbol-enumeration") {
		t.Fatal("no symbol-enumeration flag in the audit record")
	}
}

func TestAMonotonicSweepIsDetected(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true, DailyCallCap: 1000},
		stubSource{verdicts: sampleVerdicts()})
	cl := fullClient()
	for _, sym := range []string{"AAA", "AAB", "AAC", "AAD", "AAE", "AAF"} {
		call(t, s, cl, "get_regime_verdict", map[string]any{"symbol": sym})
	}
	if !hasAnomaly(s, "monotonic-sweep") {
		t.Fatal("an alphabetical sweep was not detected")
	}
	if _, _, throttled := s.budget.state(cl.ID); !throttled {
		t.Fatal("a detected sweep did not throttle the client")
	}
}

func TestOrdinaryUseIsNotFlagged(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true, DailyCallCap: 1000},
		stubSource{verdicts: sampleVerdicts()})
	cl := fullClient()
	for _, sym := range []string{"AAPL", "MSFT", "AAPL", "NVDA", "AAPL"} {
		call(t, s, cl, "get_regime_verdict", map[string]any{"symbol": sym})
	}
	call(t, s, cl, "get_track_record", map[string]any{})
	call(t, s, cl, "explain_methodology", map[string]any{"topic": "matched_nulls"})
	if _, _, throttled := s.budget.state(cl.ID); throttled {
		t.Fatal("a normal advisory session was throttled — the thresholds are too tight")
	}
}

func TestThrottleCutsTheBudgetRatherThanBanning(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true, DailyCallCap: 100},
		stubSource{verdicts: sampleVerdicts()})
	cl := fullClient()
	s.budget.throttle(cl.ID, "test-flag")
	// 25% of 100 = 25 calls remain, then it closes.
	served := 0
	for i := 0; i < 40; i++ {
		if _, rerr := call(t, s, cl, "explain_methodology",
			map[string]any{"topic": "walk_forward"}); rerr == nil {
			served++
		}
	}
	if served == 0 {
		t.Fatal("a throttled client was banned outright rather than slowed")
	}
	if served > 26 {
		t.Fatalf("a throttled client served %d calls against a 25-call reduced cap", served)
	}
}

// A throttle is time-based, not day-based: crossing midnight resets the daily
// counters but must not clear the flag, or a slow extractor gets a fresh
// reputation every night. It clears only after throttleCooldown of quiet.
func TestAThrottleSurvivesTheDayBoundaryAndClearsOnlyOnCooldown(t *testing.T) {
	base := time.Date(2026, 7, 27, 23, 0, 0, 0, time.UTC)
	clock := base
	s := newTestServer(t, Options{ReachablePrivately: true, DailyCallCap: 10}, stubSource{})
	s.SetClock(func() time.Time { return clock })

	s.budget.throttle("slow-extractor", "symbol-enumeration")
	clock = base.Add(2 * time.Hour) // now the next UTC day, inside the cooldown
	s.budget.admit("slow-extractor")
	if _, _, throttled := s.budget.state("slow-extractor"); !throttled {
		t.Fatal("midnight laundered an extraction flag")
	}
	calls, _, _ := s.budget.state("slow-extractor")
	if calls != 1 {
		t.Fatalf("daily counters did not reset across the day boundary: %d calls", calls)
	}

	clock = base.Add(throttleCooldown + time.Hour)
	s.budget.admit("slow-extractor")
	if _, _, throttled := s.budget.state("slow-extractor"); throttled {
		t.Fatal("the throttle never expires — it is a ban, not a throttle")
	}
}

func hasAnomaly(s *Server, flag string) bool {
	for _, e := range s.audit.entries() {
		if e.Anomaly == flag {
			return true
		}
	}
	return false
}
