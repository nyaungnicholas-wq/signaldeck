package store

import (
	"context"
	"fmt"
)

// PadRun represents a contiguous run of synthetic flat zero-volume bars.
// The predicate is load-bearing in every clause:
//
//	volume = 0           104,319 flat daily bars in this database CARRY volume and are
//	                     real (a stock pinned at one price all session), so flatness
//	                     alone is not diagnostic
//	open=high=low=close  a real zero-volume session still has a range
//	one distinct close   a run at SEVERAL flat prices is illiquidity, not a pad
//	consecutive run      an isolated flat zero-volume day is an ordinary thin session
//	length >= minRun     the threshold separating the two
//
// The predicate deliberately does NOT use symbols.delisted_at, because it fails in
// both directions (SBNY's stamp sits at the END of its pad, so a day <= delisted_at
// filter admits the whole run; NVDQ has no stamp at all), and because the stamp is
// midnight UTC while US daily bars are stamped 04:00/05:00, so a ts > delisted_at
// rule deletes the genuine final trading day. And the pad value is NOT compared to
// the last traded close, because in 40 of 76 measured cases the pad sits at a
// different price entirely.
//
// InUniverse is how many of the run's days are materialized in universe_membership,
// i.e. how much of the run sits inside the cross-sectional denominator right now.
type PadRun struct {
	SymbolID   int64
	Symbol     string
	TF         string
	Bars       int
	Close      float64
	FromTs     int64
	ToTs       int64
	InUniverse int
}

// FlatPadRuns returns every qualifying flat-pad run for the given timeframe.
// minRun must be >= 2; a single flat zero-volume day is an ordinary thin session,
// not a pad.
func (s *Store) FlatPadRuns(ctx context.Context, tf string, minRun int) ([]PadRun, error) {
	if minRun < 2 {
		return nil, fmt.Errorf("minRun must be >= 2, got %d", minRun)
	}

	const sql = `
WITH b AS (
  SELECT symbol_id, ts, close,
         (volume = 0 AND open = high AND high = low AND low = close) AS flat,
         ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts) AS rn
  FROM bars WHERE tf = ?
),
f AS (
  SELECT symbol_id, ts, close,
         rn - ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts) AS grp
  FROM b WHERE flat = 1
),
runs AS (
  SELECT symbol_id, grp,
         COUNT(*) AS n, COUNT(DISTINCT close) AS closes,
         MIN(close) AS px, MIN(ts) AS from_ts, MAX(ts) AS to_ts
  FROM f GROUP BY symbol_id, grp
)
SELECT r.symbol_id, COALESCE(s.symbol,''), r.n, r.px, r.from_ts, r.to_ts,
       (SELECT COUNT(*) FROM universe_membership um
        WHERE um.symbol_id = r.symbol_id
          AND um.day BETWEEN (r.from_ts/86400)*86400 AND (r.to_ts/86400)*86400)
FROM runs r LEFT JOIN symbols s ON s.id = r.symbol_id
WHERE r.n >= ? AND r.closes = 1
ORDER BY r.n DESC
`

	rows, err := s.db.QueryContext(ctx, sql, tf, minRun)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var runs []PadRun
	for rows.Next() {
		var r PadRun
		if err := rows.Scan(&r.SymbolID, &r.Symbol, &r.Bars, &r.Close, &r.FromTs, &r.ToTs, &r.InUniverse); err != nil {
			return nil, err
		}
		r.TF = tf
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// QuarantineFlatPads moves qualifying bars from bars into bars_quarantine in ONE
// transaction, returning how many rows were removed from bars.
// minRun must be >= 2. runID must be non-empty; a quarantine that cannot be
// identified cannot be undone.
//
// THE MEMBER SUBQUERY MUST MATCH flatRunsSQL EXACTLY. The islands technique needs
// rn to be the row number over ALL of a symbol's bars and the second row number
// over the FLAT subset only; the difference is constant just within a consecutive
// run. Filtering the flat condition into the first CTE's WHERE instead makes both
// row numbers range over the same filtered set, so grp is constant for the whole
// symbol and EVERY flat bar it owns collapses into one "run" — which would
// quarantine scattered single thin-trading days. The dry run and the apply also
// have to agree, or the report describes a different set than the one that moves.
//
// The runs are RECOMPUTED inside the transaction rather than taken from a caller's
// earlier read, so a concurrent write cannot make the applied set differ from the
// reported one.
func (s *Store) QuarantineFlatPads(ctx context.Context, tf string, minRun int, runID, reason string, now int64) (int64, error) {
	if minRun < 2 {
		return 0, fmt.Errorf("minRun must be >= 2, got %d", minRun)
	}
	if runID == "" {
		return 0, fmt.Errorf("runID must not be empty")
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Statement one: INSERT OR IGNORE INTO bars_quarantine
	const insertSQL = `
INSERT OR IGNORE INTO bars_quarantine (symbol_id, tf, ts, open, high, low, close, volume, run_id, reason, quarantined_at)
SELECT bb.symbol_id, bb.tf, bb.ts, bb.open, bb.high, bb.low, bb.close, bb.volume, ?, ?, ?
FROM bars bb
JOIN (
  WITH b AS (
    SELECT symbol_id, ts, close,
           (volume = 0 AND open = high AND high = low AND low = close) AS flat,
           ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts) AS rn
    FROM bars WHERE tf = ?
  ),
  f AS (
    SELECT symbol_id, ts, close,
           rn - ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts) AS grp
    FROM b WHERE flat = 1
  ),
  runs AS (
    SELECT symbol_id, grp
    FROM f GROUP BY symbol_id, grp
    HAVING COUNT(*) >= ? AND COUNT(DISTINCT close) = 1
  )
  SELECT f.symbol_id, f.ts FROM f JOIN runs USING (symbol_id, grp)
) m ON m.symbol_id = bb.symbol_id AND m.ts = bb.ts
WHERE bb.tf = ?
`

	_, err = tx.ExecContext(ctx, insertSQL, runID, reason, now, tf, minRun, tf)
	if err != nil {
		return 0, err
	}

	// Statement two: DELETE FROM bars
	const deleteSQL = `
DELETE FROM bars WHERE tf = ? AND EXISTS (
  SELECT 1 FROM bars_quarantine q
  WHERE q.run_id = ? AND q.symbol_id = bars.symbol_id AND q.tf = bars.tf AND q.ts = bars.ts
)
`

	res, err := tx.ExecContext(ctx, deleteSQL, tf, runID)
	if err != nil {
		return 0, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return affected, nil
}

// RestoreQuarantinedBars restores bars from a quarantine run back into bars.
// This existing is the reason quarantine was chosen over deletion.
// runID must be non-empty.
func (s *Store) RestoreQuarantinedBars(ctx context.Context, runID string) (int64, error) {
	if runID == "" {
		return 0, fmt.Errorf("runID must not be empty")
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	const insertSQL = `
INSERT OR IGNORE INTO bars (symbol_id, tf, ts, open, high, low, close, volume)
SELECT symbol_id, tf, ts, open, high, low, close, volume
FROM bars_quarantine WHERE run_id = ?
`

	if _, err := tx.ExecContext(ctx, insertSQL, runID); err != nil {
		return 0, err
	}

	const deleteSQL = `DELETE FROM bars_quarantine WHERE run_id = ?`
	res, err := tx.ExecContext(ctx, deleteSQL, runID)
	if err != nil {
		return 0, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return affected, nil
}
