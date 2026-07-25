package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func newSentTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "sentcorr.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// et builds an instant in US market time, which is the only timezone in which
// "before the close" means anything.
func et(y int, mo time.Month, d, h, mi int) int64 {
	return time.Date(y, mo, d, h, mi, 0, 0, marketcal.Loc()).Unix()
}

// THE NO-LOOKAHEAD TEST. Most financial headlines publish outside market hours.
// If those are keyed to their own calendar day, the study gets to act on
// information hours before it existed, and every measured IC is fiction.
func TestActionableSession(t *testing.T) {
	cases := []struct {
		name string
		ts   int64
		want string
	}{
		{
			// Wed 2026-07-08 is a normal session.
			name: "pre-market headline is actionable the same session",
			ts:   et(2026, time.July, 8, 7, 30),
			want: "2026-07-08",
		},
		{
			name: "intraday headline is actionable the same session",
			ts:   et(2026, time.July, 8, 11, 0),
			want: "2026-07-08",
		},
		{
			name: "after-the-close headline rolls to the next session",
			ts:   et(2026, time.July, 8, 16, 30),
			want: "2026-07-09",
		},
		{
			name: "exactly at the close counts as too late",
			ts:   et(2026, time.July, 8, 16, 0),
			want: "2026-07-09",
		},
		{
			// Fri 2026-07-10 after close → Mon 2026-07-13.
			name: "friday evening rolls across the weekend",
			ts:   et(2026, time.July, 10, 18, 0),
			want: "2026-07-13",
		},
		{
			name: "saturday rolls to monday",
			ts:   et(2026, time.July, 11, 9, 0),
			want: "2026-07-13",
		},
		{
			// Jul 4 2026 falls on a Saturday, observed Friday Jul 3. A Thursday
			// evening headline therefore cannot be acted on until Monday Jul 6.
			name: "holiday closure rolls past the observed day",
			ts:   et(2026, time.July, 2, 17, 0),
			want: "2026-07-06",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ActionableSession(c.ts); got != c.want {
				t.Errorf("ActionableSession(%s) = %s, want %s",
					time.Unix(c.ts, 0).In(marketcal.Loc()).Format(time.RFC3339), got, c.want)
			}
		})
	}
}

func TestActionableSessionNeverGoesBackward(t *testing.T) {
	// Whatever the calendar throws at it, the actionable session is never BEFORE
	// the headline's own day — that direction would be lookahead by construction.
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, marketcal.Loc())
	for i := 0; i < 400; i++ {
		for _, h := range []int{3, 9, 12, 15, 20, 23} {
			ts := start.AddDate(0, 0, i).Add(time.Duration(h) * time.Hour)
			got := ActionableSession(ts.Unix())
			if got < ts.Format("2006-01-02") {
				t.Fatalf("headline at %s mapped BACKWARD to session %s", ts.Format(time.RFC3339), got)
			}
		}
	}
}

