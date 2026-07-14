// Read-only store methods backing GET /api/tv-status (see
// internal/api/tvstatus.go). All reads go through the pooled read handle
// (s.db); nothing here touches the write path or the schema.
package store

import (
	"context"
	"database/sql"
)

// StreamedSymbol is one entry of the STREAMED hot set — the grid the tv-status
// page renders. That set is the streamed stocks (stream=1) PLUS active crypto:
// crypto is always live-streamed by the crypto-live worker but its stream flag
// was never set by the stock-only migration, so a stream=1-only filter would
// hide BTC/USD even while its BTCUSD alerts are firing — dishonest for a page
// whose whole job is "is it live?".
type StreamedSymbol struct {
	ID     int64
	Symbol string
	Market string
}

// StreamedSymbols returns the active hot-set symbols ordered by symbol so the
// tv-status grid is deterministic.
func (s *Store) StreamedSymbols(ctx context.Context) ([]StreamedSymbol, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, symbol, market FROM symbols
		 WHERE active=1 AND (stream=1 OR market='crypto') ORDER BY symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]StreamedSymbol, 0, 8)
	for rows.Next() {
		var ss StreamedSymbol
		if err := rows.Scan(&ss.ID, &ss.Symbol, &ss.Market); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// TVSignalTotals returns aggregate counts over every received TradingView
// signal: the grand total, how many landed since `since` (unix sec), and the
// ts + raw ticker of the newest row. lastTs is nil when no signals exist.
func (s *Store) TVSignalTotals(ctx context.Context, since int64) (total, last24h int, lastTs *int64, lastTicker string, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN ts >= ? THEN 1 ELSE 0 END), 0)
		FROM tv_signals`, since).Scan(&total, &last24h)
	if err != nil {
		return 0, 0, nil, "", err
	}
	if total == 0 {
		return 0, 0, nil, "", nil
	}
	var ts int64
	err = s.db.QueryRowContext(ctx,
		`SELECT ts, ticker FROM tv_signals ORDER BY ts DESC, id DESC LIMIT 1`).
		Scan(&ts, &lastTicker)
	if err != nil {
		return 0, 0, nil, "", err
	}
	return total, last24h, &ts, lastTicker, nil
}

// TVSignalAgg is one (symbol_id, ticker) group of received signals with its
// count and newest ts. The handler folds these onto the streamed set, matching
// either by resolved symbol_id or by normalized-ticker fallback.
type TVSignalAgg struct {
	SymbolID sql.NullInt64
	Ticker   string
	Count    int
	MaxTs    int64
}

// TVSignalAggregates groups every received signal by (symbol_id, ticker),
// returning the per-group count and newest ts. Grouping by both lets the
// handler match a streamed symbol via its resolved symbol_id OR via a
// normalized-ticker fallback (so a crypto pair streamed as "BTC/USD" still
// matches ticker "BTCUSD"/"BITSTAMP:BTCUSD").
func (s *Store) TVSignalAggregates(ctx context.Context) ([]TVSignalAgg, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ticker, COUNT(*), MAX(ts)
		FROM tv_signals
		GROUP BY symbol_id, ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]TVSignalAgg, 0, 8)
	for rows.Next() {
		var a TVSignalAgg
		if err := rows.Scan(&a.SymbolID, &a.Ticker, &a.Count, &a.MaxTs); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
