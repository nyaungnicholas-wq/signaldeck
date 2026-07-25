package moneymetrics

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestHighWinRateCanStillLose is the whole reason this package exists: an 80%
// win rate with a couple of large losers has NEGATIVE expectancy and a profit
// factor below 1 — it loses money. Win rate alone lies.
func TestHighWinRateCanStillLose(t *testing.T) {
	// 8 wins of +1, 2 losses of -5.
	returns := []float64{1, 1, 1, 1, 1, 1, 1, 1, -5, -5}
	m := FromReturns(returns)

	if !approx(m.WinRate, 0.8) {
		t.Fatalf("winRate = %v, want 0.8", m.WinRate)
	}
	if !approx(m.Expectancy, -0.2) {
		t.Fatalf("expectancy = %v, want -0.2 (loses money)", m.Expectancy)
	}
	if !m.ProfitFactorValid || !approx(m.ProfitFactor, 0.8) {
		t.Fatalf("profitFactor = %v (valid %v), want 0.8 (<1 ⇒ unprofitable)", m.ProfitFactor, m.ProfitFactorValid)
	}
	if !approx(m.AvgWin, 1) || !approx(m.AvgLoss, 5) {
		t.Fatalf("avgWin/avgLoss = %v/%v, want 1/5", m.AvgWin, m.AvgLoss)
	}
	if !m.PayoffRatioValid || !approx(m.PayoffRatio, 0.2) {
		t.Fatalf("payoffRatio = %v, want 0.2", m.PayoffRatio)
	}
	if !approx(m.GrossWin, 8) || !approx(m.GrossLoss, 10) {
		t.Fatalf("grossWin/grossLoss = %v/%v, want 8/10", m.GrossWin, m.GrossLoss)
	}
	if m.Expectancy >= 0 {
		t.Fatalf("an 80%% win rate here MUST show negative expectancy")
	}
}

// TestLowWinRateCanProfit is the mirror image: a 40% win rate with big winners
// and small losers is profitable — expectancy positive, profit factor > 1.
func TestLowWinRateCanProfit(t *testing.T) {
	// 4 wins of +3, 6 losses of -1.
	returns := []float64{3, 3, 3, 3, -1, -1, -1, -1, -1, -1}
	m := FromReturns(returns)

	if !approx(m.WinRate, 0.4) {
		t.Fatalf("winRate = %v, want 0.4", m.WinRate)
	}
	if !approx(m.Expectancy, 0.6) {
		t.Fatalf("expectancy = %v, want 0.6 (profitable)", m.Expectancy)
	}
	if !m.ProfitFactorValid || !approx(m.ProfitFactor, 2) {
		t.Fatalf("profitFactor = %v, want 2.0 (>1 ⇒ profitable)", m.ProfitFactor)
	}
	if !approx(m.PayoffRatio, 3) {
		t.Fatalf("payoffRatio = %v, want 3.0", m.PayoffRatio)
	}
	if m.Expectancy <= 0 {
		t.Fatalf("a 40%% win rate here MUST still be profitable")
	}
}

func TestFromReturns_NoLossesRatiosUndefined(t *testing.T) {
	m := FromReturns([]float64{1, 2, 3})
	if m.ProfitFactorValid {
		t.Fatalf("profit factor must be undefined (no losses), got valid=%v pf=%v", m.ProfitFactorValid, m.ProfitFactor)
	}
	if m.PayoffRatioValid {
		t.Fatalf("payoff ratio must be undefined (no losses)")
	}
	if !approx(m.Expectancy, 2) {
		t.Fatalf("expectancy = %v, want 2", m.Expectancy)
	}
	if !approx(m.GrossLoss, 0) {
		t.Fatalf("grossLoss = %v, want 0", m.GrossLoss)
	}
}

func TestFromReturns_ZeroIsBreakEvenNotWin(t *testing.T) {
	// A 0 return is a trade but neither a win nor a loss.
	m := FromReturns([]float64{2, 0, -1})
	if m.Trades != 3 {
		t.Fatalf("trades = %d, want 3", m.Trades)
	}
	if !approx(m.WinRate, 1.0/3.0) {
		t.Fatalf("winRate = %v, want 1/3 (the 0 is not a win)", m.WinRate)
	}
	if !approx(m.GrossWin, 2) || !approx(m.GrossLoss, 1) {
		t.Fatalf("grossWin/grossLoss = %v/%v, want 2/1", m.GrossWin, m.GrossLoss)
	}
	if !approx(m.Expectancy, (2+0-1)/3.0) {
		t.Fatalf("expectancy = %v, want %v", m.Expectancy, 1.0/3.0)
	}
}

func TestFromReturns_EmptyAndMeaningfulGate(t *testing.T) {
	if m := FromReturns(nil); m.Trades != 0 || m.Meaningful || m.ProfitFactorValid {
		t.Fatalf("empty returns must be inert: %+v", m)
	}
	// 19 trades → not meaningful; 20 → meaningful.
	mk := func(n int) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = 0.01
		}
		return out
	}
	if FromReturns(mk(19)).Meaningful {
		t.Fatalf("19 trades must NOT be meaningful")
	}
	if !FromReturns(mk(20)).Meaningful {
		t.Fatalf("20 trades must be meaningful")
	}
}
