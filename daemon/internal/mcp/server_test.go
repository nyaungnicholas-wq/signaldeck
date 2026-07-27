// LAYER 6 and whole-server tests, plus the honesty invariants the superprompt
// treats as non-negotiable.
package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ── layer 6: provenance ─────────────────────────────────────────────────────

func TestWatermarkIsPerClientDeterministicAndNeverTouchesANumber(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{verdicts: sampleVerdicts()})
	a := &Client{ID: "partner-a", Scopes: allScopes}
	b := &Client{ID: "partner-b", Scopes: allScopes}

	outA1, _ := call(t, s, a, "get_regime_verdict", map[string]any{"symbol": "AAPL"})
	outA2, _ := call(t, s, a, "get_regime_verdict", map[string]any{"symbol": "AAPL"})
	outB, _ := call(t, s, b, "get_regime_verdict", map[string]any{"symbol": "AAPL"})

	tagA1 := outA1["_provenance"].(map[string]any)["clientTag"]
	tagA2 := outA2["_provenance"].(map[string]any)["clientTag"]
	tagB := outB["_provenance"].(map[string]any)["clientTag"]
	if tagA1 != tagA2 {
		t.Fatal("the mark is not deterministic for one client — it would be detectable by self-diffing")
	}
	if tagA1 == tagB {
		t.Fatal("two clients received the same mark — output is not attributable")
	}

	// The numbers must be byte-identical between clients. Compare the verdict
	// arrays with the disclaimer and provenance removed.
	stripA, stripB := outA1["verdicts"], outB["verdicts"]
	ja, _ := json.Marshal(stripA)
	jb, _ := json.Marshal(stripB)
	if string(ja) != string(jb) {
		t.Fatalf("the watermark changed the data:\nA=%s\nB=%s", ja, jb)
	}
}

func TestWatermarkNoticeIsDisclosed(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{})
	out, _ := call(t, s, fullClient(), "explain_methodology", map[string]any{"topic": "survivorship"})
	notice, _ := out["_provenance"].(map[string]any)["notice"].(string)
	if !strings.Contains(notice, "watermarked") {
		t.Fatal("the watermark is not disclosed — deterrence is most of its value")
	}
}

// ── honesty invariants ──────────────────────────────────────────────────────

// The live directional record must be retrievable and must never be hidden —
// including on a daemon whose grading worker has written nothing at all.
func TestTheNegativeLiveRecordCannotBeHidden(t *testing.T) {
	for _, src := range []Source{
		stubSource{}, // empty store
		stubSource{health: map[string]string{"directional-ensemble-1d": ""}},
		stubSource{health: map[string]string{"directional-ensemble-1d": "not json"}},
	} {
		s := newTestServer(t, Options{ReachablePrivately: true}, src)
		out, rerr := call(t, s, fullClient(), "get_track_record", map[string]any{})
		if rerr != nil {
			t.Fatalf("%v", rerr)
		}
		dir, _ := out["directional"].([]any)
		if len(dir) == 0 {
			t.Fatal("get_track_record returned no directional record — silence reads as no bad news")
		}
		row := dir[0].(map[string]any)
		if acc, _ := row["liveAccuracy"].(float64); acc > 0.5 {
			t.Fatalf("the directional accuracy reported (%v) is not the failing record", acc)
		}
		if em, ok := row["emitting"].(bool); !ok || em {
			t.Fatal("the retired model is not reported as switched off")
		}
		blob, _ := json.Marshal(out)
		if !strings.Contains(string(blob), "retired") {
			t.Fatal("the retirement is not stated")
		}
	}
}

