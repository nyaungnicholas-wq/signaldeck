// NEWS-TRENDS wave worker tests: per-symbol z rows (real z vs honest NULL
// under the baseline gate), the 48h fresh-news scope, and the 6h-gated fleet
// trending-token insight (written once, then "not due"; verbatim caveat in
// the body). t.TempDir stores + fixture headlines only.
package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openNewsTrendsStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "newstrends_w.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedHeadlines inserts n headlines for a symbol on the UTC day daysAgo back.
func seedHeadlines(t *testing.T, st *store.Store, symbolID int64, daysAgo, n int, headline string) {
	t.Helper()
	ctx := context.Background()
	day := time.Now().UTC().AddDate(0, 0, -daysAgo)
	noon := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC).Unix()
	for i := 0; i < n; i++ {
		if err := st.InsertNews(ctx, store.NewsItem{
			ID:       fmt.Sprintf("ntw-%d-%d-%d", symbolID, daysAgo, i),
			SymbolID: symbolID, Ts: noon + int64(i), Headline: headline,
			URL: "http://x", Source: "t",
		}); err != nil {
			t.Fatalf("insert news: %v", err)
		}
	}
}

func TestNewsTrendsWorker_ZRowsAndInsight(t *testing.T) {
	st := openNewsTrendsStore(t)
	ctx := context.Background()
	rich, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")
	thin, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	stale, _ := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "Tesla")

	// rich: 12 active prior days + a spike today -> a REAL z.
	seedHeadlines(t, st, rich.ID, 0, 9, "NVDA tariffs pressure chipmakers on export tariffs")
	for d := 1; d <= 12; d++ {
		seedHeadlines(t, st, rich.ID, d, 1+d%3, "NVDA routine tariffs coverage")
	}
	// thin: news today but no baseline -> row stored with an honest NULL z.
	seedHeadlines(t, st, thin.ID, 0, 2, "Apple guidance chatter")
	// stale: last headline 10 days ago -> OUTSIDE the 48h fresh scope.
	seedHeadlines(t, st, stale.ID, 10, 3, "Tesla old story")

	w := &NewsTrends{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "news-volume rows for 2 symbol(s)") ||
		!strings.Contains(detail, "(1 below the z baseline gate") {
		t.Fatalf("detail wrong: %q", detail)
	}

	// rich carries a fresh z; thin's NULL z reads as absent; stale has no row.
	if z, ok, err := st.LatestNewsTrendZ(ctx, rich.ID, 2); err != nil || !ok || z <= 0 {
		t.Fatalf("rich z wrong: %v %v %v", z, ok, err)
	}
	if _, ok, _ := st.LatestNewsTrendZ(ctx, thin.ID, 2); ok {
		t.Fatal("thin symbol must store an honest NULL z (absent on read)")
	}
	if _, ok, _ := st.LatestNewsTrendZ(ctx, stale.ID, 30); ok {
		t.Fatal("stale symbol is outside the fresh scope — no row expected")
	}

	// First pass wrote the fleet insight (meta cursor empty -> due).
	if !strings.Contains(detail, "insight written") {
		t.Fatalf("expected the 6h fleet insight on first run: %q", detail)
	}
	ins, err := st.RecentInsights(ctx, 0, 5)
	if err != nil || len(ins) == 0 {
		t.Fatalf("insights read: %v %v", ins, err)
	}
	top := ins[0]
	if !strings.Contains(top.Body, "trending in headlines:") ||
		!strings.Contains(top.Body, "headline-frequency trend — descriptive attention, not a forecast") {
		t.Fatalf("insight must carry the token list + verbatim caveat: %q", top.Body)
	}
	if !strings.Contains(top.Body, "tariffs(") {
		t.Fatalf("top token missing from the insight body: %q", top.Body)
	}
	if !strings.Contains(top.Data, `"kind":"news_trends"`) {
		t.Fatalf("insight kind missing: %q", top.Data)
	}

	// Second pass inside the 6h window: rows recomputed, insight NOT rewritten.
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !strings.Contains(detail2, "insight not due") {
		t.Fatalf("6h gate broken: %q", detail2)
	}
	ins2, _ := st.RecentInsights(ctx, 0, 10)
	if len(ins2) != len(ins) {
		t.Fatalf("insight duplicated inside the 6h window: %d -> %d", len(ins), len(ins2))
	}
}

