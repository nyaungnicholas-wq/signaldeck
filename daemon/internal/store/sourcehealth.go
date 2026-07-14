package store

import (
	"context"
	"database/sql"
)

// MaxSourceTs runs a data-source freshness probe — a query that must SELECT one
// nullable integer of unix seconds (the newest row's timestamp for one external
// source) — and returns that value plus whether any row exists at all.
//
// The query is a compile-time constant drawn from internal/srchealth's source
// registry, never user input, so its interpolation of table/column names is
// safe. Runs on the READ pool: the freshness auditor only reads.
func (s *Store) MaxSourceTs(ctx context.Context, query string) (int64, bool, error) {
	var v sql.NullInt64
	if err := s.db.QueryRowContext(ctx, query).Scan(&v); err != nil {
		return 0, false, err
	}
	return v.Int64, v.Valid, nil
}
