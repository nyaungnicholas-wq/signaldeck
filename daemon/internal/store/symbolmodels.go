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

// LabeledFeaturesBySymbolIndependent is LabeledFeaturesBySymbol collapsed to ONE
// row per UTC day — the LATEST that day — i.e. that symbol's INDEPENDENT sample.
//
// For a per-SYMBOL learner the replication is at its most extreme: every row it
// sees is the same symbol, so a hot name's ~150 same-day feature rows are ~150
// "own resolved outcomes" that all settle against ONE forward move. Measured
// 2026-07-16, that is not theoretical — 369 symbols held the `personal` tier on
// 46-198 rows spanning just TWO distinct days, and the deepest history in the
// whole fleet was 13 independent observations against a MinPersonal floor of 40.
// Counting rows is what let a symbol earn its own calibration map in a day.
func (s *Store) LabeledFeaturesBySymbolIndependent(ctx context.Context, symbolID int64, h md.Horizon, limit int) ([]LabeledFeature, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, MAX(f.ts) AS ts, f.version, f.vec, o.up, o.fwd_return
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		WHERE f.symbol_id=? AND f.horizon=? AND o.resolved_at IS NOT NULL
		  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL
		GROUP BY f.ts/86400
		ORDER BY ts DESC LIMIT ?`,
		symbolID, string(h), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanLabeledFeatures(rows, h)
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
func (s *Store) labeledFeaturesBySymbol(ctx context.Context, symbolID int64, h md.Horizon, version, limit int) ([]LabeledFeature, error) {
	q := `
		SELECT f.ts, f.version, f.vec, o.up, o.fwd_return
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
	q += ` ORDER BY f.ts DESC LIMIT ?`
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
