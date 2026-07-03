// Store methods for the alerts engine (see internal/alerts). Kept in this
// file — separate from store.go — so the alerts wave never touches the core
// write paths.
package store

import (
	"context"
	"database/sql"
)

// Alert is one per-user actionable event.
type Alert struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"-"`
	SymbolID *int64 `json:"-"`
	Symbol   string `json:"symbol,omitempty"`
	Market   string `json:"market,omitempty"`
	Horizon  string `json:"horizon,omitempty"`
	Kind     string `json:"kind"` // breakout | regime_change | prediction_high | prediction_low
	Detail   string `json:"detail"`
	Ts       int64  `json:"ts"`
	Seen     bool   `json:"seen"`
}

// InsertAlert stores one alert row. Idempotent on (user, kind, ts, symbol,
// horizon) via idx_alerts_dedup: a sweep that failed partway re-reads events
// from the unadvanced cursor, and already-inserted alerts become no-ops
// instead of duplicates.
func (s *Store) InsertAlert(ctx context.Context, a Alert) error {
	var hz any
	if a.Horizon != "" {
		hz = a.Horizon
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO alerts (user_id, symbol_id, horizon, kind, detail, ts, seen)
		VALUES (?,?,?,?,?,?,0)`,
		a.UserID, a.SymbolID, hz, a.Kind, a.Detail, a.Ts)
	return err
}

// Alerts returns one user's alerts, newest first (unseenOnly filters).
func (s *Store) Alerts(ctx context.Context, userID int64, unseenOnly bool, limit int) ([]Alert, error) {
	q := `SELECT a.id, a.symbol_id, COALESCE(sym.symbol,''), COALESCE(sym.market,''),
	             COALESCE(a.horizon,''), a.kind, a.detail, a.ts, a.seen
	      FROM alerts a LEFT JOIN symbols sym ON sym.id = a.symbol_id
	      WHERE a.user_id=?`
	if unseenOnly {
		q += ` AND a.seen=0`
	}
	q += ` ORDER BY a.ts DESC, a.id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]Alert, 0, 8)
	for rows.Next() {
		a := Alert{UserID: userID}
		var sid sql.NullInt64
		var seen int
		if err := rows.Scan(&a.ID, &sid, &a.Symbol, &a.Market, &a.Horizon, &a.Kind, &a.Detail, &a.Ts, &seen); err != nil {
			return nil, err
		}
		if sid.Valid {
			a.SymbolID = &sid.Int64
		}
		a.Seen = seen == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkAlertsSeen flips every unseen alert for one user; returns rows changed.
func (s *Store) MarkAlertsSeen(ctx context.Context, userID int64) (int64, error) {
	res, err := s.w.ExecContext(ctx,
		`UPDATE alerts SET seen=1 WHERE user_id=? AND seen=0`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// HasAlertSince reports whether an alert of this exact shape already exists
// at/after `since` — the prediction-alert dedup: at most one per
// (user, symbol, horizon, kind/side) per window.
func (s *Store) HasAlertSince(ctx context.Context, userID, symbolID int64, horizon, kind string, since int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts
		WHERE user_id=? AND symbol_id=? AND COALESCE(horizon,'')=? AND kind=? AND ts>=?`,
		userID, symbolID, horizon, kind, since).Scan(&n)
	return n > 0, err
}

// ListUserIDs returns every account id (the alert sweep fans out per user).
func (s *Store) ListUserIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MaxBreakoutID returns the current largest breakouts rowid (0 when empty) —
// used to initialize the alert sweep cursor on its very first run.
func (s *Store) MaxBreakoutID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM breakouts`).Scan(&id)
	return id.Int64, err
}

// MaxRegimeChangeID returns the current largest regime_changes rowid.
func (s *Store) MaxRegimeChangeID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM regime_changes`).Scan(&id)
	return id.Int64, err
}

// BreakoutEvent is one breakouts row with its rowid (sweep cursor).
type BreakoutEvent struct {
	ID       int64
	SymbolID *int64 // NULL for watchlist-wide events (correlation breaks)
	Ts       int64
	Kind     string
	Detail   string
	Strength float64
}

// BreakoutsAfterID returns breakout events with id > afterID (and, when
// sinceTs > 0, ts >= sinceTs — used on the very first sweep so an adopted
// database doesn't flood alerts with ancient events), oldest first.
func (s *Store) BreakoutsAfterID(ctx context.Context, afterID, sinceTs int64, limit int) ([]BreakoutEvent, error) {
	q := `SELECT id, symbol_id, ts, kind, detail, strength FROM breakouts WHERE id>?`
	args := []any{afterID}
	if sinceTs > 0 {
		q += ` AND ts>=?`
		args = append(args, sinceTs)
	}
	q += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []BreakoutEvent
	for rows.Next() {
		var e BreakoutEvent
		var sid sql.NullInt64
		if err := rows.Scan(&e.ID, &sid, &e.Ts, &e.Kind, &e.Detail, &e.Strength); err != nil {
			return nil, err
		}
		if sid.Valid {
			e.SymbolID = &sid.Int64
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RegimeChangeEvent is one regime_changes row with its rowid (sweep cursor).
type RegimeChangeEvent struct {
	ID       int64
	SymbolID int64
	Ts       int64
	From, To string
}

// RegimeChangesAfterID returns regime transitions with id > afterID (and,
// when sinceTs > 0, ts >= sinceTs), oldest first.
func (s *Store) RegimeChangesAfterID(ctx context.Context, afterID, sinceTs int64, limit int) ([]RegimeChangeEvent, error) {
	q := `SELECT id, symbol_id, ts, from_lbl, to_lbl FROM regime_changes WHERE id>?`
	args := []any{afterID}
	if sinceTs > 0 {
		q += ` AND ts>=?`
		args = append(args, sinceTs)
	}
	q += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeChangeEvent
	for rows.Next() {
		var e RegimeChangeEvent
		if err := rows.Scan(&e.ID, &e.SymbolID, &e.Ts, &e.From, &e.To); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
