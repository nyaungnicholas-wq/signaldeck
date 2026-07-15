package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func newCompositeStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "composite.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, context.Background()
}

func TestCompositeScoreRoundTrip(t *testing.T) {
	st, ctx := newCompositeStore(t)
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := st.LatestCompositeScore(ctx, sym.ID, ""); err != nil || ok {
		t.Fatalf("empty table: ok=%v err=%v, want honest absence", ok, err)
	}

	rows := []CompositeScore{
		{SymbolID: sym.ID, Ts: 1000, Horizon: "1d", Score: 4, CurvePct: 40, Edge: -0.01, Payload: `{"a":1}`},
		{SymbolID: sym.ID, Ts: 2000, Horizon: "1d", Score: 8, CurvePct: 80, Edge: 0.04, Payload: `{"a":2}`},
	}
	for _, r := range rows {
		if err := st.UpsertCompositeScore(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// Re-upsert of the same (symbol, ts, horizon) is idempotent, newest wins.
	rows[1].Score = 9
	if err := st.UpsertCompositeScore(ctx, rows[1]); err != nil {
		t.Fatal(err)
	}

	got, ok, err := st.LatestCompositeScore(ctx, sym.ID, "")
	if err != nil || !ok {
		t.Fatalf("latest: ok=%v err=%v", ok, err)
	}
	if got.Ts != 2000 || got.Score != 9 || got.CurvePct != 80 || got.Edge != 0.04 || got.Payload != `{"a":2}` {
		t.Fatalf("latest = %+v", got)
	}
}

func TestTopCompositeScoresAndBefore(t *testing.T) {
	st, ctx := newCompositeStore(t)
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")

	yesterday, today := int64(1000), int64(90000+1000)
	seed := []CompositeScore{
		// Yesterday's pass: AAPL outranked NVDA.
		{SymbolID: nvda.ID, Ts: yesterday, Horizon: "1d", Score: 5, CurvePct: 50, Edge: 0.01, Payload: `{}`},
		{SymbolID: aapl.ID, Ts: yesterday, Horizon: "1d", Score: 9, CurvePct: 90, Edge: 0.05, Payload: `{}`},
		// Today's pass: NVDA on top; BTC scored too.
		{SymbolID: nvda.ID, Ts: today, Horizon: "1d", Score: 10, CurvePct: 98, Edge: 0.08, Payload: `{}`},
		{SymbolID: aapl.ID, Ts: today, Horizon: "1d", Score: 6, CurvePct: 60, Edge: 0.02, Payload: `{}`},
		{SymbolID: btc.ID, Ts: today, Horizon: "1d", Score: 7, CurvePct: 70, Edge: 0.03, Payload: `{}`},
	}
	for _, r := range seed {
		if err := st.UpsertCompositeScore(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	top, err := st.TopCompositeScores(ctx, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 3 || top[0].Symbol != "NVDA" || top[1].Symbol != "BTC/USD" || top[2].Symbol != "AAPL" {
		t.Fatalf("top order = %+v", top)
	}
	if top[0].Ts != today || top[0].Score != 10 {
		t.Fatalf("top row must be today's newest per symbol: %+v", top[0])
	}

	// Market filter + limit.
	stocks, err := st.TopCompositeScores(ctx, 1, "stocks", "")
	if err != nil || len(stocks) != 1 || stocks[0].Symbol != "NVDA" {
		t.Fatalf("stocks top-1 = %+v (err %v)", stocks, err)
	}

	// The previous pass set: rows strictly before today's pass.
	prev, err := st.CompositeScoresBefore(ctx, today, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(prev) != 2 || prev[0].Symbol != "AAPL" || prev[1].Symbol != "NVDA" {
		t.Fatalf("prev pass = %+v", prev)
	}

	// Inactive symbols drop out.
	if err := st.SetSymbolActive(ctx, btc.ID, false); err != nil {
		t.Fatal(err)
	}
	top, err = st.TopCompositeScores(ctx, 0, "", "")
	if err != nil || len(top) != 2 {
		t.Fatalf("after deactivate: %d rows (err %v), want 2", len(top), err)
	}
}

func TestLatestPredictionsForScoring(t *testing.T) {
	st, ctx := newCompositeStore(t)
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	dead, _ := st.UpsertSymbol(ctx, "DEAD", md.Stocks, "")
	if err := st.SetSymbolStream(ctx, nvda.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolActive(ctx, dead.ID, false); err != nil {
		t.Fatal(err)
	}

	preds := []Prediction{
		{SymbolID: nvda.ID, Horizon: md.H1d, Ts: 100, RawProb: 0.55, CalProb: 0.54, NUsed: 2, Components: `{"PressureScore":0.1}`},
		{SymbolID: nvda.ID, Horizon: md.H1d, Ts: 200, RawProb: 0.60, CalProb: 0.58, NUsed: 3, Components: `{"PressureScore":0.2}`},
		{SymbolID: aapl.ID, Horizon: md.H1d, Ts: 150, RawProb: 0.45, CalProb: 0.47, NUsed: 1, Components: `{"PressureScore":-0.1}`},
		{SymbolID: dead.ID, Horizon: md.H1d, Ts: 150, RawProb: 0.5, CalProb: 0.5, NUsed: 1, Components: `{}`},
		// A 1w row must never leak into the 1d read.
		{SymbolID: aapl.ID, Horizon: md.H1w, Ts: 500, RawProb: 0.9, CalProb: 0.9, NUsed: 1, Components: `{}`},
	}
	for _, p := range preds {
		if err := st.UpsertPrediction(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := st.LatestPredictionsForScoring(ctx, md.H1d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (inactive excluded): %+v", len(rows), rows)
	}
	byName := map[string]PredForScoring{}
	for _, r := range rows {
		byName[r.Symbol] = r
	}
	nv := byName["NVDA"]
	if nv.Ts != 200 || nv.CalProb != 0.58 || nv.NUsed != 3 || !nv.Stream ||
		nv.Components != `{"PressureScore":0.2}` {
		t.Fatalf("NVDA newest row = %+v", nv)
	}
	if ap := byName["AAPL"]; ap.Ts != 150 || ap.Stream || ap.Market != md.Stocks {
		t.Fatalf("AAPL row = %+v", ap)
	}
}

func TestInsiderNetActivity(t *testing.T) {
	st, ctx := newCompositeStore(t)
	sym, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	now := time.Now().Unix()
	since := now - 90*86400

	trades := []InsiderTradeRow{
		{Accession: "a1", SymbolID: sym.ID, Code: "P", Shares: 100, Price: 10, Value: 300_000, TxTs: now - 86400},
		{Accession: "a2", SymbolID: sym.ID, Code: "P", Shares: 50, Price: 10, Value: 112_000, TxTs: now - 5*86400},
		{Accession: "a3", SymbolID: sym.ID, Code: "S", Shares: 20, Price: 10, Value: 88_000, TxTs: now - 10*86400},
		// Grants/exercises never count as conviction.
		{Accession: "a4", SymbolID: sym.ID, Code: "A", Shares: 999, Price: 0, Value: 1_000_000, TxTs: now - 86400},
		// Outside the 90d window.
		{Accession: "a5", SymbolID: sym.ID, Code: "P", Shares: 1, Price: 1, Value: 5_000_000, TxTs: since - 86400},
	}
	for _, tr := range trades {
		if err := st.InsertInsiderTrade(ctx, tr); err != nil {
			t.Fatal(err)
		}
	}

	buys, sells, nBuys, nSells, err := st.InsiderNetActivity(ctx, sym.ID, since)
	if err != nil {
		t.Fatal(err)
	}
	if buys != 412_000 || sells != 88_000 || nBuys != 2 || nSells != 1 {
		t.Fatalf("net activity = buys %.0f (%d) sells %.0f (%d)", buys, nBuys, sells, nSells)
	}

	// A symbol with no rows returns zeros, not an error.
	other, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if b, s2, nb, ns, err := st.InsiderNetActivity(ctx, other.ID, since); err != nil || b != 0 || s2 != 0 || nb != 0 || ns != 0 {
		t.Fatalf("empty symbol: %v %v %v %v (err %v)", b, s2, nb, ns, err)
	}
}

func TestLatestBreakoutFor(t *testing.T) {
	st, ctx := newCompositeStore(t)
	sym, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")

	if _, ok, err := st.LatestBreakoutFor(ctx, sym.ID); err != nil || ok {
		t.Fatalf("no breakouts: ok=%v err=%v", ok, err)
	}
	sid := sym.ID
	if err := st.InsertBreakout(ctx, &sid, 1000, "donchian_up", "old", 0.5); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertBreakout(ctx, &sid, 2000, "volume_spike", "new", 0.8); err != nil {
		t.Fatal(err)
	}
	b, ok, err := st.LatestBreakoutFor(ctx, sym.ID)
	if err != nil || !ok {
		t.Fatalf("latest breakout: ok=%v err=%v", ok, err)
	}
	if b.Ts != 2000 || b.Kind != "volume_spike" || b.Detail != "new" {
		t.Fatalf("latest breakout = %+v", b)
	}
}