func TestLexScoreRoundTripAndWorkQueue(t *testing.T) {
	ctx := context.Background()
	st := newSentTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	for i, h := range []string{"AAA beats estimates", "AAA to hold meeting", "AAA cuts guidance"} {
		if err := st.InsertNews(ctx, NewsItem{
			ID: string(rune('a' + i)), SymbolID: sym.ID, Ts: et(2026, time.July, 8, 10, 0), Headline: h,
		}); err != nil {
			t.Fatalf("insert news: %v", err)
		}
	}

	// Everything is unscored at version 1.
	pending, err := st.UnscoredNews(ctx, 1, 100)
	if err != nil || len(pending) != 3 {
		t.Fatalf("UnscoredNews = %d rows, err=%v; want 3", len(pending), err)
	}

	if err := st.SetLexScores(ctx, 1, []LexScore{
		{ID: "a", Score: 0.4, Polar: true},
		{ID: "b", Score: 0, Polar: false},
		{ID: "c", Score: -0.5, Polar: true, Hedged: true},
	}); err != nil {
		t.Fatalf("SetLexScores: %v", err)
	}

	if pending, err = st.UnscoredNews(ctx, 1, 100); err != nil || len(pending) != 0 {
		t.Errorf("after scoring, UnscoredNews = %d rows, want 0", len(pending))
	}
	// A VERSION BUMP must re-queue everything: scores from different lexicon
	// versions are not comparable and silently mixing them would corrupt the
	// study.
	if pending, err = st.UnscoredNews(ctx, 2, 100); err != nil || len(pending) != 3 {
		t.Errorf("at version 2, UnscoredNews = %d rows, want 3 (version bump re-queues)", len(pending))
	}

	if _, err := st.ReconcileNewsSymbols(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rows, err := st.ScoredNewsSince(ctx, 1, 0)
	if err != nil || len(rows) != 3 {
		t.Fatalf("ScoredNewsSince = %d rows, err=%v; want 3", len(rows), err)
	}
	polar := 0
	for _, r := range rows {
		if r.Polar {
			polar++
		}
	}
	if polar != 2 {
		t.Errorf("polar rows = %d, want 2 (the factual headline is not an opinion)", polar)
	}
}

func TestNewsSymbolsMappingCoversEveryTaggedTicker(t *testing.T) {
	// One article tagging three companies must contribute to all three. Under the
	// bare news table it could only ever belong to one, and the names that lose
	// out are the ones with the least coverage to begin with.
	ctx := context.Background()
	st := newSentTestStore(t)
	var ids []int64
	for _, s := range []string{"AAA", "BBB", "CCC"} {
		sym, err := st.UpsertSymbol(ctx, s, md.Stocks, "")
		if err != nil {
			t.Fatalf("upsert %s: %v", s, err)
		}
		ids = append(ids, sym.ID)
	}
	if err := st.InsertNews(ctx, NewsItem{
		ID: "multi", SymbolID: ids[0], Ts: et(2026, time.July, 8, 10, 0),
		Headline: "AAA and BBB settle with CCC",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.InsertNewsSymbols(ctx, "multi", ids); err != nil {
		t.Fatalf("map: %v", err)
	}
	if err := st.SetLexScores(ctx, 1, []LexScore{{ID: "multi", Score: 0.2, Polar: true}}); err != nil {
		t.Fatalf("score: %v", err)
	}

	rows, err := st.ScoredNewsSince(ctx, 1, 0)
	if err != nil {
		t.Fatalf("ScoredNewsSince: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows for a 3-ticker article, want 3", len(rows))
	}
	seen := map[int64]bool{}
	for _, r := range rows {
		seen[r.SymbolID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("symbol %d missing from the mapping", id)
		}
	}

	// Re-running the reconcile must not duplicate anything.
	before := len(rows)
	if _, err := st.ReconcileNewsSymbols(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rows, _ = st.ScoredNewsSince(ctx, 1, 0); len(rows) != before {
		t.Errorf("reconcile changed row count %d -> %d; must be idempotent", before, len(rows))
	}
}

func TestSentCorrObservationsAlignmentAndForwardWindow(t *testing.T) {
	ctx := context.Background()
	st := newSentTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 40 consecutive daily bars with a known geometric path, one per calendar day
	// (weekends included is fine — the bar series defines the sessions here).
	start := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)
	days := make([]string, 0, 40)
	price := 100.0
	for i := 0; i < 40; i++ {
		d := start.AddDate(0, 0, i)
		days = append(days, d.Format("2006-01-02"))
		if err := st.UpsertBars(ctx, []md.Bar{{
			SymbolID: sym.ID, TF: md.TF1d, Ts: d.Unix(),
			Open: price, High: price, Low: price, Close: price, Volume: 1000,
		}}); err != nil {
			t.Fatalf("bar %d: %v", i, err)
		}
		price *= 1.01
	}

	// A feature on a session in the middle, well clear of both edges.
	target := days[20]
	if err := st.UpsertSentimentFeatures(ctx, []SentimentFeature{{
		SymbolID: sym.ID, Day: target, NPolar: 2, NAll: 3, MeanScore: 0.5, Ver: 1,
	}}); err != nil {
		t.Fatalf("upsert feature: %v", err)
	}

	obs, err := st.SentCorrObservations(ctx, 5, 1)
	if err != nil {
		t.Fatalf("observations: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	o := obs[0]
	if o.Day != target {
		t.Errorf("day = %s, want %s", o.Day, target)
	}
	// Each step is +1%, so a 5-session forward return is 1.01^5-1 and the
	// same-session return is exactly 1%.
	wantFwd := 1.01*1.01*1.01*1.01*1.01 - 1
	if diff := o.Fwd - wantFwd; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Fwd = %.10f, want %.10f (measured FROM the aligned session's close)", o.Fwd, wantFwd)
	}
	if diff := o.Ret0 - 0.01; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Ret0 = %.10f, want 0.01", o.Ret0)
	}
	if o.RetTrail <= o.Ret0 {
		t.Errorf("RetTrail (%.6f) should exceed the one-session return (%.6f) on a rising path",
			o.RetTrail, o.Ret0)
	}
}

func TestSentCorrObservationsOmitsIncompleteForwardWindows(t *testing.T) {
	// The most recent sessions have no forward return yet. They must be ABSENT,
	// not truncated to a shorter window — a shorter window silently mixes
	// horizons and biases the newest observations.
	ctx := context.Background()
	st := newSentTestStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	start := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)
	var days []string
	for i := 0; i < 20; i++ {
		d := start.AddDate(0, 0, i)
		days = append(days, d.Format("2006-01-02"))
		if err := st.UpsertBars(ctx, []md.Bar{{
			SymbolID: sym.ID, TF: md.TF1d, Ts: d.Unix(),
			Open: 100, High: 100, Low: 100, Close: 100 + float64(i), Volume: 1,
		}}); err != nil {
			t.Fatalf("bar: %v", err)
		}
	}
	// Features on the last three sessions AND one safely in the middle.
	feats := []SentimentFeature{
		{SymbolID: sym.ID, Day: days[10], NPolar: 1, NAll: 1, MeanScore: 0.3, Ver: 1},
		{SymbolID: sym.ID, Day: days[17], NPolar: 1, NAll: 1, MeanScore: 0.3, Ver: 1},
		{SymbolID: sym.ID, Day: days[18], NPolar: 1, NAll: 1, MeanScore: 0.3, Ver: 1},
		{SymbolID: sym.ID, Day: days[19], NPolar: 1, NAll: 1, MeanScore: 0.3, Ver: 1},
	}
	if err := st.UpsertSentimentFeatures(ctx, feats); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	obs, err := st.SentCorrObservations(ctx, 5, 1)
	if err != nil {
		t.Fatalf("observations: %v", err)
	}
	if len(obs) != 1 || obs[0].Day != days[10] {
		t.Errorf("got %d observations (%v), want only the one with a complete forward window (%s)",
			len(obs), obsDays(obs), days[10])
	}
}

func TestSentCorrObservationsSkipsSplitArtifacts(t *testing.T) {
	// A >65% one-session jump in this data is a split-adjustment artifact, not a
	// return. Feeding it to the study would let one corrupted bar dominate the
	// correlation.
	ctx := context.Background()
	st := newSentTestStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	start := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)
	var days []string
	for i := 0; i < 30; i++ {
		d := start.AddDate(0, 0, i)
		days = append(days, d.Format("2006-01-02"))
		c := 100.0
		if i >= 15 {
			c = 1000.0 // 10x "move": a reverse-split artifact
		}
		if err := st.UpsertBars(ctx, []md.Bar{{
			SymbolID: sym.ID, TF: md.TF1d, Ts: d.Unix(),
			Open: c, High: c, Low: c, Close: c, Volume: 1,
		}}); err != nil {
			t.Fatalf("bar: %v", err)
		}
	}
	if err := st.UpsertSentimentFeatures(ctx, []SentimentFeature{
		{SymbolID: sym.ID, Day: days[15], NPolar: 1, NAll: 1, MeanScore: 0.5, Ver: 1},
		{SymbolID: sym.ID, Day: days[22], NPolar: 1, NAll: 1, MeanScore: 0.5, Ver: 1},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	obs, err := st.SentCorrObservations(ctx, 5, 1)
	if err != nil {
		t.Fatalf("observations: %v", err)
	}
	for _, o := range obs {
		if o.Day == days[15] {
			t.Errorf("the split-artifact session %s was included with Ret0=%.2f", o.Day, o.Ret0)
		}
	}
	if len(obs) != 1 {
		t.Errorf("got %d observations (%v), want 1 (the clean one)", len(obs), obsDays(obs))
	}
}

func TestSentimentFeatureStatsReportsCoverage(t *testing.T) {
	ctx := context.Background()
	st := newSentTestStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err := st.UpsertSentimentFeatures(ctx, []SentimentFeature{
		{SymbolID: sym.ID, Day: "2026-03-02", NPolar: 2, NAll: 4, MeanScore: 0.1, Ver: 1},
		{SymbolID: sym.ID, Day: "2026-03-03", NPolar: 1, NAll: 2, MeanScore: -0.2, Ver: 1},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	stats, err := st.SentimentFeatureStats(ctx, 1)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Rows != 2 || stats.Days != 2 || stats.Symbols != 1 {
		t.Errorf("stats = %+v; want 2 rows / 2 days / 1 symbol", stats)
	}
	if stats.FirstDay != "2026-03-02" || stats.LastDay != "2026-03-03" {
		t.Errorf("span = %s..%s, want 2026-03-02..2026-03-03", stats.FirstDay, stats.LastDay)
	}
	if stats.PolarRows != 3 || stats.Headlines != 6 {
		t.Errorf("polar=%d headlines=%d, want 3 and 6", stats.PolarRows, stats.Headlines)
	}
}

func obsDays(obs []SentCorrObsRow) []string {
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		out = append(out, o.Day)
	}
	return out
}
