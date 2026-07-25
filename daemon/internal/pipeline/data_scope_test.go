package pipeline

// Tests for the scoped NewsFetcher: fetching is limited to the streamed hot
// set + top-N ranked + user-watchlisted symbols, and the one-time meta-gated
// cleanup relabels the out-of-scope unrated backlog as 'skipped'.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/news"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newsTestServer returns an httptest server speaking the Alpaca /news wire
// shape (one fresh article per request) plus the per-symbol request counter.
// NewsFetcher runs sequentially, so the plain map needs no locking.
func newsTestServer(t *testing.T) (*httptest.Server, map[string]int) {
	t.Helper()
	requested := map[string]int{}
	nextID := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sym := r.URL.Query().Get("symbols")
		requested[sym]++
		nextID++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"news":[{"id":%d,"headline":"h%d","source":"t","url":"","created_at":"2026-07-06T00:00:00Z","symbols":[%q]}],"next_page_token":null}`,
			nextID, nextID, sym)
	}))
	t.Cleanup(srv.Close)
	return srv, requested
}

// sentimentOf returns id → sentiment for one symbol's stored headlines.
func sentimentOf(t *testing.T, st *store.Store, symbolID int64) map[string]string {
	t.Helper()
	items, err := st.SymbolNews(context.Background(), symbolID, 50)
	if err != nil {
		t.Fatalf("SymbolNews: %v", err)
	}
	out := map[string]string{}
	for _, it := range items {
		out[it.ID] = it.Sentiment
	}
	return out
}

func TestNewsFetcher_ScopeAndSkipMigration(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	t.Setenv("SIGNALDECK_NEWS_TOP_RANKED", "2") // top-2 of the ranking below

	mk := func(sym string, market md.Market) md.Symbol {
		s, err := st.UpsertSymbol(ctx, sym, market, "")
		if err != nil {
			t.Fatalf("upsert %s: %v", sym, err)
		}
		return s
	}
	hot := mk("HOT", md.Stocks)    // streamed hot set
	rnkA := mk("RNKA", md.Stocks)  // ranking #1 → in top-2
	rnkB := mk("RNKB", md.Stocks)  // ranking #2 → in top-2
	watch := mk("WTCH", md.Stocks) // on a user watchlist
	cold := mk("COLD", md.Stocks)  // active, daily-only, unranked-top, unwatched → OUT of scope
	mk("BTC/USD", md.Crypto)       // crypto: in scope but never fetched (Alpaca news is stocks-only)

	if err := st.SetSymbolStream(ctx, hot.ID, true); err != nil {
		t.Fatal(err)
	}
	// COLD is ranked too — but 3rd of 3 with topN=2, so ranking doesn't save it.
	if err := st.ReplaceRanking(ctx, 1_751_000_000, []struct {
		SymbolID     int64
		Score        float64
		Rank         int
		Ret1M, Ret3M float64
	}{
		{SymbolID: rnkA.ID, Score: 90, Rank: 1},
		{SymbolID: rnkB.ID, Score: 80, Rank: 2},
		{SymbolID: cold.ID, Score: 10, Rank: 3},
	}); err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser(ctx, "u", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUserSymbol(ctx, uid, watch.ID); err != nil {
		t.Fatal(err)
	}

	// Pre-existing backlog from the old fetch-everything policy: COLD has two
	// pending headlines + one already rated; HOT has one pending.
	seed := func(id string, symID int64, sentiment string, score float64) {
		t.Helper()
		if err := st.InsertNews(ctx, store.NewsItem{ID: id, SymbolID: symID, Ts: 1_751_000_000, Headline: "h " + id}); err != nil {
			t.Fatal(err)
		}
		if sentiment != "unrated" {
			if err := st.RateNews(ctx, id, sentiment, score, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed("cold-p1", cold.ID, "unrated", 0)
	seed("cold-p2", cold.ID, "unrated", 0)
	seed("cold-rated", cold.ID, "bullish", 0.7)
	seed("hot-p1", hot.ID, "unrated", 0)

	srv, requested := newsTestServer(t)
	w := &NewsFetcher{St: st, Client: &news.Client{Base: srv.URL, HTTP: srv.Client()}}

	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Scope filter: exactly the hot + top-2-ranked + watched STOCK symbols
	// were fetched — never COLD (out of scope) and never BTC/USD (crypto).
	for _, wantSym := range []string{"HOT", "RNKA", "RNKB", "WTCH"} {
		if requested[wantSym] != 1 {
			t.Errorf("symbol %s fetched %d times, want 1 (requested=%v)", wantSym, requested[wantSym], requested)
		}
	}
	for _, noSym := range []string{"COLD", "BTC/USD"} {
		if requested[noSym] != 0 {
			t.Errorf("out-of-scope symbol %s was fetched (requested=%v)", noSym, requested)
		}
	}
	if len(requested) != 4 {
		t.Errorf("fetched %d symbols %v, want exactly 4", len(requested), requested)
	}
	if !strings.Contains(detail, "4 in-scope symbols") || !strings.Contains(detail, "cleanup") {
		t.Errorf("detail = %q, want in-scope count + one-time cleanup note", detail)
	}

	// Skip migration: COLD's pending backlog is now terminal 'skipped'; its
	// rated row is untouched; HOT (in scope) stays pending.
	coldSent := sentimentOf(t, st, cold.ID)
	if coldSent["cold-p1"] != "skipped" || coldSent["cold-p2"] != "skipped" || coldSent["cold-rated"] != "bullish" {
		t.Errorf("COLD sentiments = %v, want skipped/skipped/bullish", coldSent)
	}
	if got := sentimentOf(t, st, hot.ID)["hot-p1"]; got != "unrated" {
		t.Errorf("HOT pending headline = %q, want still unrated (in scope)", got)
	}
	pending, err := st.UnratedNews(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pending {
		if p.SymbolID == cold.ID {
			t.Errorf("skipped COLD headline %s still in the unrated queue", p.ID)
		}
	}

	// The gate is recorded…
	gate, err := st.GetMeta(ctx, metaNewsScopeSkip)
	if err != nil || gate == "" {
		t.Fatalf("meta gate = %q err=%v, want non-empty", gate, err)
	}

	// …so the cleanup is ONE-TIME: a new out-of-scope unrated headline
	// arriving later stays 'unrated' (pending), it is NOT re-skipped.
	seed("cold-late", cold.ID, "unrated", 0)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := sentimentOf(t, st, cold.ID)["cold-late"]; got != "unrated" {
		t.Errorf("post-gate COLD headline = %q, want unrated (cleanup must not rerun)", got)
	}
	if requested["COLD"] != 0 {
		t.Errorf("COLD fetched on second run (requested=%v)", requested)
	}
}

// TestNewsFetcher_ScopeGracefulWithoutRankingOrUsers: a fresh deployment with
// no ranking snapshot and no user watchlists must still fetch the hot set and
// not error.
func TestNewsFetcher_ScopeGracefulWithoutRankingOrUsers(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	hot, err := st.UpsertSymbol(ctx, "HOT", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolStream(ctx, hot.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSymbol(ctx, "COLD", md.Stocks, ""); err != nil {
		t.Fatal(err)
	}

	srv, requested := newsTestServer(t)
	w := &NewsFetcher{St: st, Client: &news.Client{Base: srv.URL, HTTP: srv.Client()}}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requested["HOT"] != 1 || requested["COLD"] != 0 || len(requested) != 1 {
		t.Errorf("requested = %v, want only HOT", requested)
	}
}
