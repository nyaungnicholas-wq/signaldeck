package store

import (
	"context"
	"database/sql"
)

// NewestDailyBarTs is the timestamp of the newest stored stock daily bar, or 0
// when there is none. A forecast is recomputed on a clock, so its own
// timestamp says nothing about how old its input is; this does.
func (s *Store) NewestDailyBarTs(ctx context.Context) (int64, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT MAX(b.ts) FROM bars b JOIN symbols s ON s.id = b.symbol_id
		WHERE b.tf = '1d' AND s.market = 'stocks'`).Scan(&ts)
	return ts.Int64, err
}
