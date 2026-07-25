// Volatility-regime runner — the platform's validated-edge forecast worker.
//
// For every active STOCK it loads ~2 years of daily bars and computes every
// validated regime forecast from the same bars: the quarterly vol regime
// (internal/volregime) plus the 2026-07-17 alpha-loop winners
// (internal/structregime — trend21 / trend63 / liquidity21 / vol21). Crypto
// symbols get ONLY the two crypto-validated kinds (trend21-crypto /
// liquidity21-crypto, 2026-07-18 crypto loop — see volregimecrypto.go); the
// stock kinds were validated on the stock universe only and the vol regime
// failed to replicate on crypto. Cadence 6h — the forecasts only move as
// daily bars arrive, so more often would be waste.
package pipeline

import (
	"context"
	"fmt"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

// volLookbackDays caps the daily bars loaded per symbol: the method needs ~230
// (200-day rank window + warm-up); ~2 calendar years covers it with slack.
const volLookbackDays = 760

// VolRegimeRunner computes + stores the volatility-regime forecast per stock.
type VolRegimeRunner struct {
	St  *store.Store
	Now func() time.Time // injectable clock; nil ⇒ time.Now
}

func (w *VolRegimeRunner) Name() string            { return "vol-regime-runner" }
func (w *VolRegimeRunner) Interval() time.Duration { return 6 * time.Hour }

func (w *VolRegimeRunner) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *VolRegimeRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := w.now()
	from := now.AddDate(0, 0, -volLookbackDays).Unix()
	forecast, confident, structural, cryptoCalls := 0, 0, 0, 0
	for _, s := range syms {
		if s.Market != md.Stocks {
			// Crypto regime kinds (2026-07-18 crypto loop): trend21-crypto +
			// liquidity21-crypto only — vol21/vol63/trend63 did not validate on
			// crypto and are skipped (see volregimecrypto.go).
			if s.Market == md.Crypto {
				n, err := w.runCryptoSymbol(ctx, s, from, now)
				if err != nil {
					return "", err
				}
				cryptoCalls += n
			}
			continue // vol63 + stock structural kinds: validated on stocks only
		}
		bars, err := w.St.Bars(ctx, s.ID, md.TF1d, from, now.Unix()+1, 5000)
		if err != nil {
			return "", fmt.Errorf("bars %s: %w", s.Symbol, err)
		}
		rets := barReturns(bars)
		f, ok := volregime.Predict(rets)
		if ok {
			if err := w.St.UpsertVolForecast(ctx, s.ID, now.Unix(), f); err != nil {
				return "", fmt.Errorf("upsert %s: %w", s.Symbol, err)
			}
			forecast++
			if f.Conviction >= 0.8 {
				confident++
			}
		} else if err := w.St.DeleteVolForecast(ctx, s.ID); err != nil {
			return "", fmt.Errorf("clear vol63 %s: %w", s.Symbol, err)
		}
		// structural regimes from the same bars (each refuses thin history
		// on its own — honest absences, no rows)
		closes := make([]float64, 0, len(bars))
		vols := make([]float64, 0, len(bars))
		for _, b := range bars {
			if b.Close > 0 {
				closes = append(closes, b.Close)
				vols = append(vols, b.Volume)
			}
		}
		if sf, ok := structregime.PredictTrend(closes); ok {
			if err := w.St.UpsertRegimeForecast(ctx, s.ID, now.Unix(), sf); err != nil {
				return "", fmt.Errorf("upsert trend %s: %w", s.Symbol, err)
			}
			structural++
		} else if err := w.St.DeleteRegimeForecast(ctx, s.ID, structregime.KindTrend21); err != nil {
			return "", fmt.Errorf("clear trend %s: %w", s.Symbol, err)
		}
		// trend63 (credibility wave): the same trend predictor at the quarterly
		// horizon — measured cumulative tiers 70.0/77.7/81.7/83.7 (see
		// internal/structregime/trend63.go).
		if sf, ok := structregime.PredictTrend63(closes); ok {
			if err := w.St.UpsertRegimeForecast(ctx, s.ID, now.Unix(), sf); err != nil {
				return "", fmt.Errorf("upsert trend63 %s: %w", s.Symbol, err)
			}
			structural++
		} else if err := w.St.DeleteRegimeForecast(ctx, s.ID, structregime.KindTrend63); err != nil {
			return "", fmt.Errorf("clear trend63 %s: %w", s.Symbol, err)
		}
		if sf, ok := structregime.PredictLiquidity(closes, vols); ok {
			if err := w.St.UpsertRegimeForecast(ctx, s.ID, now.Unix(), sf); err != nil {
				return "", fmt.Errorf("upsert liquidity %s: %w", s.Symbol, err)
			}
			structural++
		} else if err := w.St.DeleteRegimeForecast(ctx, s.ID, structregime.KindLiquidity21); err != nil {
			return "", fmt.Errorf("clear liquidity %s: %w", s.Symbol, err)
		}
		if sf, ok := structregime.PredictVol21(rets); ok {
			if err := w.St.UpsertRegimeForecast(ctx, s.ID, now.Unix(), sf); err != nil {
				return "", fmt.Errorf("upsert vol21 %s: %w", s.Symbol, err)
			}
			structural++
		} else if err := w.St.DeleteRegimeForecast(ctx, s.ID, structregime.KindVol21); err != nil {
			return "", fmt.Errorf("clear vol21 %s: %w", s.Symbol, err)
		}
		// gap-fill is NOT emitted: the verification pass (2026-07-17) showed
		// the high fill rates are only true at the OPEN of the gap day; for
		// gaps surviving day 0 unfilled — the only population an end-of-day
		// worker can forecast — every bucket measures below the 70% bar (see
		// internal/structregime/gapfill.go). Clear any rows from the brief
		// window when this kind was live so no stale claim survives.
		if err := w.St.DeleteRegimeForecast(ctx, s.ID, structregime.KindGapFill5); err != nil {
			return "", fmt.Errorf("clear gapfill %s: %w", s.Symbol, err)
		}
	}
	return fmt.Sprintf("forecast %d stocks vol63 (%d high-conviction) + %d structural regime calls + %d crypto regime calls", forecast, confident, structural, cryptoCalls), nil
}

// barReturns turns ascending daily bars into simple returns (drops non-positive
// closes defensively — a corporate-action zero would poison the vol estimate).
func barReturns(bars []md.Bar) []float64 {
	out := make([]float64, 0, len(bars))
	var prev float64
	for _, b := range bars {
		if b.Close <= 0 {
			prev = 0
			continue
		}
		if prev > 0 {
			out = append(out, b.Close/prev-1)
		}
		prev = b.Close
	}
	return out
}
