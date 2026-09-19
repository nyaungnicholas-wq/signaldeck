package pipeline

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestWriteEvidenceRowCountsWholeCrossSection(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	runner := &PredictionRunner{St: st}
	a, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "Aaa Inc")
	if err != nil {
		t.Fatalf("upsert AAA: %v", err)
	}
	b, err := st.UpsertSymbol(ctx, "BBB", md.Stocks, "Bbb Inc")
	if err != nil {
		t.Fatalf("upsert BBB: %v", err)
	}
	ts := time.Now().Unix()
	evidenceDay := map[md.Horizon]map[int64]int64{}
	if err := runner.writeEvidenceRow(ctx, evidenceDay, md.H1d, a.ID, ts, 0.5, map[string]float64{"x": 1}, "test"); err != nil {
		t.Fatalf("write evidence A ts: %v", err)
	}
	if err := runner.writeEvidenceRow(ctx, evidenceDay, md.H1d, a.ID, ts+60, 0.5, map[string]float64{"x": 1}, "test"); err != nil {
		t.Fatalf("write evidence A ts+60: %v", err)
	}
	if err := runner.writeEvidenceRow(ctx, evidenceDay, md.H1d, b.ID, ts, 0.5, map[string]float64{"x": 1}, "test"); err != nil {
		t.Fatalf("write evidence B ts: %v", err)
	}
	stats, err := st.ForecastDayStatsRaw(ctx, string(md.H1d), time.Now().Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("forecast day stats: %v", err)
	}
	if len(stats) == 0 {
		t.Fatalf("no forecast day stats")
	}
	last := stats[len(stats)-1]
	if last.Symbols != 2 {
		t.Fatalf("denominator: want 2 symbols counted (both withheld), got %d (%+v)", last.Symbols, last)
	}
	if last.Withheld != 2 {
		t.Fatalf("want 2 withheld rows, got %d", last.Withheld)
	}
	p, ok, err := st.LatestPrediction(ctx, a.ID, md.H1d)
	if err != nil {
		t.Fatalf("latest prediction: %v", err)
	}
	if ok {
		t.Fatalf("an evidence row must never be served as a forecast: %+v", p)
	}
}
