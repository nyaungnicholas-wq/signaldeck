package pipeline

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// The market boundary: crypto symbols get ONLY the crypto kinds
// (trend21-crypto / liquidity21-crypto), stocks get ONLY the stock kinds —
// neither side may ever carry the other's accuracy tables.
func TestVolRegimeRunnerCryptoKindsMarketBoundary(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "volregime_crypto.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	stock, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("stock sym: %v", err)
	}
	crypto, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err != nil {
		t.Fatalf("crypto sym: %v", err)
	}

	now := time.Unix(int64(20000)*86400, 0).UTC() // fixed clock, day-aligned
	startDay := 20000 - 320
	trending := func(i int) (float64, float64) {
		return 100 + float64(i)*0.5 + 3*math.Sin(float64(i)/7), 1e6 + 1e4*float64(i%50)
	}
	seedRegimeBars(t, st, stock.ID, startDay, 300, trending)
	seedRegimeBars(t, st, crypto.ID, startDay, 300, trending)

	w := &VolRegimeRunner{St: st, Now: func() time.Time { return now }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	kindsFor := func(id int64) map[structregime.Kind]bool {
		rows, err := st.RegimeForecastsForSymbol(ctx, id)
		if err != nil {
			t.Fatalf("forecasts: %v", err)
		}
		out := map[structregime.Kind]bool{}
		for _, r := range rows {
			out[r.Kind] = true
		}
		return out
	}

	ck := kindsFor(crypto.ID)
	if !ck[structregime.KindTrendCrypto21] || !ck[structregime.KindLiquidityCrypto21] {
		t.Fatalf("crypto symbol missing crypto kinds, got %v", ck)
	}
	for _, forbidden := range []structregime.Kind{structregime.KindTrend21,
		structregime.KindTrend63, structregime.KindLiquidity21, structregime.KindVol21} {
		if ck[forbidden] {
			t.Fatalf("crypto symbol must not carry stock kind %s", forbidden)
		}
	}

	sk := kindsFor(stock.ID)
	if sk[structregime.KindTrendCrypto21] || sk[structregime.KindLiquidityCrypto21] {
		t.Fatalf("stock symbol must not carry crypto kinds, got %v", sk)
	}
	if !sk[structregime.KindTrend21] {
		t.Fatalf("stock symbol should still get its stock kinds, got %v", sk)
	}

	// A crypto kind's stored accuracy must come from the CRYPTO table.
	rows, err := st.RegimeForecastsForSymbol(ctx, crypto.ID)
	if err != nil {
		t.Fatalf("forecasts: %v", err)
	}
	for _, r := range rows {
		if r.Kind == structregime.KindLiquidityCrypto21 {
			want := []float64{0.795, 0.912, 0.935, 0.964}
			found := false
			for _, v := range want {
				if r.HistoricalAccuracy == v {
					found = true
				}
			}
			if !found {
				t.Fatalf("liquidity21-crypto accuracy %.3f is not from the crypto table", r.HistoricalAccuracy)
			}
		}
	}
}
