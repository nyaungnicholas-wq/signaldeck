package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// JournalSymbol is one entry the member journal's picker offers.
type JournalSymbol struct {
	ID     int64
	Symbol string
	Name   string // the SEC directory name when the ticker has one, else symbols.name
}

// journalPickable is the shared predicate for pickable symbols:
// market = 'stocks' AND active = 1 AND at least one daily bar exists.
// The picker and the server-side validation use this exact condition
// so the server accepts exactly what the picker offers.
const journalPickable = "s.market = 'stocks' AND s.active = 1 AND EXISTS (SELECT 1 FROM bars b WHERE b.symbol_id = s.id AND b.tf = '1d')"

// JournalSymbols returns up to limit pickable symbols matching q.
func (s *Store) JournalSymbols(ctx context.Context, q string, limit int) ([]JournalSymbol, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []JournalSymbol{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 8
	}
	upperQ := strings.ToUpper(q)
	query := `
		SELECT s.id, s.symbol, COALESCE(NULLIF(c.name, ''), s.name)
		FROM symbols s
		LEFT JOIN companies c ON c.ticker = s.symbol
		WHERE ` + journalPickable + `
		  AND (s.symbol LIKE ? || '%' OR s.name LIKE '%' || ? || '%' OR c.name LIKE '%' || ? || '%')
		ORDER BY
		  (s.symbol = ?) DESC,
		  (s.symbol LIKE ? || '%') DESC,
		  s.symbol
		LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, upperQ, q, q, upperQ, upperQ, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []JournalSymbol
	for rows.Next() {
		var js JournalSymbol
		if err := rows.Scan(&js.ID, &js.Symbol, &js.Name); err != nil {
			return nil, err
		}
		out = append(out, js)
	}
	return out, rows.Err()
}

// JournalSymbolByTicker returns the pickable symbol with exactly this ticker
// (case-insensitive input, compared upper-cased). ok is false, with a nil
// error, when no pickable symbol has that ticker.
func (s *Store) JournalSymbolByTicker(ctx context.Context, ticker string) (JournalSymbol, bool, error) {
	t := strings.ToUpper(strings.TrimSpace(ticker))
	if t == "" {
		return JournalSymbol{}, false, nil
	}
	var js JournalSymbol
	err := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.symbol, COALESCE(NULLIF(c.name, ''), s.name)
		 FROM symbols s
		 LEFT JOIN companies c ON c.ticker = s.symbol
		 WHERE s.symbol = ? AND `+journalPickable+` LIMIT 1`,
		t).Scan(&js.ID, &js.Symbol, &js.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JournalSymbol{}, false, nil
		}
		return JournalSymbol{}, false, err
	}
	return js, true, nil
}
