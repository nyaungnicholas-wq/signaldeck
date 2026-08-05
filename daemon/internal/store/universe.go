// Broad-universe store methods (Stage 2). The symbols table now carries a
// `stream` flag: stream=1 is the STREAMED HOT SET (live websocket + full 1m
// pipeline, bounded by the free ws cap); stream=0 is the BROAD DAILY-ONLY
// universe (hundreds of REST-daily names that feed cross-sectional
// ranking/correlation/regime + per-symbol daily models, but are never
// streamed). These helpers register daily-only symbols, promote/demote the
// stream flag, and count each set so the caps can be enforced independently.
package store

import (
	"context"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// UpsertDailyUniverseSymbol registers (or reactivates) a broad-universe stock
// symbol as active-for-daily but NOT streamed (stream=0). It never demotes an
// existing streamed symbol: if the symbol is already in the hot set the
// stream flag is preserved, so re-seeding the universe can't silently pull a
// hot symbol off the live stream. Returns the resulting row.
func (s *Store) UpsertDailyUniverseSymbol(ctx context.Context, symbol string, name string) (md.Symbol, error) {
	now := time.Now().Unix()
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO symbols (symbol, market, name, active, added_at, stream) VALUES (?,?,?,1,?,0)
		ON CONFLICT(symbol, market) DO UPDATE SET
		  -- Same guard as UpsertSymbol: a row carrying delisted_at is a company
		  -- the market retired, and re-seeding the broad universe must not
		  -- resurrect it onto a recycled ticker. See store.go's UpsertSymbol.
		  active = CASE WHEN symbols.delisted_at IS NULL OR symbols.delisted_at = 0 THEN 1 ELSE symbols.active END,
		  name   = CASE WHEN excluded.name != '' THEN excluded.name ELSE symbols.name END`,
		symbol, string(md.Stocks), name, now)
	if err != nil {
		return md.Symbol{}, err
	}
	return s.GetSymbol(ctx, symbol, md.Stocks)
}

// SetSymbolStream flips a symbol's stream flag (promote into / demote out of
// the live hot set). History and the active flag are untouched.
func (s *Store) SetSymbolStream(ctx context.Context, id int64, stream bool) error {
	v := 0
	if stream {
		v = 1
	}
	_, err := s.w.ExecContext(ctx, `UPDATE symbols SET stream=? WHERE id=?`, v, id)
	return err
}

// StreamedSymbolCount returns how many STOCK symbols are in the live hot set
// (active AND stream=1 AND market=stocks) — the number the STREAM cap governs.
// The cap models Alpaca's free stock-websocket concurrent-symbol limit, so
// crypto (which streams via TickStream, not the Alpaca ws) is excluded even
// though the migration marks the consolidated crypto symbol stream=1. This is
// what discovery's budget checks — NOT ActiveSymbolCount, which now also
// includes the hundreds of daily-only universe symbols.
func (s *Store) StreamedSymbolCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols WHERE active=1 AND stream=1 AND market=?`,
		string(md.Stocks)).Scan(&n)
	return n, err
}

// DailyUniverseCount returns how many active stock symbols are daily-only
// (active AND stream=0) — the broad REST universe the UNIVERSE cap governs.
func (s *Store) DailyUniverseCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols WHERE active=1 AND stream=0 AND market=?`,
		string(md.Stocks)).Scan(&n)
	return n, err
}

// ActiveStockSymbols returns the symbol strings of every active stock,
// optionally filtered to daily-only (streamed=false) or hot-set (streamed=true)
// via the streamed argument; streamed=nil returns all active stocks. Ordered
// for deterministic batching.
func (s *Store) ActiveStockSymbols(ctx context.Context, streamed *bool) ([]md.Symbol, error) {
	q := `SELECT id, symbol, market, name, active, added_at, stream
	      FROM symbols WHERE active=1 AND market=?`
	args := []any{string(md.Stocks)}
	if streamed != nil {
		q += ` AND stream=?`
		v := 0
		if *streamed {
			v = 1
		}
		args = append(args, v)
	}
	q += ` ORDER BY symbol`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Symbol
	for rows.Next() {
		var sym md.Symbol
		var active, stream int
		var mkt string
		if err := rows.Scan(&sym.ID, &sym.Symbol, &mkt, &sym.Name, &active, &sym.AddedAt, &stream); err != nil {
			return nil, err
		}
		sym.Market, sym.Active, sym.Stream = md.Market(mkt), active == 1, stream == 1
		out = append(out, sym)
	}
	return out, rows.Err()
}
