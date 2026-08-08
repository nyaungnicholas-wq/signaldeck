// Live-record queries for internal/forecastmon.
//
// Both queries DEDUPLICATE to one row per symbol per trading day before they
// measure anything, for the reason directionalrecord.go states: the pipeline
// writes many rows per symbol per forward period that all resolve against the
// SAME move, and pooling them inflates n by ~60x. A collapse detector fed
// pseudo-replicated rows would count one market-wide call as 328 observations —
// which is precisely the condition it exists to detect.
package store

import (
	"context"
	"time"
)

// ForecastDayStat is one trading day's forecast cross-section: how many symbols
// were forecast, and how many DISTINCT probabilities they received between them.
type ForecastDayStat struct {
	Day           string
	Symbols       int
	DistinctProbs int
}

// ForecastDayStats returns the per-day cross-section since `since`, oldest first.
//
// Probabilities are rounded to 3 decimals before the distinct count. Without
// rounding, floating-point noise in the last bits makes every value distinct and
// the collapse detector can never fire — the failure mode where a monitor exists,
// runs, reports success, and watches nothing.
func (s *Store) ForecastDayStats(ctx context.Context, horizon string, since time.Time) ([]ForecastDayStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH dedup AS (
		  SELECT symbol_id, date(ts,'unixepoch') AS d, prob,
		         ROW_NUMBER() OVER (PARTITION BY symbol_id, date(ts,'unixepoch')
		                            ORDER BY ts DESC) rn
		  FROM prediction_outcomes
		  WHERE horizon = ? AND resolved_at IS NOT NULL AND up IS NOT NULL
		    AND prob IS NOT NULL AND ts >= ?
		)
		SELECT d, COUNT(*), COUNT(DISTINCT ROUND(prob, 3))
		FROM dedup WHERE rn = 1 GROUP BY d ORDER BY d`,
		horizon, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ForecastDayStat
	for rows.Next() {
		var d ForecastDayStat
		if err := rows.Scan(&d.Day, &d.Symbols, &d.DistinctProbs); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ForecastBucket is one confidence band's claim measured against its outcome.
type ForecastBucket struct {
	Label  string
	N      int
	Said   float64
	Actual float64
}

// ForecastBuckets returns the per-confidence-band record since `since`, together
// with the window's realized base rate and its distinct trading-day count.
//
// The day count is returned rather than derived from N on purpose: N is
// symbol-days and the caller needs to know how many INDEPENDENT days stand
// behind it before it is allowed to draw a conclusion.
func (s *Store) ForecastBuckets(ctx context.Context, horizon string, since time.Time) ([]ForecastBucket, float64, int, error) {
	const q = `
	WITH dedup AS (
	  SELECT symbol_id, date(ts,'unixepoch') AS d, prob, up,
	         ROW_NUMBER() OVER (PARTITION BY symbol_id, date(ts,'unixepoch')
	                            ORDER BY ts DESC) rn
	  FROM prediction_outcomes
	  WHERE horizon = ? AND resolved_at IS NOT NULL AND up IS NOT NULL
	    AND prob IS NOT NULL AND ts >= ?
	), b AS (
	  SELECT prob, up, d,
	         CASE WHEN prob < 0.30 THEN '<30%%'
	              WHEN prob < 0.45 THEN '30-45%%'
	              WHEN prob < 0.55 THEN '45-55%%'
	              WHEN prob < 0.70 THEN '55-70%%'
	              ELSE '>=70%%' END AS label
	  FROM dedup WHERE rn = 1
	)
	SELECT label, COUNT(*), AVG(prob), AVG(CAST(up AS REAL)) FROM b GROUP BY label`

	rows, err := s.db.QueryContext(ctx, sqlPct(q), horizon, since.Unix())
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ForecastBucket
	for rows.Next() {
		var b ForecastBucket
		if err := rows.Scan(&b.Label, &b.N, &b.Said, &b.Actual); err != nil {
			return nil, 0, 0, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}

	var base float64
	var days int
	err = s.db.QueryRowContext(ctx, `
		WITH dedup AS (
		  SELECT symbol_id, date(ts,'unixepoch') AS d, up,
		         ROW_NUMBER() OVER (PARTITION BY symbol_id, date(ts,'unixepoch')
		                            ORDER BY ts DESC) rn
		  FROM prediction_outcomes
		  WHERE horizon = ? AND resolved_at IS NOT NULL AND up IS NOT NULL
		    AND prob IS NOT NULL AND ts >= ?
		)
		SELECT COALESCE(AVG(CAST(up AS REAL)), 0), COUNT(DISTINCT d)
		FROM dedup WHERE rn = 1`, horizon, since.Unix()).Scan(&base, &days)
	if err != nil {
		return nil, 0, 0, err
	}
	return out, base, days, nil
}

// sqlPct un-escapes the doubled percent signs used in the bucket labels above.
// The labels are display text ("<30%"), and doubling them in the literal keeps
// the query readable next to Go's own formatting verbs without either side
// silently rewriting the other.
func sqlPct(q string) string {
	out := make([]byte, 0, len(q))
	for i := 0; i < len(q); i++ {
		if q[i] == '%' && i+1 < len(q) && q[i+1] == '%' {
			out = append(out, '%')
			i++
			continue
		}
		out = append(out, q[i])
	}
	return string(out)
}
