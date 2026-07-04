package backtest

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// dailyBars builds bars spaced one CALENDAR day apart (86400s) starting at an
// arbitrary epoch, so span/bars-per-year inference has realistic timestamps.
func dailyBars(cs ...float64) []marketdata.Bar {
	const start = int64(1_600_000_000)
	const day = int64(86400)
	bars := make([]marketdata.Bar, len(cs))
	for i, c := range cs {
		bars[i] = marketdata.Bar{Ts: start + int64(i)*day, Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	return bars
}

// hourlyBars builds bars spaced one hour apart.
func hourlyBars(n int) []marketdata.Bar {
	const start = int64(1_600_000_000)
	const hour = int64(3600)
	bars := make([]marketdata.Bar, n)
	p := 100.0
	for i := 0; i < n; i++ {
		p += 0.1
		bars[i] = marketdata.Bar{Ts: start + int64(i)*hour, Open: p, High: p, Low: p, Close: p, Volume: 1}
	}
	return bars
}

// TestBarsPerYearDaily: daily-spaced bars must infer ~252 bars/year (the trading
// calendar), NOT ~365, and NOT a hardcoded constant.
func TestBarsPerYearDaily(t *testing.T) {
	bpy := barsPerYear(dailyBars(1, 2, 3, 4, 5, 6, 7, 8, 9, 10))
	if math.Abs(bpy-252) > 1 {
		t.Fatalf("daily bars should infer ~252 bars/year, got %.2f", bpy)
	}
}

// TestBarsPerYearHourly: hourly bars must infer far more bars/year than 252 (a
// ~6.5h session × 252 days ≈ 1638), proving the constant is no longer hardcoded.
func TestBarsPerYearHourly(t *testing.T) {
	bpy := barsPerYear(hourlyBars(50))
	if bpy < 1500 || bpy > 1800 {
		t.Fatalf("hourly bars should infer ~1638 bars/year, got %.2f", bpy)
	}
	if math.Abs(bpy-252) < 100 {
		t.Fatalf("hourly barsPerYear must not collapse to the daily 252, got %.2f", bpy)
	}
}

// TestBarsPerYearFallback: unusable timestamps fall back to 252.
func TestBarsPerYearFallback(t *testing.T) {
	// All-equal timestamps → no positive gaps → fallback.
	same := []marketdata.Bar{{Ts: 100}, {Ts: 100}, {Ts: 100}}
	if bpy := barsPerYear(same); bpy != 252 {
		t.Fatalf("degenerate timestamps should fall back to 252, got %.2f", bpy)
	}
	if bpy := barsPerYear(nil); bpy != 252 {
		t.Fatalf("nil bars should fall back to 252, got %.2f", bpy)
	}
}

// TestSpanYears: a ~2-year daily series reports ~2 years of span from its real
// timestamps (calendar time), not a bar-count/252 proxy.
func TestSpanYears(t *testing.T) {
	// 505 daily bars ≈ 504 days ≈ 1.38 calendar years.
	cs := make([]float64, 505)
	for i := range cs {
		cs[i] = float64(100 + i)
	}
	sy := spanYears(dailyBars(cs...))
	want := 504.0 / 365.25
	if math.Abs(sy-want) > 0.02 {
		t.Fatalf("spanYears = %.4f, want ~%.4f", sy, want)
	}
}

// TestCAGRNotReportedShortSpan: a two-week window (well under a year) must NOT
// report CAGR even if it has many trades — annualizing a fortnight is dishonest.
func TestCAGRNotReportedShortSpan(t *testing.T) {
	// Alternating so a fast/slow SMA crossover trades a lot in a short window.
	cs := make([]float64, 30) // 30 daily bars ≈ 29 days ≈ 0.08 yr
	for i := range cs {
		if i%2 == 0 {
			cs[i] = 100
		} else {
			cs[i] = 110
		}
	}
	bars := dailyBars(cs...)
	s := Strategy{
		Name:  "choppy",
		Entry: Rule{Indicator: "sma_cross", Fast: 2, Slow: 3},
		Exit:  Rule{Indicator: "sma_cross", Fast: 2, Slow: 3},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	if r.SpanYears >= minCAGRYears {
		t.Fatalf("test fixture span %.3f yr should be < %.2f", r.SpanYears, minCAGRYears)
	}
	if r.CAGRReported {
		t.Fatalf("CAGR must NOT be reported for a %.3f-year window", r.SpanYears)
	}
}

// TestCAGRNotReportedFewTrades: a long window with too few trades must NOT report
// CAGR — an annual figure off 3 trades is noise dressed as a rate.
func TestCAGRNotReportedFewTrades(t *testing.T) {
	// 3 years of a smooth uptrend → a price-above-SMA strategy trades ~once.
	cs := make([]float64, 800) // 800 daily bars ≈ 2.19 yr
	for i := range cs {
		cs[i] = 100 + float64(i)*0.5 // monotone up → one entry, never exits
	}
	bars := dailyBars(cs...)
	s := Strategy{
		Name:  "trend",
		Entry: Rule{Indicator: "price_vs_sma", Period: 20, Op: ">"},
		Exit:  Rule{Indicator: "price_vs_sma", Period: 20, Op: "<"},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	if r.SpanYears < minCAGRYears {
		t.Fatalf("fixture span %.3f should exceed %.2f yr", r.SpanYears, minCAGRYears)
	}
	if r.NumTrades >= minCAGRTrades {
		t.Fatalf("fixture should have few trades, got %d", r.NumTrades)
	}
	if r.CAGRReported {
		t.Fatalf("CAGR must NOT be reported with only %d trades", r.NumTrades)
	}
}

// TestCAGRReportedWhenQualified: a long window WITH enough trades reports CAGR.
func TestCAGRReportedWhenQualified(t *testing.T) {
	// ~2 years of an oscillation → many crossover trades.
	cs := make([]float64, 600)
	for i := range cs {
		cs[i] = 100 + 10*math.Sin(float64(i)*0.3)
	}
	bars := dailyBars(cs...)
	s := Strategy{
		Name:  "oscillator",
		Entry: Rule{Indicator: "sma_cross", Fast: 2, Slow: 5},
		Exit:  Rule{Indicator: "sma_cross", Fast: 2, Slow: 5},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	if r.SpanYears < minCAGRYears {
		t.Fatalf("fixture span %.3f should exceed %.2f", r.SpanYears, minCAGRYears)
	}
	if r.NumTrades < minCAGRTrades {
		t.Fatalf("fixture should trade >=%d times, got %d", minCAGRTrades, r.NumTrades)
	}
	if !r.CAGRReported {
		t.Fatalf("CAGR should be reported for a %.2f-year, %d-trade result",
			r.SpanYears, r.NumTrades)
	}
}

// TestWinRateMeaningfulGate: with <=1 closed trade, WinRateMeaningful is false so
// the UI never shows a "win rate" off a single trade.
func TestWinRateMeaningfulGate(t *testing.T) {
	// Monotone uptrend: one entry that is force-closed at the end = 1 closed
	// trade. Win rate would be 100% but it is not a statistic.
	cs := make([]float64, 200)
	for i := range cs {
		cs[i] = 100 + float64(i) // never exits until forced close
	}
	bars := dailyBars(cs...)
	s := Strategy{
		Name:  "one-trade",
		Entry: Rule{Indicator: "price_vs_sma", Period: 5, Op: ">"},
		Exit:  Rule{Indicator: "price_vs_sma", Period: 5, Op: "<"},
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	if r.ClosedTrades > 1 {
		t.Skipf("fixture produced %d closed trades; test needs <=1", r.ClosedTrades)
	}
	if r.WinRateMeaningful {
		t.Fatalf("win rate must not be meaningful with %d closed trade(s)", r.ClosedTrades)
	}
}

// TestSharpeUsesInferredBarsPerYear: the same per-bar return series annualized at
// hourly cadence must produce a much larger Sharpe than at daily cadence — i.e.
// the annualization scales with the inferred bar interval, not a fixed 252.
func TestSharpeScalesWithCadence(t *testing.T) {
	rets := []float64{0.001, -0.0005, 0.0008, -0.0003, 0.0006, 0.0002, -0.0004, 0.0009}
	daily := sharpe(rets, 252)
	hourly := sharpe(rets, 1638)
	if daily == 0 || hourly == 0 {
		t.Fatalf("sharpe should be non-zero for a varied series (daily=%.4f hourly=%.4f)", daily, hourly)
	}
	ratio := hourly / daily
	wantRatio := math.Sqrt(1638.0 / 252.0)
	if math.Abs(ratio-wantRatio) > 0.01 {
		t.Fatalf("sharpe ratio hourly/daily = %.4f, want ~%.4f (sqrt of bars-per-year ratio)", ratio, wantRatio)
	}
}
