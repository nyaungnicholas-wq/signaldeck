// Per-symbol agents wave: persistence for each symbol's OWN learned model
// (weights + calibration + skill + personality + tier), plus the symbol-scoped
// labeled-feature join the learner trains on.
//
// Read helpers go through the pooled s.db handle; the single upsert goes
// through the write path s.w — same discipline as the rest of the store.
package store

import (
	"context"
	"database/sql"
	"encoding/json"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// LabeledFeaturesBySymbol joins ONE symbol's stored feature vectors to its
// RESOLVED prediction outcomes for one horizon — that symbol's own labeled
// training set. Newest first, capped at limit. Only resolved, non-voided rows
// are returned (o.up / o.fwd_return NOT NULL), so a caller can never train on a
// still-open prediction: this is the prequential no-leakage guarantee carried
// down to the per-symbol level (a feature row is a training example only AFTER
// its own outcome has been realized).
func (s *Store) LabeledFeaturesBySymbol(ctx context.Context, symbolID int64, h md.Horizon, limit int) ([]LabeledFeature, error) {
	return s.labeledFeaturesBySymbol(ctx, symbolID, h, 0, limit)
}

// LabeledFeaturesBySymbolVersion is LabeledFeaturesBySymbol restricted to ONE
// feature-schema version. Trainers that flatten the key union across rows (the
// GBM) must use this: mixing v2 rows (no micro_*/vix_* keys) with v3 rows makes
// "feature absent because older schema" indistinguishable from a real 0-valued
// signal, diluting the training set (fails safe toward "no edge", but cleaner
// to avoid entirely).
func (s *Store) LabeledFeaturesBySymbolVersion(ctx context.Context, symbolID int64, h md.Horizon, version, limit int) ([]LabeledFeature, error) {
	return s.labeledFeaturesBySymbol(ctx, symbolID, h, version, limit)
}

// labeledFeaturesBySymbol implements both variants; version 0 = all versions.
//
// ONE ROW PER UTC DAY, newest wins. The prediction runner re-scores a symbol
// many times a day and writes a feature row each time, but a 1d/1w label is a
// property of the DAY, not of the scoring instant: every row inside one day
// carries the identical outcome. Measured 2026-08-05 on the live v12 corpus,
// each symbol's labeled set was ~310 rows over 8 distinct days — 39 same-label
// copies per day — so the honest sample was 8 observations, not 310.
//
// Returning those copies corrupted BOTH things this query feeds:
//
//   - TRAINING: 39 near-identical vectors sharing one label let a model fit the
//     handful of days that happened to be re-scored most, which is a sampling
//     artifact of the runner's schedule and nothing about the market.
//   - GRADING: the out-of-sample lift that admits a leg to the live blend was
//     computed over the same duplicated rows. A window of 8 days where one day
//     dominates the row count is wildly class-imbalanced (measured up-rates of
//     0.029 and 0.971 on real symbols), which drove the majority-class floor to
//     0.966 and benched every leg by arithmetic — 0 of 43 gbm legs admitted, 0
//     alphax ever — no matter how good the model was.
//
// Deduping here fixes both at the source, and uses the SAME independence rule
// the accuracy registry already grades with, so the surface that TRAINS a leg
// and the surface that JUDGES it finally agree about what one observation is.
func (s *Store) labeledFeaturesBySymbol(ctx context.Context, symbolID int64, h md.Horizon, version, limit int) ([]LabeledFeature, error) {
	q := `
		SELECT ts, version, vec, up, fwd_return FROM (
			SELECT f.ts AS ts, f.version AS version, f.vec AS vec,
			       o.up AS up, o.fwd_return AS fwd_return,
			       ROW_NUMBER() OVER (
			         PARTITION BY f.ts/86400 ORDER BY f.ts DESC
			       ) AS rn
			FROM features f
			JOIN prediction_outcomes o
			  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
			WHERE f.symbol_id=? AND f.horizon=? AND o.resolved_at IS NOT NULL
			  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL`
	args := []any{symbolID, string(h)}
	if version > 0 {
		q += ` AND f.version=?`
		args = append(args, version)
	}
	q += `
		)
		WHERE rn=1
		ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []LabeledFeature
	for rows.Next() {
		lf := LabeledFeature{SymbolID: symbolID, Horizon: h}
		var vec string
		if err := rows.Scan(&lf.Ts, &lf.Version, &vec, &lf.Up, &lf.FwdReturn); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(vec), &lf.Vec); err != nil {
			return nil, err
		}
		out = append(out, lf)
	}
	return out, rows.Err()
}

// StockFeatureDayFold counts the same stock feature rows two ways: folded on a
// UTC-midnight boundary (what the PARTITION BY above and every other
// day-clustered statistic currently uses) and folded on the trading-day
// boundary (md.TradingDay). Only rows at or after `since` are counted.
//
// The gap between the two IS the pseudo-replication the day fold is supposed to
// remove. A US extended session closes at 20:00 ET — 00:00Z under EDT, 01:00Z
// under EST — so its tail lands in the NEXT UTC day and is counted as a second
// independent observation of the same session. Every published interval divides
// by that count, so the excess overstates effective N and narrows intervals in
// the direction that flatters the platform.
//
// Returned as raw counts rather than a rate so the caller can state both
// numbers in the finding — "15,976 real days, 630 phantom" is auditable in a
// way that "3.9%" is not.
func (s *Store) StockFeatureDayFold(ctx context.Context, since int64) (utcDays, tradingDays int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT f.symbol_id || ':' || (f.ts/?)),
		       COUNT(DISTINCT f.symbol_id || ':' || ((f.ts-?)/?))
		FROM features f
		JOIN symbols s ON s.id=f.symbol_id
		WHERE s.market='stocks' AND f.ts >= ?`,
		md.SecondsPerDay, md.TradingDayOffsetSecs, md.SecondsPerDay, since).
		Scan(&utcDays, &tradingDays)
	return utcDays, tradingDays, err
}

