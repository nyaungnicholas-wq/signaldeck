// TARGETS: daemon/internal/maintain/dqdelisted_test.go (NEW FILE)
//
// HOW TO APPLY: copy this file to daemon/internal/maintain/dqdelisted_test.go,
// then run:  cd daemon && go test ./internal/maintain/ -run TestDQAuditorSkipsDelisted
//
// It FAILS on HEAD 0b1f73b and PASSES with
// drafts/patches/dq-auditor-skip-delisted.patch applied. Apply the patch first
// — without it this file still compiles (it uses only existing store methods)
// and the assertion is what fails.
//
// NOT VERIFIED BY ME: never compiled or executed (this engagement forbids
// building into daemon/). Run it before trusting it.

package maintain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// A DELISTED ticker's feed is frozen because the company stopped trading, not
// because anything broke. The survivorship wave keeps those rows active=1 so
// point-in-time universe reconstruction can see them, which means the DQAuditor
// receives them from ListSymbols(active=true). Grading them on freshness makes
// them stale forever: on 2026-08-06 a historical daily-bar backfill gave all
// 1,868 delisted-but-active symbols a (very old) daily bar and the auditor
// began emitting ~1,897 stale events per hour, saturating both dq_events
// readers (/api/quality reads 100, the risk watcher reads 50).
func TestDQAuditorSkipsDelisted(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "dq.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().Unix()

	// A LIVE daily-only symbol whose daily feed really did die 6 days ago.
	// This is a genuine incident and MUST still be flagged — the fix must not
	// silence the check, only stop it firing on the dead.
	live, err := st.UpsertSymbol(ctx, "LIVE", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert live: %v", err)
	}
	if err := st.SetSymbolStream(ctx, live.ID, false); err != nil {
		t.Fatalf("stream live: %v", err)
	}

	// A DELISTED ticker, still active=1, carrying an ancient daily bar exactly
	// as the historical backfill left it. MUST NOT be flagged.
	dead, err := st.UpsertSymbol(ctx, "DEAD", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert dead: %v", err)
	}
	if err := st.SetSymbolStream(ctx, dead.ID, false); err != nil {
		t.Fatalf("stream dead: %v", err)
	}
	if err := st.MarkDelisted(ctx, dead.ID, now-900*86400); err != nil {
		t.Fatalf("mark delisted: %v", err)
	}

	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: live.ID, TF: md.TF1m, Ts: now - 30*86400, Close: 1},
		{SymbolID: live.ID, TF: md.TF1d, Ts: now - 6*86400, Close: 1},
		{SymbolID: dead.ID, TF: md.TF1m, Ts: now - 950*86400, Close: 1},
		{SymbolID: dead.ID, TF: md.TF1d, Ts: now - 900*86400, Close: 1},
	}); err != nil {
		t.Fatalf("bars: %v", err)
	}

	a := &DQAuditor{St: st}
	if _, err := a.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	events, err := st.RecentDQ(ctx, 50)
	if err != nil {
		t.Fatalf("dq: %v", err)
	}
	var liveFlagged, deadFlagged bool
	for _, e := range events {
		if e.Kind != "stale" || e.SymbolID == nil {
			continue
		}
		switch *e.SymbolID {
		case live.ID:
			liveFlagged = true
		case dead.ID:
			deadFlagged = true
		}
	}
	if deadFlagged {
		t.Error("DELISTED symbol was flagged stale — this is the ~1,897/hour dq_events flood")
	}
	if !liveFlagged {
		t.Error("live symbol with a 6d-old daily bar was NOT flagged: the fix silenced a real incident")
	}
}
