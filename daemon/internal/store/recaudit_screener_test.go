// Tests for the recommendation audit chain (append/dedup/verify tamper-
// evidence) and the screener's batched bar/score reads.
package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestRecAuditChain_AppendDedupVerifyTamper(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Empty chain is trivially intact.
	v, err := st.VerifyRecAudit(ctx)
	if err != nil || !v.Intact || v.Count != 0 || v.HeadHash != "" {
		t.Fatalf("empty verify = %+v, %v", v, err)
	}

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if _, ok, err := st.LatestRecAuditForSymbol(ctx, sym.ID); err != nil || ok {
		t.Fatalf("absent entry = ok=%v, %v; want false", ok, err)
	}

	// The identity digest is deterministic on its inputs.
	ch1 := RecAuditContentHash("BUY", "medium", "score=72|edge=0.01")
	if ch1 != RecAuditContentHash("BUY", "medium", "score=72|edge=0.01") {
		t.Fatal("content hash not reproducible")
	}

	e1 := RecAuditEntry{CreatedAt: 100, SymbolID: sym.ID, Symbol: "AAPL",
		Market: md.Stocks, Decision: "BUY", Confidence: "medium", ContentHash: ch1,
		SourcesJSON: `["bars"]`, VersionsJSON: `{"gbm":3}`, AssumptionsJSON: `[]`}
	got1, appended, err := st.AppendRecAudit(ctx, e1)
	if err != nil || !appended || got1.Seq == 0 || got1.PrevHash != "" {
		t.Fatalf("genesis append = %+v, %v, %v", got1, appended, err)
	}

	// Identical content re-poll: no new row, the head entry comes back.
	same, appended, err := st.AppendRecAudit(ctx, e1)
	if err != nil || appended || same.Seq != got1.Seq {
		t.Fatalf("dedup append = %+v, %v, %v; want existing head", same, appended, err)
	}

	// Changed decision: a new chained row.
	e2 := e1
	e2.CreatedAt, e2.Decision = 200, "HOLD"
	e2.ContentHash = RecAuditContentHash("HOLD", "medium", "score=55|edge=0.00")
	got2, appended, err := st.AppendRecAudit(ctx, e2)
	if err != nil || !appended || got2.PrevHash != got1.EntryHash {
		t.Fatalf("second append = %+v, %v, %v; want chained", got2, appended, err)
	}

	head, ok, err := st.LatestRecAuditForSymbol(ctx, sym.ID)
	if err != nil || !ok || head.Seq != got2.Seq || head.Decision != "HOLD" ||
		head.Market != md.Stocks {
		t.Fatalf("latest entry = %+v, %v, %v", head, ok, err)
	}

	v, err = st.VerifyRecAudit(ctx)
	if err != nil || !v.Intact || v.Count != 2 || v.HeadHash != got2.EntryHash {
		t.Fatalf("verify intact = %+v, %v", v, err)
	}

	// Silently rewrite the historical decision — the walk must name the row.
	if _, err := st.w.ExecContext(ctx,
		`UPDATE recommendation_audit SET decision='SELL' WHERE seq=?`, got1.Seq); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	v, err = st.VerifyRecAudit(ctx)
	if err != nil || v.Intact || v.BrokenAtSeq == nil || *v.BrokenAtSeq != got1.Seq {
		t.Fatalf("tampered verify = %+v, %v; want broken at %d", v, err, got1.Seq)
	}
}

func TestScreenerBatchedReads(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	c, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "Nvidia") // no data at all

	// Empty inputs are cheap no-ops, never queries that explode.
	if out, err := st.LastBarsBatch(ctx, nil, md.TF1d, 5); err != nil || len(out) != 0 {
		t.Fatalf("empty batch = %v, %v", out, err)
	}
	if out, err := st.LatestScoresBatch(ctx, nil); err != nil || len(out) != 0 {
		t.Fatalf("empty scores batch = %v, %v", out, err)
	}

	var bars []md.Bar
	for i := int64(1); i <= 4; i++ {
		bars = append(bars, md.Bar{SymbolID: a.ID, TF: md.TF1d, Ts: i * 100,
			Open: 1, High: 2, Low: 0.5, Close: float64(i), Volume: 10})
	}
	bars = append(bars, md.Bar{SymbolID: b.ID, TF: md.TF1d, Ts: 100,
		Open: 1, High: 2, Low: 0.5, Close: 9, Volume: 10})
	// A different timeframe must never leak into the 1d batch.
	bars = append(bars, md.Bar{SymbolID: a.ID, TF: md.TF1h, Ts: 500,
		Open: 1, High: 2, Low: 0.5, Close: 99, Volume: 10})
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	got, err := st.LastBarsBatch(ctx, []int64{a.ID, b.ID, c.ID}, md.TF1d, 3)
	if err != nil || len(got) != 2 {
		t.Fatalf("LastBarsBatch = %d symbols, %v; want 2 (no-bar symbol absent)", len(got), err)
	}
	// n most-recent per symbol, ascending ts within the symbol.
	ab := got[a.ID]
	if len(ab) != 3 || ab[0].Ts != 200 || ab[2].Ts != 400 || ab[2].Close != 4 {
		t.Fatalf("a bars wrong: %+v", ab)
	}
	if len(got[b.ID]) != 1 || got[b.ID][0].Close != 9 {
		t.Fatalf("b bars wrong: %+v", got[b.ID])
	}

	// Scores: two horizons for a (newest ts wins), one for b.
	for _, sc := range []md.Score{
		{SymbolID: a.ID, Horizon: md.H1d, Ts: 100, Score: 0.1},
		{SymbolID: a.ID, Horizon: md.H1d, Ts: 200, Score: 0.9},
		{SymbolID: a.ID, Horizon: md.H1w, Ts: 100, Score: -0.3},
		{SymbolID: b.ID, Horizon: md.H1d, Ts: 150, Score: 0.4},
	} {
		if err := st.InsertScore(ctx, sc); err != nil {
			t.Fatalf("insert score: %v", err)
		}
	}
	scores, err := st.LatestScoresBatch(ctx, []int64{a.ID, b.ID, c.ID})
	if err != nil || len(scores) != 2 {
		t.Fatalf("LatestScoresBatch = %d symbols, %v; want 2", len(scores), err)
	}
	if s := scores[a.ID][md.H1d]; s.Ts != 200 || s.Score != 0.9 {
		t.Fatalf("a 1d score not the newest: %+v", s)
	}
	if s := scores[a.ID][md.H1w]; s.Score != -0.3 {
		t.Fatalf("a 1w score wrong: %+v", s)
	}
	if s := scores[b.ID][md.H1d]; s.Score != 0.4 {
		t.Fatalf("b score wrong: %+v", s)
	}
}
