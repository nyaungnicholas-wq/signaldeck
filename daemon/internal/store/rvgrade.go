package store

import "context"

// RVGradeRow is one resolved forecast as the live grader (internal/rvgrade)
// reads it.
type RVGradeRow struct {
	SymbolID, Ts            int64
	RVHat, NullRW, NullEWMA float64
	Actual                  float64
}

// RVGradeRows returns the resolved forecasts at one horizon that were frozen
// STRICTLY AFTER createdAfter (unix seconds), ordered by symbol then call bar.
// The registered start rule: forecasts frozen at or before the registration
// are not part of the live record.
func (s *Store) RVGradeRows(ctx context.Context, horizon int, createdAfter int64) ([]RVGradeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, rv_hat, null_rw, null_ewma, actual
		  FROM rv_forecasts
		 WHERE horizon=? AND actual IS NOT NULL AND created_ts > ?
		 ORDER BY symbol_id, ts`, horizon, createdAfter)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RVGradeRow
	for rows.Next() {
		var r RVGradeRow
		if err := rows.Scan(&r.SymbolID, &r.Ts, &r.RVHat, &r.NullRW, &r.NullEWMA, &r.Actual); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
