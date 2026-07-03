package store

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// tsOnDay returns a unix timestamp at noon UTC of a YYYY-MM-DD day.
func tsOnDay(t *testing.T, day string) int64 {
	t.Helper()
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("bad day %q: %v", day, err)
	}
	return d.Add(12 * time.Hour).Unix()
}

// addRatedNews inserts one headline and immediately rates it.
func addRatedNews(t *testing.T, st *Store, id string, symbolID, ts int64, sentiment string, score float64) {
	t.Helper()
	ctx := context.Background()
	if err := st.InsertNews(ctx, NewsItem{ID: id, SymbolID: symbolID, Ts: ts, Headline: "h " + id}); err != nil {
		t.Fatalf("insert news %s: %v", id, err)
	}
	if sentiment != "unrated" {
		if err := st.RateNews(ctx, id, sentiment, score, "because"); err != nil {
			t.Fatalf("rate news %s: %v", id, err)
		}
	}
}

func TestUpsertSentimentDaily_AggregatesRatedPerSymbolPerDay(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	a, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	day, other := "2026-07-01", "2026-06-30"
	// Symbol A on `day`: bullish +0.8, bearish -0.4, neutral 0.0, one UNRATED
	// (must not count), and one rated headline on ANOTHER day (must not leak).
	addRatedNews(t, st, "a1", a.ID, tsOnDay(t, day), "bullish", 0.8)
	addRatedNews(t, st, "a2", a.ID, tsOnDay(t, day), "bearish", -0.4)
	addRatedNews(t, st, "a3", a.ID, tsOnDay(t, day), "neutral", 0.0)
	addRatedNews(t, st, "a4", a.ID, tsOnDay(t, day), "unrated", 0)
	addRatedNews(t, st, "a5", a.ID, tsOnDay(t, other), "bullish", 0.9)
	// Symbol B on `day`: a single bullish headline.
	addRatedNews(t, st, "b1", b.ID, tsOnDay(t, day), "bullish", 0.6)

	n, err := st.UpsertSentimentDaily(ctx, day)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 symbol-day rows written, got %d", n)
	}

	rowsA, err := st.SentimentSeries(ctx, a.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 1 {
		t.Fatalf("A: want 1 aggregate row, got %d", len(rowsA))
	}
	got := rowsA[0]
	wantMean := (0.8 - 0.4 + 0.0) / 3
	if got.Day != day || got.N != 3 || math.Abs(got.MeanScore-wantMean) > 1e-9 ||
		got.Pos != 1 || got.Neg != 1 || got.Neu != 1 {
		t.Fatalf("A aggregate wrong: %+v (want day=%s n=3 mean=%.4f pos/neg/neu=1/1/1)", got, day, wantMean)
	}

	rowsB, err := st.SentimentSeries(ctx, b.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsB) != 1 || rowsB[0].N != 1 || math.Abs(rowsB[0].MeanScore-0.6) > 1e-9 {
		t.Fatalf("B aggregate wrong: %+v", rowsB)
	}

	// Idempotent recompute: rating the previously-unrated headline and
	// re-upserting must REPLACE the row with the corrected aggregate.
	if err := st.RateNews(ctx, "a4", "bullish", 1.0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSentimentDaily(ctx, day); err != nil {
		t.Fatal(err)
	}
	rowsA, err = st.SentimentSeries(ctx, a.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantMean = (0.8 - 0.4 + 0.0 + 1.0) / 4
	if len(rowsA) != 1 || rowsA[0].N != 4 || math.Abs(rowsA[0].MeanScore-wantMean) > 1e-9 || rowsA[0].Pos != 2 {
		t.Fatalf("A recompute wrong: %+v (want n=4 mean=%.4f pos=2)", rowsA, wantMean)
	}

	// A day with no rated news writes nothing.
	if n, err := st.UpsertSentimentDaily(ctx, "1999-01-01"); err != nil || n != 0 {
		t.Fatalf("empty day: n=%d err=%v (want 0, nil)", n, err)
	}
}

func TestLatestSentiment_FreshnessGate(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	// No aggregates at all -> absent.
	if _, _, ok, err := st.LatestSentiment(ctx, sym.ID, 3); err != nil || ok {
		t.Fatalf("no data: ok=%v err=%v (want absent)", ok, err)
	}

	// A STALE aggregate (10 days old) must not pass the 3-day gate.
	stale := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	addRatedNews(t, st, "s1", sym.ID, tsOnDay(t, stale), "bullish", 0.9)
	if _, err := st.UpsertSentimentDaily(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := st.LatestSentiment(ctx, sym.ID, 3); err != nil || ok {
		t.Fatalf("stale-only: ok=%v err=%v (want absent)", ok, err)
	}
	// …but a wider window sees it.
	if mean, n, ok, err := st.LatestSentiment(ctx, sym.ID, 30); err != nil || !ok || n != 1 || math.Abs(mean-0.9) > 1e-9 {
		t.Fatalf("wide window: mean=%v n=%d ok=%v err=%v", mean, n, ok, err)
	}

	// Fresh aggregates: the NEWEST fresh day wins.
	yday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	today := time.Now().UTC().Format("2006-01-02")
	addRatedNews(t, st, "s2", sym.ID, tsOnDay(t, yday), "bearish", -0.5)
	addRatedNews(t, st, "s3", sym.ID, tsOnDay(t, today), "bullish", 0.4)
	addRatedNews(t, st, "s4", sym.ID, tsOnDay(t, today), "bullish", 0.2)
	for _, d := range []string{yday, today} {
		if _, err := st.UpsertSentimentDaily(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	mean, n, ok, err := st.LatestSentiment(ctx, sym.ID, 3)
	if err != nil || !ok {
		t.Fatalf("fresh: ok=%v err=%v", ok, err)
	}
	if n != 2 || math.Abs(mean-0.3) > 1e-9 {
		t.Fatalf("fresh: mean=%v n=%d, want mean=0.3 n=2 (today's aggregate)", mean, n)
	}
}

func TestSentimentSeries_NewestFirstAndCapped(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	days := []string{"2026-06-01", "2026-06-02", "2026-06-03"}
	for i, d := range days {
		addRatedNews(t, st, fmt.Sprintf("n%d", i), sym.ID, tsOnDay(t, d), "bullish", 0.5)
		if _, err := st.UpsertSentimentDaily(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.SentimentSeries(ctx, sym.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Day != "2026-06-03" || rows[1].Day != "2026-06-02" {
		t.Fatalf("series wrong (want newest-first, capped at 2): %+v", rows)
	}
}
