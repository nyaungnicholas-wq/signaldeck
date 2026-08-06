// Stage 6 — persistence for the GATED MODEL FORECAST LEGS (GBM + mean-reversion).
//
// model_forecasts is exactly parallel to quant.go's `forecasts` (the linear
// logit), but keyed additionally by a model name so multiple model legs coexist
// per symbol+horizon. The trainer upserts one row per (symbol, horizon, model)
// with its LATEST out-of-sample grade; the PredictionRunner reads them and
// includes a leg in the blend ONLY when its lift > 0 — the same honesty gate the
// logit forecast passes through.
//
// Reads use the pooled s.db handle; the single upsert goes through s.w, matching
// the rest of the store's write discipline.
package store

import (
	"context"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Model names for model_forecasts.model (kept here so store + pipeline agree).
const (
	ModelGBM     = "gbm"
	ModelMeanRev = "meanrev"
	// ModelPressure grades the ensemble's oldest base leg (the composite
	// Pressure Score) walk-forward OOS and stores its lift here like a model
	// leg. Unlike the opt-in model legs, pressure is OPT-OUT: the row is written
	// even when lift<=0 so the PredictionRunner can see the measured
	// anti-predictive grade and bench the leg (it is not deleted on a gated
	// regrade the way alphax is).
	ModelPressure = "pressure"
	// ModelExpectancy grades the ensemble's EXPECTANCY leg (the per-state
	// historical hit rate) against the live prequential record. OPT-OUT like
	// pressure, and for the same reason: the leg is otherwise admitted on
	// absence of evidence, so a measured anti-predictive grade has to be
	// PERSISTED for the gate to bench it.
	//
	// Graded off predictions.components rather than the feature store because
	// expectancy_hit_rate is no longer written into the feature vector (the
	// ablation harness removed it), while the leg itself is still live on ~98%
	// of forecasts. The components snapshot is frozen at prediction time and the
	// outcome resolves later, so the pairs are out-of-sample by construction —
	// a cleaner grade than any refit could produce.
	ModelExpectancy = "expectancy"
)

// ModelForecast is one stored model-leg forecast + its out-of-sample grade.
type ModelForecast struct {
	SymbolID int64      `json:"-"`
	Horizon  md.Horizon `json:"horizon"`
	Model    string     `json:"model"`
	Ts       int64      `json:"ts"`
	Prob     float64    `json:"prob"`
	Accuracy float64    `json:"accuracy"`
	Brier    float64    `json:"brier"`
	AUC      float64    `json:"auc"`
	BaseRate float64    `json:"baseRate"`
	Lift     float64    `json:"lift"`
	NTrain   int        `json:"nTrain"`
	NEval    int        `json:"nEval"`
}

// UpsertModelForecast writes one model leg's latest forecast+grade in place
// (idempotent on the (symbol, horizon, model) key).
func (s *Store) UpsertModelForecast(ctx context.Context, f ModelForecast) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO model_forecasts
		  (symbol_id, horizon, model, ts, prob, accuracy, brier, auc, base_rate, lift, n_train, n_eval)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id, horizon, model) DO UPDATE SET
		  ts=excluded.ts, prob=excluded.prob, accuracy=excluded.accuracy,
		  brier=excluded.brier, auc=excluded.auc, base_rate=excluded.base_rate,
		  lift=excluded.lift, n_train=excluded.n_train, n_eval=excluded.n_eval`,
		f.SymbolID, string(f.Horizon), f.Model, f.Ts, f.Prob, f.Accuracy, f.Brier,
		f.AUC, f.BaseRate, f.Lift, f.NTrain, f.NEval)
	return err
}

// ModelForecasts returns all stored model-leg forecasts for a symbol (every
// horizon + model), ordered by horizon then model.
func (s *Store) ModelForecasts(ctx context.Context, symbolID int64) ([]ModelForecast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT horizon, model, ts, prob, accuracy, brier, auc, base_rate, lift, n_train, n_eval
		FROM model_forecasts WHERE symbol_id=? ORDER BY horizon, model`, symbolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ModelForecast
	for rows.Next() {
		f := ModelForecast{SymbolID: symbolID}
		var h string
		if err := rows.Scan(&h, &f.Model, &f.Ts, &f.Prob, &f.Accuracy, &f.Brier,
			&f.AUC, &f.BaseRate, &f.Lift, &f.NTrain, &f.NEval); err != nil {
			return nil, err
		}
		f.Horizon = md.Horizon(h)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FleetLegAUC returns each leg's FLEET-WIDE out-of-sample AUC, keyed
// "<leg>|<horizon>", as the evaluation-weighted mean over every symbol the
// trainers have graded. Legs live in two tables — the model legs in
// model_forecasts, the forecast leg in forecasts — so both are read here and
// the caller sees one map.
//
// WHY A FLEET NUMBER EXISTS AT ALL. Admission is decided per symbol, and a
// per-symbol grade rests on a few hundred observations: across ~1,000 symbols,
// a one-sided 90% bound admits ~100 of them on chance alone. Measured on the
// live table, the pressure leg averages AUC 0.31 at 1d — it ranks BACKWARDS —
// yet 151 symbols individually cleared the bound. A leg that is anti-predictive
// on the fleet cannot be rescued by the tail of its own noise, so the caller
// uses this as a VETO over the per-symbol edge, never as a promotion.
//
// The weighted mean is a description of grades already taken, not a new
// interval, so it carries no z and no effective-N choice: the per-symbol Wilson
// bound still does the statistics.
func (s *Store) FleetLegAUC(ctx context.Context) (map[string]float64, error) {
	out := map[string]float64{}
	sums := map[string]float64{}
	wts := map[string]float64{}
	add := func(leg, h string, auc float64, n int) {
		// Exactly 0 or 1 is degenerate and excluded, matching
		// clusterstat.RankEdge so the fleet number and the per-symbol bound
		// never disagree about which rows count. 88 rows in the live table sit
		// at auc==0 and 13 at auc>=1; letting those into a weighted mean would
		// drag the veto threshold with artifacts.
		if n <= 0 || auc <= 0 || auc >= 1 {
			return
		}
		k := leg + "|" + h
		sums[k] += auc * float64(n)
		wts[k] += float64(n)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT model, horizon, auc, n_eval FROM model_forecasts`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var model, h string
		var auc float64
		var n int
		if err := rows.Scan(&model, &h, &auc, &n); err != nil {
			rows.Close() //nolint:errcheck
			return nil, err
		}
		add(model, h, auc, n)
	}
	rows.Close() //nolint:errcheck
	if err := rows.Err(); err != nil {
		return nil, err
	}
	frows, err := s.db.QueryContext(ctx, `SELECT horizon, auc, n_eval FROM forecasts`)
	if err != nil {
		return nil, err
	}
	defer frows.Close() //nolint:errcheck
	for frows.Next() {
		var h string
		var auc float64
		var n int
		if err := frows.Scan(&h, &auc, &n); err != nil {
			return nil, err
		}
		add("forecast", h, auc, n)
	}
	for k, w := range wts {
		if w > 0 {
			out[k] = sums[k] / w
		}
	}
	return out, frows.Err()
}
