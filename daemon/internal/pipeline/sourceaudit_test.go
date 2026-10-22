package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestSourceAuditorFlagsStaleAndDedups uses wiki_views — an ungated source, so
// the classification is deterministic regardless of the live clock: a newest
// day older than its 3-day budget must produce exactly one source_stale dq
// event, and a second run the same day must NOT duplicate it. crypto_perp is
// seeded stale too, but crypto is paused (2026-10-22), so it must stay silent.
func TestSourceAuditorFlagsStaleAndDedups(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	// Wikipedia views 5 days old: budget 3d ⇒ stale (ungated ⇒ always checked).
	if err := st.UpsertWikiViews(ctx, []store.WikiViewRow{{
		SymbolID: aapl.ID, Day: time.Now().UTC().AddDate(0, 0, -5).Format("2006-01-02"), Views: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	// A 3h-old perp snapshot would be stale (budget 2h), but the source is paused.
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err := st.InsertCryptoPerp(ctx, store.CryptoPerpRow{SymbolID: btc.ID, Ts: time.Now().Unix() - 3*3600, MarkPx: 1}); err != nil {
		t.Fatal(err)
	}

	w := &SourceAuditor{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(detail, "STALE") {
		t.Errorf("detail = %q; want a STALE count", detail)
	}
	if n := countStale(t, st, "wiki_views"); n != 1 {
		t.Fatalf("want 1 wiki_views source_stale event, got %d", n)
	}
	if n := countStale(t, st, "crypto_perp"); n != 0 {
		t.Errorf("paused crypto_perp must not be flagged, got %d", n)
	}

	// Second run the same day must dedup.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countStale(t, st, "wiki_views"); n != 1 {
		t.Errorf("dedup failed: want 1 wiki_views event after 2 runs, got %d", n)
	}
}

func countStale(t *testing.T, st *store.Store, source string) int {
	t.Helper()
	events, err := st.RecentDQ(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.Kind == "source_stale" && strings.Contains(e.Detail, "source="+source+" ") {
			n++
		}
	}
	return n
}
