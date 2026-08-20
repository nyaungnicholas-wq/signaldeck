package pipeline

import (
	"context"
	"strings"
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

// seedGoodForecast stores a return-distribution forecast with clearly positive
// cost-adjusted EV, so the EV decision engine (internal/ev) lets the entry
// through and the test under it exercises what it was written to exercise. The
// engine's own refusal branches are proven in evgate_test.go / internal/ev.
func seedGoodForecast(t *testing.T, st *store.Store, id int64, h md.Horizon, ts int64) {
	t.Helper()
	if err := st.UpsertReturnForecast(context.Background(), store.ReturnForecast{
		SymbolID: id, Horizon: h, Ts: ts, Regime: "calm", N: 200,
		Tau: 0.002, Mean: 0.012, Sigma: 0.02, Q10: -0.01, Q50: 0.01, Q90: 0.03,
		PUp: 0.55, PDown: 0.15, PInside: 0.30, Edge: 0.40, ExpectedValue: 0.010,
	}); err != nil {
		t.Fatalf("seed forecast: %v", err)
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
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

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

// TestPaperTrader_ExitsPositionInDeactivatedSymbol: a position held in a symbol
// that later leaves the ACTIVE universe must still be reachable by the exit path.
//
// Before the fix, Run walked only ListSymbols(ctx, true) and buildStep evaluated
// every exit inside that walk, so a pruned-but-held name was never examined:
// no stop, no take-profit, no horizon expiry, no probability flip, no kill-switch
// flatten could close it. The book could open a position it was structurally
// unable to exit. This test fails (position still open) without the exit-only
// re-admission in Run.
func TestPaperTrader_ExitsPositionInDeactivatedSymbol(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	keep, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "") // stays active: holds the as-of clock up
	drop, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "") // gets pruned while held

	// Both names need bars on the same days so the as-of clock covers the exit bar.
	for _, id := range []int64{keep.ID, drop.ID} {
		seedDailyPx(t, st, id, [][3]float64{
			{1, 100, 101},
			{2, 102, 103}, // entry prediction stamped here
			{3, 110, 111}, // entry fills at this open
		})
	}
	seedPrediction(t, st, drop.ID, md.H1d, 2*86400, 0.90) // strong long -> open a position
	seedGoodForecast(t, st, drop.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", drop.ID); !ok {
		t.Fatal("setup failed: expected an open position in BBB after a strong-long prediction")
	}

	// BBB leaves the universe while the book still holds it — the exact situation
	// a delisting or a universe rotation produces.
	if err := st.SetSymbolActive(ctx, drop.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// A new day arrives, and the signal flips hard to the downside. Every risk
	// control in the book agrees this position should close.
	for _, id := range []int64{keep.ID, drop.ID} {
		seedDailyPx(t, st, id, [][3]float64{{4, 60, 59}}) // -46% gap: flip AND stop
	}
	seedPrediction(t, st, drop.ID, md.H1d, 3*86400, 0.02) // decisive short
	seedGoodForecast(t, st, drop.ID, md.H1d, 3*86400)

	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}

	_, stillOpen, err := st.PaperPosition(ctx, "flagship-1d", drop.ID)
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if stillOpen {
		t.Fatal("position in a DEACTIVATED symbol was never exited: buildStep only " +
			"walks the active universe, so a held name pruned from it is unreachable " +
			"by every barrier, the probability flip and the kill-switch flatten alike")
	}

	// And the exit must be a real, ledgered sell — not a silently dropped position.
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	var sold bool
	for _, tr := range trades {
		if tr.SymbolID == drop.ID && tr.Side == "sell" {
			sold = true
		}
	}
	if !sold {
		t.Fatalf("expected a ledgered SELL closing BBB; trades=%+v", trades)
	}
}

// TestPaperTrader_DeactivatedSymbolNeverEntered: the exit-only re-admission must
// not become an entry-path bug. A symbol outside the active universe may be
// walked so its OPEN position stays reachable, but it must never be bought.
//
// THE PATH THIS MUST EXERCISE IS CROSS-STRATEGY, and getting that wrong makes the
// test vacuous. `syms` is built once in Run and shared by every strategy, while
// PaperPositions is per-strategy. A symbol held by flagship-1d is re-admitted into
// that shared slice, and flagship-1w then walks it with hasPos == FALSE — which is
// the only way execution reaches the entry path for an inactive symbol at all
// (a held symbol always `continue`s out of the exit block first).
//
// An earlier version of this test seeded an inactive, UNHELD symbol. That symbol
// was never re-admitted, so the loop never saw it and the test passed with the
// `!s.Active` guard deleted — vacuous, and exactly the defect this file's
// TestTradingDayAtET-style guards exist to prevent. Verified: with the guard
// removed, this version FAILS and the old one PASSED.
func TestPaperTrader_DeactivatedSymbolNeverEntered(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	keep, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	gone, _ := st.UpsertSymbol(ctx, "ZZZ", md.Stocks, "")

	for _, id := range []int64{keep.ID, gone.ID} {
		seedDailyPx(t, st, id, [][3]float64{{1, 100, 101}, {2, 102, 103}, {3, 110, 111}})
	}

	// Step 1: ZZZ is ACTIVE and flagship-1d opens a position in it.
	seedPrediction(t, st, gone.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, gone.ID, md.H1d, 2*86400)
	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", gone.ID); !ok {
		t.Fatal("setup failed: flagship-1d should hold ZZZ before it is dropped")
	}

	// Step 2: the universe drops ZZZ. It is now held by flagship-1d, so Run
	// re-admits it into the SHARED syms slice — where flagship-1w will meet it
	// holding no position of its own.
	if err := st.SetSymbolActive(ctx, gone.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// Step 3: a screaming-long 1w signal on the dropped name. flagship-1w has no
	// position in it, so without the !s.Active guard the entry path buys it.
	for _, id := range []int64{keep.ID, gone.ID} {
		seedDailyPx(t, st, id, [][3]float64{{4, 112, 113}})
	}
	seedPrediction(t, st, gone.ID, md.H1w, 3*86400, 0.99)
	seedGoodForecast(t, st, gone.ID, md.H1w, 3*86400)

	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}

	if _, ok, _ := st.PaperPosition(ctx, "flagship-1w", gone.ID); ok {
		t.Fatal("flagship-1w opened a NEW position in a symbol the universe had already " +
			"dropped: exit-only re-admission leaked into the entry path across strategies")
	}
	trades, _ := st.PaperTrades(ctx, "flagship-1w", 20)
	for _, tr := range trades {
		if tr.SymbolID == gone.ID && tr.Side == "buy" {
			t.Fatalf("flagship-1w BOUGHT a dropped symbol: %+v", tr)
		}
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
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

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
	// The charge is now spread PLUS square-root-law market impact, so it must
	// STRICTLY EXCEED the old flat spread-only assumption. Asserting equality
	// with the constant is what let the book's fills beat their own cost model.
	spreadOnly := trades[0].Qty * trades[0].Px * papertrade.CostBpsFor(md.Stocks) / 1e4
	if trades[0].Cost <= spreadOnly {
		t.Fatalf("recorded cost=%v is not above the spread-only charge=%v; impact was not applied",
			trades[0].Cost, spreadOnly)
	}
	// And the fill price must BE the stored bar's open, exactly — the property
	// that makes the trade log reconcilable against the bars.
	bar, okBar, _ := st.BarAtOrBefore(ctx, sym.ID, md.TF1d, trades[0].Ts)
	if !okBar || bar.Ts != trades[0].Ts {
		t.Fatalf("no stored bar at the fill ts %d", trades[0].Ts)
	}
	if trades[0].Px != bar.Open {
		t.Fatalf("fill px=%v != stored bar open=%v", trades[0].Px, bar.Open)
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
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)
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
	// Costs are modelled per fill now, so reconcile against the RECORDED costs
	// rather than a constant — the identity being checked is that the cash the
	// book holds equals the cash the trade log says it should.
	cur, _, _ := st.PaperCursor(ctx, "flagship-1d")
	start := papertrade.StartingCash()
	trades, _ := st.AllPaperTradesAsc(ctx, "flagship-1d")
	if len(trades) != 2 {
		t.Fatalf("want a buy and a sell, got %d fills", len(trades))
	}
	entryNotional := qty * 100
	exitNotional := qty * 120
	wantCash := start - entryNotional - trades[0].Cost + exitNotional - trades[1].Cost
	if diff := cur.Cash - wantCash; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("final cash=%v want %v (buy@100, sell@120, both-side costs)", cur.Cash, wantCash)
	}
	// Both sides must have paid more than the spread-only constant.
	for i, n := range []float64{entryNotional, exitNotional} {
		spreadOnly := n * papertrade.CostBpsFor(md.Stocks) / 1e4
		if trades[i].Cost <= spreadOnly {
			t.Errorf("fill %d cost=%v not above spread-only %v", i, trades[i].Cost, spreadOnly)
		}
	}
	if cur.Cash <= start {
		t.Fatalf("a +20%% move should net positive after costs: cash=%v start=%v", cur.Cash, start)
	}
}

