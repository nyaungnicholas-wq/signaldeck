package briefing

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── NY-time gate ────────────────────────────────────────────────────────

func TestShouldRun(t *testing.T) {
	ny := NYLoc()
	at := func(h, m int) time.Time {
		return time.Date(2026, 7, 3, h, m, 0, 0, ny)
	}
	cases := []struct {
		now     time.Time
		lastDay string
		want    bool
		wantKey string
	}{
		{at(6, 59), "", false, "2026-07-03"},           // before 7am: never
		{at(7, 0), "", true, "2026-07-03"},             // 7:00 sharp: fire
		{at(9, 30), "", true, "2026-07-03"},            // later same morning: fire
		{at(9, 30), "2026-07-03", false, "2026-07-03"}, // already ran today
		{at(23, 59), "2026-07-02", true, "2026-07-03"}, // ran yesterday: fire
		{at(6, 0), "2026-07-02", false, "2026-07-03"},  // new day but pre-7am
	}
	for i, c := range cases {
		got, key := ShouldRun(c.now, c.lastDay, ny)
		if got != c.want || key != c.wantKey {
			t.Errorf("case %d: ShouldRun(%v, %q) = %v,%q want %v,%q",
				i, c.now, c.lastDay, got, key, c.want, c.wantKey)
		}
	}
	// UTC clock, NY gate: 10:59 UTC in July is 6:59am EDT → hold.
	utc := time.Date(2026, 7, 3, 10, 59, 0, 0, time.UTC)
	if ok, _ := ShouldRun(utc, "", ny); ok {
		t.Error("10:59 UTC (= 6:59 EDT) should not fire")
	}
	utc = time.Date(2026, 7, 3, 11, 1, 0, 0, time.UTC)
	if ok, _ := ShouldRun(utc, "", ny); !ok {
		t.Error("11:01 UTC (= 7:01 EDT) should fire")
	}
}

func TestVolRegime(t *testing.T) {
	if _, label, ok := VolRegime(nil); ok || label != "unknown" {
		t.Errorf("empty bars: %q,%v", label, ok)
	}
	// 30 flat bars → ~0 vol → calm.
	bars := make([]md.Bar, 30)
	for i := range bars {
		bars[i] = md.Bar{Ts: int64(i) * 86400, Close: 100}
	}
	pct, label, ok := VolRegime(bars)
	if !ok || label != "calm" || pct != 0 {
		t.Errorf("flat bars: %v,%q,%v", pct, label, ok)
	}
}

