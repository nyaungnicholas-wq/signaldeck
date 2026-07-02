package backtest

import (
	"strings"
	"testing"
)

func TestExplainContent(t *testing.T) {
	s := Strategy{
		Name:    "50/200 crossover",
		CostBps: 10,
	}
	r := Result{
		TotalReturn: 0.25,
		CAGR:        0.12,
		MaxDrawdown: 0.18,
		Sharpe:      0.9,
		NumTrades:   30,
		WinRate:     0.6,
		ExposurePct: 0.55,
		VsBuyHold:   0.05, // beat buy&hold by 5% => bh = 20%
	}
	got := Explain(s, r)

	// Must contain the headline numbers and be honest about costs + trades.
	mustContain := []string{
		"50/200 crossover",
		"25.0%", // total return
		"12.0%", // CAGR
		"20.0%", // buy&hold (25 - 5)
		"30 trades",
		"10bps",
		"beat",  // it beat buy&hold
		"18.0%", // drawdown
	}
	for _, m := range mustContain {
		if !strings.Contains(got, m) {
			t.Errorf("Explain output missing %q.\nGot: %s", m, got)
		}
	}
}

// TestExplainHonestyLowN: a low-trade result must read as LOW CONFIDENCE.
func TestExplainLowConfidence(t *testing.T) {
	s := Strategy{Name: "lucky", CostBps: 5}
	r := Result{TotalReturn: 0.8, CAGR: 0.5, NumTrades: 2, VsBuyHold: 0.7}
	got := Explain(s, r)
	if !strings.Contains(got, "LOW CONFIDENCE") {
		t.Errorf("low-N result not flagged LOW CONFIDENCE.\nGot: %s", got)
	}
	if !strings.Contains(got, "in-sample") {
		t.Errorf("result should be flagged as in-sample/hypothesis.\nGot: %s", got)
	}
}

// TestExplainZeroTrades: no trades => "nothing to conclude".
func TestExplainZeroTrades(t *testing.T) {
	s := Strategy{Name: "idle", CostBps: 10}
	r := Result{NumTrades: 0}
	got := Explain(s, r)
	if !strings.Contains(got, "No trades") {
		t.Errorf("zero-trade result not flagged.\nGot: %s", got)
	}
}

// TestExplainLaggedVerdict: negative VsBuyHold reads "lagged".
func TestExplainLaggedVerdict(t *testing.T) {
	s := Strategy{Name: "underperformer", CostBps: 25}
	r := Result{TotalReturn: 0.05, CAGR: 0.02, NumTrades: 40, VsBuyHold: -0.15}
	got := Explain(s, r)
	if !strings.Contains(got, "lagged") {
		t.Errorf("underperforming strategy not marked 'lagged'.\nGot: %s", got)
	}
	// Signed vs-buy-hold shown.
	if !strings.Contains(got, "-15.0%") {
		t.Errorf("signed vs-buy-hold not shown.\nGot: %s", got)
	}
}

// TestExplainEndToEnd runs a real backtest and explains it — the string must be
// non-empty and mention the strategy name.
func TestExplainEndToEnd(t *testing.T) {
	cs := []float64{100, 102, 101, 104, 103, 106, 108, 107, 110}
	bars := barsFromCloses(cs...)
	s, err := Parse("buy above the 3-day, sell below")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r, err := Backtest(bars, s)
	if err != nil {
		t.Fatalf("Backtest: %v", err)
	}
	got := Explain(s, r)
	if len(got) == 0 {
		t.Fatal("Explain returned empty string")
	}
	if !strings.Contains(got, s.Name) {
		t.Errorf("Explain missing strategy name %q", s.Name)
	}
}