// TestPaperTrader_Idempotent: re-running with NO new bar does not trade again;
// the trade log and cursor are unchanged.
// TestPaperTrader_SkipsWhenLiquidityIsUnknown: with no traded volume on record
// there is no way to price a fill's market impact or bound its size. The old
// engine filled anyway, at the bar open, with a flat cost — the most optimistic
// answer available, applied by default. The worker must now decline.
func TestPaperTrader_SkipsWhenLiquidityIsUnknown(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "NOVOL", md.Stocks, "")

	// Same shape as the other fixtures, but every bar has zero volume.
	bars := []md.Bar{}
	for d, o := range map[int64]float64{1: 100, 2: 102, 3: 110} {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: d * 86400,
			Open: o, High: o * 1.01, Low: o * 0.99, Close: o, Volume: 0,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.95)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	if len(trades) != 0 {
		t.Fatalf("filled %d trade(s) on a symbol with no volume on record; want none", len(trades))
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("opened a position that could not be priced")
	}
}

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
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

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

// TestPaperTrader_NeverFillsBehindTheBooksClock: a step transacts only inside the
// window it advances over, (cursor.LastBarTs, asof]. A prediction left behind by a
// starved stretch must NOT fill at a bar the book has already marched past.
//
// LatestPrediction returns the newest row with n_used > 0. While the 1d model is
// retired most fresh rows carry n_used = 0, so the query resolves to a weeks-old
// prediction whose next bar sits far behind the cursor. Before the fill-window
// bound, the entry was booked at that old open while every decision input around
// it — the return forecast, corrToBook, the riskgate book — was measured at asof,
// and markPositions immediately marked it at the asof close. That booked the whole
// intervening move as one step's P&L. It happened 46 times in the live book: fills
// 94-102 all landed on 2026-07-20 immediately after fill 93 landed on 2026-08-11,
// and flagship-1d equity printed 99,491.93 -> 103,218.71 -> 98,745.79 on what was
// really a small loss.
func TestPaperTrader_NeverFillsBehindTheBooksClock(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	aaa, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	bbb, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")

	// Step 1 — only days 1..3 exist. AAA carries a fresh day-2 prediction, fills
	// at day 3's open, and drags the strategy cursor up to day 3.
	early := [][3]float64{{1, 100, 101}, {2, 102, 103}, {3, 110, 111}}
	seedDailyPx(t, st, aaa.ID, early)
	seedDailyPx(t, st, bbb.ID, early)
	seedPrediction(t, st, aaa.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, aaa.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	cur, _, err := st.PaperCursor(ctx, "flagship-1d")
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cur.LastBarTs != 3*86400 {
		t.Fatalf("cursor=%d want day-3 (%d) after step 1", cur.LastBarTs, 3*86400)
	}

	// BBB's ONLY prediction is of the same day-2 vintage the cursor has passed.
	seedPrediction(t, st, bbb.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, bbb.ID, md.H1d, 2*86400)

	// Step 2 — the market runs on to day 12 at a much higher level, so a
	// back-dated day-3 fill would show up as an enormous instant gain.
	late := make([][3]float64, 0, 9)
	for d := 4; d <= 12; d++ {
		late = append(late, [3]float64{float64(d), 200, 201})
	}
	seedDailyPx(t, st, aaa.ID, late)
	seedDailyPx(t, st, bbb.ID, late)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}

	trades, err := st.PaperTrades(ctx, "flagship-1d", 50)
	if err != nil {
		t.Fatalf("trades: %v", err)
	}
	for _, tr := range trades {
		if tr.SymbolID == bbb.ID && tr.Ts <= 3*86400 {
			t.Fatalf("BBB filled at ts=%d (day %d), at or behind the cursor at day 3: "+
				"a back-dated fill books the day-3 -> day-12 move as instant P&L",
				tr.Ts, tr.Ts/86400)
		}
	}
}

