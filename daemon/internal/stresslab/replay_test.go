package stresslab

import (
	"math/rand"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/riskgate"
)

// stressLimits concentrates the book so a single-name -8% crash is a
// book-level event: one position allowed at full weight, breaker at 4%.
func stressLimits() riskgate.Limits {
	return riskgate.Limits{
		MaxDrawdown:       0.04,
		MaxDailyLoss:      0.99, // isolate the drawdown breaker
		MaxPositionWeight: 1.0,
		MaxPositions:      1,
		MinEdgeTrips:      20, // unmeasured edge -> equal slice = 100%
	}
}

// crashSignals: enter early, get flatted after the crash, try to re-enter.
func crashSignals(w Window) Window {
	bars := w.Bars
	idx := ShockIndex(len(bars))
	w.Signals = []Signal{
		{Ts: bars[2].Ts, P: 0.90},       // enter
		{Ts: bars[idx+2].Ts, P: 0.10},   // exit after the shock
		{Ts: bars[idx+4].Ts, P: 0.90},   // re-entry attempt
	}
	return w
}

func TestReplay_FlashCrashTripsDrawdownBreaker_CalmDoesNot(t *testing.T) {
	base := crashSignals(flatWindow(30, 100))
	lim := stressLimits()

	calm := Replay(base, lim, nil, "baseline")
	if calm.BreakerTrips != 0 {
		t.Fatalf("calm baseline tripped a breaker: %+v", calm.RefusalReasons)
	}
	if calm.TradesTaken < 3 { // enter, exit, re-enter all allowed
		t.Fatalf("calm baseline took %d trades, want 3", calm.TradesTaken)
	}

	sc, _ := Lookup([]string{"flash_crash"})
	stressed := Replay(Apply(base, Combine(sc...), rand.New(rand.NewSource(1))), lim, nil, "flash_crash")
	if stressed.BreakerTrips == 0 {
		t.Fatalf("flash crash did not trip the drawdown breaker: refusals=%v maxDD=%v",
			stressed.RefusalReasons, stressed.MaxDrawdown)
	}
	if stressed.RefusalReasons["max-drawdown"] == 0 {
		t.Fatalf("expected a max-drawdown refusal, got %v", stressed.RefusalReasons)
	}
	if stressed.MaxDrawdown <= calm.MaxDrawdown {
		t.Fatalf("stressed maxDD %v not worse than calm %v", stressed.MaxDrawdown, calm.MaxDrawdown)
	}
	if stressed.MaxDrawdown < 0.05 {
		t.Fatalf("an -8%% crash on a fully-deployed book should show >=5%% drawdown, got %v", stressed.MaxDrawdown)
	}
	// The refusal shows up in the trade ledger too.
	var refused bool
	for _, ev := range stressed.WorstTrades {
		if ev.Side == "refused" {
			refused = true
		}
	}
	if !refused {
		t.Fatal("refusal missing from the trade ledger")
	}
}

func TestReplay_FillDegradationUnderLiquidityDrought(t *testing.T) {
	base := crashSignals(flatWindow(30, 100))
	lim := stressLimits()
	calm := Replay(base, lim, nil, "baseline")

	sc, _ := Lookup([]string{"liquidity_drought"})
	dry := Replay(Apply(base, Combine(sc...), rand.New(rand.NewSource(1))), lim, nil, "liquidity_drought")

	if dry.MeanFillCostBps <= calm.MeanFillCostBps {
		t.Fatalf("drought mean fill cost %.2f bps not worse than calm %.2f bps",
			dry.MeanFillCostBps, calm.MeanFillCostBps)
	}
	if dry.FinalEquity >= calm.FinalEquity {
		t.Fatalf("drought final equity %v not below calm %v", dry.FinalEquity, calm.FinalEquity)
	}
}

func TestReplay_DelayedFeedShiftsDecisions(t *testing.T) {
	base := crashSignals(flatWindow(30, 100))
	lim := stressLimits()
	sc, _ := Lookup([]string{"delayed_feed"})
	late := Replay(Apply(base, Combine(sc...), rand.New(rand.NewSource(1))), lim, nil, "delayed_feed")
	if late.SignalsSeen == 0 || late.TradesTaken == 0 {
		t.Fatalf("delayed feed replay saw %d signals, took %d trades", late.SignalsSeen, late.TradesTaken)
	}
}

func TestReplay_PureAndDeterministic(t *testing.T) {
	base := crashSignals(flatWindow(30, 100))
	lim := stressLimits()
	sc, _ := Lookup([]string{"flash_crash", "missing_candles"})
	a := Replay(Apply(base, Combine(sc...), rand.New(rand.NewSource(9))), lim, nil, "x")
	b := Replay(Apply(base, Combine(sc...), rand.New(rand.NewSource(9))), lim, nil, "x")
	if a.FinalEquity != b.FinalEquity || a.TradesTaken != b.TradesTaken || a.MaxDrawdown != b.MaxDrawdown {
		t.Fatalf("same seed, different behavior: %+v vs %+v", a, b)
	}
}

func TestReplay_EmptyWindow(t *testing.T) {
	sb := Replay(Window{}, riskgate.Limits{}, nil, "empty")
	if sb.FinalEquity != replayStartEquity || sb.TradesTaken != 0 {
		t.Fatalf("empty window misbehaved: %+v", sb)
	}
}
