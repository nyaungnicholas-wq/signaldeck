package store

import (
	"context"
	"fmt"
	"strings"
)

// BackfillSettleTs fills settle_ts on resolved prediction_outcomes rows that
// predate the column, deriving it the same way the resolver does: the newest
// 1d bar at or before the prediction timestamp.
//
// Idempotent and bounded, so it can run on every boot without a migration
// framework. Batched on the PRIMARY KEY, not rowid: prediction_outcomes is
// WITHOUT ROWID, so a rowid predicate would fail at runtime rather than at
// compile time. Rows with no bar at or before them stay NULL — an unknown
// settle bar is the honest value, and a day-clustered consumer must treat
// NULL as "fall back to trading_day" rather than as a claim.
func (s *Store) BackfillSettleTs(ctx context.Context, limit int) (int64, error) {
	return s.backfillSettleFor(ctx, "prediction_outcomes", []string{"symbol_id", "horizon", "ts"}, limit)
}

// BackfillScoreSettleTs fills score_outcomes settle_ts; identical derivation to
// BackfillSettleTs so all three agree by construction.
func (s *Store) BackfillScoreSettleTs(ctx context.Context, limit int) (int64, error) {
	return s.backfillSettleFor(ctx, "score_outcomes", []string{"symbol_id", "horizon", "ts"}, limit)
}

// BackfillConfluenceSettleTs fills confluence_outcomes settle_ts; identical
// derivation to BackfillSettleTs so all three agree by construction.
func (s *Store) BackfillConfluenceSettleTs(ctx context.Context, limit int) (int64, error) {
	return s.backfillSettleFor(ctx, "confluence_outcomes", []string{"symbol_id", "ts", "horizon"}, limit)
}

func (s *Store) backfillSettleFor(ctx context.Context, table string, keyCols []string, limit int) (int64, error) {
	// The table name and key columns are compile-time constants from this file,
	// never request data, so interpolating them with fmt.Sprintf is not an
	// injection surface.
	keys := strings.Join(keyCols, ", ")
	q := fmt.Sprintf(`
		UPDATE %s
		SET settle_ts = (
		  SELECT MAX(b.ts) FROM bars b
		  WHERE b.symbol_id = %s.symbol_id
		    AND b.tf = '1d' AND b.ts <= %s.ts
		)
		WHERE settle_ts IS NULL AND resolved_at IS NOT NULL
		  AND (%s) IN (
		    SELECT %s FROM %s
		    WHERE settle_ts IS NULL AND resolved_at IS NOT NULL LIMIT ?
		  )`, table, table, table, keys, keys, table)
	res, err := s.w.ExecContext(ctx, q, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
