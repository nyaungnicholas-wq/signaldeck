package papertrade

import (
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestDecideTarget_Deadband(t *testing.T) {
	// Defaults: long 0.60, flat 0.40.
	cases := []struct {
		cal  float64
		want Target
	}{
		{0.75, GoLong},
		{0.60, GoLong}, // tie at long threshold -> long
		{0.55, Hold},   // deadband
		{0.50, Hold},   // deadband
		{0.41, Hold},   // deadband
		{0.40, GoFlat}, // tie at flat threshold -> flat
		{0.25, GoFlat},
	}
	for _, c := range cases {
		if got := DecideTarget(c.cal); got != c.want {
			t.Errorf("DecideTarget(%.2f)=%v want %v", c.cal, got, c.want)
		}
	}
}

func TestDecideTarget_EnvOverride(t *testing.T) {
	t.Setenv("SIGNALDECK_PAPER_LONG", "0.70")
	t.Setenv("SIGNALDECK_PAPER_FLAT", "0.30")
	if DecideTarget(0.65) != Hold {
		t.Fatalf("0.65 should be Hold under long=0.70")
	}
	if DecideTarget(0.71) != GoLong {
		t.Fatalf("0.71 should be GoLong under long=0.70")
	}
	if DecideTarget(0.29) != GoFlat {
		t.Fatalf("0.29 should be GoFlat under flat=0.30")
	}
}

func TestCostBpsFor_ByMarket(t *testing.T) {
	// CostBpsFor is now the SPREAD component only; the size-dependent impact
	// component is added by the execution model (execution_test.go).
	if s := CostBpsFor(md.Stocks); s != 7.5 {
		t.Fatalf("stock cost bps=%v want 7.5", s)
	}
	if c := CostBpsFor(md.Crypto); c != 3.5 {
		t.Fatalf("crypto cost bps=%v want 3.5", c)
	}
	// Crypto must be cheaper than stocks by default (tighter spreads).
	if CostBpsFor(md.Crypto) >= CostBpsFor(md.Stocks) {
		t.Fatalf("crypto cost should be < stock cost")
	}
}

// EnterLong must deploy ALL cash including every modelled cost so the book ends
// at exactly zero cash, never negative. The cost is now spread + impact rather
// than a flat rate, so the assertion is on the invariant, not the constant.
func TestEnterLong_FullyFundedNeverNegative(t *testing.T) {
	cash := 100_000.0
	px := 200.0
	in := ExecInputs{
		Bar:    md.Bar{Ts: 1783396800, Open: px, High: px * 1.01, Low: px * 0.99, Close: px, Volume: 1e6},
		Market: md.Stocks,
		ADVUSD: 5e9, // deep enough that the ADV cap cannot bind
	}
	f, qty, avgPx, ok := EnterLong(cash, in)
	if !ok {
		t.Fatalf("EnterLong not ok: %s", f.Reason)
	}
	if f.Side != "buy" {
		t.Fatalf("side=%q", f.Side)
	}
	if avgPx != px {
		t.Fatalf("avgPx=%v want %v", avgPx, px)
	}
	notional := qty * px
	wantCost := notional * (f.SpreadBps + f.ImpactBps) / 1e4
	if !approx(f.Cost, wantCost, 1e-6) {
		t.Fatalf("cost=%v want %v (spread %.3f + impact %.3f bps)", f.Cost, wantCost, f.SpreadBps, f.ImpactBps)
	}
	// Cash after applying the fill = starting + CashDelta must be ~0 (all-in) and
	// never negative.
	after := cash + f.CashDelta
	if after < -1e-6 {
		t.Fatalf("cash went negative: %v", after)
	}
	if !approx(after, 0, 1e-6) {
		t.Fatalf("cash after all-in=%v want ~0", after)
	}
}

func TestEnterLong_RejectsBadInputs(t *testing.T) {
	ok1 := ExecInputs{Bar: md.Bar{Open: 100, High: 101, Low: 99, Close: 100, Volume: 1e6}, Market: md.Stocks, ADVUSD: 1e9}
	if _, _, _, ok := EnterLong(0, ok1); ok {
		t.Fatal("zero budget should not enter")
	}
	noPx := ok1
	noPx.Bar.Open = 0
	if _, _, _, ok := EnterLong(1000, noPx); ok {
		t.Fatal("zero price should not enter")
	}
}

// PositionBudget splits equity across MaxPositions but never exceeds cash on
// hand, so entries stay funded and multiple names can coexist.
func TestPositionBudget_SplitsAndClamps(t *testing.T) {
	t.Setenv("SIGNALDECK_PAPER_MAX_POSITIONS", "10")
	// Full cash: slice = equity/10.
	if b := PositionBudget(100_000, 100_000); !approx(b, 10_000, 1e-9) {
		t.Fatalf("budget=%v want 10000 (equity/10)", b)
	}
	// Cash below the slice clamps to cash.
	if b := PositionBudget(100_000, 3_000); !approx(b, 3_000, 1e-9) {
		t.Fatalf("budget=%v want 3000 (clamped to cash)", b)
	}
	// No cash -> zero budget (can't enter).
	if b := PositionBudget(100_000, 0); b != 0 {
		t.Fatalf("budget=%v want 0", b)
	}
}

// A budgeted entry deploys exactly the budget (notional + cost) and no more, so
// the remaining cash is cash-budget and multiple entries fit.
func TestEnterLong_DeploysBudgetOnly(t *testing.T) {
	cash := 100_000.0
	budget := 10_000.0
	in := ExecInputs{
		Bar:    md.Bar{Ts: 1783396800, Open: 250, High: 252.5, Low: 247.5, Close: 250, Volume: 1e6},
		Market: md.Stocks,
		ADVUSD: 5e9,
	}
	f, qty, _, ok := EnterLong(budget, in)
	if !ok {
		t.Fatalf("enter failed: %s", f.Reason)
	}
	outlay := qty*250 + f.Cost
	if !approx(outlay, budget, 1e-6) {
		t.Fatalf("outlay=%v want budget %v", outlay, budget)
	}
	after := cash + f.CashDelta
	if !approx(after, cash-budget, 1e-6) {
		t.Fatalf("cash after=%v want %v (only the budget deployed)", after, cash-budget)
	}
}

// A buy then an immediate sell at the SAME price must LOSE exactly the two-sided
// cost (round-trip cost), proving costs are charged on both sides.
func TestRoundTrip_LosesTwoSidedCost(t *testing.T) {
	cash := 100_000.0
	px := 50.0
	in := ExecInputs{
		Bar:    md.Bar{Ts: 1783396800, Open: px, High: px * 1.01, Low: px * 0.99, Close: px, Volume: 1e6},
		Market: md.Stocks,
		ADVUSD: 5e9,
	}

	enter, qty, _, ok := EnterLong(cash, in)
	if !ok {
		t.Fatalf("enter failed: %s", enter.Reason)
	}
	cashAfterBuy := cash + enter.CashDelta

	exit, ok := ExitLong(qty, in)
	if !ok {
		t.Fatalf("exit failed: %s", exit.Reason)
	}
	cashAfterSell := cashAfterBuy + exit.CashDelta

	// Expected loss = entry cost + exit cost, each modelled from its own side.
	wantLoss := enter.Cost + exit.Cost
	gotLoss := cash - cashAfterSell
	if !approx(gotLoss, wantLoss, 1e-6) {
		t.Fatalf("round-trip loss=%v want %v (two-sided cost)", gotLoss, wantLoss)
	}
	if cashAfterSell >= cash {
		t.Fatalf("flat round trip at same price must lose money: before=%v after=%v", cash, cashAfterSell)
	}
}

func TestSummarize_GatesThinSamples(t *testing.T) {
	// A 3-point curve: below both the Sharpe (5 marks) and win-rate (5 trades)
	// gates.
	curve := []EquityPoint{
		{Ts: 0, Equity: 100000},
		{Ts: 86400, Equity: 101000},
		{Ts: 2 * 86400, Equity: 100500},
	}
	closed := []Trade{{Won: true, Notional: 50000}, {Won: false, Notional: 50000}}
	s := Summarize(curve, closed, 4, 200000)
	if s.SharpeValid {
		t.Fatalf("Sharpe should be withheld on %d marks", len(curve))
	}
	if s.WinRateValid {
		t.Fatalf("win rate should be withheld on %d closed trades", len(closed))
	}
	if !approx(s.TotalReturn, 100500.0/100000.0-1, 1e-9) {
		t.Fatalf("totalReturn=%v", s.TotalReturn)
	}
	if !approx(s.Turnover, 200000.0/100000.0, 1e-9) {
		t.Fatalf("turnover=%v want 2.0", s.Turnover)
	}
	if s.NumFills != 4 {
		t.Fatalf("numFills=%d want 4", s.NumFills)
	}
}

func TestSummarize_ValidWhenEnough(t *testing.T) {
	// 6 marks + 5 closed trades -> both stats valid.
	curve := make([]EquityPoint, 0, 6)
	eq := 100000.0
	for i := 0; i < 6; i++ {
		eq *= 1.01
		curve = append(curve, EquityPoint{Ts: int64(i) * 86400, Equity: eq})
	}
	closed := []Trade{{Won: true}, {Won: true}, {Won: false}, {Won: true}, {Won: false}}
	s := Summarize(curve, closed, 10, 300000)
	if !s.SharpeValid {
		t.Fatal("Sharpe should be valid at 6 marks")
	}
	if !s.WinRateValid {
		t.Fatal("win rate should be valid at 5 closed trades")
	}
	if !approx(s.WinRate, 3.0/5.0, 1e-9) {
		t.Fatalf("winRate=%v want 0.6", s.WinRate)
	}
}

func TestSummarize_MaxDrawdown(t *testing.T) {
	curve := []EquityPoint{
		{Ts: 0, Equity: 100},
		{Ts: 1, Equity: 120}, // peak
		{Ts: 2, Equity: 90},  // trough: dd = 1 - 90/120 = 0.25
		{Ts: 3, Equity: 110},
	}
	s := Summarize(curve, nil, 0, 0)
	if !approx(s.MaxDrawdown, 0.25, 1e-9) {
		t.Fatalf("maxDD=%v want 0.25", s.MaxDrawdown)
	}
}

// marksPerYear must never claim a curve that marks MORE often than daily fits
// FEWER marks in a year than a daily one.
//
// The live book stamps two marks per calendar day, so its gaps alternate ~4h and
// ~20h and the median lands at 72,000s — between the 6.5h session length and a
// full day. The old session extrapolation, (23400/med)*252, returned 81.9 for
// that median against an actual ~504, understating the annualization factor 6.6x
// and pulling every published Sharpe and Sortino toward zero.
func TestMarksPerYear_TwiceDailyCurveBeatsDaily(t *testing.T) {
	// 30 days, two marks a day: 04:00 and 00:00 the next day -> gaps 14400/72000.
	var twice []EquityPoint
	for d := int64(1); d <= 30; d++ {
		twice = append(twice,
			EquityPoint{Ts: d*86400 + 4*3600, Equity: 100},
			EquityPoint{Ts: d*86400 + 18*3600, Equity: 100},
		)
	}
	got := marksPerYear(twice)
	if got < 252 {
		t.Fatalf("twice-daily curve annualized at %.1f marks/year — fewer than a DAILY curve's 252", got)
	}
	if want := 504.0; math.Abs(got-want) > 1 {
		t.Fatalf("twice-daily curve: got %.1f marks/year, want ~%.0f", got, want)
	}

	// A plain daily curve must still be exactly 252.
	var daily []EquityPoint
	for d := int64(1); d <= 30; d++ {
		daily = append(daily, EquityPoint{Ts: d * 86400, Equity: 100})
	}
	if got := marksPerYear(daily); math.Abs(got-252) > 0.001 {
		t.Fatalf("daily curve: got %.3f marks/year, want 252", got)
	}

	// Sparser than daily must still scale down: every third day -> 84.
	var sparse []EquityPoint
	for d := int64(1); d <= 30; d++ {
		sparse = append(sparse, EquityPoint{Ts: d * 3 * 86400, Equity: 100})
	}
	if got := marksPerYear(sparse); math.Abs(got-84) > 0.001 {
		t.Fatalf("every-third-day curve: got %.3f marks/year, want 84", got)
	}

	// Genuinely intraday marks keep the answer the session formula gave:
	// 5-minute marks over a 6.5h session = 78 a day = 19,656 a year.
	var intraday []EquityPoint
	for d := int64(1); d <= 5; d++ {
		for i := int64(0); i < 78; i++ {
			intraday = append(intraday, EquityPoint{Ts: d*86400 + 14*3600 + i*300, Equity: 100})
		}
	}
	if got := marksPerYear(intraday); math.Abs(got-19656) > 1 {
		t.Fatalf("5-minute intraday curve: got %.1f marks/year, want 19656", got)
	}
}
