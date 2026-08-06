package maintain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The DQAuditor must not report "stale feed" on a company that stopped trading.
// Freshness is undefined for a delisted security, and 1,868 of them carrying
// active=1 produced 5,599 of the 5,719 stale events in a 24h window on the live
// database — the only data-quality signal the platform has, reading 98% noise.
//
// The silence must be NARROW. A delisted name the paper book still holds cannot
// be exited without a price, and a delisted name still carrying an unresolved
// graded outcome is a pending accuracy measurement; a feed fault on either is a
// real incident, so both stay audited. This test pins all four cases at once:
// silencing everything delisted, or silencing nothing, both fail it.
func TestDQAuditorSkipsDelistedButNotHeldOrGraded(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "dq.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().Unix()

	// Every symbol here is a daily-only stock whose newest daily bar is 40 days
	// old — unambiguously stale under the auditor's >4d rule, so the ONLY thing
	// that can differ between them is the delisted skip.
	staleSymbol := func(name string) md.Symbol {
		s, err := st.UpsertSymbol(ctx, name, md.Stocks, "")
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		if err := st.SetSymbolStream(ctx, s.ID, false); err != nil {
			t.Fatalf("stream %s: %v", name, err)
		}
		if err := st.UpsertBars(ctx, []md.Bar{
			{SymbolID: s.ID, TF: md.TF1m, Ts: now - 60*86400, Close: 1},
			{SymbolID: s.ID, TF: md.TF1d, Ts: now - 40*86400, Close: 1},
		}); err != nil {
			t.Fatalf("bars %s: %v", name, err)
		}
		return s
	}

	live := staleSymbol("LIVE")   // active, listed, stale -> must flag
	dead := staleSymbol("DEAD")   // delisted, nothing depends on it -> must NOT flag
	held := staleSymbol("HELD")   // delisted but open in the paper book -> must flag
	graded := staleSymbol("GRAD") // delisted, unresolved score_outcomes -> must flag
	pred := staleSymbol("PRED")   // delisted, unresolved prediction_outcomes -> must flag

	// Exactly the live shape: delisted_at set, active left at 1 (MarkDelisted
	// records a market fact and deliberately does not touch the subscription).
	for _, s := range []md.Symbol{dead, held, graded, pred} {
		if err := st.MarkDelisted(ctx, s.ID, now-40*86400); err != nil {
			t.Fatalf("mark delisted %s: %v", s.Symbol, err)
		}
	}

	if _, err := st.InitPaperBook(ctx, "flagship-1d", 100000, now-2*86400); err != nil {
		t.Fatalf("init paper book: %v", err)
	}
	applied, err := st.ApplyPaperStep(ctx, store.PaperApply{
		Strategy: "flagship-1d",
		BarTs:    now - 86400,
		NewCash:  99000,
		Opens: []store.PaperPosition{{
			Strategy: "flagship-1d", SymbolID: held.ID, Qty: 10, AvgPx: 100, OpenedTs: now - 86400,
		}},
		EquityTs: now - 86400, EquityCash: 99000, EquityPositionsValue: 1000, EquityValue: 100000,
	})
	if err != nil || !applied {
		t.Fatalf("open paper position: applied=%v err=%v", applied, err)
	}

	// InsertScore seeds an UNRESOLVED score_outcomes row alongside the score.
	if err := st.InsertScore(ctx, md.Score{
		SymbolID: graded.ID, Horizon: md.H1d, Ts: now - 86400, Score: 0.5,
	}); err != nil {
		t.Fatalf("insert score: %v", err)
	}

	// UpsertPrediction seeds an UNRESOLVED prediction_outcomes row when NUsed>0.
	// That table is the one tools/accuracy_registry.py grades for the published
	// accuracy number, so it needs its own arm, not score_outcomes' coattails.
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: pred.ID, Horizon: md.H1d, Ts: now - 86400,
		RawProb: 0.6, CalProb: 0.6, NUsed: 3, Components: "{}",
	}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}

	if _, err := (&DQAuditor{St: st}).Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	events, err := st.RecentDQ(ctx, 50)
	if err != nil {
		t.Fatalf("dq: %v", err)
	}
	flagged := map[int64]bool{}
	for _, e := range events {
		if e.Kind == "stale" && e.SymbolID != nil {
			flagged[*e.SymbolID] = true
		}
	}

	if !flagged[live.ID] {
		t.Error("a LISTED symbol with a 40d-old daily bar was not flagged — the auditor stopped auditing")
	}
	if flagged[dead.ID] {
		t.Error("a DELISTED symbol was flagged stale — the 1,868-name flood is back")
	}
	if !flagged[held.ID] {
		t.Error("a delisted symbol still HELD in the paper book was silenced — a stranded position lost its feed alarm")
	}
	if !flagged[graded.ID] {
		t.Error("a delisted symbol with an UNRESOLVED score outcome was silenced — a pending accuracy measurement lost its feed alarm")
	}
	if !flagged[pred.ID] {
		t.Error("a delisted symbol with an UNRESOLVED prediction outcome was silenced — the PUBLISHED accuracy number lost its feed alarm")
	}
}
