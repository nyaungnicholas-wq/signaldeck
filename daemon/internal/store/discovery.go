// Universe-discovery store methods (discovery wave): candidate symbols found
// by the universe-discovery worker, plus the active-symbol count that drives
// the symbol budget.
package store

import (
	"context"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Candidate is one discovered symbol awaiting review or auto-add.
type Candidate struct {
	Symbol      string    `json:"symbol"`
	Market      md.Market `json:"market"`
	FirstSeenTs int64     `json:"firstSeenTs"`
	LastSeenTs  int64     `json:"lastSeenTs"`
	SeenCount   int       `json:"seenCount"`
	DollarVol   float64   `json:"dollarVol"`
	PctChange   float64   `json:"pctChange"`
	Status      string    `json:"status"` // new | added | dismissed
}

// UpsertCandidate inserts a candidate or, when it already exists, bumps
// last_seen_ts/seen_count and refreshes dollar_vol/pct_change (a zero metric
// never overwrites a previously known non-zero one — screener fallbacks may
// only deliver partial data). Status is preserved on conflict, so a dismissed
// candidate stays dismissed across sweeps. Call at most once per sweep per
// symbol: every call counts as one sighting.
func (s *Store) UpsertCandidate(ctx context.Context, c Candidate) error {
	ts := c.LastSeenTs
	if ts == 0 {
		ts = time.Now().Unix()
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO candidates (symbol, market, first_seen_ts, last_seen_ts, seen_count, dollar_vol, pct_change, status)
		VALUES (?,?,?,?,1,?,?,'new')
		ON CONFLICT(symbol, market) DO UPDATE SET
		  last_seen_ts = excluded.last_seen_ts,
		  seen_count   = candidates.seen_count + 1,
		  dollar_vol   = CASE WHEN excluded.dollar_vol != 0 THEN excluded.dollar_vol ELSE candidates.dollar_vol END,
		  pct_change   = CASE WHEN excluded.pct_change != 0 THEN excluded.pct_change ELSE candidates.pct_change END`,
		c.Symbol, string(c.Market), ts, ts, c.DollarVol, c.PctChange)
	return err
}

// Candidates returns candidate rows filtered by status ("" = all), best-first
// (highest dollar volume, then most recently seen).
func (s *Store) Candidates(ctx context.Context, status string) ([]Candidate, error) {
	q := `SELECT symbol, market, first_seen_ts, last_seen_ts, seen_count, dollar_vol, pct_change, status
	      FROM candidates`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY dollar_vol DESC, last_seen_ts DESC, symbol`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var mkt string
		if err := rows.Scan(&c.Symbol, &mkt, &c.FirstSeenTs, &c.LastSeenTs, &c.SeenCount, &c.DollarVol, &c.PctChange, &c.Status); err != nil {
			return nil, err
		}
		c.Market = md.Market(mkt)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCandidateStatus moves a candidate to new|added|dismissed. Missing rows
// are a no-op (the schema CHECK rejects invalid statuses).
func (s *Store) SetCandidateStatus(ctx context.Context, symbol string, market md.Market, status string) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE candidates SET status=? WHERE symbol=? AND market=?`,
		status, symbol, string(market))
	return err
}

// ActiveSymbolCount returns how many symbols are currently active (live
// ingestion) — the number the symbol budget caps.
func (s *Store) ActiveSymbolCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM symbols WHERE active=1`).Scan(&n)
	return n, err
}
