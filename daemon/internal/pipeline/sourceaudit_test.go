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

// TestSourceAuditorFlagsStaleAndDedups uses crypto_perp — a 24/7 (ungated)
// source, so the classification is deterministic regardless of the live clock:
// a snapshot older than its 2h budget must produce exactly one source_stale dq
// event, and a second run the same day must NOT duplicate it.
func TestSourceAuditorFlagsStaleAndDedups(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	// 3h-old perp snapshot: budget 2h ⇒ stale (ungated ⇒ always checked).
	if err := st.InsertCryptoPerp(ctx, store.CryptoPerpRow{SymbolID: btc.ID, Ts: time.Now().Unix() - 3*3600, MarkPx: 1}); err != nil {
		t.Fatal(err)
	}
	// A fresh 1s snapshot ⇒ that source stays silent.
	if err := st.InsertSnap1s(ctx, md.Snap1s{SymbolID: btc.ID, Ts: time.Now().Unix() - 30, Mid: 1}); err != nil {
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
	if n := countStale(t, st, "crypto_perp"); n != 1 {
		t.Fatalf("want 1 crypto_perp source_stale event, got %d", n)
	}
	if n := countStale(t, st, "snapshots_1s"); n != 0 {
		t.Errorf("fresh snapshots_1s must not be flagged, got %d", n)
	}

	// Second run the same day must dedup.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countStale(t, st, "crypto_perp"); n != 1 {
		t.Errorf("dedup failed: want 1 crypto_perp event after 2 runs, got %d", n)
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
