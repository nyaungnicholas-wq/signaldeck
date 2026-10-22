package store

import (
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// regime_forecasts is a current-forecast cache, and VolRegimeRunner never
// refreshes or deletes a paused symbol's rows. Serving them would present the
// frozen crypto calls as current forever; outcomes keep the history.
func TestRegimeForecastsSkipInactiveSymbols(t *testing.T) {
	st, ctx := openTemp(t), t.Context()
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	f := structregime.Forecast{Kind: "vol21", HorizonDays: 21, Regime: "elevated", Conviction: 0.7, Tier: "high", N: 50}
	if err := st.UpsertRegimeForecast(ctx, btc.ID, 1000, f); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolActive(ctx, btc.ID, false); err != nil {
		t.Fatal(err)
	}
	all, err1 := st.RegimeForecasts(ctx)
	one, err2 := st.RegimeForecastsForSymbol(ctx, btc.ID)
	if err1 != nil || err2 != nil || len(all) != 0 || len(one) != 0 {
		t.Fatalf("paused symbol served: all=%d one=%d (%v %v), want 0/0", len(all), len(one), err1, err2)
	}
}
