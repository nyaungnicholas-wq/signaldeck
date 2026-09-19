package pipeline

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

func freshBars(symbolID int64, n int, start time.Time) []md.Bar {
	var bars []md.Bar
	day := start
	i := 0
	for len(bars) < n {
		if marketcal.IsTradingDay(day) {
			px := 100.0 * (1.0 + 0.004*math.Sin(float64(i)/11.0))
			rng := px * (0.006 + 0.004*math.Abs(math.Cos(float64(i)/7.0)))
			open := px
			high := px + rng
			low := px - rng
			close := px + 0.3*rng*math.Sin(float64(i)/3.0)
			bar := md.Bar{
				SymbolID: symbolID,
				TF:       md.TF1d,
				Ts:       day.Unix(),
				Open:     open,
				High:     high,
				Low:      low,
				Close:    close,
				Volume:   1e6,
			}
			bars = append(bars, bar)
			i++
		}
		day = day.AddDate(0, 0, 1) // calendar step, not 24h: DST nights are 23h or 25h
	}
	return bars
}

func TestRVForecastRunnerRefusesFormingAndStaleCallBars(t *testing.T) {
	tests := []struct {
		name   string
		nowAdj time.Duration
	}{
		{"forming bar", 10 * time.Hour},
		{"stale bar", 10 * 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "x.db")
			s, err := store.Open(dbPath)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			defer s.Close() //nolint:errcheck

			ctx := context.Background()
			sym, err := s.UpsertSymbol(ctx, "TEST", md.Stocks, "Test Symbol")
			if err != nil {
				t.Fatalf("upsert symbol: %v", err)
			}

			start := time.Date(2020, 1, 2, 0, 0, 0, 0, marketcal.Loc())
			bars := freshBars(sym.ID, 900, start)
			if err := s.UpsertBars(ctx, bars); err != nil {
				t.Fatalf("upsert bars: %v", err)
			}

			lastTs := bars[len(bars)-1].Ts
			now := time.Unix(lastTs, 0).In(marketcal.Loc()).Add(tt.nowAdj)

			runner := RVForecastRunner{
				St:  s,
				Now: func() time.Time { return now },
				Rev: func() string { return "0000000000000000000000000000000000000000" },
			}
			detail, err := runner.Run(ctx)
			if !errors.Is(err, workers.ErrDegraded) {
				t.Fatalf("expected ErrDegraded, got %v", err)
			}
			if !strings.Contains(detail, "forming or stale") {
				t.Fatalf("detail missing 'forming or stale': %q", detail)
			}

			fc, err := s.OpenRVForecasts(ctx, 1, 10)
			if err != nil {
				t.Fatalf("open forecasts: %v", err)
			}
			if len(fc) != 0 {
				t.Fatalf("expected no forecasts, got %d", len(fc))
			}
		})
	}
}

func TestRVForecastRunnerRefusesFormingAndStaleCallBars_settled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	ctx := context.Background()
	sym, err := s.UpsertSymbol(ctx, "TEST", md.Stocks, "Test Symbol")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	start := time.Date(2020, 1, 2, 0, 0, 0, 0, marketcal.Loc())
	bars := freshBars(sym.ID, 900, start)
	if err := s.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	lastTs := bars[len(bars)-1].Ts
	now := time.Unix(lastTs, 0).In(marketcal.Loc()).Add(22 * time.Hour)

	runner := RVForecastRunner{
		St:  s,
		Now: func() time.Time { return now },
		Rev: func() string { return "0000000000000000000000000000000000000000" },
	}
	detail, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(detail, "1 frozen") && !strings.Contains(detail, "frozen") {
		// Accept any detail indicating success; not strictly required but we check.
		t.Logf("detail: %q", detail)
	}

	for _, hz := range []int{1, 5} {
		fc, err := s.OpenRVForecasts(ctx, hz, 10)
		if err != nil {
			t.Fatalf("open forecasts horizon %d: %v", hz, err)
		}
		if len(fc) != 1 {
			t.Fatalf("horizon %d: expected 1 forecast, got %d", hz, len(fc))
		}
		if fc[0].Ts != lastTs {
			t.Fatalf("horizon %d: forecast Ts %d != last bar Ts %d", hz, fc[0].Ts, lastTs)
		}
	}
}

func TestRVForecastRunnerFreezesOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	ctx := context.Background()
	sym, err := s.UpsertSymbol(ctx, "TEST", md.Stocks, "Test Symbol")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	start := time.Date(2020, 1, 2, 0, 0, 0, 0, marketcal.Loc())
	bars := freshBars(sym.ID, 900, start)
	if err := s.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	lastTs := bars[len(bars)-1].Ts
	now := time.Unix(lastTs, 0).In(marketcal.Loc()).Add(22 * time.Hour)

	runner := RVForecastRunner{
		St:  s,
		Now: func() time.Time { return now },
		Rev: func() string { return "0000000000000000000000000000000000000000" },
	}

	// First run
	_, err1 := runner.Run(ctx)
	if err1 != nil {
		t.Fatalf("first run error: %v", err1)
	}
	// Second run
	detail2, err2 := runner.Run(ctx)
	if err2 != nil {
		t.Fatalf("second run error: %v", err2)
	}
	if !strings.Contains(detail2, "already frozen") {
		t.Fatalf("second run detail missing 'already frozen': %q", detail2)
	}

	fc, err := s.OpenRVForecasts(ctx, 1, 10)
	if err != nil {
		t.Fatalf("open forecasts: %v", err)
	}
	if len(fc) != 1 {
		t.Fatalf("expected 1 forecast after two runs, got %d", len(fc))
	}
	if fc[0].Ts != lastTs {
		t.Fatalf("forecast Ts %d != last bar Ts %d", fc[0].Ts, lastTs)
	}
}
