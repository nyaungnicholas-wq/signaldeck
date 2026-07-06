// Daily sentiment aggregates (learning-flywheel wave).
//
// Rated news rows are rolled up per (symbol, UTC day) into sentiment_daily —
// a PERMANENT time series (never pruned) that turns the news feed into a
// feature the ensemble can consume. Aggregation is a full idempotent
// recompute per day, so re-rating headlines or late-arriving articles simply
// correct the row on the next pass.
package store

import (
	"context"
	"time"
)

// SentimentDay is one symbol's aggregated sentiment for one UTC day.
type SentimentDay struct {
	SymbolID  int64   `json:"-"`
	Day       string  `json:"day"` // YYYY-MM-DD (UTC)
	N         int     `json:"n"`
	MeanScore float64 `json:"meanScore"`
	Pos       int     `json:"pos"`
	Neg       int     `json:"neg"`
	Neu       int     `json:"neu"`
}

// UpsertSentimentDaily recomputes the sentiment_daily rows for one UTC day
// (YYYY-MM-DD) across ALL symbols in a single INSERT..SELECT..ON CONFLICT
// statement: rated headlines whose article timestamp falls on that day are
// aggregated per symbol; existing rows for the day are replaced with the
// fresh aggregate (idempotent recompute). Unrated headlines never count, and
// neither do 'skipped' ones (deliberately not rated — out-of-scope symbols):
// both carry a default 0 score that would silently dilute the mean.
// Returns the number of symbol-day rows written.
func (s *Store) UpsertSentimentDaily(ctx context.Context, day string) (int, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO sentiment_daily (symbol_id, day, n, mean_score, pos, neg, neu)
		SELECT symbol_id, ?, COUNT(*), AVG(score),
		       SUM(sentiment='bullish'), SUM(sentiment='bearish'), SUM(sentiment='neutral')
		FROM news
		WHERE sentiment NOT IN ('unrated','skipped') AND date(ts, 'unixepoch') = ?
		GROUP BY symbol_id
		ON CONFLICT(symbol_id, day) DO UPDATE SET
		  n=excluded.n, mean_score=excluded.mean_score,
		  pos=excluded.pos, neg=excluded.neg, neu=excluded.neu`,
		day, day)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// LatestSentiment returns the most recent daily sentiment aggregate for a
// symbol that is at most maxAgeDays old (UTC days). ok=false when there is no
// fresh-enough aggregate — stale sentiment is treated as ABSENT, never as a
// zero signal, so the ensemble cannot lean on old headlines.
func (s *Store) LatestSentiment(ctx context.Context, symbolID int64, maxAgeDays int) (meanScore float64, n int, ok bool, err error) {
	if maxAgeDays < 0 {
		maxAgeDays = 0
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -maxAgeDays).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `
		SELECT mean_score, n FROM sentiment_daily
		WHERE symbol_id=? AND day>=? ORDER BY day DESC LIMIT 1`,
		symbolID, cutoff)
	if err != nil {
		return 0, 0, false, err
	}
	defer rows.Close() //nolint:errcheck
	if !rows.Next() {
		return 0, 0, false, rows.Err()
	}
	if err := rows.Scan(&meanScore, &n); err != nil {
		return 0, 0, false, err
	}
	return meanScore, n, true, rows.Err()
}

// SentimentSeries returns a symbol's daily sentiment aggregates, newest
// first, capped at limit (for the symbol page's sentiment time series).
func (s *Store) SentimentSeries(ctx context.Context, symbolID int64, limit int) ([]SentimentDay, error) {
	if limit <= 0 {
		limit = 90
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT day, n, mean_score, pos, neg, neu FROM sentiment_daily
		WHERE symbol_id=? ORDER BY day DESC LIMIT ?`, symbolID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []SentimentDay
	for rows.Next() {
		d := SentimentDay{SymbolID: symbolID}
		if err := rows.Scan(&d.Day, &d.N, &d.MeanScore, &d.Pos, &d.Neg, &d.Neu); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
