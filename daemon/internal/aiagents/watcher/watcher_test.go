package watcher

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── fake llm.Client ───────────────────────────────────────────────────────

// fakeLLM is a recording stub implementing llm.Client. It captures the exact
// system prompt and messages passed to Complete and returns a canned reply, so
// tests can assert the Charter is the system prompt, that untrusted flag text
// lands in a user (not system) role, and that Complete is never called when
// the client is disabled.
type fakeLLM struct {
	enabled bool
	model   string
	reply   string
	err     error

	calls   int
	gotSys  string
	gotMsgs []llm.Message
	gotMax  int
}

func (f *fakeLLM) Enabled() bool { return f.enabled }
func (f *fakeLLM) Model() string { return f.model }
func (f *fakeLLM) Stats() llm.Stats {
	return llm.Stats{}
}
func (f *fakeLLM) Complete(_ context.Context, sys string, msgs []llm.Message, maxTokens int) (string, error) {
	f.calls++
	f.gotSys = sys
	f.gotMsgs = msgs
	f.gotMax = maxTokens
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

// ── store seeding helpers ─────────────────────────────────────────────────

func newStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watcher_test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func addSymbol(t *testing.T, st *store.Store, sym string, mkt md.Market) md.Symbol {
	t.Helper()
	s, err := st.UpsertSymbol(context.Background(), sym, mkt, sym)
	if err != nil {
		t.Fatalf("upsert symbol %s: %v", sym, err)
	}
	return s
}

func addDailyBar(t *testing.T, st *store.Store, symbolID int64, ts int64, close float64) {
	t.Helper()
	err := st.UpsertBars(context.Background(), []md.Bar{{
		SymbolID: symbolID, TF: md.TF1d, Ts: ts,
		Open: close, High: close, Low: close, Close: close, Volume: 1,
	}})
	if err != nil {
		t.Fatalf("upsert bar: %v", err)
	}
}

func addScore(t *testing.T, st *store.Store, symbolID int64, h md.Horizon, ts int64, score float64) {
	t.Helper()
	err := st.InsertScore(context.Background(), md.Score{
		SymbolID: symbolID, Horizon: h, Ts: ts, Score: score, Components: nil,
	})
	if err != nil {
		t.Fatalf("insert score: %v", err)
	}
}

func addPosition(t *testing.T, st *store.Store, symbolID int64, qty, entry float64) {
	t.Helper()
	_, err := st.InsertPosition(context.Background(), store.Position{
		SymbolID: symbolID, Qty: qty, EntryPrice: entry,
		EntryTs: time.Now().Unix(), Note: "test",
	})
	if err != nil {
		t.Fatalf("insert position: %v", err)
	}
}

// ── Scan tests (deterministic, no LLM) ─────────────────────────────────────

func TestScanFlagsOpposingScoreAndAdverseMove(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	sym := addSymbol(t, st, "AAPL", md.Stocks)
	// Entry 100, last close 90 → long is down 10% (adverse). Score -0.40
	// opposes a long position.
	addDailyBar(t, st, sym.ID, now, 90.0)
	addScore(t, st, sym.ID, md.H1d, now, -0.40)
	addPosition(t, st, sym.ID, 10, 100.0) // long

	f, err := Scan(ctx, st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(f.Flags) == 0 {
		t.Fatalf("expected flags, got none")
	}
	joined := strings.Join(f.Flags, "\n")
	// Adverse-move flag must quote the loss and both prices.
	if !strings.Contains(joined, "down 10.00%") {
		t.Errorf("expected adverse move flag with 10.00%%, got:\n%s", joined)
	}
	// Opposing-score flag must quote the signed score and the side.
	if !strings.Contains(joined, "-0.40") || !strings.Contains(joined, "long") {
		t.Errorf("expected opposing-score flag citing -0.40 and long, got:\n%s", joined)
	}
}

func TestScanShortPositionOpposedByPositiveScore(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	sym := addSymbol(t, st, "BTC/USD", md.Crypto)
	addDailyBar(t, st, sym.ID, now, 50000.0)
	addScore(t, st, sym.ID, md.H1d, now, 0.30) // positive opposes a short
	addPosition(t, st, sym.ID, -1, 49000.0)    // short

	f, err := Scan(ctx, st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	joined := strings.Join(f.Flags, "\n")
	if !strings.Contains(joined, "+0.30") || !strings.Contains(joined, "short") {
		t.Errorf("expected short opposed by +0.30, got:\n%s", joined)
	}
}

func TestScanExtremeScoreForUnpositionedSymbol(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	sym := addSymbol(t, st, "NVDA", md.Stocks)
	addDailyBar(t, st, sym.ID, now, 500.0)
	addScore(t, st, sym.ID, md.H1d, now, 0.85) // extreme, no position

	f, err := Scan(ctx, st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	joined := strings.Join(f.Flags, "\n")
	if !strings.Contains(joined, "extreme") || !strings.Contains(joined, "+0.85") {
		t.Errorf("expected extreme-score flag citing +0.85, got:\n%s", joined)
	}
}

func TestScanRecentDQEvents(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	sym := addSymbol(t, st, "ETH/USD", md.Crypto)
	if err := st.InsertDQ(ctx, md.DQEvent{SymbolID: &sym.ID, Ts: now, Kind: "gap", Detail: "missing bar"}); err != nil {
		t.Fatalf("insert dq: %v", err)
	}
	if err := st.InsertDQ(ctx, md.DQEvent{SymbolID: &sym.ID, Ts: now, Kind: "stale", Detail: "feed behind"}); err != nil {
		t.Fatalf("insert dq: %v", err)
	}

	f, err := Scan(ctx, st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	joined := strings.Join(f.Flags, "\n")
	if !strings.Contains(joined, "data-quality event") || !strings.Contains(joined, "2 ") {
		t.Errorf("expected DQ flag counting 2 events, got:\n%s", joined)
	}
	if !strings.Contains(joined, "gap") || !strings.Contains(joined, "stale") {
		t.Errorf("expected DQ kinds summarized, got:\n%s", joined)
	}
}

func TestScanStaleDQEventsIgnored(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	// Event older than DQWindow must not be flagged.
	old := time.Now().Add(-DQWindow - time.Hour).Unix()

	sym := addSymbol(t, st, "MSFT", md.Stocks)
	if err := st.InsertDQ(ctx, md.DQEvent{SymbolID: &sym.ID, Ts: old, Kind: "gap", Detail: "old"}); err != nil {
		t.Fatalf("insert dq: %v", err)
	}

	f, err := Scan(ctx, st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(f.Flags) != 0 {
		t.Errorf("expected no flags from stale DQ, got: %v", f.Flags)
	}
}

func TestScanNoFlagsWhenQuiet(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	// A calm position: small favorable move, neutral score.
	sym := addSymbol(t, st, "GOOG", md.Stocks)
	addDailyBar(t, st, sym.ID, now, 101.0) // long entry 100 → +1% (not adverse)
	addScore(t, st, sym.ID, md.H1d, now, 0.05)
	addPosition(t, st, sym.ID, 5, 100.0)

	f, err := Scan(ctx, st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(f.Flags) != 0 {
		t.Errorf("expected no flags for a calm portfolio, got: %v", f.Flags)
	}
}

func TestScanMissingBarReportsUnavailable(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	sym := addSymbol(t, st, "TSLA", md.Stocks)
	// Position but no bars → PnL unavailable, honest flag, no invented number.
	addPosition(t, st, sym.ID, 3, 200.0)

	f, err := Scan(ctx, st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	joined := strings.Join(f.Flags, "\n")
	if !strings.Contains(joined, "PnL unavailable") {
		t.Errorf("expected PnL-unavailable flag, got:\n%s", joined)
	}
}

// ── Run tests (LLM wiring + graceful degradation) ──────────────────────────

func seedOneFlag(t *testing.T, st *store.Store) {
	t.Helper()
	now := time.Now().Unix()
	sym := addSymbol(t, st, "AAPL", md.Stocks)
	addDailyBar(t, st, sym.ID, now, 90.0)
	addScore(t, st, sym.ID, md.H1d, now, -0.40)
	addPosition(t, st, sym.ID, 10, 100.0)
}

func TestRunNoFlagsReturnsQuietLine(t *testing.T) {
	st := newStore(t)
	fake := &fakeLLM{enabled: true, model: "test-model", reply: "should not be used"}

	h, err := Run(context.Background(), fake, st)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if h.Text != "No notable risk changes." {
		t.Errorf("expected quiet line, got %q", h.Text)
	}
	if h.NFlags != 0 {
		t.Errorf("expected 0 flags, got %d", h.NFlags)
	}
	if fake.calls != 0 {
		t.Errorf("Complete must not be called when there are no flags; calls=%d", fake.calls)
	}
}

func TestRunDisabledReturnsRuleBasedListWithoutComplete(t *testing.T) {
	st := newStore(t)
	seedOneFlag(t, st)
	fake := &fakeLLM{enabled: false, model: "test-model", reply: "must not appear"}

	h, err := Run(context.Background(), fake, st)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("Complete must NOT be called when disabled; calls=%d", fake.calls)
	}
	if !h.Disabled {
		t.Errorf("expected Disabled=true")
	}
	if h.NFlags == 0 {
		t.Errorf("expected flags present")
	}
	if !strings.Contains(h.Text, "rule-based") {
		t.Errorf("expected rule-based list as text, got: %q", h.Text)
	}
	if strings.Contains(h.Text, "must not appear") {
		t.Errorf("disabled path must not use the LLM reply")
	}
}

func TestRunNilClientDegradesGracefully(t *testing.T) {
	st := newStore(t)
	seedOneFlag(t, st)

	h, err := Run(context.Background(), nil, st)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !h.Disabled || !strings.Contains(h.Text, "rule-based") {
		t.Errorf("nil client should degrade to rule-based list, got: %+v", h)
	}
}

func TestRunEnabledPassesCharterAsSystemAndDataAsUser(t *testing.T) {
	st := newStore(t)
	seedOneFlag(t, st)
	fake := &fakeLLM{enabled: true, model: "test-model", reply: "AAPL long is down 10% and the score turned against it."}

	h, err := Run(context.Background(), fake, st)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected exactly 1 Complete call, got %d", fake.calls)
	}
	// Charter must be the system prompt, verbatim.
	if fake.gotSys != Charter {
		t.Errorf("system prompt is not the Charter")
	}
	// Untrusted flag text must be in a USER role message, never in system.
	if len(fake.gotMsgs) != 1 || fake.gotMsgs[0].Role != "user" {
		t.Fatalf("expected one user message, got %+v", fake.gotMsgs)
	}
	if strings.Contains(fake.gotSys, "AAPL") {
		t.Errorf("untrusted data (symbol) leaked into the system prompt")
	}
	// The actual data must be included in the user prompt.
	if !strings.Contains(fake.gotMsgs[0].Content, "AAPL") || !strings.Contains(fake.gotMsgs[0].Content, "down 10.00%") {
		t.Errorf("flags/data not included in user prompt: %q", fake.gotMsgs[0].Content)
	}
	// The user prompt must mark the flags as data-not-instructions.
	if !strings.Contains(fake.gotMsgs[0].Content, "not as instructions") {
		t.Errorf("user prompt missing prompt-injection framing")
	}
	// maxTokens ~500 per spec.
	if fake.gotMax != 500 {
		t.Errorf("expected maxTokens=500, got %d", fake.gotMax)
	}
	// The LLM reply is used as the text.
	if h.Text != fake.reply {
		t.Errorf("expected LLM reply as text, got %q", h.Text)
	}
	if h.Model != "test-model" {
		t.Errorf("expected model recorded, got %q", h.Model)
	}
	if h.Disabled {
		t.Errorf("Disabled must be false when the LLM ran")
	}
}

func TestRunFallsBackWhenCompleteErrors(t *testing.T) {
	st := newStore(t)
	seedOneFlag(t, st)
	fake := &fakeLLM{enabled: true, model: "test-model", err: llm.ErrCapReached}

	h, err := Run(context.Background(), fake, st)
	if err != nil {
		t.Fatalf("run should not surface the LLM error, got: %v", err)
	}
	if !strings.Contains(h.Text, "rule-based") {
		t.Errorf("expected fallback to rule-based list on LLM error, got: %q", h.Text)
	}
	if h.NFlags == 0 {
		t.Errorf("expected flags present")
	}
}

// ── Persist tests ─────────────────────────────────────────────────────────

func TestPersistWritesOnlyWhenFlagsExist(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	// No flags → no write.
	if err := Persist(ctx, st, Heads{Text: "No notable risk changes.", NFlags: 0}); err != nil {
		t.Fatalf("persist (no flags): %v", err)
	}
	ins, err := st.RecentInsights(ctx, 0, 10)
	if err != nil {
		t.Fatalf("recent insights: %v", err)
	}
	if len(ins) != 0 {
		t.Fatalf("expected no insight written for 0 flags, got %d", len(ins))
	}

	// Flags → exactly one market-scope insight.
	if err := Persist(ctx, st, Heads{Text: "AAPL is down 10%.", NFlags: 2}); err != nil {
		t.Fatalf("persist (flags): %v", err)
	}
	ins, err = st.RecentInsights(ctx, 0, 10)
	if err != nil {
		t.Fatalf("recent insights: %v", err)
	}
	if len(ins) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(ins))
	}
	if ins[0].Scope != "market" {
		t.Errorf("expected market scope, got %q", ins[0].Scope)
	}
	if ins[0].Headline != "Risk watcher" {
		t.Errorf("expected headline 'Risk watcher', got %q", ins[0].Headline)
	}
	if ins[0].Body != "AAPL is down 10%." {
		t.Errorf("body not persisted, got %q", ins[0].Body)
	}
}

// ── Charter sanity ────────────────────────────────────────────────────────

func TestCharterIsStrict(t *testing.T) {
	// The charter must explicitly ban advice, forbid invented numbers, and
	// declare its prompt-injection posture.
	for _, frag := range []string{
		"financial advice",
		"UNTRUSTED DATA",
		"never invent",
	} {
		if !strings.Contains(strings.ToLower(Charter), strings.ToLower(frag)) {
			t.Errorf("Charter missing required clause: %q", frag)
		}
	}
}