// SymbolModelRow is one persisted per-symbol agent. The JSON blob columns are
// carried as raw strings here — the symbolagent package owns their shape and
// (un)marshals them, so this layer stays free of the learning types.
type SymbolModelRow struct {
	SymbolID    int64  `json:"-"`
	Horizon     string `json:"horizon"`
	Weights     string `json:"weights"`     // JSON: component -> weight
	Calibration string `json:"calibration"` // JSON: isotonic knots
	Skill       string `json:"skill"`       // JSON: component -> {hitRate, ic, n}
	Personality string `json:"personality"`
	NSamples    int    `json:"nSamples"`
	Tier        string `json:"tier"`
	UpdatedTs   int64  `json:"updatedTs"`
}

// UpsertSymbolModel writes one symbol+horizon model in place (idempotent on the
// unique key). Cheap enough to call for every active symbol each learner pass.
func (s *Store) UpsertSymbolModel(ctx context.Context, m SymbolModelRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO symbol_models
		  (symbol_id, horizon, weights, calibration, skill, personality, n_samples, tier, updated_ts)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id, horizon) DO UPDATE SET
		  weights=excluded.weights,
		  calibration=excluded.calibration,
		  skill=excluded.skill,
		  personality=excluded.personality,
		  n_samples=excluded.n_samples,
		  tier=excluded.tier,
		  updated_ts=excluded.updated_ts`,
		m.SymbolID, m.Horizon, m.Weights, m.Calibration, m.Skill, m.Personality,
		m.NSamples, m.Tier, m.UpdatedTs)
	return err
}

// SymbolModel fetches one symbol+horizon model. ok=false when none is stored
// yet (the learner hasn't run for it, or it's a brand-new symbol).
func (s *Store) SymbolModel(ctx context.Context, symbolID int64, h md.Horizon) (SymbolModelRow, bool, error) {
	m := SymbolModelRow{SymbolID: symbolID, Horizon: string(h)}
	err := s.db.QueryRowContext(ctx, `
		SELECT weights, calibration, skill, personality, n_samples, tier, updated_ts
		FROM symbol_models WHERE symbol_id=? AND horizon=?`,
		symbolID, string(h)).
		Scan(&m.Weights, &m.Calibration, &m.Skill, &m.Personality, &m.NSamples, &m.Tier, &m.UpdatedTs)
	if err == sql.ErrNoRows {
		return m, false, nil
	}
	return m, err == nil, err
}
