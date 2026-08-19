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

	"github.com/nyaungnicholas-wq/signaldeck/internal/publication"
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
		 WHERE superseded_by IS NULL
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
// be RESOLVED. ok=false means there are no unresolved structural calls at all,
// which callers must render as "none pending" rather than as a zero time.
//
// This is NOT the date a verdict can exist — see EarliestVerdictAt. The
// distinction is the whole of the 2026-08-04 gradability amendment
// (prereg_records seq 37): a resolved forecast is one data point, and the
// grader refuses to publish an interval until MIN_DISTINCT_BLOCKS of them
// exist. Serving this date as "gradeable" is what made 2026-08-07 look
// reachable when the real figure was seven months later.
func (s *Store) EarliestGradeableAt(ctx context.Context) (t time.Time, ok bool, err error) {
	var due sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
		SELECT MIN(ts + CAST(horizon_days * ? * 86400 AS INTEGER))
		  FROM regime_outcomes
		 WHERE resolved_at IS NULL AND superseded_by IS NULL`, tradingToCalendar).Scan(&due)
	if err != nil || !due.Valid {
		return time.Time{}, false, err
	}
	return time.Unix(due.Int64, 0).UTC(), true, nil
}

// EarliestVerdictAt is the soonest moment any structural kind can produce a
// published VERDICT rather than merely a resolved row. Two gates, not one:
//
//	block gate: publication.MinDistinctBlocks distinct blocks must exist, where
//	            a block is call_day / horizon_days (integer division, matching
//	            tools/accuracy_registry.py's horizon_blocks). The first day that
//	            lands in the Nth distinct block is (firstBlock + N - 1) * horizon.
//	resolution: the calls in that final block must themselves come due, which is
//	            another horizon_days * tradingToCalendar days.
//
// Only rows that can enter a benchmark denominator start the clock. A row with
// a NULL naive_label carries no frozen naive-persistence baseline, is excluded
// from every structural benchmark, and is NOT backfillable (a persistence label
// computed after the outcome is known is a hindsight baseline). Counting those
// rows would repeat the exact defect this function exists to fix: a date that
// ignores one of its own determinants.
//
// ok=false means no kind has a benchmark-eligible row yet, which callers must
// render as "no verdict date can be derived" rather than as a zero time.
//
// The Python twin tools/structural_liveness.py deliberately does NOT gain this:
// it asks whether a DUE row went ungraded, which keys off resolution. The two
// have not drifted; they answer different questions.
func (s *Store) EarliestVerdictAt(ctx context.Context) (t time.Time, ok bool, err error) {
	// MIN(ts/86400), NOT MIN(day). `day` is md.SettleDay — the UTC day of the
	// last 1d bar at or before the call (see writeRegimeOutcome) — so for a
	// stale or delisted symbol it trails the call by however long that symbol's
	// data has been dead. MIN() over the whole kind therefore selects the single
	// stalest name in the table, not the earliest call.
	//
	// Measured 2026-08-11: every structural call in regime_outcomes was made on
	// or after 2026-07-18, yet MIN(day) returned 2025-07-15 for trend21/trend63/
	// liquidity21 (lag 373-393d; e.g. symbol 42, called 2026-07-23, day
	// 2025-07-15). That fed a firstBlock 373 days early and this function served
	// 2026-01-31 — a verdict date 191 days IN THE PAST — over MCP, when the
	// truthful figure was 2027-02-13. Which is precisely the defect the header
	// above says this function exists to eliminate, re-entered through the
	// column rather than the arithmetic.
	rows, err := s.db.QueryContext(ctx, `
		SELECT horizon_days, MIN(ts / 86400)
		  FROM regime_outcomes
		 WHERE naive_label IS NOT NULL AND horizon_days > 0
		   AND superseded_by IS NULL
		 GROUP BY kind`)
	if err != nil {
		return time.Time{}, false, err
	}
	defer rows.Close() //nolint:errcheck

	var best int64
	found := false
	for rows.Next() {
		var horizon int64
		var firstDay sql.NullInt64
		if err := rows.Scan(&horizon, &firstDay); err != nil {
			return time.Time{}, false, err
		}
		if !firstDay.Valid || horizon <= 0 {
			continue
		}
		// Block-aligned, matching the grader's day/horizon bucketing. This can
		// be up to horizon-1 days EARLIER than the conservative
		// firstDay + (N-1)*horizon form recorded in the seq-37 amendment; both
		// are far past the original 2026-08-07 and the amendment states which
		// form it used.
		firstBlock := firstDay.Int64 / horizon
		gateDay := (firstBlock + int64(publication.MinDistinctBlocks) - 1) * horizon
		due := gateDay*86400 + int64(float64(horizon)*tradingToCalendar*86400)
		if !found || due < best {
			best, found = due, true
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, false, err
	}
	if !found {
		return time.Time{}, false, nil
	}
	return time.Unix(best, 0).UTC(), true, nil
}
