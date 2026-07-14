// Derived-table retention read/prune helpers (tiered-storage wave, phase 2).
//
// The bars/snapshots/anomalies tiers already archive-before-prune in the
// Downsampler. This file adds the SAME contract for the high-volume DERIVED
// tables that otherwise grow unbounded — scores, score_outcomes, and the
// feature store — so the maintain.DerivedRetention worker can export every row
// to the cold gzip-CSV archive BEFORE it is deleted, and prune ONLY what was
// durably archived. Reads use s.db; deletes go through the single writer s.w.
//
// HONESTY / PERMANENCE:
//   - predictions + prediction_outcomes are NEVER touched here — they are the
//     live track record (the flywheel's labels) and stay forever.
//   - features are only ever pruned once their prediction has RESOLVED: an
//     unlabeled training row is never deleted (it may still become a label).
//   - Every read is ts-ascending and bounded by limit so a huge first-run
//     backlog archives+prunes in batches instead of buffering a whole table.
package store

import (
	"context"
	"database/sql"
)

// ScoreRow is one scores row for the cold archive (symbol join done by the
// Archiver's name map, like bars).
type ScoreRow struct {
	SymbolID   int64
	Horizon    string
	Ts         int64
	Score      float64
	Components string
}

// ScoreOutcomeRow is one score_outcomes row for the cold archive. FwdReturn and
// ResolvedAt are nullable (an outcome may be pruned while still unresolved is
// impossible — the window has closed — but the columns are nullable in the
// schema, so they are carried faithfully).
type ScoreOutcomeRow struct {
	SymbolID   int64
	Horizon    string
	Ts         int64
	Score      float64
	FwdReturn  sql.NullFloat64
	ResolvedAt sql.NullInt64
}

// FeatureArchiveRow is one features row for the cold archive (the raw JSON vec
// is carried verbatim so the archived training set round-trips exactly).
type FeatureArchiveRow struct {
	ID       int64
	SymbolID int64
	Horizon  string
	Ts       int64
	Version  int
	Vec      string
}

// ScoresBefore returns up to limit scores with ts < cutoff, ts ascending — the
// archive-before-prune read (same contract as BarsBelow): the caller archives
// the returned rows, then prunes the SAME [<cutoff) predicate.
func (s *Store) ScoresBefore(ctx context.Context, cutoff int64, limit int) ([]ScoreRow, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, horizon, ts, score, components FROM scores
		WHERE ts < ? ORDER BY ts LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]ScoreRow, 0, 8)
	for rows.Next() {
		var r ScoreRow
		if err := rows.Scan(&r.SymbolID, &r.Horizon, &r.Ts, &r.Score, &r.Components); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteScoresBefore deletes scores with ts < cutoff (retention). Callers MUST
// have durably archived the rows first (DerivedRetention fail-safe).
func (s *Store) DeleteScoresBefore(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM scores WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ScoreOutcomesBefore returns up to limit score_outcomes with ts < cutoff, ts
// ascending — archive-before-prune read, same contract as ScoresBefore.
func (s *Store) ScoreOutcomesBefore(ctx context.Context, cutoff int64, limit int) ([]ScoreOutcomeRow, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, horizon, ts, score, fwd_return, resolved_at FROM score_outcomes
		WHERE ts < ? ORDER BY ts LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]ScoreOutcomeRow, 0, 8)
	for rows.Next() {
		var r ScoreOutcomeRow
		if err := rows.Scan(&r.SymbolID, &r.Horizon, &r.Ts, &r.Score, &r.FwdReturn, &r.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteScoreOutcomesBefore deletes score_outcomes with ts < cutoff (retention).
// Callers MUST have durably archived the rows first.
func (s *Store) DeleteScoreOutcomesBefore(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM score_outcomes WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ResolvedFeaturesBefore returns up to limit feature rows with ts < cutoff
// WHOSE PREDICTION HAS RESOLVED, ts ascending. An unlabeled feature (no resolved
// prediction_outcomes row on its (symbol,horizon,ts)) is NEVER returned — it may
// still become a training label. The delete uses the identical predicate, so the
// archived set equals the deleted set exactly (batch fail-safe correctness).
func (s *Store) ResolvedFeaturesBefore(ctx context.Context, cutoff int64, limit int) ([]FeatureArchiveRow, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.symbol_id, f.horizon, f.ts, f.version, f.vec
		FROM features f
		WHERE f.ts < ? AND EXISTS (
		  SELECT 1 FROM prediction_outcomes po
		  WHERE po.symbol_id = f.symbol_id AND po.horizon = f.horizon
		    AND po.ts = f.ts AND po.resolved_at IS NOT NULL)
		ORDER BY f.ts, f.id LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]FeatureArchiveRow, 0, 8)
	for rows.Next() {
		var r FeatureArchiveRow
		if err := rows.Scan(&r.ID, &r.SymbolID, &r.Horizon, &r.Ts, &r.Version, &r.Vec); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteResolvedFeaturesBefore deletes features with ts < cutoff whose
// prediction has resolved (identical predicate to ResolvedFeaturesBefore).
// Callers MUST have durably archived the rows first.
func (s *Store) DeleteResolvedFeaturesBefore(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.w.ExecContext(ctx, `
		DELETE FROM features WHERE ts < ? AND EXISTS (
		  SELECT 1 FROM prediction_outcomes po
		  WHERE po.symbol_id = features.symbol_id AND po.horizon = features.horizon
		    AND po.ts = features.ts AND po.resolved_at IS NOT NULL)`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
