// HAR realized-variance forecast runner, and the resolver that grades it.
//
// This is the live half of the volatility work. internal/harrv is the model
// (pure math, no I/O); this schedules it, freezes each call with BOTH nulls
// beside it, and resolves outcomes once their window has closed.
//
// Cadence 6h. The inputs are daily bars, so a forecast only moves when a
// session closes and running more often would be waste.
//
// NOTHING HERE PUBLISHES A CLAIM. It accumulates a record. Whether that record
// shows skill is a question for the grader and the pre-registration, and the
// honest answer today is that it is far too early to say -- which is exactly
// why the rows have to start accruing before anyone asks.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/harrv"
	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// rvLookbackDays caps the bars loaded per symbol. harrv.MinHistory is 530
// sessions and the monthly aggregate needs a warm-up above that; ~4 calendar
// years covers it with slack for holidays and holes.
const rvLookbackDays = 1500

// RVHorizons are the registered horizons. Both are computed and stored; the
// pre-registration fixes which is the headline. Computing only the one that
// scores better would be the selection effect this platform has retired
// predictors for.
var RVHorizons = []harrv.Horizon{1, 5}

// RVForecastRunner freezes one HAR forecast per symbol per horizon.
type RVForecastRunner struct {
	St  *store.Store
	Now func() time.Time // injectable clock; nil means time.Now
}

func (w *RVForecastRunner) Name() string            { return "rv-forecast-runner" }
func (w *RVForecastRunner) Interval() time.Duration { return 6 * time.Hour }

func (w *RVForecastRunner) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *RVForecastRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := w.now()
	from := now.AddDate(0, 0, -rvLookbackDays).Unix()
	rev := lineage.RevisionStamp()

	var wrote, thin, noFit, flat int
	for _, s := range syms {
		// Stocks only. The estimator is a daily RANGE estimator validated on
		// equity sessions; crypto trades continuously, so "the overnight gap"
		// is not the same object and a 24/7 series would be a different
		// estimand wearing the same name.
		if s.Market != md.Stocks {
			continue
		}
		bars, err := w.St.Bars(ctx, s.ID, md.TF1d, from, now.Unix(), rvLookbackDays)
		if err != nil {
			return "", fmt.Errorf("bars %s: %w", s.Symbol, err)
		}
		if len(bars) < harrv.MinHistory {
			thin++
			continue
		}
		rv, ts, _ := harrv.RVSeries(bars)
		t := len(rv) - 1

		for _, h := range RVHorizons {
			fit, ok := harrv.FitAt(rv, t, h)
			if !ok {
				noFit++
				continue
			}
			rvHat, ok := harrv.PredictAt(rv, t, fit)
			if !ok {
				flat++
				continue
			}
			// BOTH nulls are computed HERE, at call time, and travel with the
			// forecast. The store refuses a row without them.
			nullRW, okRW := harrv.RWAt(rv, t)
			nullEW, okEW := harrv.EWMAAt(rv, t, harrv.RiskMetricsLambda)
			if !okRW || !okEW {
				flat++
				continue
			}
			err := w.St.UpsertRVForecast(ctx, store.RVForecast{
				SymbolID: s.ID, Ts: ts[t], Horizon: int(h),
				RVHat: rvHat, NullRW: nullRW, NullEWMA: nullEW,
				Beta0: fit.Beta0, BetaD: fit.BetaD, BetaW: fit.BetaW, BetaM: fit.BetaM,
				ResidVar: fit.ResidVar, NTrain: fit.N, Revision: rev,
			}, now)
			if err != nil {
				return "", fmt.Errorf("store %s h=%d: %w", s.Symbol, h, err)
			}
			wrote++
		}
	}

	detail := fmt.Sprintf(
		"froze %d forecast(s) over %d horizon(s); %d symbol(s) below the %d-session floor, "+
			"%d could not fit, %d had no usable regressor row",
		wrote, len(RVHorizons), thin, harrv.MinHistory, noFit, flat)
	if wrote == 0 {
		// Completed without delivering. ErrDegraded is the honest status: the
		// run worked, the fleet is fine, and there was nothing to write.
		return detail, fmt.Errorf("%s: %w", detail, workers.ErrDegraded)
	}
	return detail, nil
}

// RVOutcomeWorker resolves forecasts whose window has closed.
type RVOutcomeWorker struct {
	St    *store.Store
	Now   func() time.Time
	Limit int // rows per horizon per pass; 0 means a sensible default
}

func (w *RVOutcomeWorker) Name() string            { return "rv-outcome-runner" }
func (w *RVOutcomeWorker) Interval() time.Duration { return 6 * time.Hour }

func (w *RVOutcomeWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// abandonAfter is how long past its window a forecast waits before being
// closed as ungradable. Generous on purpose: a symbol can simply stop
// reporting for a while, and closing early would drop exactly the cases where
// something went wrong with the data.
const abandonAfter = 45 * 24 * time.Hour

func (w *RVOutcomeWorker) Run(ctx context.Context) (string, error) {
	limit := w.Limit
	if limit <= 0 {
		limit = 5000
	}
	now := w.now()
	var resolved, waiting, abandoned int

	for _, h := range RVHorizons {
		open, err := w.St.OpenRVForecasts(ctx, int(h), limit)
		if err != nil {
			return "", err
		}
		for _, f := range open {
			// Reload the bars around the call so the outcome is computed from
			// the SAME estimator the forecast used. Recomputing the target with
			// a different definition is the estimator mismatch that inflated an
			// earlier result here by 9.5pp.
			from := time.Unix(f.Ts, 0).AddDate(0, 0, -rvLookbackDays).Unix()
			bars, err := w.St.Bars(ctx, f.SymbolID, md.TF1d, from, now.Unix(), rvLookbackDays+90)
			if err != nil {
				return "", err
			}
			rv, ts, _ := harrv.RVSeries(bars)

			idx := -1
			for i, v := range ts {
				if v == f.Ts {
					idx = i
					break
				}
			}
			if idx < 0 {
				if now.Sub(time.Unix(f.Ts, 0)) > abandonAfter {
					if err := w.St.MarkRVUngradable(ctx, f.SymbolID, f.Ts, f.Horizon,
						"the call bar is no longer present in the series", now); err != nil {
						return "", err
					}
					abandoned++
				} else {
					waiting++
				}
				continue
			}
			actual, ok := harrv.TargetAt(rv, idx, harrv.Horizon(f.Horizon))
			if !ok {
				if now.Sub(time.Unix(f.Ts, 0)) > abandonAfter {
					if err := w.St.MarkRVUngradable(ctx, f.SymbolID, f.Ts, f.Horizon,
						"the outcome window never became fully estimable", now); err != nil {
						return "", err
					}
					abandoned++
				} else {
					waiting++
				}
				continue
			}
			if err := w.St.ResolveRVForecast(ctx, f.SymbolID, f.Ts, f.Horizon, actual, now); err != nil {
				return "", err
			}
			resolved++
		}
	}

	detail := fmt.Sprintf("resolved %d, %d still inside their window, %d abandoned as ungradable",
		resolved, waiting, abandoned)
	if resolved == 0 && waiting == 0 && abandoned == 0 {
		return detail, fmt.Errorf("no forecasts are open yet: %w", workers.ErrDegraded)
	}
	return detail, nil
}
