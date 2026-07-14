package debate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// fakeTiered is a recording llm.Client that also implements llm.Tiered, so the
// judge path exercises CompleteWith(DeepModel). It returns a per-charter reply
// so the test can tell bull/bear/judge apart.
type fakeTiered struct {
	enabled    bool
	calls      int
	usedModels []string
}

func (f *fakeTiered) Enabled() bool     { return f.enabled }
func (f *fakeTiered) Model() string     { return "fast-model" }
func (f *fakeTiered) DeepModel() string { return "deep-model" }
func (f *fakeTiered) FastModel() string { return "fast-model" }
func (f *fakeTiered) KeyCount() int     { return 1 }
func (f *fakeTiered) Stats() llm.Stats  { return llm.Stats{} }

func (f *fakeTiered) reply(sys string) string {
	switch {
	case strings.HasPrefix(sys, "You are the BULL"):
		return "Bull: the 1d score is positive."
	case strings.HasPrefix(sys, "You are the BEAR"):
		return "Bear: the forecast shows no edge."
	default: // judge
		return "VERDICT: NEUTRAL / NO EDGE\nCONFIDENCE: low\n" +
			"CRUXES: forecast lift <= 0; thin expectancy n\n" +
			"RATIONALE: The measured fields do not agree strongly enough to lean."
	}
}

func (f *fakeTiered) Complete(ctx context.Context, sys string, msgs []llm.Message, maxTokens int) (string, error) {
	f.calls++
	f.usedModels = append(f.usedModels, "default")
	return f.reply(sys), nil
}

func (f *fakeTiered) CompleteWith(ctx context.Context, model, sys string, msgs []llm.Message, maxTokens int) (string, error) {
	f.calls++
	f.usedModels = append(f.usedModels, model)
	return f.reply(sys), nil
}

var _ llm.Tiered = (*fakeTiered)(nil)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedSymbol(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1_700_000_000, Score: 0.42,
		Components: []md.ScoreComponent{{Name: "trend_sma", Contrib: 0.30, Note: "above 200sma"}},
	}); err != nil {
		t.Fatalf("insert score: %v", err)
	}
}

// TestRunDisabled: a client with no key is a safe no-op — no store access, no
// panic, Disabled true.
func TestRunDisabled(t *testing.T) {
	d, err := Run(context.Background(), &fakeTiered{enabled: false}, nil, "NVDA")
	if err != nil {
		t.Fatalf("disabled Run should not error: %v", err)
	}
	if !d.Disabled {
		t.Fatal("expected Disabled=true")
	}
	if d.Cruxes == nil {
		t.Fatal("Cruxes must be non-nil (JSON [] not null)")
	}
}

// TestRunFull: with a tracked symbol and a tiered client, Run produces bull +
// bear + judged verdict, and the JUDGE uses the deep model while bull/bear use
// the default.
func TestRunFull(t *testing.T) {
	st := openStore(t)
	seedSymbol(t, st)
	f := &fakeTiered{enabled: true}

	d, err := Run(context.Background(), f, st, "nvda") // lower-case → resolves
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.calls != 3 {
		t.Fatalf("expected 3 model calls (bull/bear/judge), got %d", f.calls)
	}
	// All three calls use the default instruct model (the judge no longer uses
	// the deep reasoning model — instruct follows the strict format reliably).
	for _, m := range f.usedModels {
		if m == "deep-model" {
			t.Fatalf("no call should use the deep model now, saw %v", f.usedModels)
		}
	}
	if !strings.HasPrefix(d.Bull, "Bull:") || !strings.HasPrefix(d.Bear, "Bear:") {
		t.Fatalf("bull/bear not wired: bull=%q bear=%q", d.Bull, d.Bear)
	}
	if d.Verdict != "NEUTRAL / NO EDGE" || d.Confidence != "low" {
		t.Fatalf("judge parse failed: verdict=%q conf=%q", d.Verdict, d.Confidence)
	}
	if len(d.Cruxes) != 2 {
		t.Fatalf("expected 2 cruxes, got %d: %v", len(d.Cruxes), d.Cruxes)
	}
	if d.Symbol != "NVDA" || d.Model != "fast-model" || d.JudgeModel != "fast-model" {
		t.Fatalf("metadata wrong: %+v", d)
	}
	if !strings.Contains(d.Digest, "NVDA") || !strings.Contains(d.Digest, "1d score") {
		t.Fatalf("digest not grounded: %q", d.Digest)
	}
}

// TestRunUnknownSymbol: an untracked ticker errors clearly rather than debating
// nothing.
func TestRunUnknownSymbol(t *testing.T) {
	st := openStore(t)
	seedSymbol(t, st)
	if _, err := Run(context.Background(), &fakeTiered{enabled: true}, st, "ZZZZ"); err == nil {
		t.Fatal("unknown symbol must error")
	}
}

func TestParseJudge(t *testing.T) {
	out := "VERDICT: Lean Long\nCONFIDENCE: High\nCRUXES: strong 1d score; positive forecast lift; rvol elevated\nRATIONALE: Three fields agree."
	d := parseJudge(out)
	if d.Verdict != "LEAN LONG" {
		t.Errorf("verdict=%q", d.Verdict)
	}
	if d.Confidence != "high" {
		t.Errorf("conf=%q", d.Confidence)
	}
	if len(d.Cruxes) != 3 {
		t.Errorf("cruxes=%v", d.Cruxes)
	}
	if !strings.Contains(d.Rationale, "agree") {
		t.Errorf("rationale=%q", d.Rationale)
	}
	// Missing fields → safe honest defaults.
	empty := parseJudge("no structure here")
	if empty.Verdict != "NEUTRAL / NO EDGE" || empty.Confidence != "low" {
		t.Errorf("defaults not honest: %+v", empty)
	}
}

func TestNormalizeVerdict(t *testing.T) {
	cases := map[string]string{
		"lean long": "LEAN LONG", "SHORT": "LEAN SHORT", "neutral / no edge": "NEUTRAL / NO EDGE",
		"unclear": "NEUTRAL / NO EDGE",
	}
	for in, want := range cases {
		if got := normalizeVerdict(in); got != want {
			t.Errorf("normalizeVerdict(%q)=%q want %q", in, got, want)
		}
	}
}
