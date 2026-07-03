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
	defer st.Close()

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
