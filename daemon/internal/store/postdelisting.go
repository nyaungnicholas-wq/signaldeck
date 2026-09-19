package store

// POST-DELISTING ADJUDICATION.
//
// A bar dated after its symbol's delisted_at is one of two very different
// things, and the whole point of this file is that they get different treatment:
//
//	a GENUINE print (real volume, continuous price) means the STAMP is wrong —
//	the vendor's coverage had a hole and delisted_at was derived from where the
//	hole started. The bars are market history and stay.
//
//	a PADDED print (volume 0, open=high=low=close) means nothing traded. It is
//	quarantined, reversibly.
//
// The day-strict comparison matters. delisted_at is stored at UTC midnight while
// US daily bars carry 04:00/05:00 stamps, so `ts > delisted_at` catches the
// symbol's genuine FINAL session — 2,200 bars across 1,838 symbols by that test,
// against 362 across 46 by the day-strict one. Off-by-one on a date is how a
// real last trading day gets deleted.

import (
	"context"
	"fmt"
)

// PostDelistingCase summarises one symbol's bars after its stamped delisting.
type PostDelistingCase struct {
	SymbolID int64
	Symbol   string
	Name     string
	// DelistedAt is the stamp as stored (UTC midnight).
	DelistedAt int64
	// Bars is how many daily bars fall strictly after the stamped DAY.
	Bars int
	// GenuineBars carry non-zero volume; PaddedBars are zero-volume and flat.
	GenuineBars int
	PaddedBars  int
	VolumeAfter float64
	// FirstAfterTs / LastGenuineTs bound the run. LastGenuineTs is 0 when no bar
	// after the stamp carried volume.
	FirstAfterTs  int64
	LastGenuineTs int64
}

// postDelistingWhere is the day-strict predicate, written once so the report,
// the quarantine and the restamp cannot drift apart.
const postDelistingWhere = `
  s.delisted_at IS NOT NULL
  AND b.tf = ?
  AND date(b.ts,'unixepoch') > date(s.delisted_at,'unixepoch')`

