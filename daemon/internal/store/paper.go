package store

import (
	"context"
	"database/sql"
)

// ── Stage 4: INTERNAL SIMULATED paper-trading persistence ────────────────────
//
// These helpers back internal/papertrade's worker + the /api/paper read. The
// book is a set of tables (paper_cursor, paper_positions, paper_trades,
// paper_equity) that together form an out-of-sample track record of the
// platform's own signal. Nothing here places a real order — every row is a
// simulated fill computed from stored bars. Writes go through the dedicated
// single write-connection (s.w); reads use the read pool (s.db).

// PaperPosition is one open simulated position (present row => currently long).
type PaperPosition struct {
	Strategy string  `json:"strategy"`
	SymbolID int64   `json:"-"`
	Symbol   string  `json:"symbol,omitempty"`
	Qty      float64 `json:"qty"`
	AvgPx    float64 `json:"avgPx"`
	OpenedTs int64   `json:"openedTs"`
}

// PaperTrade is one simulated fill in the append-only trade log.
type PaperTrade struct {
	ID       int64   `json:"id"`
	Strategy string  `json:"strategy"`
	SymbolID int64   `json:"-"`
	Symbol   string  `json:"symbol,omitempty"`
	Side     string  `json:"side"` // buy | sell
	Qty      float64 `json:"qty"`
	Px       float64 `json:"px"`
	Cost     float64 `json:"cost"`
	Ts       int64   `json:"ts"`
	Reason   string  `json:"reason"`
}

// PaperCursor is a strategy's idempotency + book state.
type PaperCursor struct {
	Strategy  string
	LastBarTs int64
	Cash      float64
	StartedTs int64
}

