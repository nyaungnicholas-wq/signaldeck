// Earliest-gradeable-date derivation for structural forecasts.
//
// WHY THIS EXISTS. "First gradable 2026-08-07" was written into a served MCP
// tool field, into seeded evidence-claim text, and into eight documents. The
// real date, derived from the forecasts actually on disk, is 2026-08-17 — ten
// days later. Nothing had gone wrong; the calls simply accrued differently than
// the date was written. But a hard-coded date cannot notice that, so it went on
// being served as fact.
//
// The rule this closes: a date the data determines must be read from the data.
// The formula matches tools/structural_liveness.py exactly — a call issued at
// ts over horizon_days trading days comes due at ts + horizon_days*1.45 days,
// the 1.45 converting trading days to calendar days. Both implementations must
// move together; the Go side is the one the API serves and the Python side is
// the one the liveness check alarms on.
package store

import (
	"context"
	"database/sql"
	"time"
)

// tradingToCalendar converts a trading-day horizon to calendar days. Kept
// identical to structural_liveness.py's 1.45 factor.
const tradingToCalendar = 1.45

// EarliestGradeable is one kind's first gradable moment.
type EarliestGradeable struct {
	Kind        string    `json:"kind"`
	Total       int       `json:"total"`
	Resolved    int       `json:"resolved"`
	GradeableAt time.Time `json:"gradeable_at"`
}

// EarliestGradeableByKind returns, per structural kind, when its earliest
// UNRESOLVED call first becomes gradable. Kinds with nothing unresolved are
// omitted: there is no future date to report for work already done.
func (s *Store) EarliestGradeableByKind(ctx context.Context) ([]EarliestGradeable, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind,
		       COUNT(*),
		       SUM(CASE WHEN resolved_at IS NOT NULL THEN 1 ELSE 0 END),
		       MIN(CASE WHEN resolved_at IS NULL
		                THEN ts + CAST(horizon_days * ? * 86400 AS INTEGER) END)
		  FROM regime_outcomes
		 GROUP BY kind
		 ORDER BY kind`, tradingToCalendar)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []EarliestGradeable
	for rows.Next() {
		var e EarliestGradeable
		var resolved sql.NullInt64
		var due sql.NullInt64
		if err := rows.Scan(&e.Kind, &e.Total, &resolved, &due); err != nil {
			return nil, err
		}
		if !due.Valid {
			continue
		}
		e.Resolved = int(resolved.Int64)
		e.GradeableAt = time.Unix(due.Int64, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// EarliestGradeableAt is the single soonest moment any structural forecast can
// be graded. ok=false means there are no unresolved structural calls at all,
// which callers must render as "none pending" rather than as a zero time.
func (s *Store) EarliestGradeableAt(ctx context.Context) (t time.Time, ok bool, err error) {
	var due sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
		SELECT MIN(ts + CAST(horizon_days * ? * 86400 AS INTEGER))
		  FROM regime_outcomes WHERE resolved_at IS NULL`, tradingToCalendar).Scan(&due)
	if err != nil || !due.Valid {
		return time.Time{}, false, err
	}
	return time.Unix(due.Int64, 0).UTC(), true, nil
}