// PostDelistingCases returns one row per symbol holding bars after its stamp.
func (s *Store) PostDelistingCases(ctx context.Context, tf string) ([]PostDelistingCase, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT s.id, s.symbol, COALESCE(s.name,''), s.delisted_at,
       COUNT(*),
       SUM(CASE WHEN b.volume > 0 THEN 1 ELSE 0 END),
       SUM(CASE WHEN b.volume = 0 AND b.open = b.high AND b.high = b.low AND b.low = b.close THEN 1 ELSE 0 END),
       COALESCE(SUM(b.volume),0),
       MIN(b.ts),
       COALESCE(MAX(CASE WHEN b.volume > 0 THEN b.ts END), 0)
FROM bars b JOIN symbols s ON s.id = b.symbol_id
WHERE `+postDelistingWhere+`
GROUP BY s.id
ORDER BY COUNT(*) DESC`, tf)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PostDelistingCase
	for rows.Next() {
		var c PostDelistingCase
		if err := rows.Scan(&c.SymbolID, &c.Symbol, &c.Name, &c.DelistedAt, &c.Bars,
			&c.GenuineBars, &c.PaddedBars, &c.VolumeAfter, &c.FirstAfterTs, &c.LastGenuineTs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// QuarantinePostDelistingPads moves ZERO-VOLUME flat bars dated after the last
// genuine print into bars_quarantine, in one transaction.
//
// The cutoff is the last GENUINE print, not the stamp: for a symbol whose stamp
// is early, the padded bars interleaved with real trading are ordinary no-trade
// sessions of a live security and must not be touched. Only the padding that
// trails the end of real trading qualifies.
func (s *Store) QuarantinePostDelistingPads(ctx context.Context, tf, runID, reason string, now int64) (int64, error) {
	if runID == "" {
		return 0, fmt.Errorf("runID must not be empty")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	// The member set: zero-volume flat bars that are BOTH after the stamped day
	// and after the symbol's last genuine print anywhere in its history.
	const member = `
SELECT b.symbol_id, b.ts
FROM bars b JOIN symbols s ON s.id = b.symbol_id
WHERE ` + postDelistingWhere + `
  AND b.volume = 0 AND b.open = b.high AND b.high = b.low AND b.low = b.close
  AND b.ts > COALESCE((SELECT MAX(g.ts) FROM bars g
                        WHERE g.symbol_id = b.symbol_id AND g.tf = b.tf AND g.volume > 0), 0)`

	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO bars_quarantine (symbol_id, tf, ts, open, high, low, close, volume, run_id, reason, quarantined_at)
SELECT bb.symbol_id, bb.tf, bb.ts, bb.open, bb.high, bb.low, bb.close, bb.volume, ?, ?, ?
FROM bars bb JOIN (`+member+`) m ON m.symbol_id = bb.symbol_id AND m.ts = bb.ts
WHERE bb.tf = ?`, runID, reason, now, tf, tf); err != nil {
		return 0, err
	}

	res, err := tx.ExecContext(ctx, `
DELETE FROM bars WHERE tf = ? AND EXISTS (
  SELECT 1 FROM bars_quarantine q
  WHERE q.run_id = ? AND q.symbol_id = bars.symbol_id AND q.tf = bars.tf AND q.ts = bars.ts)`, tf, runID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// RestampDelistedAtFromGenuineBars moves delisted_at FORWARD to the UTC midnight
// of a symbol's last genuine (non-zero-volume) print, for every symbol whose
// stamp currently precedes it.
//
// FORWARD ONLY. The stamp is an assertion that the security stopped trading; a
// genuine print after it disproves that date and nothing else. Moving a stamp
// BACKWARD would be inferring a delisting from an absence of data, which is the
// error that produced these rows in the first place.
//
// The resulting value is an observational lower bound. No corporate-actions feed
// is consulted here and none is claimed.
// maxGapDays bounds what counts as a CONTINUATION. A genuine print that resumes
// within the gap says the stamp was early; one that resumes long afterwards may
// be a different security holding a recycled ticker, and moving the stamp would
// merge two companies into one row.
//
// SIC is the case that forced this parameter. It traded at ~$14.50 through
// 2021-10-19 — a SPAC at trust value — went silent for 1,762 days, and resumed
// on 2026-08-17 at ~$49.90. Same ticker, plainly not the same security. Without
// a bound it would have had its delisted_at dragged 4.8 years forward, splicing
// the two together, which is precisely the ATC-splice failure the import guard
// exists to prevent.
//
// A symbol beyond the bound is left ALONE and reported as ambiguous. That is the
// honest outcome without a corporate-actions feed: build-universe already
// excludes post-delisting prints from the point-in-time universe, so the
// research path is protected either way; what is refused here is the CLAIM that
// the two runs are one security.
func (s *Store) RestampDelistedAtFromGenuineBars(ctx context.Context, tf string, maxGapDays int) (int64, error) {
	if maxGapDays <= 0 {
		return 0, fmt.Errorf("maxGapDays must be positive: without a bound a recycled ticker is " +
			"indistinguishable from a stamp that was merely early")
	}
	res, err := s.w.ExecContext(ctx, `
UPDATE symbols SET delisted_at = (
  SELECT (MAX(b.ts)/86400)*86400 FROM bars b
  WHERE b.symbol_id = symbols.id AND b.tf = ? AND b.volume > 0)
WHERE delisted_at IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM bars b
    WHERE b.symbol_id = symbols.id AND b.tf = ? AND b.volume > 0
      AND date(b.ts,'unixepoch') > date(symbols.delisted_at,'unixepoch'))
  AND (SELECT MIN(b.ts) FROM bars b
        WHERE b.symbol_id = symbols.id AND b.tf = ? AND b.volume > 0
          AND date(b.ts,'unixepoch') > date(symbols.delisted_at,'unixepoch'))
      <= symbols.delisted_at + ? * 86400`, tf, tf, tf, maxGapDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
