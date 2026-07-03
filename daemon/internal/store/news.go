package store

import (
	"context"
	"database/sql"
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
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO news (id, symbol_id, ts, headline, url, source)
		VALUES (?,?,?,?,?,?)`,
		n.ID, n.SymbolID, n.Ts, n.Headline, n.URL, n.Source)
	return err
}

// UnratedNews returns headlines awaiting sentiment tagging.
func (s *Store) UnratedNews(ctx context.Context, limit int) ([]NewsItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.symbol_id, sym.symbol, n.ts, n.headline
		FROM news n JOIN symbols sym ON sym.id=n.symbol_id
		WHERE n.sentiment='unrated' ORDER BY n.ts DESC LIMIT ?`, limit)
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
	_, err := s.db.ExecContext(ctx,
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
// the last `sinceTs` window (for a symbol-level sentiment gauge).
func (s *Store) NewsSentimentAgg(ctx context.Context, symbolID, sinceTs int64) (mean float64, n int, err error) {
	var m sql.NullFloat64
	err = s.db.QueryRowContext(ctx, `
		SELECT AVG(score), COUNT(*) FROM news
		WHERE symbol_id=? AND ts>=? AND sentiment!='unrated'`, symbolID, sinceTs).Scan(&m, &n)
	return m.Float64, n, err
}
