// Store methods for the CONFLUENCE GATE + MONEY SCOREBOARD wave (see
// internal/confluence + pipeline/confluence.go). Kept in this file — separate
// from store.go — so the wave never touches the core write paths. All writes go
// through the single write connection s.w; reads use s.db.
//
// HONESTY: a setup row is a strict AND over INDEPENDENT signal families (no
// manufactured edge); outcomes are FORWARD-TRACKED with no lookahead (fwd_return
// filled only once the forward bar exists); events are day-deduped exactly like
// smart-money / anomalies.
package store

import (
	"context"
	"database/sql"
)

// ConfluenceSetup is one stored per-symbol confluence assessment (latest only —
// the table is PK'd on symbol_id and upserted). Payload is the votes JSON the
// API re-encodes typed. Symbol/Market are populated only by joined queries.
type ConfluenceSetup struct {
	SymbolID  int64   `json:"-"`
	Symbol    string  `json:"symbol,omitempty"`
	Market    string  `json:"market,omitempty"`
	Ts        int64   `json:"ts"`
	Direction int     `json:"direction"`
	Agree     int     `json:"agree"`
	Dissent   int     `json:"dissent"`
	Score     float64 `json:"score"`
	IsSetup   bool    `json:"isSetup"`
	Payload   string  `json:"-"`
}

// ConfluenceOutcome is one FORWARD-TRACKED flagged setup. FwdReturn/Win/ResolvedAt
// are nil until the resolver grades it against realized bars (no lookahead).
type ConfluenceOutcome struct {
	SymbolID  int64
	Symbol    string
	Market    string
	Ts        int64
	Horizon   string
	Direction int
	Agree     int
	EntryPx   float64
	FwdReturn float64
	Win       int
}

// ConfluenceEvent is one "confluence setup" detection to persist (day-deduped).
type ConfluenceEvent struct {
	SymbolID  int64
	Ts        int64
	Kind      string
	Detail    string
	DayBucket string
}

// ConfluenceEventRow is one stored event with its rowid + joined symbol (the
// alert sweep's fan-out source, mirroring SmartMoneyEventRow).
type ConfluenceEventRow struct {
	ID       int64  `json:"id"`
	SymbolID int64  `json:"-"`
	Symbol   string `json:"symbol,omitempty"`
	Market   string `json:"market,omitempty"`
	Ts       int64  `json:"ts"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

// UpsertConfluenceSetup stores one symbol's latest assessment; INSERT OR REPLACE
// on the symbol_id PK makes a re-run of the same pass idempotent.
func (s *Store) UpsertConfluenceSetup(ctx context.Context, c ConfluenceSetup) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO confluence_setups
		  (symbol_id, ts, direction, agree, dissent, score, is_setup, payload)
		VALUES (?,?,?,?,?,?,?,?)`,
		c.SymbolID, c.Ts, c.Direction, c.Agree, c.Dissent, c.Score, boolToInt(c.IsSetup), c.Payload)
	return err
}

// LatestConfluenceSetup returns a symbol's stored assessment (ok=false when the
// symbol has never been assessed — honest absence, not an error).
func (s *Store) LatestConfluenceSetup(ctx context.Context, symbolID int64) (ConfluenceSetup, bool, error) {
	c := ConfluenceSetup{SymbolID: symbolID}
	var isSetup int
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, direction, agree, dissent, score, is_setup, payload
		FROM confluence_setups WHERE symbol_id=?`, symbolID).
		Scan(&c.Ts, &c.Direction, &c.Agree, &c.Dissent, &c.Score, &isSetup, &c.Payload)
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	c.IsSetup = isSetup == 1
	return c, err == nil, err
}

// TopConfluenceSetups returns stored assessments joined to their symbols, flagged
// setups first then strongest agreement (|score|) first. market "" means both
// markets; onlySetups restricts to rows that cleared the gate; limit <= 0 = all.
func (s *Store) TopConfluenceSetups(ctx context.Context, market string, limit int, onlySetups bool) ([]ConfluenceSetup, error) {
	q := `
		SELECT c.symbol_id, sy.symbol, sy.market, c.ts, c.direction, c.agree, c.dissent, c.score, c.is_setup, c.payload
		FROM confluence_setups c
		JOIN symbols sy ON sy.id = c.symbol_id AND sy.active = 1`
	var conds []string
	var args []any
	if market != "" {
		conds = append(conds, "sy.market = ?")
		args = append(args, market)
	}
	if onlySetups {
		conds = append(conds, "c.is_setup = 1")
	}
	for i, cond := range conds {
		if i == 0 {
			q += " WHERE " + cond
		} else {
			q += " AND " + cond
		}
	}
	q += ` ORDER BY c.is_setup DESC, ABS(c.score) DESC, sy.symbol`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []ConfluenceSetup{}
	for rows.Next() {
		var c ConfluenceSetup
		var isSetup int
		if err := rows.Scan(&c.SymbolID, &c.Symbol, &c.Market, &c.Ts, &c.Direction,
			&c.Agree, &c.Dissent, &c.Score, &isSetup, &c.Payload); err != nil {
			return nil, err
		}
		c.IsSetup = isSetup == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertConfluenceOutcome forward-tracks a flagged setup; INSERT OR IGNORE on the
// (symbol_id, ts, horizon) PK makes it idempotent (a re-run of the same pass, or
// two setups the same rounded ts, never double-inserts).
func (s *Store) InsertConfluenceOutcome(ctx context.Context, o ConfluenceOutcome) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO confluence_outcomes
		  (symbol_id, ts, horizon, direction, agree, entry_px)
		VALUES (?,?,?,?,?,?)`,
		o.SymbolID, o.Ts, o.Horizon, o.Direction, o.Agree, o.EntryPx)
	return err
}

