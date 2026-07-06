package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestShortVolume_UpsertSeriesExtremes covers the Stage-5 store surface:
// batch upsert (idempotent REPLACE), ASC series windowing, latest-day lookup,
// extremes ordering + the labeled min-total floor, and the day-presence check.
func TestShortVolume_UpsertSeriesExtremes(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	aapl, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("upsert AAPL: %v", err)
	}
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "SPDR S&P 500")
	if err != nil {
		t.Fatalf("upsert SPY: %v", err)
	}

	rows := []ShortVolumeRow{
		{SymbolID: aapl.ID, Day: "2026-07-01", ShortVol: 10, ShortExempt: 0, TotalVol: 40, ShortPct: 0.25},
		{SymbolID: aapl.ID, Day: "2026-07-02", ShortVol: 12, ShortExempt: 1, TotalVol: 30, ShortPct: 0.4},
		{SymbolID: spy.ID, Day: "2026-07-02", ShortVol: 90, ShortExempt: 0, TotalVol: 100, ShortPct: 0.9},
		// tiny-volume name: high ratio but under the extremes floor
		{SymbolID: spy.ID, Day: "2026-07-01", ShortVol: 1, ShortExempt: 0, TotalVol: 1, ShortPct: 1},
	}
	if err := st.UpsertShortVolume(ctx, rows); err != nil {
		t.Fatalf("upsert batch: %v", err)
	}
	// Re-upsert with a corrected value (FINRA "Updated" re-post): REPLACE wins.
	if err := st.UpsertShortVolume(ctx, []ShortVolumeRow{
		{SymbolID: aapl.ID, Day: "2026-07-02", ShortVol: 15, ShortExempt: 1, TotalVol: 30, ShortPct: 0.5},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	series, err := st.ShortVolumeSeries(ctx, aapl.ID, 30)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series) != 2 || series[0].Day != "2026-07-01" || series[1].Day != "2026-07-02" {
		t.Fatalf("series not ASC windowed: %+v", series)
	}
	if series[1].ShortPct != 0.5 {
		t.Fatalf("re-upsert did not replace: %+v", series[1])
	}

	day, err := st.LatestShortVolumeDay(ctx)
	if err != nil || day != "2026-07-02" {
		t.Fatalf("latest day = %q err=%v, want 2026-07-02", day, err)
	}

	ext, err := st.ShortVolumeExtremes(ctx, "2026-07-02", 20, 10)
	if err != nil {
		t.Fatalf("extremes: %v", err)
	}
	if len(ext) != 2 || ext[0].Symbol != "SPY" || ext[0].ShortPct != 0.9 || ext[1].Symbol != "AAPL" {
		t.Fatalf("extremes order/join wrong: %+v", ext)
	}
	// Floor: on 07-01 SPY's total (1) is under minTotal 10 — only AAPL shows.
	ext, err = st.ShortVolumeExtremes(ctx, "2026-07-01", 10, 10)
	if err != nil {
		t.Fatalf("extremes floored: %v", err)
	}
	if len(ext) != 1 || ext[0].Symbol != "AAPL" {
		t.Fatalf("min-total floor not applied: %+v", ext)
	}

	has, err := st.HasShortVolumeDay(ctx, "2026-07-02")
	if err != nil || !has {
		t.Fatalf("HasShortVolumeDay(2026-07-02) = %v err=%v, want true", has, err)
	}
	has, err = st.HasShortVolumeDay(ctx, "2026-07-03")
	if err != nil || has {
		t.Fatalf("HasShortVolumeDay(2026-07-03) = %v err=%v, want false", has, err)
	}

	// Empty table honesty: a fresh symbol has an empty series, not an error.
	empty, err := st.ShortVolumeSeries(ctx, 99999, 30)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty series = %+v err=%v", empty, err)
	}
}
