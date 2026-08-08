// The settled-move independence key.
package store

import "context"

// BackfillSettleTs fills settle_ts on resolved rows that predate the column,
// deriving it the same way the resolver does: the newest 1d bar at or before the
// prediction timestamp.
//
// Idempotent and bounded, so it can run on every boot without a migration
// framework. Batched on the PRIMARY KEY, not rowid: prediction_outcomes is
// WITHOUT ROWID, so a rowid predicate would fail at runtime rather than at
// compile time. Rows with no bar at or before them stay NULL — an unknown settle
// bar is the honest value, and a day-clustered consumer must treat NULL as "fall
// back to trading_day" rather than as a claim.
func (s *Store) BackfillSettleTs(ctx context.Context, limit int) (int64, error) {
	res, err := s.w.ExecContext(ctx, `
		UPDATE prediction_outcomes
		SET settle_ts = (
		  SELECT MAX(b.ts) FROM bars b
		  WHERE b.symbol_id = prediction_outcomes.symbol_id
		    AND b.tf = '1d' AND b.ts <= prediction_outcomes.ts
		)
		WHERE settle_ts IS NULL AND resolved_at IS NOT NULL
		  AND (symbol_id, horizon, ts) IN (
		    SELECT symbol_id, horizon, ts FROM prediction_outcomes
		    WHERE settle_ts IS NULL AND resolved_at IS NOT NULL LIMIT ?
		  )`, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
