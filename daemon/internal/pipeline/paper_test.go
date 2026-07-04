package pipeline

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedDailyPx writes daily bars for the given (dayIndex, open, close) triples.
// High/Low bracket the close so downstream engines see sane bars.
func seedDailyPx(t *testing.T, st *store.Store, id int64, rows [][3]float64) {
	t.Helper()
	bars := make([]md.Bar, 0, len(rows))
	for _, r := range rows {
		d := int64(r[0])
		o, c := r[1], r[2]
		hi := o
		if c > hi {
			hi = c
		}
		lo := o
		if c < lo {
			lo = c
		}
		bars = append(bars, md.Bar{
			SymbolID: id, TF: md.TF1d, Ts: d * 86400,
			Open: o, High: hi * 1.001, Low: lo * 0.999, Close: c, Volume: 1000,
		})
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

func seedPrediction(t *testing.T, st *store.Store, id int64, h md.Horizon, ts int64, cal float64) {
	t.Helper()
	if err := st.UpsertPrediction(context.Background(), store.Prediction{
		SymbolID: id, Horizon: h, Ts: ts, RawProb: cal, CalProb: cal, NUsed: 10, Components: "{}",
	}); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}
}

// TestPaperTrader_NextBarFillNoLookahead: a strong-long prediction stamped at
// the ts of day-2's bar must fill at the OPEN of day 3 (the first bar STRICTLY
// after the prediction ts), never on day 2's own bar.
func TestPaperTrader_NextBarFillNoLookahead(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	// Days 1,2,3 with distinct opens so the fill price identifies the fill bar.
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 101},
		{2, 102, 103}, // prediction is stamped at this bar's ts
		{3, 110, 111}, // the correct fill bar (next bar's OPEN = 110)
	})
	// Prediction ts == day-2 bar ts. cal_prob strong long.
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	pos, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID)
	if !ok {
		t.Fatal("expected a long position after strong-long prediction")
	}
	// Fill price MUST be day-3's OPEN (110), NOT day-2's open (102) or close.
	if pos.AvgPx != 110 {
		t.Fatalf("fill px=%v want 110 (next bar OPEN, no lookahead)", pos.AvgPx)
	}
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	if len(trades) != 1 || trades[0].Side != "buy" || trades[0].Px != 110 {
		t.Fatalf("trade: %+v", trades)
	}
	if trades[0].Ts != 3*86400 {
		t.Fatalf("fill ts=%d want day-3", trades[0].Ts)
	}
}

// TestPaperTrader_CostChargedOnEntry: after entering, equity < starting cash by
// exactly the entry cost (marked at the same fill bar's close == open here so
// there's no price move to confound the cost).
func TestPaperTrader_CostChargedOnEntry(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	// Fill bar day-3 has open==close==100 so mark==fill price (isolate the cost).
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	curve, _ := st.PaperEquityCurve(ctx, "flagship-1d", 10)
	if len(curve) == 0 {
		t.Fatal("no equity mark")
	}
	start := papertrade.StartingCash()
	eq := curve[len(curve)-1].Equity
	// The only P&L on this bar is the entry cost (fill price == mark price). So
	// equity = start - entryCost, and entryCost must equal the trade's recorded
	// cost (fill price == mark, no price move to confound it).
	if eq >= start {
		t.Fatalf("equity %v should be below start %v (entry cost)", eq, start)
	}
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	if len(trades) != 1 {
		t.Fatalf("want exactly one entry trade, got %d", len(trades))
	}
	loss := start - eq
	if diff := loss - trades[0].Cost; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("equity loss=%v must equal the recorded entry cost=%v", loss, trades[0].Cost)
	}
	// And the cost must be positive and equal notional*costFraction.
	c := papertrade.CostBpsFor(md.Stocks) / 1e4
	wantCost := trades[0].Qty * trades[0].Px * c
	if diff := trades[0].Cost - wantCost; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("recorded cost=%v want notional*c=%v", trades[0].Cost, wantCost)
	}
}

