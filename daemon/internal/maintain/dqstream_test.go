package maintain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The DQAuditor must grade the daily-only broad universe on DAILY bars, never
// on 1m freshness (only the streamed hot set receives live 1m bars — grading
// ~500 daily-only symbols on 1m false-flagged them every sweep).
func TestDQAuditorDailyOnlyUniverse(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "dq.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().Unix()

	// healthy daily-only symbol: ancient 1m bars (from its one-time backfill),
	// fresh daily bar — must NOT be flagged.
	healthy, _ := st.UpsertSymbol(ctx, "HLT", md.Stocks, "")
	if err := st.SetSymbolStream(ctx, healthy.ID, false); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: healthy.ID, TF: md.TF1m, Ts: now - 30*86400, Close: 1},
		{SymbolID: healthy.ID, TF: md.TF1d, Ts: now - 86400, Close: 1},
	}); err != nil {
		t.Fatalf("bars: %v", err)
	}

	// broken daily-only symbol: daily bar 6 days old — MUST be flagged.
	broken, _ := st.UpsertSymbol(ctx, "BRK", md.Stocks, "")
	if err := st.SetSymbolStream(ctx, broken.ID, false); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: broken.ID, TF: md.TF1m, Ts: now - 30*86400, Close: 1},
		{SymbolID: broken.ID, TF: md.TF1d, Ts: now - 6*86400, Close: 1},
	}); err != nil {
		t.Fatalf("bars: %v", err)
	}

	a := &DQAuditor{St: st}
	if _, err := a.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	events, err := st.RecentDQ(ctx, 20)
	if err != nil {
		t.Fatalf("dq: %v", err)
	}
	var healthyFlagged, brokenFlagged bool
	for _, e := range events {
		if e.Kind != "stale" || e.SymbolID == nil {
			continue
		}
		switch *e.SymbolID {
		case healthy.ID:
			healthyFlagged = true
		case broken.ID:
			brokenFlagged = true
			if !strings.Contains(e.Detail, "daily-only universe") {
				t.Errorf("broken symbol detail = %q, want the daily-only wording", e.Detail)
			}
		}
	}
	if healthyFlagged {
		t.Error("healthy daily-only symbol was false-flagged (the 1m-freshness spam regression)")
	}
	if !brokenFlagged {
		t.Error("daily-only symbol with a 6d-old daily bar was not flagged")
	}
}
