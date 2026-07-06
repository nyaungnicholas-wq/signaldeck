package store

// Tests for the news-scope backlog cleanup ('skipped' sentiment label) and the
// watchlist-union helper that feeds the news-fetch scope.

import (
	"context"
	"math"
	"sort"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestSkipUnratedOutside: only UNRATED rows of symbols outside keepIDs are
// relabeled 'skipped'; in-scope unrated rows stay pending and rated rows are
// never touched. Skipped rows must vanish from the UnratedNews queue and from
// both sentiment aggregates (their default 0 score must not dilute means).
func TestSkipUnratedOutside(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	in, err := st.UpsertSymbol(ctx, "KEEP", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := st.UpsertSymbol(ctx, "DROP", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	day := "2026-07-01"
	ts := tsOnDay(t, day)
	addRatedNews(t, st, "in-pending", in.ID, ts, "unrated", 0)
	addRatedNews(t, st, "in-rated", in.ID, ts, "bullish", 0.8)
	addRatedNews(t, st, "out-pending-1", out.ID, ts, "unrated", 0)
	addRatedNews(t, st, "out-pending-2", out.ID, ts, "unrated", 0)
	addRatedNews(t, st, "out-rated", out.ID, ts, "bearish", -0.5)

	n, err := st.SkipUnratedOutside(ctx, []int64{in.ID})
	if err != nil {
		t.Fatalf("SkipUnratedOutside: %v", err)
	}
	if n != 2 {
		t.Fatalf("relabeled %d rows, want 2 (only DROP's unrated)", n)
	}

	// Queue: only the in-scope pending row remains.
	pending, err := st.UnratedNews(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "in-pending" {
		t.Fatalf("UnratedNews after skip = %+v, want only in-pending", pending)
	}

	// Row-level labels: DROP's pending rows are 'skipped', rated untouched.
	items, err := st.SymbolNews(ctx, out.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.ID] = it.Sentiment
	}
	if got["out-pending-1"] != "skipped" || got["out-pending-2"] != "skipped" || got["out-rated"] != "bearish" {
		t.Fatalf("DROP sentiments = %v, want skipped/skipped/bearish", got)
	}

	// NewsSentimentAgg: skipped rows excluded — DROP's mean is its single
	// rated headline, not diluted by two 0-score skipped rows.
	mean, cnt, err := st.NewsSentimentAgg(ctx, out.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 1 || math.Abs(mean-(-0.5)) > 1e-9 {
		t.Fatalf("NewsSentimentAgg = mean %.3f n %d, want -0.5 / 1 (skipped excluded)", mean, cnt)
	}

	// Daily aggregate: skipped rows excluded the same way.
	if _, err := st.UpsertSentimentDaily(ctx, day); err != nil {
		t.Fatal(err)
	}
	rows, err := st.SentimentSeries(ctx, out.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].N != 1 || math.Abs(rows[0].MeanScore-(-0.5)) > 1e-9 {
		t.Fatalf("sentiment_daily for DROP = %+v, want n=1 mean=-0.5 (skipped excluded)", rows)
	}

	// Idempotent + terminal: a second pass with the same scope changes nothing
	// (the first pass already relabeled; skipped rows never re-qualify).
	if n, err := st.SkipUnratedOutside(ctx, []int64{in.ID}); err != nil || n != 0 {
		t.Fatalf("second pass: n=%d err=%v, want 0/nil", n, err)
	}
}

// TestSkipUnratedOutside_EmptyKeep: with an empty scope EVERY unrated row is
// relabeled (degenerate but well-defined).
func TestSkipUnratedOutside_EmptyKeep(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "ONLY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	addRatedNews(t, st, "p1", sym.ID, tsOnDay(t, "2026-07-01"), "unrated", 0)
	if n, err := st.SkipUnratedOutside(ctx, nil); err != nil || n != 1 {
		t.Fatalf("empty keep: n=%d err=%v, want 1/nil", n, err)
	}
	pending, err := st.UnratedNews(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("queue after empty-keep skip = %v (err %v), want empty", pending, err)
	}
}

// TestWatchedSymbolIDs: distinct union across ALL users' watchlists.
func TestWatchedSymbolIDs(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// No users/watchlists yet → empty, no error.
	if ids, err := st.WatchedSymbolIDs(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("empty case: ids=%v err=%v", ids, err)
	}

	a, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSymbol(ctx, "CCC", md.Stocks, ""); err != nil {
		t.Fatal(err) // active but unwatched — must NOT appear
	}
	u1, err := st.CreateUser(ctx, "alice", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := st.CreateUser(ctx, "bob", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	// Overlapping watchlists: AAA watched by both → appears ONCE.
	for _, add := range []struct{ uid, sid int64 }{{u1, a.ID}, {u2, a.ID}, {u2, b.ID}} {
		if err := st.AddUserSymbol(ctx, add.uid, add.sid); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := st.WatchedSymbolIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	want := []int64{a.ID, b.ID}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("WatchedSymbolIDs = %v, want %v (distinct union)", ids, want)
	}
}