// ── composition from a seeded temp store ────────────────────────────────

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "briefing.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedBars writes n synthetic daily closes ending at `end`.
func seedBars(t *testing.T, st *store.Store, symbolID int64, closes []float64, end time.Time) {
	t.Helper()
	bars := make([]md.Bar, len(closes))
	for i, c := range closes {
		bars[i] = md.Bar{
			SymbolID: symbolID, TF: md.TF1d,
			Ts:   end.Add(-time.Duration(len(closes)-1-i) * 24 * time.Hour).Unix(),
			Open: c, High: c, Low: c, Close: c,
		}
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

func TestBriefingWorkerEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ny := NYLoc()
	now := time.Date(2026, 7, 3, 8, 0, 0, 0, ny) // 8:00am ET

	// Two symbols with bars; NVDA is the big mover (+10%), SPY flat-ish.
	nvda, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	seedBars(t, st, nvda.ID, []float64{100, 110}, now)
	closes := make([]float64, 30)
	for i := range closes {
		closes[i] = 500 + float64(i%2) // tiny alternation → measurable vol
	}
	seedBars(t, st, spy.ID, closes, now)

	// Scores (breadth), a regime change 1h ago, a conviction prediction,
	// and one open paper position.
	for _, s := range []md.Symbol{nvda, spy} {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: s.ID, Horizon: md.H1d, Ts: now.Unix(), Score: 0.4,
			Components: []md.ScoreComponent{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertRegime(ctx, nvda.ID, now.Add(-2*time.Hour).Unix(), "range", 0.5, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRegime(ctx, nvda.ID, now.Add(-1*time.Hour).Unix(), "uptrend", 0.8, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: nvda.ID, Horizon: md.H1d, Ts: now.Add(-time.Hour).Unix(),
		RawProb: 0.7, CalProb: 0.7, NUsed: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertPosition(ctx, store.Position{
		SymbolID: nvda.ID, UserID: 1, Qty: 10, EntryPrice: 100, EntryTs: now.Add(-48 * time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	w := &Worker{St: st, Loc: ny, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "2026-07-03") {
		t.Errorf("detail = %q", detail)
	}

	ins, err := st.InsightsByKind(ctx, Kind, 5)
	if err != nil {
		t.Fatalf("InsightsByKind: %v", err)
	}
	if len(ins) != 1 {
		t.Fatalf("briefings stored = %d want 1", len(ins))
	}
	body := ins[0].Body
	for _, want := range []string{
		"not a forecast",              // standard honesty line
		"NVDA +10.0%",                 // real mover from bars
		"range → uptrend",             // regime change last 24h
		"P(up) 70%",                   // conviction prediction
		"calibrated on 0 resolved",    // honest caveat: no resolved outcomes yet
		"Open paper positions: 1",     // P&L summary present
		"2 of 2 tracked symbols show", // breadth from scores
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	// P&L: 10 shares from 100 → 110 = +100 (+10%).
	if !strings.Contains(body, "+100.00") || !strings.Contains(body, "+10.0%") {
		t.Errorf("body missing position P&L:\n%s", body)
	}
	// Evidence blob carries the kind + day for the web pin.
	var data struct {
		Kind string `json:"kind"`
		Day  string `json:"day"`
	}
	if err := json.Unmarshal([]byte(ins[0].Data), &data); err != nil {
		t.Fatalf("data blob: %v", err)
	}
	if data.Kind != Kind || data.Day != "2026-07-03" {
		t.Errorf("data = %+v", data)
	}

	// Same-day rerun: dedup via meta day key → still exactly one insight.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if ins, _ = st.InsightsByKind(ctx, Kind, 5); len(ins) != 1 {
		t.Fatalf("same-day rerun duplicated briefing: %d", len(ins))
	}

	// Next day at 7:05am → a second briefing.
	w.Now = func() time.Time { return now.Add(23*time.Hour + 5*time.Minute) } // 7:05am next day
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("next-day run: %v", err)
	}
	if ins, _ = st.InsightsByKind(ctx, Kind, 5); len(ins) != 2 {
		t.Fatalf("next-day briefing missing: %d", len(ins))
	}
}

func TestBriefingHoldsBeforeSeven(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ny := NYLoc()
	w := &Worker{St: st, Loc: ny, Now: func() time.Time {
		return time.Date(2026, 7, 3, 6, 30, 0, 0, ny)
	}}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "waiting") {
		t.Errorf("detail = %q want waiting", detail)
	}
	if ins, _ := st.InsightsByKind(ctx, Kind, 5); len(ins) != 0 {
		t.Fatalf("briefing written before 7am: %d", len(ins))
	}
}

// fakeLLM asserts the polish path: facts in, polished text out, and the
// disclaimer is re-appended when the model drops it.
type fakeLLM struct {
	reply string
	err   error
	seen  string
}

func (f *fakeLLM) Enabled() bool    { return true }
func (f *fakeLLM) Model() string    { return "fake" }
func (f *fakeLLM) Stats() llm.Stats { return llm.Stats{} }
func (f *fakeLLM) Complete(_ context.Context, _ string, msgs []llm.Message, _ int) (string, error) {
	if len(msgs) > 0 {
		f.seen = msgs[len(msgs)-1].Content
	}
	return f.reply, f.err
}

func TestPolish(t *testing.T) {
	w := &Worker{}
	base := "Facts here. " + disclaimer

	// No LLM → unchanged.
	if got := w.polish(context.Background(), base); got != base {
		t.Errorf("nil LLM changed body")
	}
	// LLM error → deterministic fallback.
	w.LLM = &fakeLLM{err: fmt.Errorf("boom")}
	if got := w.polish(context.Background(), base); got != base {
		t.Errorf("error path changed body")
	}
	// Model drops the disclaimer → re-appended.
	f := &fakeLLM{reply: "Polished words."}
	w.LLM = f
	got := w.polish(context.Background(), base)
	if !strings.Contains(got, "Polished words.") || !strings.Contains(got, "not a forecast") {
		t.Errorf("polish = %q", got)
	}
	if !strings.Contains(f.seen, "DATA:") || !strings.Contains(f.seen, "Facts here.") {
		t.Errorf("LLM did not receive the facts as data: %q", f.seen)
	}
}