// TestPaperTrader_PnLAcrossSequence scripts enter -> price up -> exit and checks
// realized P&L: buy at 100, sell at 120, minus both-side costs, ends above start.
func TestPaperTrader_PnLAcrossSequence(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	w := &PaperTrader{St: st}

	// Phase 1: days 1..3, strong-long prediction at day2 -> buy at day3 open=100.
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100}, // fill open = 100
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run1: %v", err)
	}
	pos, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID)
	if !ok || pos.AvgPx != 100 {
		t.Fatalf("phase1 position: ok=%v avgPx=%v", ok, pos.AvgPx)
	}
	qty := pos.Qty

	// Phase 2: price rises to 120; a strong-FLAT prediction at day4 -> sell at
	// day5 open=120.
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{4, 120, 120}, // prediction stamped here
		{5, 120, 120}, // exit fill open = 120
	})
	seedPrediction(t, st, sym.ID, md.H1d, 4*86400, 0.10) // strong flat
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("phase2 should be flat after strong-flat signal")
	}

	// Realized: bought qty@100 (paid entry cost), sold qty@120 (paid exit cost).
	cur, _, _ := st.PaperCursor(ctx, "flagship-1d")
	start := papertrade.StartingCash()
	c := papertrade.CostBpsFor(md.Stocks) / 1e4
	entryNotional := qty * 100
	exitNotional := qty * 120
	// cash after buy = start - entryNotional - entryNotional*c
	// cash after sell = that + exitNotional - exitNotional*c
	wantCash := start - entryNotional - entryNotional*c + exitNotional - exitNotional*c
	if diff := cur.Cash - wantCash; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("final cash=%v want %v (buy@100, sell@120, both-side costs)", cur.Cash, wantCash)
	}
	if cur.Cash <= start {
		t.Fatalf("a +20%% move should net positive after costs: cash=%v start=%v", cur.Cash, start)
	}
}

// TestPaperTrader_Idempotent: re-running with NO new bar does not trade again;
// the trade log and cursor are unchanged.
func TestPaperTrader_Idempotent(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run1: %v", err)
	}
	tradesAfter1, _ := st.PaperTrades(ctx, "flagship-1d", 100)
	curveAfter1, _ := st.PaperEquityCurve(ctx, "flagship-1d", 100)
	cur1, _, _ := st.PaperCursor(ctx, "flagship-1d")

	// Re-run several times with no new bars — must be a pure no-op.
	for i := 0; i < 3; i++ {
		if _, err := w.Run(ctx); err != nil {
			t.Fatalf("re-run %d: %v", i, err)
		}
	}
	tradesAfter2, _ := st.PaperTrades(ctx, "flagship-1d", 100)
	curveAfter2, _ := st.PaperEquityCurve(ctx, "flagship-1d", 100)
	cur2, _, _ := st.PaperCursor(ctx, "flagship-1d")

	if len(tradesAfter2) != len(tradesAfter1) {
		t.Fatalf("idempotency: trades grew %d -> %d", len(tradesAfter1), len(tradesAfter2))
	}
	if len(curveAfter2) != len(curveAfter1) {
		t.Fatalf("idempotency: equity marks grew %d -> %d", len(curveAfter1), len(curveAfter2))
	}
	if cur2.LastBarTs != cur1.LastBarTs || cur2.Cash != cur1.Cash {
		t.Fatalf("idempotency: cursor changed %+v -> %+v", cur1, cur2)
	}
}

// TestPaperTrader_DeadbandHolds: a mid-range prediction (between flat and long)
// must NOT open a position.
func TestPaperTrader_DeadbandHolds(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.50) // deadband

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("deadband prediction must not open a position")
	}
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	if len(trades) != 0 {
		t.Fatalf("expected no trades in deadband, got %d", len(trades))
	}
}

// TestPaperTrader_NoFillBarYetWaits: a strong-long prediction whose ts is the
// LATEST bar (no bar strictly after it) must NOT fill — there is no next-bar
// open yet. This is the live-edge no-lookahead case.
func TestPaperTrader_NoFillBarYetWaits(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
	})
	// Prediction stamped at the LAST bar's ts — no bar strictly after it.
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.95)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("must not fill when no bar exists strictly after the prediction")
	}
}
