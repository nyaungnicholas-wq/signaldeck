package store

import "context"

// UniverseMembersAt returns the symbol ids observed in the universe on the UTC
// day containing `ts`, plus whether the day has any record at all.
//
// A REPLAY MUST NOT USE symbols.active. That column is a single mutable flag
// holding today's answer: a name delisted since a past bar reads inactive for
// that bar too, and a name added since reads active. Ranking or trading a past
// day against today's membership is survivorship bias in the denominator of
// every cross-sectional decision — the exact defect universe_membership was
// populated to close (see the P3A data-integrity amendment).
//
// ok=false means the day has NO membership record. Callers must treat that as
// "the universe at this instant is unknown" and refuse, not fall back to the
// current flag: a silent fallback would reintroduce precisely the bias this
// exists to prevent. Coverage is finite and ends where the poller last ran.
func (s *Store) UniverseMembersAt(ctx context.Context, ts int64) (map[int64]bool, bool, error) {
	day := (ts / 86400) * 86400
	rows, err := s.db.QueryContext(ctx,
		`SELECT symbol_id FROM universe_membership WHERE day=?`, day)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, len(out) > 0, nil
}

// UniverseMembershipCoverage reports the first and last day universe_membership
// records, so a replay can refuse a window it cannot reconstruct BEFORE it writes
// rows rather than discovering the gap partway through.
func (s *Store) UniverseMembershipCoverage(ctx context.Context) (first, last int64, days int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MIN(day),0), COALESCE(MAX(day),0), COUNT(DISTINCT day) FROM universe_membership`).
		Scan(&first, &last, &days)
	return first, last, days, err
}

// DailyBarTimesBetween returns the DISTINCT daily-bar timestamps in [from, to],
// ascending — the sessions a replay must step through, one pass per bar.
//
// Distinct across the whole universe rather than per symbol, because the paper
// book's as-of clock is global: one step covers every symbol that has a bar at
// that instant, exactly as the live worker does when a new daily bar lands.
func (s *Store) DailyBarTimesBetween(ctx context.Context, tf string, from, to int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ts FROM bars
		WHERE tf=? AND ts>=? AND ts<=?
		ORDER BY ts`, tf, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []int64
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// CountPaperRows reports how many rows a strategy already has in the book
// tables, so a replay can refuse to append to a previous run's output rather
// than silently doubling it.
func (s *Store) CountPaperRows(ctx context.Context, strategy string) (trades, equity, positions int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM paper_trades    WHERE strategy=?),
		       (SELECT COUNT(*) FROM paper_equity    WHERE strategy=?),
		       (SELECT COUNT(*) FROM paper_positions WHERE strategy=?)`,
		strategy, strategy, strategy).Scan(&trades, &equity, &positions)
	return trades, equity, positions, err
}
