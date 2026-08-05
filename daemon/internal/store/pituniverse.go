package store

import (
	"context"
	"fmt"
)

// POINT-IN-TIME UNIVERSE MEMBERSHIP (2026-08-04)
//
// universe_membership had a schema and zero rows for its entire existence, so
// every cross-sectional denominator — rank percentile, z-score, "beat the
// same-day universe median" — was computed against whatever membership the
// reading query happened to reconstruct. TradableAt(ts) is the accessor meant
// to reconstruct it, but it answers from symbols.added_at and delisted_at,
// which are STAMPS: they record when we first saw a symbol and when we noticed
// its death, not which names actually traded on a given day. Both stamps have
// been wrong here before — added_at was repaired twice, and 735 inactive
// symbols still carry no delisted_at at all — and nothing downstream could
// tell, because there was no recorded membership to disagree with.
//
// This materialises membership from the only per-day EVIDENCE the database
// holds: a daily bar. A symbol is a member of day D's universe exactly when it
// printed a tf='1d' bar on D. That definition cannot admit look-ahead by
// construction — a bar is a fact stamped on the day it happened, so a name
// cannot enter before its first print or survive past its last one, whatever
// its symbols-row says. It also makes the denominator auditable: a reviewer
// COUNTs a table per day instead of re-deriving a join and trusting it.
//
// The cost of the definition, stated rather than hidden: a symbol halted for a
// session is absent from that day's universe. For a cross-sectional rank that
// is the right answer — a name that did not trade cannot be ranked against
// names that did — but it is not the same set as "listed that day".
//
// Days are floored to the UTC day. Daily bars in this database carry three
// intraday alignments (00:00, 04:00 and 05:00 UTC, the ET-midnight offsets
// across DST), so a raw ts is not a day key and one symbol can hold two bars
// for one calendar day. Flooring then DISTINCTing collapses both.

// UniverseSource is the provenance written by RebuildUniverseMembership. It
// names the EVIDENCE rather than the worker, because the evidence is what a
// reviewer has to be able to re-derive.
const UniverseSource = "bars-1d"

// RebuildResult is what one rebuild did. ReusedTickerDays is the count that
// matters to a reviewer: days where a bar EXISTS after the symbol's recorded
// delisting, which is the fingerprint of an exchange recycling a ticker onto a
// row that already holds a dead company's history.
type RebuildResult struct {
	Rows             int64
	ReusedTickerDays int64
}

// RebuildUniverseMembership rewrites universe_membership from daily-bar
// evidence and reports what it wrote.
//
// A full rebuild rather than an append: bars are corrected in place by the
// split-repair path, and an append-only membership would keep serving a day a
// later correction deleted. Only rows this function owns (source =
// UniverseSource) are cleared, so any other provenance — a vendor membership
// file, were one ever loaded — survives untouched.
//
// THE ONE PLACE A STAMP OVERRIDES THE EVIDENCE. Bars after a recorded
// delisting are excluded. That looks like a contradiction of this file's whole
// argument, and it is the opposite: when the bar record and the delisting
// record disagree, the disagreement itself is the finding. It means one symbol
// row now holds two different securities — measured here on 2026-08-04, row
// 1122 carries Atotech's 2021-02→2022-08 history AND a GraniteShares ETF that
// began printing under the recycled ticker ATC in 2026-05. Admitting the second
// segment would put a spliced price series into the cross-sectional denominator
// as one continuous name, which is a worse error than dropping 58 days. The
// count is returned rather than swallowed so the real repair — splitting the
// row in two — has a number attached to it.
//
// Rides the single write connection, like every other mutation in this package.
func (s *Store) RebuildUniverseMembership(ctx context.Context) (RebuildResult, error) {
	var res RebuildResult
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM universe_membership WHERE source = ?`, UniverseSource); err != nil {
		return res, fmt.Errorf("clear %s rows: %w", UniverseSource, err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
		  SELECT DISTINCT (b.ts / 86400) * 86400 AS day, b.symbol_id
		  FROM bars b JOIN symbols s ON s.id = b.symbol_id
		  WHERE b.tf = '1d' AND s.delisted_at > 0
		    AND (b.ts / 86400) * 86400 > s.delisted_at)`).Scan(&res.ReusedTickerDays); err != nil {
		return res, fmt.Errorf("count reused-ticker days: %w", err)
	}
	// OR IGNORE guards the case where a row for this (day, symbol) already
	// exists under a DIFFERENT source string: the DELETE above cannot have
	// removed it, and the primary key would otherwise abort the whole rebuild.
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO universe_membership (day, symbol_id, source)
		SELECT DISTINCT (b.ts / 86400) * 86400, b.symbol_id, ?
		FROM bars b JOIN symbols s ON s.id = b.symbol_id
		WHERE b.tf = '1d'
		  AND (s.delisted_at IS NULL OR s.delisted_at = 0
		       OR (b.ts / 86400) * 86400 <= s.delisted_at)`, UniverseSource); err != nil {
		return res, fmt.Errorf("insert membership: %w", err)
	}

	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM universe_membership`).Scan(&res.Rows); err != nil {
		return res, fmt.Errorf("count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// UniverseAt returns the symbol ids recorded in the universe on the UTC day
// containing ts. Unlike TradableAt this reads a recorded fact instead of
// re-deriving one from stamps: a caller that needs a cross-sectional
// DENOMINATOR should use this, and a caller asking "could I have bought it"
// should use TradableAt.
func (s *Store) UniverseAt(ctx context.Context, ts int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT symbol_id FROM universe_membership WHERE day = ? ORDER BY symbol_id`,
		(ts/86400)*86400)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UniverseSpanStats is the extent of the materialised membership. Days == 0 is
// the state this file exists to make impossible to ship silently.
type UniverseSpanStats struct {
	Days     int64
	FirstDay int64
	LastDay  int64
	Rows     int64
	Symbols  int64
}

// UniverseSpan reports that extent.
func (s *Store) UniverseSpan(ctx context.Context) (UniverseSpanStats, error) {
	var v UniverseSpanStats
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT day), COALESCE(MIN(day),0), COALESCE(MAX(day),0),
		       COUNT(*), COUNT(DISTINCT symbol_id)
		FROM universe_membership`).
		Scan(&v.Days, &v.FirstDay, &v.LastDay, &v.Rows, &v.Symbols)
	return v, err
}
