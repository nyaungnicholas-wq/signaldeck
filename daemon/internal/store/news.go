package store

import (
	"context"
	"database/sql"
	"strings"
)

// NewsItem is one headline + its sentiment tag.
type NewsItem struct {
	ID        string  `json:"id"`
	SymbolID  int64   `json:"-"`
	Symbol    string  `json:"symbol,omitempty"`
	Ts        int64   `json:"ts"`
	Headline  string  `json:"headline"`
	URL       string  `json:"url"`
	Source    string  `json:"source"`
	Sentiment string  `json:"sentiment"`
	Score     float64 `json:"score"`
	Rationale string  `json:"rationale"`
}

// InsertNews stores a headline (ignored if the provider id already exists).
func (s *Store) InsertNews(ctx context.Context, n NewsItem) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO news (id, symbol_id, ts, headline, url, source)
		VALUES (?,?,?,?,?,?)`,
		n.ID, n.SymbolID, n.Ts, n.Headline, n.URL, n.Source)
	return err
}

// UnratedNews returns headlines awaiting sentiment tagging. Only
// sentiment='unrated' rows qualify — 'skipped' rows (deliberately not rated;
// symbol outside the news scope) are terminal and never re-enter the queue.
//
// minTs bounds the queue by headline age (unix seconds). 0 means unbounded,
// which needs no conditional because every real ts is positive. The bound
// exists because an unbounded queue is DEAD WORK: sentiment-aggregator only
// ever recomputes today and yesterday, nothing backfills older days, and both
// readers of news.sentiment take the newest N rows. On 2026-09-04 that left
// 353,671 unrated rows reaching back to 2012 (81% of the table) consuming the
// entire 2,000-call daily LLM budget to write tags nothing could ever read,
// while fresh news was already fully tagged: 1 unrated headline in the
// trailing 7 days out of 822 that arrived.
func (s *Store) UnratedNews(ctx context.Context, limit int, minTs int64) ([]NewsItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.symbol_id, sym.symbol, n.ts, n.headline
		FROM news n JOIN symbols sym ON sym.id=n.symbol_id
		WHERE n.sentiment='unrated' AND n.ts >= ? ORDER BY n.ts DESC LIMIT ?`, minTs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []NewsItem
	for rows.Next() {
		var n NewsItem
		if err := rows.Scan(&n.ID, &n.SymbolID, &n.Symbol, &n.Ts, &n.Headline); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RateNews records a sentiment tag for one article.
func (s *Store) RateNews(ctx context.Context, id, sentiment string, score float64, rationale string) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE news SET sentiment=?, score=?, rationale=? WHERE id=?`,
		sentiment, score, rationale, id)
	return err
}

// SymbolNews returns recent headlines for a symbol.
func (s *Store) SymbolNews(ctx context.Context, symbolID int64, limit int) ([]NewsItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ts, headline, url, source, sentiment, score, rationale
		FROM news WHERE symbol_id=? ORDER BY ts DESC LIMIT ?`, symbolID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []NewsItem
	for rows.Next() {
		n := NewsItem{SymbolID: symbolID}
		if err := rows.Scan(&n.ID, &n.Ts, &n.Headline, &n.URL, &n.Source, &n.Sentiment, &n.Score, &n.Rationale); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RecentNews returns the latest rated headlines across all symbols.
func (s *Store) RecentNews(ctx context.Context, limit int) ([]NewsItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, sym.symbol, n.ts, n.headline, n.url, n.source, n.sentiment, n.score, n.rationale
		FROM news n JOIN symbols sym ON sym.id=n.symbol_id
		ORDER BY n.ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []NewsItem
	for rows.Next() {
		var n NewsItem
		if err := rows.Scan(&n.ID, &n.Symbol, &n.Ts, &n.Headline, &n.URL, &n.Source, &n.Sentiment, &n.Score, &n.Rationale); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NewsSentimentAgg returns the mean sentiment score + counts for a symbol over
// the last `sinceTs` window (for a symbol-level sentiment gauge). Only rated
// headlines count: 'unrated' (pending) and 'skipped' (deliberately not rated)
// are both excluded so their default 0 scores can't dilute the mean.
func (s *Store) NewsSentimentAgg(ctx context.Context, symbolID, sinceTs int64) (mean float64, n int, err error) {
	var m sql.NullFloat64
	err = s.db.QueryRowContext(ctx, `
		SELECT AVG(score), COUNT(*) FROM news
		WHERE symbol_id=? AND ts>=? AND sentiment NOT IN ('unrated','skipped')`, symbolID, sinceTs).Scan(&m, &n)
	return m.Float64, n, err
}

// SkipUnratedOutside marks every still-unrated headline whose symbol is NOT in
// keepIDs as sentiment='skipped' — an honest terminal label meaning "we chose
// not to spend LLM budget rating this" (symbol outside the news scope), as
// opposed to 'unrated' which means "still pending". Skipped rows are excluded
// from UnratedNews and from all sentiment aggregates. Rated rows are never
// touched. Returns the number of rows relabeled.
//
// keepIDs is bound as one placeholder per id; the news scope is at most a few
// hundred symbols, far below SQLite's 32766-variable limit.
func (s *Store) SkipUnratedOutside(ctx context.Context, keepIDs []int64) (int64, error) {
	q := `UPDATE news SET sentiment='skipped' WHERE sentiment='unrated'`
	args := make([]any, 0, len(keepIDs))
	if len(keepIDs) > 0 {
		q += ` AND symbol_id NOT IN (?` + strings.Repeat(",?", len(keepIDs)-1) + `)`
		for _, id := range keepIDs {
			args = append(args, id)
		}
	}
	res, err := s.w.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
