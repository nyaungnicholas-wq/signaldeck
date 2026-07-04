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
