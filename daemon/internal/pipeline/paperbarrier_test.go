package pipeline

// Pipeline-level proof of the triple-barrier exits: the REAL worker, a real
// store, and the two guarantees that matter — a barrier fills at a strictly
// later OPEN than the CLOSE that confirmed it, and a wick through a barrier
// does not exit.

import (
	"context"
	"encoding/json"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedOHLC writes daily bars with explicit highs and lows, which seedDailyPx
// cannot express — and a wick is exactly a high or low that the close does not
// follow, so these tests need the real four.
func seedOHLC(t *testing.T, st *store.Store, id int64, rows [][5]float64) {
	t.Helper()
	bars := make([]md.Bar, 0, len(rows))
	for _, r := range rows {
		bars = append(bars, md.Bar{
			SymbolID: id, TF: md.TF1d, Ts: int64(r[0]) * 86400,
			Open: r[1], High: r[2], Low: r[3], Close: r[4], Volume: 1_000_000,
		})
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

// barrierSnapshot is the ledgered barrier, read back out of inputs_json.
type barrierSnapshot struct {
	Barrier struct {
		Kind      string  `json:"kind"`
		TriggerTs int64   `json:"triggerTs"`
		Level     float64 `json:"level"`
		ClosePx   float64 `json:"closePx"`
		ATR       float64 `json:"atr"`
		HeldBars  int     `json:"heldBars"`
	} `json:"barrier"`
	FillTs int64 `json:"fillTs"`
}

func readBarrier(t *testing.T, inputsJSON string) barrierSnapshot {
	t.Helper()
	var s barrierSnapshot
	if err := json.Unmarshal([]byte(inputsJSON), &s); err != nil {
		t.Fatalf("inputs_json: %v", err)
	}
	return s
}

// THE NO-LOOKAHEAD GUARANTEE, end to end. The exit is confirmed by a close and
// filled at a strictly later open — never on the bar that triggered it.
func TestBarrier_FillsAtAStrictlyLaterOpen(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	// Days 1-2 build volatility; the prediction on day 2 fills the entry at the
	// OPEN of day 3. One pass per new bar, exactly as the daemon runs: a
	// position opened in a pass is not visible for exit until the next one.
	seedOHLC(t, st, sym.ID, [][5]float64{
		{1, 100, 101, 99, 100},
		{2, 100, 101, 99, 100},
		{3, 100, 101, 99, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); !ok {
		t.Fatal("setup: the entry did not fill")
	}

	// Day 3's close already resolved the 1-bar horizon. Day 4's open is where
	// the exit fills, and it is deliberately a price nothing else uses.
	seedOHLC(t, st, sym.ID, [][5]float64{{4, 88.88, 89, 88, 88.5}})
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("exit run: %v", err)
	}

	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("the 1-bar horizon expired on day 3's close — the position must be closed")
	}

	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	var sell *store.PaperTrade
	for i := range trades {
		if trades[i].Side == "sell" {
			sell = &trades[i]
		}
	}
	if sell == nil {
		t.Fatalf("no exit trade, got %+v", trades)
	}
	if sell.Ts != 4*86400 {
		t.Fatalf("exit filled on ts %d, want day 4 — the bar STRICTLY AFTER the close that confirmed it", sell.Ts)
	}
	// The recorded price is day 4's OPEN exactly, so the log stays reconcilable
	// against the bars. Day 3's close was 100; filling there would be lookahead.
	if sell.Px != 88.88 {
		t.Fatalf("exit price %v, want day 4's open 88.88 — a fill at day 3's close would be lookahead", sell.Px)
	}

	// And the ledger proves it: fillTs strictly after the trigger close.
	decs, _ := st.EVDecisions(ctx, "SELL", "AAA", 10)
	if len(decs) != 1 || decs[0].Reason != "barrier-expiry" {
		t.Fatalf("want one ledgered barrier-expiry SELL, got %+v", decs)
	}
	snap := readBarrier(t, decs[0].InputsJSON)
	if snap.Barrier.TriggerTs != 3*86400 {
		t.Fatalf("trigger ts %d, want day 3's close", snap.Barrier.TriggerTs)
	}
	if snap.FillTs <= snap.Barrier.TriggerTs {
		t.Fatalf("fillTs %d must be STRICTLY after triggerTs %d", snap.FillTs, snap.Barrier.TriggerTs)
	}
}

// A 1-day forecast implies a 1-bar hold, so on flagship-1d the horizon expiry
// is reached before any probability flip can matter. Stated as a test because
// it is a real behavioural consequence, not an accident.
func TestBarrier_HorizonExpiryDominatesTheOneDayBook(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	seedOHLC(t, st, sym.ID, [][5]float64{
		{1, 100, 101, 99, 100},
		{2, 100, 101, 99, 100},
		{3, 100, 101, 99, 100},
	})
	// The signal stays STRONGLY LONG throughout — nothing here would ever
	// trigger a flip exit.
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.95)
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	seedOHLC(t, st, sym.ID, [][5]float64{{4, 100, 101, 99, 100}, {5, 100, 101, 99, 100}})
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("exit run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("a 1-day forecast must not leave a position open past its horizon, however bullish the signal stays")
	}
	decs, _ := st.EVDecisions(ctx, "SELL", "BBB", 10)
	if len(decs) != 1 || decs[0].Reason != "barrier-expiry" {
		t.Fatalf("want the time stop to close it, got %+v", decs)
	}
	if snap := readBarrier(t, decs[0].InputsJSON); snap.Barrier.HeldBars != 1 {
		t.Fatalf("heldBars = %d, want 1 for a 1-day horizon", snap.Barrier.HeldBars)
	}
}

// THE WICK RULE, end to end. A bar that trades far through the stop and closes
// back above it is not an exit. On the weekly book the horizon is 5 bars, so
// the stop has room to be the thing under test.
func TestBarrier_WickThroughTheStopDoesNotExitTheBook(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")

	// Quiet 2-point ranges give ATR ≈ 2, so the stop sits near 96 and the target
	// near 106. Day 3 spikes down to 80 — far through the stop — and closes at
	// 100, untouched.
	seedOHLC(t, st, sym.ID, [][5]float64{
		{1, 100, 101, 99, 100},
		{2, 100, 101, 99, 100},
		{3, 100, 101, 80, 100},
		{4, 100, 101, 99, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1w, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1w, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1w", sym.ID); !ok {
		t.Fatal("a WICK through the stop must not close the position — only a CLOSE beyond it does")
	}
	if decs, _ := st.EVDecisions(ctx, "SELL", "CCC", 10); len(decs) != 0 {
		t.Fatalf("no exit should have been ledgered, got %+v", decs)
	}
}

// And the stop DOES fire when a close goes through it — same setup, one bar
// changed, so the difference is unambiguously the close and not the wick.
func TestBarrier_CloseThroughTheStopExitsTheBook(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "DDD", md.Stocks, "")
	seedOHLC(t, st, sym.ID, [][5]float64{
		{1, 100, 101, 99, 100},
		{2, 100, 101, 99, 100},
		{3, 100, 101, 99, 100}, // entry fills at this open; ATR 2 → stop ~96
	})
	seedPrediction(t, st, sym.ID, md.H1w, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1w, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1w", sym.ID); !ok {
		t.Fatal("setup: the entry did not fill")
	}

	seedOHLC(t, st, sym.ID, [][5]float64{
		{4, 100, 101, 90, 91},    // CLOSES at 91, through the stop
		{5, 90.5, 91, 90, 90.75}, // the fill bar
	})
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("exit run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1w", sym.ID); ok {
		t.Fatal("a close through the stop must exit")
	}
	decs, _ := st.EVDecisions(ctx, "SELL", "DDD", 10)
	if len(decs) != 1 || decs[0].Reason != "barrier-adverse" {
		t.Fatalf("want a ledgered barrier-adverse SELL, got %+v", decs)
	}
	snap := readBarrier(t, decs[0].InputsJSON)
	if snap.Barrier.TriggerTs != 4*86400 || snap.FillTs != 5*86400 {
		t.Fatalf("trigger/fill = %d/%d, want day 4's close and day 5's open", snap.Barrier.TriggerTs, snap.FillTs)
	}
	// The gap is paid in full: the fill is day 5's open, not the stop level.
	trades, _ := st.PaperTrades(ctx, "flagship-1w", 10)
	for _, tr := range trades {
		if tr.Side == "sell" && tr.Px != 90.5 {
			t.Fatalf("exit price %v, want day 5's open 90.5 — a daily-bar stop pays the gap, it does not fill at the level", tr.Px)
		}
	}
}

// The favorable barrier fires the same way.
func TestBarrier_CloseThroughTheTargetExitsTheBook(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "EEE", md.Stocks, "")
	seedOHLC(t, st, sym.ID, [][5]float64{
		{1, 100, 101, 99, 100},
		{2, 100, 101, 99, 100},
		{3, 100, 101, 99, 100}, // entry fills at this open; ATR 2 → target ~106
	})
	seedPrediction(t, st, sym.ID, md.H1w, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1w, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	seedOHLC(t, st, sym.ID, [][5]float64{
		{4, 100, 110, 99, 109}, // CLOSES at 109, through the target
		{5, 108, 109, 107, 108},
	})
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("exit run: %v", err)
	}
	decs, _ := st.EVDecisions(ctx, "SELL", "EEE", 10)
	if len(decs) != 1 || decs[0].Reason != "barrier-favorable" {
		t.Fatalf("want a ledgered barrier-favorable SELL, got %+v", decs)
	}
}

// Barriers are a risk control, so they must fire on a position whose symbol has
// stopped producing predictions entirely. A stop that only works when the model
// has an opinion is not a stop.
func TestBarrier_FiresWithoutAFreshPrediction(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "FFF", md.Stocks, "")
	seedOHLC(t, st, sym.ID, [][5]float64{
		{1, 100, 101, 99, 100},
		{2, 100, 101, 99, 100},
		{3, 100, 101, 99, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1w, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1w, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1w", sym.ID); !ok {
		t.Fatal("setup: the entry did not fill")
	}

	// Five more bars, and NOT ONE new prediction. The weekly horizon expires
	// anyway.
	seedOHLC(t, st, sym.ID, [][5]float64{
		{4, 100, 101, 99, 100},
		{5, 100, 101, 99, 100},
		{6, 100, 101, 99, 100},
		{7, 100, 101, 99, 100},
		{8, 100, 101, 99, 100},
		{9, 100, 101, 99, 100},
	})
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("exit run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1w", sym.ID); ok {
		t.Fatal("the horizon expired with no fresh prediction — the position must still have closed")
	}
}

// The switch turns them off, and with them off the old probability-flip
// behaviour is exactly what it was.
func TestBarrier_DisabledRestoresTheFlipOnlyBook(t *testing.T) {
	t.Setenv("SIGNALDECK_PAPER_BARRIERS", "false")
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "GGG", md.Stocks, "")
	seedOHLC(t, st, sym.ID, [][5]float64{
		{1, 100, 101, 99, 100},
		{2, 100, 101, 99, 100},
		{3, 100, 101, 99, 100},
		{4, 100, 101, 99, 100},
		{5, 100, 101, 99, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.95)
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); !ok {
		t.Fatal("with barriers off, a still-bullish 1d position must stay open")
	}
}
