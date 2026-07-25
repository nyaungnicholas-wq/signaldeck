// Survivorship-clean universe accessors (2026-07-24).
//
// THE BIAS THIS CLOSES
// --------------------
// Symbols are never deleted here — unsubscribing sets active=0 and daily bars
// are protected from pruning in code. The history survives. What did NOT
// survive was its VISIBILITY: every research path iterates ActiveStockSymbols
// (active=1), so any name that was dropped, delisted or acquired silently left
// the study population. That is textbook survivorship bias, and it inflates
// exactly the strategies this platform researches most — oversold-bounce and
// mean-reversion, where the names that kept falling to zero are the ones
// missing.
//
// So the fix is not "keep the data" (already true) but "let research SEE the
// data": a universe accessor that returns everything ever tracked, plus a
// delisting marker so a point-in-time universe can be reconstructed rather
// than guessed.
//
// Honest limit, stated rather than hidden: this recovers names the platform
// itself once tracked. Companies that died BEFORE they were ever added remain
// absent, and no free data source fixes that — only a paid point-in-time
// vendor with delisted coverage does.
package store

import (
	"context"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ResearchUniverse returns EVERY stock symbol ever tracked, active or not, for
// backtests and statistical studies. Live trading paths must keep using
// ActiveStockSymbols — this one deliberately includes the dead.
func (s *Store) ResearchUniverse(ctx context.Context) ([]md.Symbol, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol, market, name, active, added_at
		FROM symbols WHERE market=? ORDER BY symbol`, string(md.Stocks))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Symbol
	for rows.Next() {
		var v md.Symbol
		var mkt string
		var active int
		if err := rows.Scan(&v.ID, &v.Symbol, &mkt, &v.Name, &active, &v.AddedAt); err != nil {
			return nil, err
		}
		v.Market = md.Market(mkt)
		v.Active = active == 1
		out = append(out, v)
	}
	return out, rows.Err()
}

// UniverseCoverage counts the universe by liveness so a study can DECLARE how
// much of its population is inactive rather than quietly omitting it.
func (s *Store) UniverseCoverage(ctx context.Context) (total, active, inactive int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(active),0)
		FROM symbols WHERE market=?`, string(md.Stocks)).Scan(&total, &active)
	if err != nil {
		return 0, 0, 0, err
	}
	return total, active, total - active, nil
}

// MarkDelisted records that a symbol stopped trading, so a point-in-time
// universe can be rebuilt. Deactivating is a SUBSCRIPTION decision (the user
// stopped watching); delisting is a MARKET fact — conflating them is what made
// the bias invisible, so they are stored separately.
func (s *Store) MarkDelisted(ctx context.Context, symbolID, ts int64) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE symbols SET delisted_at=? WHERE id=? AND (delisted_at IS NULL OR delisted_at=0)`,
		ts, symbolID)
	return err
}

// TradableAt returns the symbols that were tradable at ts — added by then and
// not yet delisted. This is the population a point-in-time backtest should
// draw from at that date.
func (s *Store) TradableAt(ctx context.Context, ts int64) ([]md.Symbol, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol, market, name, active, added_at
		FROM symbols
		WHERE market=? AND added_at <= ?
		  AND (delisted_at IS NULL OR delisted_at = 0 OR delisted_at > ?)
		ORDER BY symbol`, string(md.Stocks), ts, ts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Symbol
	for rows.Next() {
		var v md.Symbol
		var mkt string
		var active int
		if err := rows.Scan(&v.ID, &v.Symbol, &mkt, &v.Name, &active, &v.AddedAt); err != nil {
			return nil, err
		}
		v.Market = md.Market(mkt)
		v.Active = active == 1
		out = append(out, v)
	}
	return out, rows.Err()
}
