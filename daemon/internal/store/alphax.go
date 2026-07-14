// CROSS-SECTIONAL ALPHA wave (alphax) store methods.
//
// alphax_models holds ONE row per horizon: the pooled cross-sectional model's
// latest purged-walk-forward OOS grade, its gate state, and the serialized
// model (provenance — the grade that shipped is reproducible from the model
// that produced it). Per-symbol CURRENT scores ride the existing
// model_forecasts table under model="alphax", written ONLY when the grade's
// OOS lift > 0 and DELETED when the model regrades gated — an edgeless score
// must never linger anywhere it could be mistaken for a signal.
//
// Reads use the pooled s.db handle; writes go through s.w — the same
// discipline as the rest of the store.
package store

import (
	"context"
	"database/sql"
	"encoding/json"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ModelAlphaX names the pooled cross-sectional leg in model_forecasts. Since
// the alphax-leg wave the PredictionRunner DOES match it (alongside ModelGBM /
// ModelMeanRev) and feeds it into the ensemble as a GATED leg: a row exists
// only while the pooled model's OOS lift > 0 (gated regrades delete rows), and
// the ensemble re-applies the identical lift>0 gate before blending.
const ModelAlphaX = "alphax"

// AlphaXModelRow is one horizon's stored cross-sectional model + OOS grade.
type AlphaXModelRow struct {
	Horizon   md.Horizon `json:"horizon"`
	Ts        int64      `json:"ts"`
	OOSLift   float64    `json:"oosLift"`
	OOSAUC    float64    `json:"oosAuc"`
	OOSAcc    float64    `json:"oosAcc"`
	BaseRate  float64    `json:"baseRate"`
	NTrain    int        `json:"nTrain"`
	NTest     int        `json:"nTest"`
	Gated     bool       `json:"gated"` // true = OOS lift <= 0: never blended, never displayed as signal
	ModelJSON string     `json:"-"`     // serialized {keys, gbm model} — provenance, not an API payload
}

// UpsertAlphaXModel writes one horizon's latest model+grade in place
// (idempotent on the horizon key — the trainer regrades every pass).
func (s *Store) UpsertAlphaXModel(ctx context.Context, r AlphaXModelRow) error {
	gated := 0
	if r.Gated {
		gated = 1
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO alphax_models
		  (horizon, ts, oos_lift, oos_auc, oos_acc, base_rate, n_train, n_test, gated, model_json)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(horizon) DO UPDATE SET
		  ts=excluded.ts, oos_lift=excluded.oos_lift, oos_auc=excluded.oos_auc,
		  oos_acc=excluded.oos_acc, base_rate=excluded.base_rate,
		  n_train=excluded.n_train, n_test=excluded.n_test,
		  gated=excluded.gated, model_json=excluded.model_json`,
		string(r.Horizon), r.Ts, r.OOSLift, r.OOSAUC, r.OOSAcc, r.BaseRate,
		r.NTrain, r.NTest, gated, r.ModelJSON)
	return err
}

// AlphaXModel returns one horizon's stored model row (ok=false when the
// trainer has never graded that horizon — honest absence).
func (s *Store) AlphaXModel(ctx context.Context, h md.Horizon) (AlphaXModelRow, bool, error) {
	r := AlphaXModelRow{Horizon: h}
	var gated int
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, oos_lift, oos_auc, oos_acc, base_rate, n_train, n_test, gated, model_json
		FROM alphax_models WHERE horizon=?`, string(h)).
		Scan(&r.Ts, &r.OOSLift, &r.OOSAUC, &r.OOSAcc, &r.BaseRate, &r.NTrain, &r.NTest, &gated, &r.ModelJSON)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	r.Gated = gated == 1
	return r, true, nil
}

// LabeledFeaturesAll is LabeledFeaturesBySymbolVersion UNSCOPED by symbol —
// the pooled cross-sectional training set: every symbol's stored feature
// vectors joined to their RESOLVED, non-voided outcomes for one horizon,
// newest first, capped at limit. minVersion admits every schema version at or
// above it: the alphax trainer pools v3 AND v4 (and later) rows TOGETHER —
// unlike the per-symbol GBM (which pins one version so absent-vs-zero stays
// clean), the cross-sectional pool needs volume above schema purity, the
// union-of-keys flatten handles the missing fields, and the dilution biases
// toward "no edge" (fails safe: a flattered grade is impossible, only a
// dampened one).
func (s *Store) LabeledFeaturesAll(ctx context.Context, h md.Horizon, minVersion, limit int) ([]LabeledFeature, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, f.ts, f.version, f.vec, o.up, o.fwd_return
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		WHERE f.horizon=? AND f.version>=? AND o.resolved_at IS NOT NULL
		  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL
		ORDER BY f.ts DESC LIMIT ?`,
		string(h), minVersion, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanLabeledFeatures(rows, h)
}

// scanLabeledFeatures collects LabeledFeature rows from a query over
// (symbol_id, ts, version, vec, up, fwd_return).
func scanLabeledFeatures(rows *sql.Rows, h md.Horizon) ([]LabeledFeature, error) {
	var out []LabeledFeature
	for rows.Next() {
		lf := LabeledFeature{Horizon: h}
		var vec string
		if err := rows.Scan(&lf.SymbolID, &lf.Ts, &lf.Version, &vec, &lf.Up, &lf.FwdReturn); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(vec), &lf.Vec); err != nil {
			return nil, err
		}
		out = append(out, lf)
	}
	return out, rows.Err()
}

// LatestFeaturesByHorizon returns each symbol's NEWEST stored feature vector
// for one horizon with ts >= sinceTs — the live cross-section the alphax
// worker scores. One row per symbol (ties across versions at the same ts keep
// the highest version).
func (s *Store) LatestFeaturesByHorizon(ctx context.Context, h md.Horizon, sinceTs int64) ([]FeatureRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, f.ts, f.version, f.vec
		FROM features f
		JOIN (SELECT symbol_id, MAX(ts) AS mts FROM features
		      WHERE horizon=? AND ts>=? GROUP BY symbol_id) m
		  ON m.symbol_id=f.symbol_id AND m.mts=f.ts
		WHERE f.horizon=? AND f.ts>=?
		ORDER BY f.symbol_id, f.version DESC`,
		string(h), sinceTs, string(h), sinceTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []FeatureRow
	for rows.Next() {
		f := FeatureRow{Horizon: h}
		var vec string
		if err := rows.Scan(&f.SymbolID, &f.Ts, &f.Version, &vec); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].SymbolID == f.SymbolID {
			continue // same symbol+ts at an older version — keep the first (highest)
		}
		if err := json.Unmarshal([]byte(vec), &f.Vec); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// TopModelForecastRow is one symbol's stored model-leg score with its ticker
// resolved (for leaderboard-style reads).
type TopModelForecastRow struct {
	Symbol string  `json:"symbol"`
	Prob   float64 `json:"prob"`
	Ts     int64   `json:"ts"`
}

// TopModelForecastsByModel returns the highest-prob stored scores for one
// model+horizon across the fleet, symbol-resolved, capped at limit.
func (s *Store) TopModelForecastsByModel(ctx context.Context, model string, h md.Horizon, limit int) ([]TopModelForecastRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.symbol, m.prob, m.ts
		FROM model_forecasts m JOIN symbols s ON s.id=m.symbol_id
		WHERE m.model=? AND m.horizon=?
		ORDER BY m.prob DESC LIMIT ?`, model, string(h), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []TopModelForecastRow
	for rows.Next() {
		var r TopModelForecastRow
		if err := rows.Scan(&r.Symbol, &r.Prob, &r.Ts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteModelForecastsByModel removes ALL of one model's stored per-symbol
// scores for a horizon. The alphax trainer calls this when a regrade lands
// gated (OOS lift <= 0): stale scores must not linger where they could be
// mistaken for a live signal.
func (s *Store) DeleteModelForecastsByModel(ctx context.Context, model string, h md.Horizon) (int64, error) {
	res, err := s.w.ExecContext(ctx,
		`DELETE FROM model_forecasts WHERE model=? AND horizon=?`, model, string(h))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
