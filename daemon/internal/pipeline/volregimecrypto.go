// ═══ CRYPTO REGIME KINDS (appended wave — 2026-07-18 crypto discovery loop) ═══
//
// The VolRegimeRunner's crypto branch: for every active CRYPTO symbol it
// computes the two crypto-validated regime kinds (trend21-crypto /
// liquidity21-crypto — internal/structregime/crypto.go carries the measured
// tables + caveats) from the same daily bars the stock branch uses. Vol21,
// vol63 and trend63 are deliberately NOT computed for crypto: the vol regime
// failed to replicate on crypto daily bars (never separates from the majority
// baseline at h=21; inverts at h=63) and trend63 was never measured there.
package pipeline

import (
	"context"
	"fmt"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// runCryptoSymbol computes + stores the crypto regime kinds for one symbol,
// mirroring the stock branch's honest-absence contract (thin history or wild
// windows clear the row rather than leaving a stale claim). Returns how many
// structural calls were stored.
func (w *VolRegimeRunner) runCryptoSymbol(ctx context.Context, s md.Symbol, from int64, now time.Time) (int, error) {
	bars, err := w.St.Bars(ctx, s.ID, md.TF1d, from, now.Unix()+1, 5000)
	if err != nil {
		return 0, fmt.Errorf("bars %s: %w", s.Symbol, err)
	}
	closes := make([]float64, 0, len(bars))
	vols := make([]float64, 0, len(bars))
	for _, b := range bars {
		if b.Close > 0 {
			closes = append(closes, b.Close)
			vols = append(vols, b.Volume)
		}
	}
	stored := 0
	if sf, ok := structregime.PredictTrendCrypto(closes); ok {
		if err := w.St.UpsertRegimeForecast(ctx, s.ID, now.Unix(), sf); err != nil {
			return stored, fmt.Errorf("upsert trend-crypto %s: %w", s.Symbol, err)
		}
		stored++
	} else if err := w.St.DeleteRegimeForecast(ctx, s.ID, structregime.KindTrendCrypto21); err != nil {
		return stored, fmt.Errorf("clear trend-crypto %s: %w", s.Symbol, err)
	}
	if sf, ok := structregime.PredictLiquidityCrypto(closes, vols); ok {
		if err := w.St.UpsertRegimeForecast(ctx, s.ID, now.Unix(), sf); err != nil {
			return stored, fmt.Errorf("upsert liquidity-crypto %s: %w", s.Symbol, err)
		}
		stored++
	} else if err := w.St.DeleteRegimeForecast(ctx, s.ID, structregime.KindLiquidityCrypto21); err != nil {
		return stored, fmt.Errorf("clear liquidity-crypto %s: %w", s.Symbol, err)
	}
	// The stock-only kinds must never exist on a crypto symbol — clear any rows
	// from before this wave drew the market boundary.
	for _, k := range []structregime.Kind{structregime.KindVol21, structregime.KindTrend21,
		structregime.KindTrend63, structregime.KindLiquidity21} {
		if err := w.St.DeleteRegimeForecast(ctx, s.ID, k); err != nil {
			return stored, fmt.Errorf("clear stock kind %s %s: %w", k, s.Symbol, err)
		}
	}
	return stored, nil
}
