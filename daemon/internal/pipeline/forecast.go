package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/forecast"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ForecastTrainer retrains the honest directional forecast model for every
// active symbol (1d and 1w horizons) and caches it with its out-of-sample
// grade. Training is walk-forward and moderately expensive, so it runs on a
// cadence rather than per request; the API serves the cached result.
type ForecastTrainer struct {
	St *store.Store
}

// Name implements workers.Worker.
func (w *ForecastTrainer) Name() string { return "forecast-trainer" }

// Interval implements workers.Worker.
func (w *ForecastTrainer) Interval() time.Duration { return time.Hour }

// Run trains and stores forecasts for all active symbols.
func (w *ForecastTrainer) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	ts := time.Now().Unix()
	trained := 0
	for _, s := range syms {
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			return "", err
		}
		for _, h := range []md.Horizon{md.H1d, md.H1w} {
			f, ok := forecast.Run(daily, h)
			if !ok {
				continue // insufficient history; not an error
			}
			if err := w.St.UpsertForecast(ctx, store.Forecast{
				SymbolID: s.ID, Horizon: h, Ts: ts,
				Prob:     f.Prob,
				Accuracy: f.Grade.Accuracy,
				Brier:    f.Grade.BrierScore,
				AUC:      f.Grade.AUC,
				BaseRate: f.Grade.BaseRate,
				Lift:     f.Grade.Lift,
				NTrain:   f.NTrain,
				NEval:    f.Grade.N,
			}); err != nil {
				return "", fmt.Errorf("%s %s: %w", s.Symbol, h, err)
			}
			trained++
		}
	}
	return fmt.Sprintf("trained %d forecasts over %d symbols", trained, len(syms)), nil
}
