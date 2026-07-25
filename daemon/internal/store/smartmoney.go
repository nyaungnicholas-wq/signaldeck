// Store methods for the SMART MONEY FACTS wave (see internal/smartmoney +
// pipeline/smartmoney.go). Kept in this file — separate from store.go — so the
// wave never touches the core write paths. All writes go through the single
// write connection s.w; reads use s.db.
//
// HONESTY: a score row is a read of POSITIONING, not a forecast; a symbol with
// no insider/short/institutional data simply has no row (honest absence, not a
// fabricated neutral). Events are hour/day-deduped exactly like anomalies.
package store

import (
	"context"
	"database/sql"
)

// SmartMoneyScore is one stored per-symbol Smart Money Score (latest only —
// the table is PK'd on symbol_id and upserted). Payload is the smartmoney
// evidence JSON the API re-encodes typed.
type SmartMoneyScore struct {
	SymbolID int64   `json:"-"`
	Ts       int64   `json:"ts"`
	Score    float64 `json:"score"`
	Label    string  `json:"label"`
	Payload  string  `json:"-"`
}

// SmartMoneyScoreRow is a leaderboard row: a stored score joined to its symbol.
type SmartMoneyScoreRow struct {
	SymbolID int64   `json:"-"`
	Symbol   string  `json:"symbol"`
	Market   string  `json:"market"`
	Ts       int64   `json:"ts"`
	Score    float64 `json:"score"`
	Label    string  `json:"label"`
	Payload  string  `json:"-"` // the API extracts the top factor from this
}

// SmartMoneyEvent is one insider-cluster / squeeze-setup detection to persist.
type SmartMoneyEvent struct {
	SymbolID  int64
	Ts        int64
	Kind      string
	Detail    string
	DayBucket string
}

// SmartMoneyEventRow is one stored event with its rowid + joined symbol (the
// alert sweep's fan-out source, mirroring AnomalyRow).
type SmartMoneyEventRow struct {
	ID       int64  `json:"id"`
	SymbolID int64  `json:"-"`
	Symbol   string `json:"symbol,omitempty"`
	Market   string `json:"market,omitempty"`
	Ts       int64  `json:"ts"`
	Kind     string `json:"kind"` // insider_cluster | squeeze_setup
	Detail   string `json:"detail"`
}

// UpsertSmartMoneyScore stores one symbol's latest score; INSERT OR REPLACE on
// the symbol_id PK makes a re-run of the same pass idempotent.
func (s *Store) UpsertSmartMoneyScore(ctx context.Context, c SmartMoneyScore) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO smart_money_scores (symbol_id, ts, score, label, payload)
		VALUES (?,?,?,?,?)`,
		c.SymbolID, c.Ts, c.Score, c.Label, c.Payload)
	return err
}

// LatestSmartMoneyScore returns a symbol's stored score (ok=false when the
// symbol has never been scored — honest absence, not an error).
func (s *Store) LatestSmartMoneyScore(ctx context.Context, symbolID int64) (SmartMoneyScore, bool, error) {
	c := SmartMoneyScore{SymbolID: symbolID}
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, score, label, payload FROM smart_money_scores WHERE symbol_id=?`,
		symbolID).Scan(&c.Ts, &c.Score, &c.Label, &c.Payload)
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

// TopSmartMoneyScores returns stored scores joined to their symbols, best
// (most accumulation) first. market "" means both markets; limit <= 0 means
// all rows.
func (s *Store) TopSmartMoneyScores(ctx context.Context, market string, limit int) ([]SmartMoneyScoreRow, error) {
	q := `
		SELECT m.symbol_id, sy.symbol, sy.market, m.ts, m.score, m.label, m.payload
		FROM smart_money_scores m
		JOIN symbols sy ON sy.id = m.symbol_id AND sy.active = 1`
	args := []any{}
	if market != "" {
		q += ` WHERE sy.market = ?`
		args = append(args, market)
	}
	q += ` ORDER BY m.score DESC, sy.symbol`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []SmartMoneyScoreRow{}
	for rows.Next() {
		var r SmartMoneyScoreRow
		if err := rows.Scan(&r.SymbolID, &r.Symbol, &r.Market, &r.Ts, &r.Score, &r.Label, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertSmartMoneyEvent stores one event; reports whether a NEW row was written
// (false = deduped: same symbol+kind already recorded for this day_bucket).
func (s *Store) InsertSmartMoneyEvent(ctx context.Context, e SmartMoneyEvent) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO smart_money_events (symbol_id, ts, kind, detail, day_bucket)
		VALUES (?,?,?,?,?)`,
		e.SymbolID, e.Ts, e.Kind, e.Detail, e.DayBucket)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MaxSmartMoneyEventID returns the current max smart_money_events.id (0 when
// empty) — the first-sweep cursor seed, mirroring MaxAnomalyID.
func (s *Store) MaxSmartMoneyEventID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM smart_money_events`).Scan(&id)
	return id.Int64, err
}

// SmartMoneyEventsAfterID returns events with id > afterID (and, when sinceTs >
// 0, ts >= sinceTs), ascending id — the alert engine's gap-free sweep source,
// mirroring AnomaliesAfterID.
func (s *Store) SmartMoneyEventsAfterID(ctx context.Context, afterID, sinceTs int64, limit int) ([]SmartMoneyEventRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.symbol_id, COALESCE(sym.symbol,''), COALESCE(sym.market,''),
		       e.ts, e.kind, e.detail
		FROM smart_money_events e LEFT JOIN symbols sym ON sym.id = e.symbol_id
		WHERE e.id > ? AND e.ts >= ?
		ORDER BY e.id LIMIT ?`, afterID, sinceTs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]SmartMoneyEventRow, 0, 8)
	for rows.Next() {
		var e SmartMoneyEventRow
		if err := rows.Scan(&e.ID, &e.SymbolID, &e.Symbol, &e.Market, &e.Ts, &e.Kind, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
