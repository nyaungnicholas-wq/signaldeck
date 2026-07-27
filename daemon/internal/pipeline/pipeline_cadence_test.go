package pipeline

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestSignalRunner_DailyUniverseScoredOncePerDay: a daily-only universe symbol
// (stream=0) is scored on the first pass of the day, then SKIPPED on later
// passes the same day — while a streamed hot-set symbol is scored every pass.
// This is the free-scale write-amplification guard.
func TestSignalRunner_DailyUniverseScoredOncePerDay(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/cadence.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	hot, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "") // streamed hot set
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolStream(ctx, hot.ID, true); err != nil {
		t.Fatal(err)
	}
	daily, err := st.UpsertSymbol(ctx, "ZZZZ", md.Stocks, "") // daily-only universe
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	var bars []md.Bar
	for _, id := range []int64{hot.ID, daily.ID} {
		for i := int64(0); i < 40; i++ {
			bars = append(bars, md.Bar{
				SymbolID: id, TF: md.TF1d, Ts: now - (40-i)*86400,
				Open: 100, High: 101, Low: 99, Close: 100 + float64(i), Volume: 10,
			})
		}
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	r := &SignalRunner{St: st}
	if _, err := r.Run(ctx); err != nil { // first pass of the day
		t.Fatal(err)
	}
	if _, err := r.Run(ctx); err != nil { // second pass same day
		t.Fatal(err)
	}

	// The daily-only symbol must have been scored on exactly ONE distinct ts
	// (once per day); the streamed symbol may be scored on every pass.
	dailyTs := distinctScoreTs(t, st, daily.ID, md.H1d)
	if dailyTs != 1 {
		t.Fatalf("daily-only symbol scored on %d distinct timestamps, want 1 (once/day)", dailyTs)
	}
}

func distinctScoreTs(t *testing.T, st *store.Store, symbolID int64, h md.Horizon) int {
	t.Helper()
	hist, err := st.ScoreHistory(context.Background(), symbolID, h, 0, 1<<62)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, s := range hist {
		seen[s.Ts] = true
	}
	return len(seen)
}

// Live-everything wave: universeDue cadence — 10m while market open, once per
// UTC day closed, legacy day-string cursor reads as due.
func TestUniverseDueLiveCadence(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/ucad.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	// A known NYSE-open moment: Wed 2026-07-08 17:00 UTC (13:00 ET).
	open := time.Date(2026, 7, 8, 17, 0, 0, 0, time.UTC)
	due, cur := universeDue(ctx, st, "test_ucad", open)
	if !due {
		t.Fatal("first pass should be due")
	}
	_ = st.SetMeta(ctx, "test_ucad", cur)
	if due, _ := universeDue(ctx, st, "test_ucad", open.Add(5*time.Minute)); due {
		t.Error("5m after a pass (market open) must NOT be due")
	}
	if due, _ := universeDue(ctx, st, "test_ucad", open.Add(11*time.Minute)); !due {
		t.Error("11m after a pass (market open) MUST be due")
	}
	// Market closed (Sat 2026-07-11 17:00 UTC): same-day pass blocks reruns...
	sat := time.Date(2026, 7, 11, 17, 0, 0, 0, time.UTC)
	due, cur = universeDue(ctx, st, "test_ucad2", sat)
	if !due {
		t.Fatal("first closed-day pass should be due")
	}
	_ = st.SetMeta(ctx, "test_ucad2", cur)
	if due, _ := universeDue(ctx, st, "test_ucad2", sat.Add(2*time.Hour)); due {
		t.Error("second same-closed-day pass must NOT be due")
	}
	if due, _ := universeDue(ctx, st, "test_ucad2", sat.Add(24*time.Hour)); !due {
		t.Error("next closed day MUST be due")
	}
	// Legacy day-string cursor → parses 0 → due immediately.
	_ = st.SetMeta(ctx, "test_ucad3", "2026-07-08")
	if due, _ := universeDue(ctx, st, "test_ucad3", open); !due {
		t.Error("legacy day-string cursor must read as due")
	}
}
