package store

// PRICE-BASIS PROVENANCE.
//
// Every ratio in this system that divides a STORED price by a FRESHLY READ one
// has the same latent defect: `bars` is written INSERT OR REPLACE, and a
// detected split triggers a full re-backfill (pipeline/splitrepair.go) that
// rescales a symbol's whole series. A price captured before that rescale and a
// price read after it are on DIFFERENT bases, and dividing them does not cancel
// — the rescale factor lands whole in the result.
//
// That is not hypothetical. confluence_outcomes.entry_px was frozen at flag time
// and divided into a live exit close; DFNS was flagged at 0.0493, graded against
// a rescaled exit and published +8541% on a price no bar of that symbol has ever
// carried. That path is repaired (pipeline/confluence.go re-reads the entry bar
// so both legs are live).
//
// paper_positions.avg_px has the IDENTICAL shape: it is stored at entry and then
// compared against live bars to place stop and target barriers. Measured
// 2026-08-21, zero open positions were affected — 24 successful rescales exist,
// all between 2026-07-25 and 2026-08-11, and none landed on a symbol held at the
// time. The path is clean by luck, not by construction, which is exactly the
// condition a guard is for.
//
// split_repairs.repaired_at with ok=1 is the provenance token: it is the instant
// a symbol's series was rewritten. Anything captured before it is on a dead
// basis.

import "context"

// SeriesRescaledSince reports whether a symbol's bar series was successfully
// re-backfilled (and therefore rescaled) at or after `since`, and when.
//
// ok=0 rows are deliberately ignored: those are discontinuities the repairer
// INSPECTED and declined to treat as splits ("provider returned the same move on
// a clean refetch — treating as a REAL price move"). No rewrite happened, so no
// basis changed, and counting them would fail-close on 1,181 rows that are fine.
func (s *Store) SeriesRescaledSince(ctx context.Context, symbolID, since int64) (bool, int64, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(repaired_at), 0) FROM split_repairs
		WHERE symbol_id = ? AND ok = 1 AND repaired_at >= ?`, symbolID, since).Scan(&at)
	if err != nil {
		return false, 0, err
	}
	return at > 0, at, nil
}

// StaleBasisPosition names one open paper position whose stored entry price was
// captured before its symbol's series was rescaled.
type StaleBasisPosition struct {
	Strategy   string `json:"strategy"`
	SymbolID   int64  `json:"symbolId"`
	Symbol     string `json:"symbol"`
	OpenedTs   int64  `json:"openedTs"`
	RepairedAt int64  `json:"repairedAt"`
}

// StaleBasisPositions returns every open paper position whose avg_px predates a
// successful rescale of its own symbol.
//
// This is the AUDIT, published rather than assumed: a report that says "0" is
// evidence, whereas silence is not.
func (s *Store) StaleBasisPositions(ctx context.Context) ([]StaleBasisPosition, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.strategy, p.symbol_id, COALESCE(sy.symbol,''), p.opened_ts,
		       (SELECT MAX(sr.repaired_at) FROM split_repairs sr
		         WHERE sr.symbol_id = p.symbol_id AND sr.ok = 1 AND sr.repaired_at >= p.opened_ts)
		FROM paper_positions p
		LEFT JOIN symbols sy ON sy.id = p.symbol_id
		WHERE EXISTS (SELECT 1 FROM split_repairs sr
		               WHERE sr.symbol_id = p.symbol_id AND sr.ok = 1 AND sr.repaired_at >= p.opened_ts)
		ORDER BY p.strategy, sy.symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []StaleBasisPosition{}
	for rows.Next() {
		var p StaleBasisPosition
		if err := rows.Scan(&p.Strategy, &p.SymbolID, &p.Symbol, &p.OpenedTs, &p.RepairedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
