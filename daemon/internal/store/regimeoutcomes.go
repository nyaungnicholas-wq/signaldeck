// Persistence for the LIVE regime-forecast grading loop (credibility wave):
// frozen regime calls (regime_outcomes), their later resolutions, and the
// plain-English postmortems for high-conviction misses (regime_postmortems).
// See schema.sql banners for the honesty contract; math lives in
// internal/structregime (resolve helpers) and the worker in
// internal/pipeline/regimeoutcomes.go.
package store

import (
	"context"
	"database/sql"

	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// RegimeCall is one current regime_forecasts row with its symbol id — the
// snapshot source the outcome worker freezes from.
type RegimeCall struct {
	SymbolID           int64
	Kind               structregime.Kind
	Ts                 int64
	HorizonDays        int
	Regime             string
	Conviction         float64
	HistoricalAccuracy float64
	Rank               float64
}

// RegimeForecastCalls returns every current regime forecast with its symbol id
// (RegimeForecasts joins for display; this is the grading read).
func (s *Store) RegimeForecastCalls(ctx context.Context) ([]RegimeCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, kind, ts, horizon_days, regime, conviction,
		       historical_accuracy, rank
		FROM regime_forecasts ORDER BY symbol_id, kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeCall
	for rows.Next() {
		var c RegimeCall
		var kind string
		if err := rows.Scan(&c.SymbolID, &kind, &c.Ts, &c.HorizonDays, &c.Regime,
			&c.Conviction, &c.HistoricalAccuracy, &c.Rank); err != nil {
			return nil, err
		}
		c.Kind = structregime.Kind(kind)
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertRegimeOutcome freezes one call as an ungraded outcome row, at most once
// per (symbol, kind, UTC-day of ts) — INSERT OR IGNORE on the dedup unique
// index. Returns whether a new row was written.
func (s *Store) InsertRegimeOutcome(ctx context.Context, c RegimeCall) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO regime_outcomes
		  (symbol_id, kind, ts, day, horizon_days, regime, conviction,
		   historical_accuracy, rank)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		c.SymbolID, string(c.Kind), c.Ts, c.Ts/86400, c.HorizonDays, c.Regime,
		c.Conviction, c.HistoricalAccuracy, c.Rank)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RegimeOutcomeRow is one frozen call, graded or not.
type RegimeOutcomeRow struct {
	ID                 int64
	SymbolID           int64
	Kind               structregime.Kind
	Ts                 int64
	HorizonDays        int
	Regime             string
	Conviction         float64
	HistoricalAccuracy float64
	Rank               float64
	ResolvedAt         int64 // 0 = unresolved
	Actual             string
	Correct            int // -1 unresolved, else 0/1
}

// DueRegimeOutcomes returns unresolved outcomes whose approximate forward
// window has elapsed: ts + horizon_days*1.45 calendar days <= now (trading→
// calendar approximation for stocks; the worker additionally requires enough
// NEWER daily bars before grading). Oldest first, capped.
func (s *Store) DueRegimeOutcomes(ctx context.Context, now int64, limit int) ([]RegimeOutcomeRow, error) {
	if limit <= 0 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol_id, kind, ts, horizon_days, regime, conviction,
		       historical_accuracy, rank
		FROM regime_outcomes
		WHERE resolved_at IS NULL
		  AND ts + CAST(horizon_days * 1.45 * 86400 AS INTEGER) <= ?
		ORDER BY ts ASC LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeOutcomeRow
	for rows.Next() {
		var r RegimeOutcomeRow
		var kind string
		if err := rows.Scan(&r.ID, &r.SymbolID, &kind, &r.Ts, &r.HorizonDays,
			&r.Regime, &r.Conviction, &r.HistoricalAccuracy, &r.Rank); err != nil {
			return nil, err
		}
		r.Kind = structregime.Kind(kind)
		r.Correct = -1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolveRegimeOutcome grades one frozen call with its realized label.
func (s *Store) ResolveRegimeOutcome(ctx context.Context, id int64, actual string, correct bool, resolvedAt int64) error {
	c := 0
	if correct {
		c = 1
	}
	_, err := s.w.ExecContext(ctx, `
		UPDATE regime_outcomes SET resolved_at=?, actual=?, correct=?
		WHERE id=? AND resolved_at IS NULL`, resolvedAt, actual, c, id)
	return err
}

// ResolvedRegimeOutcomes returns graded outcomes (newest call first, capped)
// for the track-record's regimes section. Rows are unique per (symbol, kind,
// UTC-day) by the dedup index, so they already ARE the independent set.
func (s *Store) ResolvedRegimeOutcomes(ctx context.Context, limit int) ([]RegimeOutcomeRow, error) {
	if limit <= 0 {
		limit = 50000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol_id, kind, ts, horizon_days, regime, conviction,
		       historical_accuracy, rank, resolved_at, actual, correct
		FROM regime_outcomes
		WHERE resolved_at IS NOT NULL
		ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeOutcomeRow
	for rows.Next() {
		var r RegimeOutcomeRow
		var kind string
		if err := rows.Scan(&r.ID, &r.SymbolID, &kind, &r.Ts, &r.HorizonDays,
			&r.Regime, &r.Conviction, &r.HistoricalAccuracy, &r.Rank,
			&r.ResolvedAt, &r.Actual, &r.Correct); err != nil {
			return nil, err
		}
		r.Kind = structregime.Kind(kind)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegimePostmortem is one stored high-conviction-miss narrative.
type RegimePostmortem struct {
	OutcomeID       int64   `json:"outcomeId"`
	Symbol          string  `json:"symbol"`
	Kind            string  `json:"kind"`
	Ts              int64   `json:"ts"`
	Regime          string  `json:"regime"`
	Conviction      float64 `json:"conviction"`
	ClaimedAccuracy float64 `json:"claimedAccuracy"`
	Actual          string  `json:"actual"`
	KeyName         string  `json:"keyName"`
	KeyValue        float64 `json:"keyValue"`
	Narrative       string  `json:"narrative"`
	CreatedAt       int64   `json:"createdAt"`
}

// InsertRegimePostmortem stores one miss narrative, at most once per outcome
// (INSERT OR IGNORE on the UNIQUE outcome_id).
func (s *Store) InsertRegimePostmortem(ctx context.Context, outcomeID, symbolID int64,
	o RegimeOutcomeRow, keyName string, keyValue float64, narrative string, now int64,
) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO regime_postmortems
		  (outcome_id, symbol_id, kind, ts, regime, conviction, claimed_accuracy,
		   actual, key_name, key_value, narrative, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		outcomeID, symbolID, string(o.Kind), o.Ts, o.Regime, o.Conviction,
		o.HistoricalAccuracy, o.Actual, keyName, keyValue, narrative, now)
	return err
}

// RecentRegimePostmortems returns the newest stored regime postmortems, capped.
func (s *Store) RecentRegimePostmortems(ctx context.Context, limit int) ([]RegimePostmortem, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT pm.outcome_id, sym.symbol, pm.kind, pm.ts, pm.regime, pm.conviction,
		       pm.claimed_accuracy, pm.actual, pm.key_name, pm.key_value,
		       pm.narrative, pm.created_at
		FROM regime_postmortems pm
		JOIN symbols sym ON sym.id = pm.symbol_id
		ORDER BY pm.created_at DESC, pm.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimePostmortem
	for rows.Next() {
		var p RegimePostmortem
		if err := rows.Scan(&p.OutcomeID, &p.Symbol, &p.Kind, &p.Ts, &p.Regime,
			&p.Conviction, &p.ClaimedAccuracy, &p.Actual, &p.KeyName, &p.KeyValue,
			&p.Narrative, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LatestPeriodicFiling returns one symbol's most recent 10-Q/10-K (amendments
// excluded, matching LatestPeriodicFilingAll) — the per-symbol earnings-window
// read. ok=false when no periodic filing is stored (an honest unknown).
func (s *Store) LatestPeriodicFiling(ctx context.Context, symbolID int64) (PeriodicFiling, bool, error) {
	var p PeriodicFiling
	err := s.db.QueryRowContext(ctx, `
		SELECT form, filed_ts FROM filings
		WHERE symbol_id = ? AND form IN ('10-Q','10-K')
		ORDER BY filed_ts DESC LIMIT 1`, symbolID).Scan(&p.Form, &p.FiledTs)
	if err == sql.ErrNoRows {
		return PeriodicFiling{}, false, nil
	}
	if err != nil {
		return PeriodicFiling{}, false, err
	}
	return p, true, nil
}

// ═══ WEEKLY DIGEST WAVE (appended block — keep at END of file so parallel
// edits by other agents never collide) ═══════════════════════════════════════

// RegimeWeekCall is one frozen regime call joined to its ACTIVE symbol — the
// digest's raw material for "what changed this week".
type RegimeWeekCall struct {
	Symbol string
	Kind   structregime.Kind
	Ts     int64
	Regime string
}

// RegimeOutcomeCallsSince returns every frozen regime call with ts >= since
// for currently-active symbols, oldest first — the digest compares each
// (symbol, kind)'s EARLIEST frozen call of the week against the CURRENT
// forecast to find regime changes.
func (s *Store) RegimeOutcomeCallsSince(ctx context.Context, since int64) ([]RegimeWeekCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, o.kind, o.ts, o.regime
		FROM regime_outcomes o JOIN symbols sym ON sym.id = o.symbol_id
		WHERE o.ts >= ? AND sym.active = 1
		ORDER BY o.ts ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeWeekCall
	for rows.Next() {
		var c RegimeWeekCall
		var kind string
		if err := rows.Scan(&c.Symbol, &kind, &c.Ts, &c.Regime); err != nil {
			return nil, err
		}
		c.Kind = structregime.Kind(kind)
		out = append(out, c)
	}
	return out, rows.Err()
}