// A WANTED exit the execution model cannot price must be REPORTED, not dropped.
//
// planExit can decide a position must go — a stop, a target, an expiry, a
// kill-switch flatten — and ExitLong can still refuse, because with no average
// daily dollar volume impact and capacity are both unknowable. The old code did
// a bare `continue`: the position stayed open past its own exit, nothing was
// ledgered, no counter moved, and the worker reported a clean pass. The only
// trace that a stop had fired and been dropped was that the position still
// existed.
//
// Fixture: enter on liquid bars, then have the name's volume vanish (a halt or a
// vendor outage — the live corpus has 24,863 such all-zero windows across 127
// symbols) so advUSD returns 0 at the exit's fill bar.
func TestPaperTrader_StrandedExitIsReportedNotSwallowed(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	seedDailyPx(t, st, sym.ID, [][3]float64{{1, 100, 100}, {2, 100, 100}, {3, 100, 100}})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if _, held, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); !held {
		t.Fatal("fixture broken: AAA should hold after pass 1")
	}

	// The name goes dark: every bar in the ADV window now carries zero volume,
	// including the day-4 bar the exit would fill on.
	dark := make([]md.Bar, 0, 4)
	for d := int64(1); d <= 4; d++ {
		dark = append(dark, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: d * 86400,
			Open: 100, High: 100.1, Low: 99.9, Close: 100, Volume: 0,
		})
	}
	if err := st.UpsertBars(ctx, dark); err != nil {
		t.Fatalf("seed zero-volume bars: %v", err)
	}
	seedPrediction(t, st, sym.ID, md.H1d, 3*86400, 0.05) // flip flat: the book wants out

	status, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if _, stillHeld, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); !stillHeld {
		t.Fatal("fixture broken: ExitLong priced an exit with no ADV, so nothing was stranded")
	}
	if !strings.Contains(status, "STRANDED") {
		t.Fatalf("a wanted exit was dropped silently; status=%q must report it as STRANDED", status)
	}
}
