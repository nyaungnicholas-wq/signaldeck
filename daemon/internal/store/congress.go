// Signal8 wave — Stage 2 store methods: congressional stock trades (public
// STOCK Act disclosures via the free Stock Watcher mirrors). HONESTY:
// disclosures lag 30-45 days by law and amounts are ranges, never exact —
// the API notes say so. The id is a deterministic content hash, so INSERT OR
// IGNORE keeps every re-download of the cumulative mirror dumps idempotent.
// All writes go through the single write connection s.w; reads use s.db.
package store

import (
	"context"
	"database/sql"
)

// CongressTradeRow is one disclosed congressional stock transaction.
type CongressTradeRow struct {
	ID          string `json:"id"`
	Chamber     string `json:"chamber"` // senate | house
	Member      string `json:"member"`
	Symbol      string `json:"symbol"`
	SymbolID    *int64 `json:"symbolId"` // nil = ticker not tracked by us (honest)
	TxType      string `json:"txType"`
	AmountRange string `json:"amountRange"` // disclosed range, never an exact value
	TxTs        int64  `json:"txTs"`        // 0 = unparseable on the disclosure
	DisclosedTs int64  `json:"disclosedTs"` // lags the trade 30-45 days by law
}

// InsertCongressTrade records one trade; INSERT OR IGNORE on the content-hash
// PK makes re-sweeps of the cumulative dumps idempotent. Returns true when the
// row is NEW.
func (s *Store) InsertCongressTrade(ctx context.Context, t CongressTradeRow) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO congress_trades
		  (id, chamber, member, symbol, symbol_id, tx_type, amount_range, tx_ts, disclosed_ts)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Chamber, t.Member, t.Symbol, t.SymbolID, t.TxType, t.AmountRange, t.TxTs, t.DisclosedTs)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CongressTrades returns disclosed trades newest-first (by transaction date,
// then disclosure date). Filters: symbol matches the DISCLOSED ticker text
// exactly (so tickers we don't track are still queryable); member is a
// case-insensitive substring; chamber is exact ("senate"/"house"); any filter
// may be empty.
func (s *Store) CongressTrades(ctx context.Context, symbol, member, chamber string, limit int) ([]CongressTradeRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, chamber, member, symbol, symbol_id, tx_type, amount_range, tx_ts, disclosed_ts
	      FROM congress_trades WHERE 1=1`
	args := []any{}
	if symbol != "" {
		q += ` AND symbol = ?`
		args = append(args, symbol)
	}
	if member != "" {
		q += ` AND member LIKE '%' || ? || '%'`
		args = append(args, member)
	}
	if chamber != "" {
		q += ` AND chamber = ?`
		args = append(args, chamber)
	}
	q += ` ORDER BY tx_ts DESC, disclosed_ts DESC, id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []CongressTradeRow
	for rows.Next() {
		var r CongressTradeRow
		var sid sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Chamber, &r.Member, &r.Symbol, &sid,
			&r.TxType, &r.AmountRange, &r.TxTs, &r.DisclosedTs); err != nil {
			return nil, err
		}
		if sid.Valid {
			v := sid.Int64
			r.SymbolID = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CongressActivitySince reports how many disclosed trades a ticker has with a
// transaction date at/after since, and the latest such date — the symbol
// page's "congressional activity in the last 90d" chip.
func (s *Store) CongressActivitySince(ctx context.Context, symbol string, since int64) (n int64, lastTxTs int64, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(tx_ts), 0)
		FROM congress_trades WHERE symbol = ? AND tx_ts >= ?`,
		symbol, since).Scan(&n, &lastTxTs)
	return n, lastTxTs, err
}
