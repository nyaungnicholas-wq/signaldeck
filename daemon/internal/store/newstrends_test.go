// NEWS-TRENDS wave store tests: daily volume series, the z-score honesty
// gates (thin baseline / flat baseline / real z), trend-row upsert + fresh-z
// reads, and the deterministic fleet token extractor (stopwords, short
// tokens and tracked tickers dropped; counts + distinct symbols; stable
// order). Fixture data only.
package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func openNewsTrendsStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "newstrends.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedNews inserts n headlines for a symbol on the UTC day `daysAgo` back.
func seedNews(t *testing.T, st *Store, symbolID int64, daysAgo, n int, headline string) {
	t.Helper()
	ctx := context.Background()
	day := time.Now().UTC().AddDate(0, 0, -daysAgo)
	noon := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC).Unix()
	for i := 0; i < n; i++ {
		if err := st.InsertNews(ctx, NewsItem{
			ID:       fmt.Sprintf("nt-%d-%d-%d", symbolID, daysAgo, i),
			SymbolID: symbolID, Ts: noon + int64(i), Headline: headline,
			URL: "http://x", Source: "t",
		}); err != nil {
			t.Fatalf("insert news: %v", err)
		}
	}
}

func TestNewsVolumeByDayAndZ_Gates(t *testing.T) {
	st := openNewsTrendsStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "NVDA", "stocks", "NVIDIA")

	// Thin baseline: only 3 prior active days -> gate refuses, with a reason.
	seedNews(t, st, sym.ID, 0, 9, "nvda tariffs headline")
	for _, d := range []int{1, 2, 3} {
		seedNews(t, st, sym.ID, d, 2, "nvda quiet day")
	}
	day, n, _, ok, reason, err := st.NewsVolumeZ(ctx, sym.ID)
	if err != nil {
		t.Fatalf("z: %v", err)
	}
	if ok || reason == "" {
		t.Fatalf("thin baseline must gate with a reason (ok=%v reason=%q)", ok, reason)
	}
	if day != time.Now().UTC().Format("2006-01-02") || n != 9 {
		t.Fatalf("day/n wrong: %s %d", day, n)
	}

	// 12 active prior days with varying counts -> a real z for today's spike.
	for d := 4; d <= 12; d++ {
		seedNews(t, st, sym.ID, d, 1+d%3, "nvda routine coverage")
	}
	var z float64
	_, n, z, ok, _, err = st.NewsVolumeZ(ctx, sym.ID)
	if err != nil {
		t.Fatalf("z: %v", err)
	}
	if !ok || n != 9 {
		t.Fatalf("expected a gated-in z for the spike day (ok=%v n=%d)", ok, n)
	}
	// Hand check: baseline mean/std over the 30 prior days (zero days count).
	series, err := st.NewsVolumeByDay(ctx, sym.ID, 31)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	counts := map[string]int{}
	for _, dd := range series {
		counts[dd.Day] = dd.N
	}
	var sum, sumSq float64
	for i := 1; i <= 30; i++ {
		c := float64(counts[time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")])
		sum += c
		sumSq += c * c
	}
	mean := sum / 30
	sd := math.Sqrt(sumSq/30 - mean*mean)
	if want := (9 - mean) / sd; math.Abs(z-want) > 1e-9 {
		t.Fatalf("z=%v want %v", z, want)
	}
	if z <= 0 {
		t.Fatalf("a 9-headline day over a ~1-2/day baseline must be a positive z, got %v", z)
	}
}

func TestNewsVolumeZ_FlatBaselineGate(t *testing.T) {
	st := openNewsTrendsStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", "stocks", "Apple")
	// A baseline with variance zero is impossible once zero-days count, so
	// flat here means EVERY one of the 30 prior days had the same count.
	for d := 1; d <= 30; d++ {
		seedNews(t, st, sym.ID, d, 2, "aapl steady coverage")
	}
	seedNews(t, st, sym.ID, 0, 7, "aapl spike")
	_, _, _, ok, reason, err := st.NewsVolumeZ(ctx, sym.ID)
	if err != nil {
		t.Fatalf("z: %v", err)
	}
	if ok || reason == "" {
		t.Fatalf("flat baseline must gate honestly (ok=%v reason=%q)", ok, reason)
	}
}

func TestUpsertNewsTrendAndLatestZ(t *testing.T) {
	st := openNewsTrendsStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "TSLA", "stocks", "Tesla")
	today := time.Now().UTC().Format("2006-01-02")

	// NULL z (gate unmet) reads back as ABSENT, not zero.
	if err := st.UpsertNewsTrend(ctx, sym.ID, today, 4, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, ok, err := st.LatestNewsTrendZ(ctx, sym.ID, 2); err != nil || ok {
		t.Fatalf("NULL z must read as absent (ok=%v err=%v)", ok, err)
	}
	// Idempotent overwrite with a real z.
	z := 2.5
	if err := st.UpsertNewsTrend(ctx, sym.ID, today, 5, &z); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got, ok, err := st.LatestNewsTrendZ(ctx, sym.ID, 2)
	if err != nil || !ok || got != 2.5 {
		t.Fatalf("fresh z read wrong: %v %v %v", got, ok, err)
	}
	// Staleness: a row 5 days old is absent under maxAge 2.
	sym2, _ := st.UpsertSymbol(ctx, "AMD", "stocks", "AMD")
	old := time.Now().UTC().AddDate(0, 0, -5).Format("2006-01-02")
	if err := st.UpsertNewsTrend(ctx, sym2.ID, old, 3, &z); err != nil {
		t.Fatalf("upsert old: %v", err)
	}
	if _, ok, _ := st.LatestNewsTrendZ(ctx, sym2.ID, 2); ok {
		t.Fatal("stale z must be absent")
	}
}

func TestFleetTrendingTokens(t *testing.T) {
	st := openNewsTrendsStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "NVDA", "stocks", "NVIDIA")
	b, _ := st.UpsertSymbol(ctx, "AAPL", "stocks", "Apple")

	// "tariffs" in 3 headlines across 2 symbols; "chipmaker" in 1;
	// stopword "the"/"and", short token "ai" via <3 chars? ("ai" is 2 chars
	// -> dropped), ticker "nvda" dropped, duplicate token in one headline
	// counted once.
	seedNews(t, st, a.ID, 0, 1, "The NVDA tariffs and tariffs again")
	seedNews(t, st, a.ID, 1, 1, "Chipmaker faces tariffs")
	seedNews(t, st, b.ID, 0, 1, "Tariffs hit AI supply chains")
	since := time.Now().UTC().AddDate(0, 0, -3).Unix()
	toks, err := st.FleetTrendingTokens(ctx, since, 10)
	if err != nil {
		t.Fatalf("tokens: %v", err)
	}
	if len(toks) == 0 || toks[0].Token != "tariffs" || toks[0].Count != 3 || toks[0].Symbols != 2 {
		t.Fatalf("top token wrong: %+v", toks)
	}
	for _, tok := range toks {
		switch tok.Token {
		case "the", "and": // stopwords
			t.Fatalf("stopword leaked: %+v", tok)
		case "ai": // <3 chars
			t.Fatalf("short token leaked: %+v", tok)
		case "nvda", "aapl": // tracked tickers
			t.Fatalf("tracked ticker leaked: %+v", tok)
		}
	}
	// topN cap + determinism (same call, same order).
	toks2, _ := st.FleetTrendingTokens(ctx, since, 2)
	if len(toks2) != 2 || toks2[0].Token != toks[0].Token || toks2[1].Token != toks[1].Token {
		t.Fatalf("topN/determinism broken: %+v vs %+v", toks2, toks[:2])
	}
}

func TestSymbolsWithNewsSince(t *testing.T) {
	st := openNewsTrendsStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "NVDA", "stocks", "NVIDIA")
	b, _ := st.UpsertSymbol(ctx, "AAPL", "stocks", "Apple")
	seedNews(t, st, a.ID, 0, 1, "fresh")
	seedNews(t, st, b.ID, 10, 1, "old")
	ids, err := st.SymbolsWithNewsSince(ctx, time.Now().UTC().AddDate(0, 0, -2).Unix())
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("scope wrong: %v", ids)
	}
}
