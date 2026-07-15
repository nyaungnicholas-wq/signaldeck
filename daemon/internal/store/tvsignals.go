// Store methods for TradingView webhook signals (see internal/api/tvwebhook.go).
// Kept separate from store.go so the integration never touches core write paths.
package store

import (
	"context"
	"database/sql"
	"strings"
)

// TVSignal is one received TradingView alert.
type TVSignal struct {
	ID       int64    `json:"id"`
	SymbolID *int64   `json:"-"`
	Symbol   string   `json:"symbol,omitempty"` // resolved tracked symbol, if any
	Market   string   `json:"market,omitempty"`
	Ticker   string   `json:"ticker"` // raw ticker as sent
	Action   string   `json:"action"`
	Price    *float64 `json:"price,omitempty"`
	Message  string   `json:"message"`
	Ts       int64    `json:"ts"`
	Seen     bool     `json:"seen"`

	rawJSON string // full raw payload for provenance; never serialized to the API
}

// SetRaw attaches the full raw request payload, stored for provenance and kept
// out of the JSON the API returns.
func (sig *TVSignal) SetRaw(raw string) { sig.rawJSON = raw }

// LatestTVSignal returns the most recent TradingView webhook signal for a
// symbol (its action + receive time) for the prediction feature. ok=false when
// the symbol has no resolved signal yet.
func (s *Store) LatestTVSignal(ctx context.Context, symbolID int64) (action string, ts int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT action, ts FROM tv_signals WHERE symbol_id=? ORDER BY ts DESC LIMIT 1`,
		symbolID).Scan(&action, &ts)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	return action, ts, true, nil
}

// InsertTVSignal stores one webhook signal, resolving the ticker to a tracked
// symbol_id on a best-effort basis (NULL when we don't track it). Returns the
// new row id.
func (s *Store) InsertTVSignal(ctx context.Context, sig TVSignal) (int64, error) {
	var symID any
	if id, ok := s.symbolIDByTicker(ctx, sig.Ticker); ok {
		symID = id
	}
	var price any
	if sig.Price != nil {
		price = *sig.Price
	}
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO tv_signals (symbol_id, ticker, action, price, message, raw, ts, seen)
		VALUES (?,?,?,?,?,?,?,0)`,
		symID, sig.Ticker, sig.Action, price, sig.Message, clipRaw(sig.rawJSON), sig.Ts)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// symbolIDByTicker resolves a TradingView ticker ("NASDAQ:NVDA", "NVDA",
// "BTCUSD") to a tracked symbol id. It strips any "EXCHANGE:" prefix and
// matches case-insensitively on the stored symbol. Best-effort: ok=false when
// there is no exact match (crypto pairs like "BTCUSD" vs stored "BTC/USD"
// legitimately won't match and stay unresolved).
func (s *Store) symbolIDByTicker(ctx context.Context, ticker string) (int64, bool) {
	t := strings.TrimSpace(ticker)
	if i := strings.LastIndex(t, ":"); i >= 0 {
		t = t[i+1:]
	}
	if t == "" {
		return 0, false
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM symbols WHERE UPPER(symbol)=UPPER(?) LIMIT 1`, t).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, true
}

// TVSignals returns received signals newest-first (unseenOnly filters to
// unread; limit caps rows).
func (s *Store) TVSignals(ctx context.Context, unseenOnly bool, limit int) ([]TVSignal, error) {
	q := `SELECT t.id, t.symbol_id, COALESCE(sym.symbol,''), COALESCE(sym.market,''),
	             t.ticker, t.action, t.price, t.message, t.ts, t.seen
	      FROM tv_signals t LEFT JOIN symbols sym ON sym.id = t.symbol_id`
	if unseenOnly {
		q += ` WHERE t.seen=0`
	}
	q += ` ORDER BY t.ts DESC, t.id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]TVSignal, 0, 8)
	for rows.Next() {
		var sig TVSignal
		var sid sql.NullInt64
		var price sql.NullFloat64
		var seen int
		if err := rows.Scan(&sig.ID, &sid, &sig.Symbol, &sig.Market,
			&sig.Ticker, &sig.Action, &price, &sig.Message, &sig.Ts, &seen); err != nil {
			return nil, err
		}
		if sid.Valid {
			sig.SymbolID = &sid.Int64
		}
		if price.Valid {
			sig.Price = &price.Float64
		}
		sig.Seen = seen == 1
		out = append(out, sig)
	}
	return out, rows.Err()
}

// clipRaw bounds the stored raw payload (defense against a huge alert body
// bloating the row); the handler already caps the request body, this is belt +
// suspenders.
func clipRaw(s string) string {
	const max = 8 << 10
	if len(s) > max {
		return s[:max]
	}
	return s
}