// FEATURE-VECTOR integration (featureVersion 4): a fresh stored news-volume
// z must ride the persisted prediction vector as news_vol_z; a NULL-z row
// (gate unmet) must leave the field ABSENT — absence is information.
func TestNewsVolZJoinsFeatureVector(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	// Minimal prediction state (mirrors TestPredictionRunnerPersistsFeatures).
	now := time.Now().Unix()
	var bars []md.Bar
	for i := int64(0); i < 30; i++ {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: now - (30-i)*86400,
			Open: 100, High: 101, Low: 99, Close: 100 + float64(i), Volume: 1,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for _, h := range predHorizons {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: h, Ts: now - 60, Score: 0.3,
			Components: []md.ScoreComponent{{Name: "rsi", Contrib: 0.3}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A fresh news-volume z stored by the news-trends worker.
	z := 1.75
	today := time.Now().UTC().Format("2006-01-02")
	if err := st.UpsertNewsTrend(ctx, sym.ID, today, 6, &z); err != nil {
		t.Fatal(err)
	}

	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	feats, err := st.FeaturesSince(ctx, sym.ID, md.H1d, 0, 0)
	if err != nil || len(feats) != 1 {
		t.Fatalf("features: %v %v", feats, err)
	}
	// news_vol_z keeps riding the CURRENT vector version (bumped over successive
	// feature waves: …7→8 trend-structure, 8→9 alpha-source insider/short-vol).
	// Assert the row is stamped with the current const — never pin the const to a
	// literal, or every future feature wave breaks this test.
	if feats[0].Version != featureVersion {
		t.Fatalf("news_vol_z rows must be stamped with the current version, got v%d (const %d)", feats[0].Version, featureVersion)
	}
	if got := feats[0].Vec["news_vol_z"]; got != z {
		t.Fatalf("news_vol_z = %v, want %v (vec %+v)", got, z, feats[0].Vec)
	}

	// NULL z (gate unmet) -> the field is simply absent from the next vector.
	if err := st.UpsertNewsTrend(ctx, sym.ID, today, 6, nil); err != nil {
		t.Fatal(err)
	}
	sym2, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	// Streamed so the second pass predicts it regardless of the broad-universe
	// cadence gate the first pass already consumed.
	if err := st.SetSymbolStream(ctx, sym2.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, func() []md.Bar {
		var b []md.Bar
		for i := int64(0); i < 30; i++ {
			b = append(b, md.Bar{SymbolID: sym2.ID, TF: md.TF1d, Ts: now - (30-i)*86400,
				Open: 100, High: 101, Low: 99, Close: 100, Volume: 1})
		}
		return b
	}()); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym2.ID, Horizon: md.H1d, Ts: now - 60, Score: 0.1,
		Components: []md.ScoreComponent{{Name: "rsi", Contrib: 0.1}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNewsTrend(ctx, sym2.ID, today, 2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	feats2, err := st.FeaturesSince(ctx, sym2.ID, md.H1d, 0, 0)
	if err != nil || len(feats2) == 0 {
		t.Fatalf("features 2: %v %v", feats2, err)
	}
	if _, present := feats2[0].Vec["news_vol_z"]; present {
		t.Fatalf("NULL z must leave news_vol_z ABSENT: %+v", feats2[0].Vec)
	}
}

func TestNewsTrendsWorker_NoFreshNews(t *testing.T) {
	st := openNewsTrendsStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")
	seedHeadlines(t, st, sym.ID, 20, 3, "ancient history")

	w := &NewsTrends{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "news-volume rows for 0 symbol(s)") {
		t.Fatalf("expected an honest empty pass: %q", detail)
	}
	// No tokens in the last 24h either -> insight skipped with a reason.
	if !strings.Contains(detail, "insight skipped: no tokens") {
		t.Fatalf("expected an honest insight skip: %q", detail)
	}
}