// The aggregate must never be quoted as skill: every response that carries the
// structural claim must carry the discrimination framing with it.
func TestTheBaseRateIsNeverQuotedAsSkill(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{verdicts: sampleVerdicts()})
	for _, tool := range []string{"get_track_record", "get_regime_verdict"} {
		args := map[string]any{}
		if tool == "get_regime_verdict" {
			args["symbol"] = "AAPL"
		}
		out, rerr := call(t, s, fullClient(), tool, args)
		if rerr != nil {
			t.Fatalf("%s: %v", tool, rerr)
		}
		guard, _ := out["headlineGuard"].(string)
		for _, need := range []string{"persistence", "24.7pp", "72.9", "97.6", "54,969"} {
			if !strings.Contains(guard, need) {
				t.Errorf("%s: the headline guard is missing %q", tool, need)
			}
		}
	}
	// And the discrimination block reports the spread, not the average.
	out, _ := call(t, s, fullClient(), "get_track_record", map[string]any{})
	disc := out["discrimination"].(map[string]any)
	if disc["spreadPP"] != 24.7 {
		t.Fatalf("spread = %v", disc["spreadPP"])
	}
	// The aggregate may appear ONLY inside the guard that tells a reader not to
	// quote it. Anywhere else it is the bug this test exists to catch.
	delete(out, "headlineGuard")
	blob, _ := json.Marshal(out)
	for _, forbidden := range []string{"0.83", "83%", "0.830"} {
		if strings.Contains(string(blob), forbidden) {
			t.Fatalf("the aggregate %q is quoted outside the headline guard: %s", forbidden, blob)
		}
	}
}

func TestRejectionsCarryEqualWeight(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{})
	out, rerr := call(t, s, fullClient(), "list_validated_findings", map[string]any{})
	if rerr != nil {
		t.Fatalf("%v", rerr)
	}
	killed := out["killed"].([]any)
	if len(killed) < 4 {
		t.Fatalf("only %d rejections are published", len(killed))
	}
	blob, _ := json.Marshal(killed)
	for _, need := range []string{"52-week-high", "gap fill", "directional ensemble"} {
		if !strings.Contains(string(blob), need) {
			t.Errorf("the killed list omits %q", need)
		}
	}
}

// ── protocol plumbing ───────────────────────────────────────────────────────

func TestInitializeAdvertisesTheAdvisoryShape(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{})
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	resp, due := s.Handle(context.Background(), fullClient(), raw)
	if !due || resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp)
	}
	m := resp.Result.(map[string]any)
	if m["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocol version = %v", m["protocolVersion"])
	}
	instr := m["instructions"].(string)
	for _, need := range []string{"does not serve market data", "never returns a trade"} {
		if !strings.Contains(instr, need) {
			t.Errorf("instructions omit %q", need)
		}
	}
}

func TestNotificationsGetNoReplyAndAToolCallWithoutAnIDIsRefused(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{})
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if _, due := s.Handle(context.Background(), fullClient(), raw); due {
		t.Fatal("a notification produced a reply")
	}
	// An unattributable, unbillable tool call must not run.
	raw, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "tools/call",
		"params": map[string]any{"name": "get_track_record"}})
	if _, due := s.Handle(context.Background(), fullClient(), raw); due {
		t.Fatal("an id-less tools/call was answered")
	}
	if calls, _, _ := s.budget.state("test-client"); calls != 0 {
		t.Fatal("an id-less tools/call consumed budget — it ran")
	}
}

func TestUnsupportedMethodsAreRefused(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{})
	for _, m := range []string{"resources/list", "prompts/list", "completion/complete",
		"sampling/createMessage", "logging/setLevel", "roots/list"} {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": m})
		resp, _ := s.Handle(context.Background(), fullClient(), raw)
		if resp.Error == nil {
			t.Errorf("method %q was served", m)
		}
	}
}

func TestAnErroringSourceFailsClosedRatherThanReturningNothingSilently(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true},
		stubSource{err: errBoom{}})
	_, rerr := call(t, s, fullClient(), "get_preregistration", map[string]any{})
	if rerr == nil {
		t.Fatal("a failing source produced a successful-looking empty response")
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "source unavailable" }
