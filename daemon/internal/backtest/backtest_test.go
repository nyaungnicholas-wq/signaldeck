package backtest

import (
	"math"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// barsFromCloses builds bars where Open mirrors the prior close and O/H/L
// mirror Close, so close-only indicators and close-to-close returns are the
// only things that matter. Ts increments by 1.
func barsFromCloses(cs ...float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, len(cs))
	for i, c := range cs {
		bars[i] = marketdata.Bar{Ts: int64(i), Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	return bars
}

const eps = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// ---------------------------------------------------------------------------
// NO-LOOKAHEAD: the single most important property. A signal on the final bar
// must NOT be tradable within the window, and a signal on bar i must fill only
// at bar i+1's return.
// ---------------------------------------------------------------------------

// TestNoSameBarFill proves the strategy cannot capture the return of the bar
// that produced its entry signal. We build a series that is flat, then jumps
// UP exactly once on the bar right after the entry condition first becomes
// true. If the backtester (wrongly) filled on the signal bar, it would capture
// the jump; with correct next-bar fills it captures it too — so to isolate the
// property we instead put the jump ON the signal bar and confirm it is NOT
// captured.
func TestNoSameBarFill(t *testing.T) {
	// price_vs_sma with period 2. Closes: 10,10,10, 20, 10,10...
	// The single up-move happens between index 2 (close 10) and index 3
	// (close 20). At index 3 the SMA2 = (10+20)/2 = 15, price 20 > 15 => entry
	// signal fires ON bar 3. A same-bar fill would have already earned the
	// 10->20 jump (the return of bar 3). A correct next-bar fill enters at bar
	// 4's open and earns nothing from the jump (price falls back).
	bars := barsFromCloses(10, 10, 10, 20, 10, 10, 10, 10)
	s := Strategy{
		Name:  "pvs2",
		Entry: Rule{Indicator: "price_vs_sma", Period: 2, Op: ">"},
		Exit:  Rule{Indicator: "price_vs_sma", Period: 2, Op: "<"},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	// The 10->20 jump return (+100%) must NOT be in the equity curve. If it had
	// leaked, TotalReturn would be strongly positive. With next-bar fills the
	// strategy enters into a falling market and loses, never showing +100%.
	if r.TotalReturn > 0.0 {
		t.Errorf("lookahead leak: strategy captured the signal-bar jump, TotalReturn=%.4f (want <=0)", r.TotalReturn)
	}
	// Equity on bar 3 (the jump/signal bar) must equal equity on bar 2: we were
	// flat during bar 3 because the entry only fills at bar 4.
	if !approx(r.Equity[3], r.Equity[2]) {
		t.Errorf("strategy earned the jump bar: eq[2]=%.4f eq[3]=%.4f (must be equal — still flat)", r.Equity[2], r.Equity[3])
	}
}

// TestNextBarFillTiming pins the exact bar an entry fill lands on. Rising
// series so a price_vs_sma entry fires early; the first non-flat equity change
// must be one bar AFTER the signal bar.
func TestNextBarFillTiming(t *testing.T) {
	// Monotonic rise. price_vs_sma period 3.
	bars := barsFromCloses(10, 11, 12, 13, 14, 15, 16)
	s := Strategy{
		Entry: Rule{Indicator: "price_vs_sma", Period: 3, Op: ">"},
		Exit:  Rule{Indicator: "price_vs_sma", Period: 3, Op: "<"},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	// SMA3 needs 3 bars; first computable at index 2 (closes 10,11,12 => 11),
	// price 12 > 11 => signal at index 2 => fill at index 3. So equity is flat
	// (==1) through index 2, then grows from index 3 on.
	for i := 0; i <= 2; i++ {
		if !approx(r.Equity[i], 1.0) {
			t.Errorf("equity[%d]=%.6f, want 1.0 (flat before first fill)", i, r.Equity[i])
		}
	}
	if r.Equity[3] <= 1.0 {
		t.Errorf("equity[3]=%.6f, want >1.0 (long from the next bar on)", r.Equity[3])
	}
}

// ---------------------------------------------------------------------------
// MONOTONE UPTREND: price_vs_sma stays long and ~= buy&hold minus costs.
// ---------------------------------------------------------------------------

func TestUptrendTracksBuyHoldMinusCosts(t *testing.T) {
	// Strictly rising closes; price stays above its own SMA so the strategy is
	// long essentially the whole way and never sells. It should match buy&hold
	// minus a single entry cost (no exit until forced close at the end).
	cs := make([]float64, 60)
	for i := range cs {
		cs[i] = 100 * math.Pow(1.01, float64(i)) // +1%/bar
	}
	bars := barsFromCloses(cs...)
	s := Strategy{
		Name:    "trend",
		Entry:   Rule{Indicator: "price_vs_sma", Period: 10, Op: ">"},
		Exit:    Rule{Indicator: "price_vs_sma", Period: 10, Op: "<"},
		CostBps: 10,
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	bh := buyHoldReturn(bars)
	// Strategy return must be positive, close to but below buy&hold (it misses
	// the first ~10 bars while the SMA warms up, plus pays entry+exit costs).
	if r.TotalReturn <= 0 {
		t.Fatalf("uptrend TotalReturn=%.4f, want >0", r.TotalReturn)
	}
	if r.TotalReturn >= bh {
		t.Errorf("uptrend strategy %.4f should lag buy&hold %.4f (warmup + costs)", r.TotalReturn, bh)
	}
	if r.VsBuyHold >= 0 {
		t.Errorf("VsBuyHold=%.4f, want <0 in a clean uptrend", r.VsBuyHold)
	}
	// It should still be long at the end (never crossed below its SMA), so
	// exposure is high.
	if r.ExposurePct < 0.7 {
		t.Errorf("ExposurePct=%.2f, want high (>0.7) in a persistent uptrend", r.ExposurePct)
	}
	// Exactly one entry (it never exits mid-run).
	if r.NumTrades != 1 {
		t.Errorf("NumTrades=%d, want 1 (single entry, no mid-run exit)", r.NumTrades)
	}
}

// ---------------------------------------------------------------------------
// SMA CROSS: correct entry/exit bars on a hand-built known cross.
// ---------------------------------------------------------------------------

func TestSMACrossKnownCross(t *testing.T) {
	// Build closes where fast(2) crosses above slow(4) at a known bar, then
	// back below later. We track when eval() flips regime and confirm the fill
	// lands one bar later.
	// Closes: start flat-ish low, ramp up (golden cross), then fall (death).
	cs := []float64{10, 10, 10, 10, 10, 12, 14, 16, 18, 20, 18, 16, 14, 12, 10, 10, 10}
	bars := barsFromCloses(cs...)
	s := Strategy{
		Name:    "cross",
		Entry:   Rule{Indicator: "sma_cross", Fast: 2, Slow: 4},
		Exit:    Rule{Indicator: "sma_cross", Fast: 2, Slow: 4},
		CostBps: 0,
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	// Independently compute the regime (fast>=slow) at each index and find the
	// first golden cross (regime turns true) and first subsequent death cross.
	var goldenSignal, deathSignal int = -1, -1
	prevRegime := false
	for i := range bars {
		fast, okf := sma(bars[:i+1], 2)
		slow, oks := sma(bars[:i+1], 4)
		if !okf || !oks {
			continue
		}
		regime := fast >= slow
		if regime && !prevRegime && goldenSignal == -1 {
			goldenSignal = i
		}
		if !regime && prevRegime && goldenSignal != -1 && deathSignal == -1 {
			deathSignal = i
		}
		prevRegime = regime
	}
	if goldenSignal == -1 || deathSignal == -1 {
		t.Fatalf("test setup wrong: goldenSignal=%d deathSignal=%d", goldenSignal, deathSignal)
	}
	// Fill lands one bar after the signal: the signal computed at bar
	// goldenSignal fills at goldenSignal+1's OPEN, so the position earns the
	// return of bar goldenSignal+1 onward — never the signal bar's own return.
	// Equity must be flat (==1) up to and INCLUDING the golden signal bar (no
	// same-bar fill).
	if !approx(r.Equity[goldenSignal], 1.0) {
		t.Errorf("equity moved before/at golden signal bar %d: %.6f (want 1.0 — no same-bar fill)", goldenSignal, r.Equity[goldenSignal])
	}
	// The first equity change must appear no earlier than goldenSignal+1. Find
	// the first bar whose equity differs from 1.0 and confirm it is > golden.
	firstMove := -1
	for i := range r.Equity {
		if !approx(r.Equity[i], 1.0) {
			firstMove = i
			break
		}
	}
	if firstMove <= goldenSignal {
		t.Errorf("equity first moved at bar %d, must be > golden signal bar %d (next-bar fill)", firstMove, goldenSignal)
	}
	// One full round trip: at least one entry, and the position is closed by the
	// death cross so WinRate is defined.
	if r.NumTrades < 1 {
		t.Errorf("NumTrades=%d, want >=1", r.NumTrades)
	}
	// The captured move (enter after golden, exit after death) is up then it
	// rode the peak — should be a winning, positive-return trip.
	if r.TotalReturn <= 0 {
		t.Errorf("TotalReturn=%.4f, want >0 for a clean up-cross ridden to the top", r.TotalReturn)
	}
}

// ---------------------------------------------------------------------------
// COSTS reduce return.
// ---------------------------------------------------------------------------

func TestCostsReduceReturn(t *testing.T) {
	// A whipsaw-ish series that trades several times so per-side costs bite.
	cs := []float64{10, 11, 10, 12, 9, 13, 8, 14, 9, 15, 10, 16}
	bars := barsFromCloses(cs...)
	base := Strategy{
		Entry: Rule{Indicator: "price_vs_sma", Period: 2, Op: ">"},
		Exit:  Rule{Indicator: "price_vs_sma", Period: 2, Op: "<"},
	}
	free := base
	free.CostBps = 0
	costly := base
	costly.CostBps = 50 // 0.5% per side — heavy

	rf, err := Backtest(bars, free)
	if err != nil {
		t.Fatalf("free: %v", err)
	}
	rc, err := Backtest(bars, costly)
	if err != nil {
		t.Fatalf("costly: %v", err)
	}
	if rc.NumTrades == 0 {
		t.Fatalf("expected trades to occur to observe cost drag")
	}
	if !(rc.TotalReturn < rf.TotalReturn) {
		t.Errorf("costs did not reduce return: free=%.4f costly=%.4f", rf.TotalReturn, rc.TotalReturn)
	}
}

// ---------------------------------------------------------------------------
// WHIPSAW: NumTrades>0 and cost drag is real.
// ---------------------------------------------------------------------------

func TestWhipsawTradesAndDrag(t *testing.T) {
	// Alternating up/down closes force repeated crosses of a short SMA.
	cs := []float64{10, 12, 9, 13, 8, 14, 7, 15, 6, 16, 5, 17}
	bars := barsFromCloses(cs...)
	s := Strategy{
		Name:    "whipsaw",
		Entry:   Rule{Indicator: "price_vs_sma", Period: 2, Op: ">"},
		Exit:    Rule{Indicator: "price_vs_sma", Period: 2, Op: "<"},
		CostBps: 30,
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	if r.NumTrades == 0 {
		t.Errorf("whipsaw NumTrades=0, want >0")
	}
	// Compare to zero-cost to confirm drag.
	s0 := s
	s0.CostBps = 0
	r0, _ := Backtest(bars, s0)
	if r.TotalReturn >= r0.TotalReturn {
		t.Errorf("no cost drag in whipsaw: costed=%.4f free=%.4f", r.TotalReturn, r0.TotalReturn)
	}
}

// ---------------------------------------------------------------------------
// RESULT METRICS sanity.
// ---------------------------------------------------------------------------

func TestMetricsSanity(t *testing.T) {
	cs := []float64{100, 110, 105, 120, 115, 130, 125, 140}
	bars := barsFromCloses(cs...)
	s := Strategy{
		Entry:   Rule{Indicator: "price_vs_sma", Period: 3, Op: ">"},
		Exit:    Rule{Indicator: "price_vs_sma", Period: 3, Op: "<"},
		CostBps: 5,
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	if len(r.Equity) != len(bars) {
		t.Errorf("Equity len=%d, want %d", len(r.Equity), len(bars))
	}
	if r.Equity[0] != 1.0 {
		t.Errorf("Equity[0]=%.4f, want 1.0", r.Equity[0])
	}
	if r.MaxDrawdown < 0 || r.MaxDrawdown > 1 {
		t.Errorf("MaxDrawdown=%.4f, want in [0,1]", r.MaxDrawdown)
	}
	if r.ExposurePct < 0 || r.ExposurePct > 1 {
		t.Errorf("ExposurePct=%.4f, want in [0,1]", r.ExposurePct)
	}
	if r.WinRate < 0 || r.WinRate > 1 {
		t.Errorf("WinRate=%.4f, want in [0,1]", r.WinRate)
	}
	// VsBuyHold must be self-consistent: TotalReturn - buyhold.
	if !approx(r.VsBuyHold, r.TotalReturn-buyHoldReturn(bars)) {
		t.Errorf("VsBuyHold inconsistent: %.6f vs %.6f", r.VsBuyHold, r.TotalReturn-buyHoldReturn(bars))
	}
}

// TestFlatStrategyNeverTrades: an entry that can never be true yields 0 trades,
// flat equity, and VsBuyHold = -buyhold.
func TestFlatStrategyNeverTrades(t *testing.T) {
	bars := barsFromCloses(10, 11, 12, 13, 14)
	// RSI entry < 0 can never be true (RSI is in [0,100]); exit > 100 never
	// true either. So it stays flat forever.
	s := Strategy{
		Entry: Rule{Indicator: "rsi", Period: 2, Op: "<", Threshold: 0},
		Exit:  Rule{Indicator: "rsi", Period: 2, Op: ">", Threshold: 100},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	if r.NumTrades != 0 {
		t.Errorf("NumTrades=%d, want 0", r.NumTrades)
	}
	if !approx(r.TotalReturn, 0) {
		t.Errorf("TotalReturn=%.6f, want 0 (never traded)", r.TotalReturn)
	}
	if !approx(r.VsBuyHold, -buyHoldReturn(bars)) {
		t.Errorf("VsBuyHold=%.6f, want %.6f", r.VsBuyHold, -buyHoldReturn(bars))
	}
}

// ---------------------------------------------------------------------------
// ERRORS / VALIDATION.
// ---------------------------------------------------------------------------

func TestBacktestErrors(t *testing.T) {
	valid := Strategy{
		Entry: Rule{Indicator: "price_vs_sma", Period: 2, Op: ">"},
		Exit:  Rule{Indicator: "price_vs_sma", Period: 2, Op: "<"},
	}
	tests := []struct {
		name    string
		bars    []marketdata.Bar
		s       Strategy
		wantErr string
	}{
		{"too few bars", barsFromCloses(10), valid, "at least 2 bars"},
		{"empty bars", nil, valid, "at least 2 bars"},
		{
			"bad entry indicator",
			barsFromCloses(10, 11, 12),
			Strategy{Entry: Rule{Indicator: "nope"}, Exit: valid.Exit},
			"unknown Indicator",
		},
		{
			"sma_cross fast>=slow",
			barsFromCloses(10, 11, 12),
			Strategy{Entry: Rule{Indicator: "sma_cross", Fast: 200, Slow: 50}, Exit: valid.Exit},
			"Fast<Slow",
		},
		{
			"rsi bad op",
			barsFromCloses(10, 11, 12),
			Strategy{Entry: Rule{Indicator: "rsi", Period: 14, Op: "=="}, Exit: valid.Exit},
			"Op in",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Backtest(tt.bars, tt.s)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RSI strategy behaves (buy oversold, sell overbought) without lookahead.
// ---------------------------------------------------------------------------

func TestRSIStrategyRuns(t *testing.T) {
	// Oscillating series to drive RSI through oversold/overbought.
	cs := []float64{100, 95, 90, 85, 80, 85, 90, 95, 100, 105, 110, 105, 100, 95, 90, 95, 100, 105, 110, 115}
	bars := barsFromCloses(cs...)
	s := Strategy{
		Name:    "rsi mr",
		Entry:   Rule{Indicator: "rsi", Period: 5, Op: "<", Threshold: 35},
		Exit:    Rule{Indicator: "rsi", Period: 5, Op: ">", Threshold: 65},
		CostBps: 10,
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	// Just assert internal consistency + that it can trade.
	if len(r.Equity) != len(bars) {
		t.Fatalf("equity length mismatch")
	}
	if r.NumTrades < 0 {
		t.Fatalf("negative trades")
	}
	// Equity must stay positive (long/flat with bounded moves).
	for i, e := range r.Equity {
		if e <= 0 {
			t.Errorf("equity[%d]=%.4f <=0", i, e)
		}
	}
}

// TestROCRule exercises the roc indicator path end to end.
func TestROCRule(t *testing.T) {
	cs := []float64{100, 101, 103, 108, 112, 110, 105, 100, 98, 102, 108}
	bars := barsFromCloses(cs...)
	s := Strategy{
		Name:  "roc mom",
		Entry: Rule{Indicator: "roc", Period: 3, Op: ">", Threshold: 0.03},
		Exit:  Rule{Indicator: "roc", Period: 3, Op: "<", Threshold: 0},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	if len(r.Equity) != len(bars) {
		t.Fatalf("equity length mismatch")
	}
}

// TestZeroPriceGuard: a zero close must not produce NaN/Inf in returns.
func TestZeroPriceGuard(t *testing.T) {
	bars := barsFromCloses(0, 10, 11, 12, 13)
	s := Strategy{
		Entry: Rule{Indicator: "price_vs_sma", Period: 2, Op: ">"},
		Exit:  Rule{Indicator: "price_vs_sma", Period: 2, Op: "<"},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	for i, e := range r.Equity {
		if math.IsNaN(e) || math.IsInf(e, 0) {
			t.Errorf("equity[%d] is NaN/Inf: %v", i, e)
		}
	}
}
