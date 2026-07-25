// Historical research-weeks store (research discovery engine wave).
//
// research_weeks holds one point-in-time weekly observation per (symbol,
// calendar week), computed retrospectively from daily bars with strict
// no-lookahead discipline — the evidence base the research engine grades
// hypotheses against. Rows are recompute-idempotent (REPLACE on the key).
// Writes go through s.w, reads through the pooled s.db, matching the rest of
// the store.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ResearchWeek is one persisted weekly research observation.
type ResearchWeek struct {
	SymbolID  int64              `json:"symbolId"`
	Week      int64              `json:"week"` // calendar-week bucket = Ts / 604800
	Ts        int64              `json:"ts"`   // anchor daily-bar ts (last bar of the week)
	Vec       map[string]float64 `json:"vec"`
	FwdReturn float64            `json:"fwdReturn"`
	Up        bool               `json:"up"`
	Era       string             `json:"era"`
	HighVol   bool               `json:"highVol"`
}

// UpsertResearchWeeks writes a batch of weekly rows idempotently (REPLACE on
// the (symbol_id, week) key) in a single transaction.
func (s *Store) UpsertResearchWeeks(ctx context.Context, rows []ResearchWeek, now int64) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO research_weeks
		  (symbol_id, week, ts, vec, fwd_return, up, era, high_vol, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck
	for _, r := range rows {
		vec, err := json.Marshal(r.Vec)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, r.SymbolID, r.Week, r.Ts, string(vec),
			r.FwdReturn, boolToInt(r.Up), r.Era, boolToInt(r.HighVol), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResearchWeeks returns every row in the [fromWeek, toWeek] inclusive
// week-bucket range, ordered (week, symbol_id). keys != nil projects each Vec
// down to those keys after unmarshal — the engine loads years of rows at once,
// so it must not hold full vectors it will never read.
func (s *Store) ResearchWeeks(ctx context.Context, fromWeek, toWeek int64, keys []string) ([]ResearchWeek, error) {
	var keep map[string]bool
	if keys != nil {
		keep = make(map[string]bool, len(keys))
		for _, k := range keys {
			keep[k] = true
		}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, week, ts, vec, fwd_return, up, era, high_vol
		FROM research_weeks WHERE week>=? AND week<=?
		ORDER BY week, symbol_id`, fromWeek, toWeek)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ResearchWeek
	for rows.Next() {
		var r ResearchWeek
		var vec string
		var up, highVol int
		if err := rows.Scan(&r.SymbolID, &r.Week, &r.Ts, &vec, &r.FwdReturn,
			&up, &r.Era, &highVol); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(vec), &r.Vec); err != nil {
			return nil, err
		}
		if keep != nil {
			for k := range r.Vec {
				if !keep[k] {
					delete(r.Vec, k)
				}
			}
		}
		r.Up = up != 0
		r.HighVol = highVol != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResearchWeekStats summarizes the historical evidence base: total rows,
// distinct symbols/weeks, the anchor-ts span, and the per-era row counts.
type ResearchWeekStats struct {
	Rows    int            `json:"rows"`
	Symbols int            `json:"symbols"`
	Weeks   int            `json:"weeks"`
	MinTs   int64          `json:"minTs"`
	MaxTs   int64          `json:"maxTs"`
	ByEra   map[string]int `json:"byEra"`
}

// ResearchWeeksStats returns the research_weeks summary (all zero values and
// an empty ByEra map when no backfill has run — the honest empty state).
func (s *Store) ResearchWeeksStats(ctx context.Context) (ResearchWeekStats, error) {
	st := ResearchWeekStats{ByEra: map[string]int{}}
	var mn, mx sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT symbol_id), COUNT(DISTINCT week), MIN(ts), MAX(ts)
		FROM research_weeks`).Scan(&st.Rows, &st.Symbols, &st.Weeks, &mn, &mx); err != nil {
		return st, err
	}
	if mn.Valid {
		st.MinTs = mn.Int64
	}
	if mx.Valid {
		st.MaxTs = mx.Int64
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT era, COUNT(*) FROM research_weeks GROUP BY era`)
	if err != nil {
		return st, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var era string
		var n int
		if err := rows.Scan(&era, &n); err != nil {
			return st, err
		}
		st.ByEra[era] = n
	}
	return st, rows.Err()
}

// EarliestBarTs returns the oldest bar timestamp stored for (symbol, tf), 0
// when none — the backfill worker's "is this symbol's history shallow?" probe.
func (s *Store) EarliestBarTs(ctx context.Context, symbolID int64, tf md.Timeframe) (int64, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MIN(ts), 0) FROM bars WHERE symbol_id=? AND tf=?`,
		symbolID, string(tf)).Scan(&ts)
	return ts, err
}

// ── autonomous research loop (2026-07-25) ────────────────────────────────────

// LoopHypothesis is one rule the automated loop discovered and judged.
type LoopHypothesis struct {
	ID          string  `json:"id"`
	Desc        string  `json:"desc"`
	Status      string  `json:"status"` // shadow | rejected — never "promoted"
	WilsonLower float64 `json:"wilsonLower"`
	Survives    bool    `json:"survives"`
	FoundAt     int64   `json:"foundAt"`
}

// ResearchObservations loads weekly observations in the shape the discovery
// grid consumes. limit<=0 loads everything.
func (s *Store) ResearchObservations(ctx context.Context, limit int) ([]researchx.Obs, error) {
	q := `SELECT symbol_id, week, ts, vec, fwd_return, up, era, high_vol
	      FROM research_weeks ORDER BY week, symbol_id`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []researchx.Obs{}
	for rows.Next() {
		var o researchx.Obs
		var vec string
		var up, highVol int
		if err := rows.Scan(&o.SymbolID, &o.Week, &o.Ts, &vec, &o.FwdRet,
			&up, &o.Era, &highVol); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(vec), &o.Vec) != nil {
			continue // a malformed vector is skipped, never guessed at
		}
		o.Up = up == 1
		o.HighVol = highVol == 1
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpsertLoopHypothesis records one discovered rule and its verdict. Idempotent
// on ID so a re-run updates rather than duplicating — the same rule rediscovered
// tomorrow is the same hypothesis, not a new one.
func (s *Store) UpsertLoopHypothesis(ctx context.Context, h LoopHypothesis) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO research_loop_hypotheses
		  (id, descr, status, wilson_lower, survives, found_at, last_seen)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  status=excluded.status, wilson_lower=excluded.wilson_lower,
		  survives=excluded.survives, last_seen=excluded.last_seen`,
		h.ID, h.Desc, h.Status, h.WilsonLower, boolToInt(h.Survives),
		h.FoundAt, h.FoundAt)
	return err
}

// LoopHypotheses returns what the loop has found, newest first.
func (s *Store) LoopHypotheses(ctx context.Context, limit int) ([]LoopHypothesis, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, descr, status, wilson_lower, survives, found_at
		FROM research_loop_hypotheses ORDER BY last_seen DESC, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []LoopHypothesis{}
	for rows.Next() {
		var h LoopHypothesis
		var sv int
		if err := rows.Scan(&h.ID, &h.Desc, &h.Status, &h.WilsonLower, &sv,
			&h.FoundAt); err != nil {
			return nil, err
		}
		h.Survives = sv == 1
		out = append(out, h)
	}
	return out, rows.Err()
}