// UnresolvedConfluenceOutcomes returns pending outcomes with ts <= before (the
// resolver's maturity cutoff), oldest first. entry_px is frozen at flag time.
func (s *Store) UnresolvedConfluenceOutcomes(ctx context.Context, before int64, limit int) ([]ConfluenceOutcome, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, horizon, direction, agree, entry_px
		FROM confluence_outcomes
		WHERE resolved_at IS NULL AND ts <= ?
		ORDER BY ts LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []ConfluenceOutcome{}
	for rows.Next() {
		var o ConfluenceOutcome
		if err := rows.Scan(&o.SymbolID, &o.Ts, &o.Horizon, &o.Direction, &o.Agree, &o.EntryPx); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ResolveConfluenceOutcome records the realized forward return + win flag for a
// matured setup (called only once the forward bar exists — no lookahead).
func (s *Store) ResolveConfluenceOutcome(ctx context.Context, symbolID, ts int64, horizon string, fwdReturn float64, win bool) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE confluence_outcomes SET fwd_return=?, win=?, resolved_at=strftime('%s','now')
		WHERE symbol_id=? AND ts=? AND horizon=?`,
		fwdReturn, boolToInt(win), symbolID, ts, horizon)
	return err
}

// ResolvedConfluenceOutcomes returns graded outcomes joined to their symbols,
// newest first — the money scoreboard's source. limit <= 0 means all.
func (s *Store) ResolvedConfluenceOutcomes(ctx context.Context, limit int) ([]ConfluenceOutcome, error) {
	q := `
		SELECT o.symbol_id, sy.symbol, sy.market, o.ts, o.horizon, o.direction, o.agree, o.entry_px, o.fwd_return, o.win
		FROM confluence_outcomes o
		JOIN symbols sy ON sy.id = o.symbol_id
		WHERE o.resolved_at IS NOT NULL
		ORDER BY o.ts DESC`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []ConfluenceOutcome{}
	for rows.Next() {
		var o ConfluenceOutcome
		var fwd sql.NullFloat64
		var win sql.NullInt64
		if err := rows.Scan(&o.SymbolID, &o.Symbol, &o.Market, &o.Ts, &o.Horizon,
			&o.Direction, &o.Agree, &o.EntryPx, &fwd, &win); err != nil {
			return nil, err
		}
		o.FwdReturn = fwd.Float64
		o.Win = int(win.Int64)
		out = append(out, o)
	}
	return out, rows.Err()
}

// InsertConfluenceEvent stores one event; reports whether a NEW row was written
// (false = deduped: same symbol+kind already recorded for this day_bucket).
func (s *Store) InsertConfluenceEvent(ctx context.Context, e ConfluenceEvent) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO confluence_events (symbol_id, ts, kind, detail, day_bucket)
		VALUES (?,?,?,?,?)`,
		e.SymbolID, e.Ts, e.Kind, e.Detail, e.DayBucket)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MaxConfluenceEventID returns the current max confluence_events.id (0 when
// empty) — the first-sweep cursor seed, mirroring MaxSmartMoneyEventID.
func (s *Store) MaxConfluenceEventID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM confluence_events`).Scan(&id)
	return id.Int64, err
}

// ConfluenceEventsAfterID returns events with id > afterID (and, when sinceTs >
// 0, ts >= sinceTs), ascending id — the alert engine's gap-free sweep source,
// mirroring SmartMoneyEventsAfterID.
func (s *Store) ConfluenceEventsAfterID(ctx context.Context, afterID, sinceTs int64, limit int) ([]ConfluenceEventRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.symbol_id, COALESCE(sym.symbol,''), COALESCE(sym.market,''),
		       e.ts, e.kind, e.detail
		FROM confluence_events e LEFT JOIN symbols sym ON sym.id = e.symbol_id
		WHERE e.id > ? AND e.ts >= ?
		ORDER BY e.id LIMIT ?`, afterID, sinceTs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]ConfluenceEventRow, 0, 8)
	for rows.Next() {
		var e ConfluenceEventRow
		if err := rows.Scan(&e.ID, &e.SymbolID, &e.Symbol, &e.Market, &e.Ts, &e.Kind, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// boolToInt is the 1/0 encoding for SQLite integer-boolean columns.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
