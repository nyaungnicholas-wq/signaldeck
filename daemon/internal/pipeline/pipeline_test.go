package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/breakout"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pipeline.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestEnvInt(t *testing.T) {
	const key = "SIGNALDECK_TEST_ENVINT"
	// Unset → default.
	if got := envInt(key, 42); got != 42 {
		t.Fatalf("unset: %d want 42", got)
	}
	// Valid override.
	t.Setenv(key, "7")
	if got := envInt(key, 42); got != 7 {
		t.Fatalf("override: %d want 7", got)
	}
	// Malformed → default.
	t.Setenv(key, "not-a-number")
	if got := envInt(key, 42); got != 42 {
		t.Fatalf("malformed: %d want 42", got)
	}
	// Zero and negative are rejected (must be > 0) → default.
	t.Setenv(key, "0")
	if got := envInt(key, 42); got != 42 {
		t.Fatalf("zero: %d want 42", got)
	}
	t.Setenv(key, "-5")
	if got := envInt(key, 42); got != 42 {
		t.Fatalf("negative: %d want 42", got)
	}
}

func TestSentimentTagger_SkipsWithoutLLMKey(t *testing.T) {
	st := openStore(t)
	w := &SentimentTagger{St: st, LLM: llm.New(nil, "http://unused", "model", "", "", 10)}
	detail, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "skipped") || !strings.Contains(detail, "no LLM key") {
		t.Fatalf("detail=%q want skip note", detail)
	}
}

func TestNewsFetcher_SkipsWithoutClient(t *testing.T) {
	st := openStore(t)
	w := &NewsFetcher{St: st, Client: nil}
	detail, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "skipped") || !strings.Contains(detail, "no Alpaca key") {
		t.Fatalf("detail=%q want skip note", detail)
	}
}

// seedDaily writes one daily bar per given day index (86400s apart), with a
// gently trending close so breakout.Detect has sane inputs.
func seedDaily(t *testing.T, st *store.Store, id int64, days []int64) {
	t.Helper()
	bars := make([]md.Bar, 0, len(days))
	for _, d := range days {
		px := 100 + float64(d)*0.5
		bars = append(bars, md.Bar{
			SymbolID: id, TF: md.TF1d, Ts: d * 86400,
			Open: px, High: px * 1.01, Low: px * 0.99, Close: px, Volume: 1000,
		})
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

// TestBreakoutRunner_SeriesCarryTimestamps: the runner builds breakout.Series
// straight from stored bars — the Ts slices must be populated so correlation
// alignment intersects on real shared dates (a symbol with a gap must line up
// with its peer on the shared days only, not by index).
func TestBreakoutRunner_SeriesCarryTimestamps(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")

	// AAA has days 1..30; BBB misses days 10-12 (halt/gap).
	var daysA, daysB []int64
	for d := int64(1); d <= 30; d++ {
		daysA = append(daysA, d)
		if d < 10 || d > 12 {
			daysB = append(daysB, d)
		}
	}
	seedDaily(t, st, a.ID, daysA)
	seedDaily(t, st, b.ID, daysB)

	// Reconstruct series exactly the way BreakoutRunner.Run does and check
	// AlignByTs intersects to the shared dates.
	var series []breakout.Series
	for _, s := range []md.Symbol{a, b} {
		daily, err := st.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			t.Fatalf("last bars: %v", err)
		}
		closes := make([]float64, len(daily))
		tss := make([]int64, len(daily))
		for i, bar := range daily {
			closes[i] = bar.Close
			tss[i] = bar.Ts
		}
		if len(tss) == 0 || tss[0] == 0 {
			t.Fatalf("%s: series missing timestamps", s.Symbol)
		}
		series = append(series, breakout.Series{Symbol: s.Symbol, Closes: closes, Ts: tss})
	}
	aligned := breakout.AlignByTs(series)
	if len(aligned) != 2 {
		t.Fatalf("aligned series: %d", len(aligned))
	}
	wantShared := 27 // 30 days minus the 3-day gap
	if len(aligned[0]) != wantShared || len(aligned[1]) != wantShared {
		t.Fatalf("aligned lengths %d/%d want %d (timestamp intersection)",
			len(aligned[0]), len(aligned[1]), wantShared)
	}
	// Same closes on shared dates: AAA close at day d is 100+d/2. Aligned
	// index 9 must be day 13 (days 10-12 dropped), not day 10.
	if got, want := aligned[0][9], 100+13*0.5; got != want {
		t.Fatalf("gap not skipped in alignment: aligned[0][9]=%v want %v", got, want)
	}

	// And the orchestration end-to-end: Run must succeed on this store.
	w := &BreakoutRunner{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("breakout run: %v", err)
	}
	if !strings.Contains(detail, "event(s) logged") {
		t.Fatalf("detail=%q", detail)
	}
	// Running again immediately must also be safe (dedup paths).
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("breakout re-run: %v", err)
	}
}
