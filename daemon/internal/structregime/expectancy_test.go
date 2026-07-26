package structregime

import "testing"

// The inversion is the reason this package exists, so the headline test is that
// ranking by expectancy does NOT surface the most accurate band.

func TestTopAccuracyBandIsRefusedAsATrade(t *testing.T) {
	// conv 0.95 on trend21: the system's best hit rate (97.2%) and a measured
	// mean forward return of -0.39%. It must be refused.
	p, ok := BuildTradePlan(KindTrend21, 0.95, 100, 2)
	if !ok {
		t.Fatal("expected a plan to be computable for a measured band")
	}
	if p.Tradeable {
		t.Fatalf("the top-accuracy band has a NEGATIVE measured return and must not "+
			"be tradeable: %+v", p)
	}
	if p.Accuracy < 0.9 {
		t.Fatalf("this band should still report its high accuracy (%.3f) — the point "+
			"is that accuracy and tradeability disagree", p.Accuracy)
	}
}

func TestPositiveReturnBandCanBeTradeable(t *testing.T) {
	// H7 hostile-review fix: this used to be `if p.MeanFwdRet <= 0 { t.Skip(...) }`
	// — a quantitative regression (the 0.8-0.9 band's measured forward return
	// going non-positive) would have reported PASS via the skip instead of
	// FAIL. conv 0.85 on trend21 sits in that band, whose 2026-07-24
	// re-validation measured mean forward return is +0.79%
	// (structregime.go's forwardReturnFor, the b80 case). Pinning that number
	// here — rather than skipping when it isn't positive — means ANY change
	// to the measured band (a genuine re-validation, or a refactor that
	// silently swaps in the wrong band/kind) FAILS this test instead of
	// silently adapting. If the number legitimately changes, update the
	// pinned constant deliberately, with a comment saying why.
	const wantMeanFwdRet = 0.79

	p, ok := BuildTradePlan(KindTrend21, 0.85, 100, 0.2)
	if !ok {
		t.Fatal("expected a computable plan")
	}
	if diff := p.MeanFwdRet - wantMeanFwdRet; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("trend21 0.8-0.9 band's measured mean forward return changed: got %.4f%%, "+
			"want %.4f%% (pinned 2026-07-24 re-validation) — this band moving is exactly the "+
			"kind of regression a t.Skip used to hide; if this is a genuine re-measurement, "+
			"update the pinned constant on purpose", p.MeanFwdRet, wantMeanFwdRet)
	}
	if !p.Tradeable {
		t.Fatalf("positive return + tight stop should clear expectancy: %+v", p)
	}
	if p.ExpectancyPct <= 0 {
		t.Fatalf("tradeable plans must have positive expectancy, got %.3f", p.ExpectancyPct)
	}
}

func TestWideStopKillsAThinEdge(t *testing.T) {
	// Same band, but volatility so high the stop dwarfs the measured move: the
	// position loses to noise even when the forecast is right.
	p, ok := BuildTradePlan(KindTrend21, 0.85, 100, 20)
	if !ok {
		t.Fatal("expected a computable plan")
	}
	if p.Tradeable {
		t.Fatalf("a stop far wider than the measured move must not be tradeable: %+v", p)
	}
}

func TestStopsScaleWithTheSymbolsOwnVolatility(t *testing.T) {
	calm, _ := BuildTradePlan(KindTrend21, 0.85, 100, 1)
	wild, _ := BuildTradePlan(KindTrend21, 0.85, 100, 5)
	if !(wild.StopPct > calm.StopPct) {
		t.Fatalf("a more volatile symbol must get a wider stop: %.2f%% vs %.2f%%",
			wild.StopPct, calm.StopPct)
	}
	if calm.StopPx >= calm.EntryPx {
		t.Fatal("a long stop must sit below entry")
	}
}

func TestUnmeasuredBandRefusesRatherThanInventing(t *testing.T) {
	// vol21/liquidity21 have no measured forward return. A plausible-looking
	// stop and target here would be fabricated, and fabricated levels get acted
	// on.
	if _, ok := BuildTradePlan(KindVol21, 0.95, 100, 2); ok {
		t.Fatal("a band with no measured forward return must refuse, not invent")
	}
}

func TestBadInputsRefuse(t *testing.T) {
	for _, c := range [][2]float64{{0, 2}, {100, 0}, {-5, 2}} {
		if _, ok := BuildTradePlan(KindTrend21, 0.85, c[0], c[1]); ok {
			t.Fatalf("entry=%v atr=%v must refuse", c[0], c[1])
		}
	}
}

func TestRankingOptimisesMoneyNotHitRate(t *testing.T) {
	plans := []TradePlan{
		{Kind: KindTrend21, Accuracy: 0.97, ExpectancyPct: -0.4},
		{Kind: KindTrend21, Accuracy: 0.73, ExpectancyPct: +0.9},
		{Kind: KindTrend21, Accuracy: 0.90, ExpectancyPct: +0.2},
	}
	got := RankByExpectancy(plans)
	if got[0].Accuracy != 0.73 {
		t.Fatalf("ranking must put the highest EXPECTANCY first, not the highest "+
			"accuracy — got accuracy %.2f first", got[0].Accuracy)
	}
	if got[len(got)-1].ExpectancyPct != -0.4 {
		t.Fatal("the negative-expectancy plan must rank last")
	}
}

func TestKellyIsHalvedAndCapped(t *testing.T) {
	// 60% win rate at 2:1 — full Kelly is 0.40, so half-Kelly is 0.20.
	k := KellyFraction(0.60, 2.0)
	if k <= 0 || k > 0.25 {
		t.Fatalf("kelly %.3f outside the half-Kelly/quarter-cap range", k)
	}
	if k > 0.21 {
		t.Fatalf("kelly should be HALVED (est. error correction), got %.3f", k)
	}
	// A losing edge must size to nothing.
	if KellyFraction(0.30, 0.5) != 0 {
		t.Fatal("a negative-edge bet must size to zero")
	}
	if KellyFraction(0.9, -1) != 0 {
		t.Fatal("invalid rr must size to zero")
	}
}