// PaperCursor returns the strategy's cursor. ok=false means the book has never
// been initialized (the worker will initialize it on first run).
func (s *Store) PaperCursor(ctx context.Context, strategy string) (PaperCursor, bool, error) {
	c := PaperCursor{Strategy: strategy}
	err := s.db.QueryRowContext(ctx, `
		SELECT last_bar_ts, cash, started_ts FROM paper_cursor WHERE strategy=?`,
		strategy).Scan(&c.LastBarTs, &c.Cash, &c.StartedTs)
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

// InitPaperBook creates a strategy's cursor at the starting cash if it does not
// already exist (INSERT OR IGNORE — safe to call every run). startedTs stamps
// the book's inception. Returns whether a new book was created.
func (s *Store) InitPaperBook(ctx context.Context, strategy string, cash float64, startedTs int64) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO paper_cursor (strategy, last_bar_ts, cash, started_ts)
		VALUES (?, 0, ?, ?)`, strategy, cash, startedTs)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PaperPositions returns the open positions for a strategy (with symbol names),
// ascending by symbol id for stable output.
func (s *Store) PaperPositions(ctx context.Context, strategy string) ([]PaperPosition, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.symbol_id, COALESCE(sy.symbol,''), p.qty, p.avg_px, p.opened_ts
		FROM paper_positions p
		LEFT JOIN symbols sy ON sy.id = p.symbol_id
		WHERE p.strategy=?
		ORDER BY p.symbol_id`, strategy)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PaperPosition
	for rows.Next() {
		p := PaperPosition{Strategy: strategy}
		if err := rows.Scan(&p.SymbolID, &p.Symbol, &p.Qty, &p.AvgPx, &p.OpenedTs); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PaperPosition returns one strategy+symbol open position. ok=false means flat.
func (s *Store) PaperPosition(ctx context.Context, strategy string, symbolID int64) (PaperPosition, bool, error) {
	p := PaperPosition{Strategy: strategy, SymbolID: symbolID}
	err := s.db.QueryRowContext(ctx, `
		SELECT qty, avg_px, opened_ts FROM paper_positions
		WHERE strategy=? AND symbol_id=?`, strategy, symbolID).Scan(&p.Qty, &p.AvgPx, &p.OpenedTs)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	return p, err == nil, err
}

// PaperTrades returns the most recent trades for a strategy (newest first),
// capped at limit.
func (s *Store) PaperTrades(ctx context.Context, strategy string, limit int) ([]PaperTrade, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.symbol_id, COALESCE(sy.symbol,''), t.side, t.qty, t.px, t.cost, t.ts, t.reason
		FROM paper_trades t
		LEFT JOIN symbols sy ON sy.id = t.symbol_id
		WHERE t.strategy=?
		ORDER BY t.id DESC LIMIT ?`, strategy, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PaperTrade
	for rows.Next() {
		t := PaperTrade{Strategy: strategy}
		if err := rows.Scan(&t.ID, &t.SymbolID, &t.Symbol, &t.Side, &t.Qty, &t.Px, &t.Cost, &t.Ts, &t.Reason); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AllPaperTradesAsc returns every trade for a strategy ascending by id — the
// full ledger the summary uses to reconstruct closed round-trips + turnover.
func (s *Store) AllPaperTradesAsc(ctx context.Context, strategy string) ([]PaperTrade, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol_id, side, qty, px, cost, ts, reason
		FROM paper_trades WHERE strategy=? ORDER BY id`, strategy)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PaperTrade
	for rows.Next() {
		t := PaperTrade{Strategy: strategy}
		if err := rows.Scan(&t.ID, &t.SymbolID, &t.Side, &t.Qty, &t.Px, &t.Cost, &t.Ts, &t.Reason); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PaperEquityCurve returns the equity curve for a strategy ascending by ts.
func (s *Store) PaperEquityCurve(ctx context.Context, strategy string, limit int) ([]struct {
	Ts             int64
	Cash           float64
	PositionsValue float64
	Equity         float64
}, error) {
	if limit <= 0 {
		limit = 5000
	}
	// Newest-N by ts DESC, then re-ascend for display.
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, cash, positions_value, equity FROM
		  (SELECT * FROM paper_equity WHERE strategy=? ORDER BY ts DESC LIMIT ?)
		ORDER BY ts`, strategy, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []struct {
		Ts             int64
		Cash           float64
		PositionsValue float64
		Equity         float64
	}
	for rows.Next() {
		var r struct {
			Ts             int64
			Cash           float64
			PositionsValue float64
			Equity         float64
		}
		if err := rows.Scan(&r.Ts, &r.Cash, &r.PositionsValue, &r.Equity); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PaperApply is one atomic paper-trading step for a strategy at a bar. It is the
// single write path the worker uses per (strategy, bar): it records any fills,
// upserts/deletes positions, marks equity, and advances the cursor + cash — all
// in ONE transaction so a re-run at/behind lastBarTs is a genuine no-op (the
// caller guards on the cursor; this makes the whole mutation atomic so a crash
// can't half-apply a bar). barTs must be strictly greater than the stored
// cursor last_bar_ts or the call is rejected as a replay (defense in depth).
type PaperApply struct {
	Strategy string
	BarTs    int64 // the bar we are acting on (fills happen at this bar's open)
	NewCash  float64
	// Opens are positions to insert/replace (entered this bar).
	Opens []PaperPosition
	// CloseSymbolIDs are positions to delete (exited this bar).
	CloseSymbolIDs []int64
	// Trades are the fills to append to the log.
	Trades []PaperTrade
	// Equity is the mark for this bar (cash + positions_value).
	EquityTs             int64
	EquityCash           float64
	EquityPositionsValue float64
	EquityValue          float64
}

// ApplyPaperStep commits a PaperApply atomically. It advances paper_cursor's
// last_bar_ts + cash to (BarTs, NewCash) ONLY when BarTs strictly exceeds the
// current cursor — so a replayed/older bar makes no change and returns
// applied=false. This is the durable idempotency guarantee.
func (s *Store) ApplyPaperStep(ctx context.Context, a PaperApply) (applied bool, err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Guard: only advance if this bar is strictly newer than the cursor. This is
	// the transactional replay guard (the worker also checks, but doing it here
	// under the write lock makes double-apply impossible even under concurrency).
	var cur int64
	err = tx.QueryRowContext(ctx, `SELECT last_bar_ts FROM paper_cursor WHERE strategy=?`, a.Strategy).Scan(&cur)
	if err != nil {
		return false, err
	}
	if a.BarTs <= cur {
		return false, nil // replay / stale bar — no-op
	}

	for _, sid := range a.CloseSymbolIDs {
		if _, err = tx.ExecContext(ctx, `DELETE FROM paper_positions WHERE strategy=? AND symbol_id=?`,
			a.Strategy, sid); err != nil {
			return false, err
		}
	}
	for _, p := range a.Opens {
		if _, err = tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO paper_positions (strategy, symbol_id, qty, avg_px, opened_ts)
			VALUES (?,?,?,?,?)`, a.Strategy, p.SymbolID, p.Qty, p.AvgPx, p.OpenedTs); err != nil {
			return false, err
		}
	}
	for _, t := range a.Trades {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO paper_trades (strategy, symbol_id, side, qty, px, cost, ts, reason)
			VALUES (?,?,?,?,?,?,?,?)`,
			a.Strategy, t.SymbolID, t.Side, t.Qty, t.Px, t.Cost, t.Ts, t.Reason); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO paper_equity (strategy, ts, cash, positions_value, equity)
		VALUES (?,?,?,?,?)`,
		a.Strategy, a.EquityTs, a.EquityCash, a.EquityPositionsValue, a.EquityValue); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE paper_cursor SET last_bar_ts=?, cash=? WHERE strategy=?`,
		a.BarTs, a.NewCash, a.Strategy); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
