package structregime

import "testing"

// On a non-Alpaca instance no forecast may carry an Alpaca-measured accuracy
// and no trade plan may be built from Alpaca-measured forward returns.
func TestDisableBacktestTables(t *testing.T) {
	if accuracyFor(KindTrend21, 0.95) == 0 {
		t.Fatal("precondition: the table is on by default")
	}
	backtestOff.Store(true)
	defer backtestOff.Store(false)
	for _, k := range []Kind{KindTrend21, KindTrend63, KindLiquidity21, KindVol21, KindTrendCrypto21, KindLiquidityCrypto21} {
		if got := AccuracyForTest(k, 0.95); got != 0 {
			t.Errorf("%s accuracy = %v with the tables off, want 0", k, got)
		}
	}
	if _, ok := forwardReturnFor(KindTrend21, 0.95); ok {
		t.Error("a forward return was served with the tables off")
	}
	if _, ok := BuildTradePlan(KindTrend21, 0.95, 100, 2); ok {
		t.Error("a trade plan was built with the tables off")
	}
}
