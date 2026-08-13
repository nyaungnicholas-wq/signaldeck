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
	Day     string
	Symbols int // whole cross-section that day: forecast + withheld
	// DistinctProbs counts distinct values among rows that CARRY a forecast.
	// Withheld rows are excluded — see ForecastDayStatsRaw for why counting
	// them made the statistic move backwards.
	DistinctProbs int
	// Withheld is how many of Symbols the ensemble declined to forecast
	// (n_used = 0). Zero on the resolved-outcome side, which has no such rows.
	Withheld int
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

// ForecastDayStatsRaw is ForecastDayStats against the PREDICTIONS table rather
// than resolved outcomes, and it exists because the outcome-side view is a full
// horizon late.
//
// prediction_outcomes only carries a row once it has RESOLVED, so at a 1d
// horizon a collapse that starts today is invisible until tomorrow. That is
// exactly how the 2026-08-07 collapse was still running unseen: raw_prob fell
// from 1,475 distinct values across 329 symbols to 78, and nothing could report
// it because none of those rows had resolved yet.
//
// This reads what the model EMITTED, so the same collapse is visible the day it
// happens. It measures raw_prob — upstream of calibration — so it separates "the
// model stopped discriminating" from "the calibrator flattened a good score",
// which are different failures with different fixes and were genuinely both
// present in the same fortnight.
func (s *Store) ForecastDayStatsRaw(ctx context.Context, horizon string, since time.Time) ([]ForecastDayStat, error) {
	// WITHHELD ROWS ARE NOT FORECASTS, AND COUNTING THEM INVERTS THE CHECK.
	// A prediction the ensemble declined to make is still persisted (since
	// 906310c, "Require measured legs, and keep recording inputs on withheld
	// rows") with n_used = 0 and raw_prob = 0.5 exactly. Verified 2026-08-11
	// across the whole table: 4442 rows have n_used = 0 AND raw_prob = 0.5, and
	// ZERO have n_used = 0 with any other raw_prob, so n_used > 0 is an exact
	// filter for "carries a forecast".
	//
	// Those abstentions all share one value, so folding them into
	// COUNT(DISTINCT raw_prob) makes the ratio fall as the ensemble abstains
	// MORE — the statistic moved in the opposite direction to the thing it
	// measures. When leg admission tightened on 2026-08-06, abstentions went
	// 7/day -> 308/day and the measured ratio fell to 0.058, tripping RAW MODEL
	// COLLAPSE every run for four days with the diagnosis "the ensemble itself
	// has stopped discriminating". Among rows that actually carried a forecast
	// the ratio those same days was 0.947-1.000 — the opposite of collapse.
	//
	// So: discrimination is measured over forecasts only, while Symbols keeps
	// counting the whole cross-section and Withheld reports how much of it was
	// declined. That preserves the real signal — coverage — instead of
	// destroying it, and leaves the healthy-day numbers essentially unchanged
	// (2026-08-05 went 0.544 -> 0.551).
	rows, err := s.db.QueryContext(ctx, `
		WITH dedup AS (
		  SELECT symbol_id, date(ts,'unixepoch') AS d, raw_prob, n_used,
		         ROW_NUMBER() OVER (PARTITION BY symbol_id, date(ts,'unixepoch')
		                            ORDER BY ts DESC) rn
		  FROM predictions
		  WHERE horizon = ? AND ts >= ?
		)
		SELECT d,
		       COUNT(*),
		       COUNT(DISTINCT CASE WHEN n_used > 0 THEN ROUND(raw_prob, 3) END),
		       SUM(CASE WHEN n_used = 0 THEN 1 ELSE 0 END)
		FROM dedup WHERE rn = 1 GROUP BY d ORDER BY d`,
		horizon, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ForecastDayStat
	for rows.Next() {
		var d ForecastDayStat
		if err := rows.Scan(&d.Day, &d.Symbols, &d.DistinctProbs, &d.Withheld); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AdmittedLegHistogram reports how many legs the ensemble actually ADMITTED per
// prediction, per day: n_used -> row count.
//
// This is the mechanism behind a raw-side collapse rather than a symptom of it.
// With one admitted leg the blend has nothing to vary across the cross-section,
// so raw_prob necessarily degenerates. Measured 2026-08-06 the fleet ran 2-5
// legs on 13,188 of 14,032 rows; by 2026-08-07 it ran 0-1 on 2,565 of 3,043,
// because every leg had failed its out-of-sample admission bar (expectancy fleet
// AUC 0.4303 - anti-predictive; pressure OOS lift <= 0; gbm 0 legs with edge).
// The gates were right. Publishing a forecast from what survived was not.
func (s *Store) AdmittedLegHistogram(ctx context.Context, horizon string, since time.Time) (map[string]map[int]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT date(ts,'unixepoch') AS d, n_used, COUNT(*)
		FROM predictions WHERE horizon = ? AND ts >= ?
		GROUP BY d, n_used ORDER BY d, n_used`, horizon, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]map[int]int{}
	for rows.Next() {
		var day string
		var nUsed, n int
		if err := rows.Scan(&day, &nUsed, &n); err != nil {
			return nil, err
		}
		if out[day] == nil {
			out[day] = map[int]int{}
		}
		out[day][nUsed] = n
	}
	return out, rows.Err()
}

// PublishedCrossSection returns the calibrated probabilities actually PUBLISHED
// for one horizon on one trading day, deduplicated to one per symbol.
//
// This exists because the publication gate used to judge a day from a single
// pass's in-memory slice, recorded to meta and overwritten every ~10 minutes.
// The stored record was therefore whatever the LAST pass of the day happened to
// emit, and a thin pass silently disarmed the gate: measured 2026-08-08, the
// record read n=12 on a 329-symbol day, where the distinct rule needs only 6 and
// the spread cleared its floor by 0.00002. Both collapsed days that followed
// sailed through a gate that would have caught them on the real cross-section.
//
// Reading the PRIOR day from the table has none of that fragility. It is the
// complete cross-section rather than a sample of it, it is the same unit the
// accuracy registry grades, and because the day is finished there is no risk of
// the gate judging the sweep it is in the middle of producing — which is the
// reason the one-pass record existed in the first place.
//
// n_used > 0 filters the evidence-only rows that a legless blend writes: those
// are deliberately not forecasts and must not count toward the shape of what was
// published.
func (s *Store) PublishedCrossSection(ctx context.Context, horizon, day string) ([]float64, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH dedup AS (
		  SELECT cal_prob,
		         ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts DESC) rn
		  FROM predictions
		  WHERE horizon = ? AND n_used > 0 AND date(ts,'unixepoch') = ?
		)
		SELECT cal_prob FROM dedup WHERE rn = 1`, horizon, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []float64
	for rows.Next() {
		var p float64
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
