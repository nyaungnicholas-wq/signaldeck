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

func TestDailyBarStaleCalendar(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	tests := []struct {
		barY, barM, barD               int
		nowY, nowM, nowD, nowH, nowMin int
		want                           bool
		name                           string
	}{
		{2026, 9, 4, 2026, 9, 8, 5, 0, false, "Fri->Tue after Labor Day"},
		{2026, 9, 4, 2026, 9, 9, 5, 0, false, "Fri->Wed after Labor Day"},
		{2026, 9, 4, 2026, 9, 10, 5, 0, true, "Fri->Thu after Labor Day"},
		{2026, 8, 28, 2026, 8, 31, 5, 0, false, "Fri->Mon weekend"},
		{2026, 8, 26, 2026, 8, 28, 23, 0, false, "Wed->Fri same week"},
		{2026, 8, 26, 2026, 8, 29, 9, 0, true, "Wed->Sat weekend"},
		{2026, 7, 2, 2026, 7, 6, 9, 0, false, "Thu->Mon with July 3 holiday"},
		{0, 0, 0, 2026, 9, 8, 5, 0, false, "zero bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var latestBarTs int64
			if tt.barY == 0 {
				latestBarTs = 0
			} else {
				barTime := time.Date(tt.barY, time.Month(tt.barM), tt.barD, 0, 0, 0, 0, ny)
				latestBarTs = barTime.Unix()
			}
			nowTime := time.Date(tt.nowY, time.Month(tt.nowM), tt.nowD, tt.nowH, tt.nowMin, 0, 0, ny)
			got := dailyBarStale(latestBarTs, nowTime)
			if got != tt.want {
				t.Errorf("dailyBarStale(%d, %v) = %v, want %v", latestBarTs, nowTime, got, tt.want)
			}
		})
	}
	t.Run("bar 90 days before now", func(t *testing.T) {
		now := time.Date(2026, 9, 8, 5, 0, 0, 0, ny)
		bar := now.AddDate(0, 0, -90)
		bar = time.Date(bar.Year(), bar.Month(), bar.Day(), 0, 0, 0, 0, ny)
		got := dailyBarStale(bar.Unix(), now)
		if !got {
			t.Errorf("dailyBarStale(%d, %v) = %v, want true (90 days)", bar.Unix(), now, got)
		}
	})
}

func TestDQAuditorSkipsDailyOnlyDuringSweep(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "dq.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().Unix()
	sym, err := st.UpsertSymbol(ctx, "BRK", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert symbol BRK: %v", err)
	}
	if err := st.SetSymbolStream(ctx, sym.ID, false); err != nil {
		t.Fatalf("set stream false: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1m, Ts: now - 30*86400, Close: 1},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: now - 20*86400, Close: 1},
	}); err != nil {
		t.Fatalf("upsert bars for BRK: %v", err)
	}
	crypto, err := st.UpsertSymbol(ctx, "XYZ/USD", md.Crypto, "")
	if err != nil {
		t.Fatalf("upsert symbol XYZ/USD: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: crypto.ID, TF: md.TF1m, Ts: now - 3*3600, Close: 1},
	}); err != nil {
		t.Fatalf("upsert bars for crypto: %v", err)
	}
	if err := st.SetMeta(ctx, "sweep_open", "1"); err != nil {
		t.Fatalf("set meta sweep_open: %v", err)
	}
	if _, err := (&DQAuditor{St: st}).Run(ctx); err != nil {
		t.Fatalf("auditor run: %v", err)
	}
	events, err := st.RecentDQ(ctx, 20)
	if err != nil {
		t.Fatalf("recent DQ: %v", err)
	}
	var staleSym int64
	var staleCrypto int64
	for _, e := range events {
		if e.Kind == "stale" && e.SymbolID != nil {
			if *e.SymbolID == sym.ID {
				staleSym++
			}
			if *e.SymbolID == crypto.ID {
				staleCrypto++
			}
		}
	}
	if staleSym != 0 {
		t.Errorf("expected no stale event for daily-only stock during sweep, got %d", staleSym)
	}
	if staleCrypto != 1 {
		t.Errorf("expected exactly one stale event for crypto during sweep, got %d", staleCrypto)
	}
	if err := st.SetMeta(ctx, "sweep_open", ""); err != nil {
		t.Fatalf("clear sweep_open: %v", err)
	}
	if _, err := (&DQAuditor{St: st}).Run(ctx); err != nil {
		t.Fatalf("auditor run after clear: %v", err)
	}
	events2, err := st.RecentDQ(ctx, 20)
	if err != nil {
		t.Fatalf("recent DQ after clear: %v", err)
	}
	var staleSymAfter int64
	var detail string
	for _, e := range events2 {
		if e.Kind == "stale" && e.SymbolID != nil && *e.SymbolID == sym.ID {
			staleSymAfter++
			detail = e.Detail
		}
	}
	if staleSymAfter != 1 {
		t.Errorf("expected stale event for daily-only stock after sweep cleared, got %d", staleSymAfter)
	}
	if !strings.Contains(detail, "daily-only universe") {
		t.Errorf("stale event detail does not contain 'daily-only universe': %q", detail)
	}
}
