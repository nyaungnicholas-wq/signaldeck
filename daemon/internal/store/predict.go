package store

import (
	"context"
	"database/sql"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Prediction is a stored calibrated ensemble prediction.
type Prediction struct {
	SymbolID   int64      `json:"-"`
	Symbol     string     `json:"symbol,omitempty"`
	Horizon    md.Horizon `json:"horizon"`
	Ts         int64      `json:"ts"`
	RawProb    float64    `json:"rawProb"`
	CalProb    float64    `json:"calProb"`
	NUsed      int        `json:"nUsed"`
	Components string     `json:"components"`
}

// UpsertPrediction stores a prediction and seeds its outcome row.
func (s *Store) UpsertPrediction(ctx context.Context, p Prediction) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO predictions (symbol_id, horizon, ts, raw_prob, cal_prob, n_used, components)
		VALUES (?,?,?,?,?,?,?)`,
		p.SymbolID, string(p.Horizon), p.Ts, p.RawProb, p.CalProb, p.NUsed, p.Components); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO prediction_outcomes (symbol_id, horizon, ts, prob)
		VALUES (?,?,?,?)`, p.SymbolID, string(p.Horizon), p.Ts, p.CalProb); err != nil {
		return err
	}
	return tx.Commit()
}

// LatestPrediction returns the newest prediction for a symbol+horizon.
func (s *Store) LatestPrediction(ctx context.Context, symbolID int64, h md.Horizon) (Prediction, bool, error) {
	p := Prediction{SymbolID: symbolID, Horizon: h}
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, raw_prob, cal_prob, n_used, components FROM predictions
		WHERE symbol_id=? AND horizon=? ORDER BY ts DESC LIMIT 1`,
		symbolID, string(h)).Scan(&p.Ts, &p.RawProb, &p.CalProb, &p.NUsed, &p.Components)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	return p, err == nil, err
}

// UnresolvedPredictions returns pending prediction outcomes at/before cutoff.
func (s *Store) UnresolvedPredictions(ctx context.Context, h md.Horizon, cutoff int64, limit int) ([]struct {
	SymbolID int64
	Ts       int64
	Prob     float64
}, error,
) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, prob FROM prediction_outcomes
		WHERE resolved_at IS NULL AND horizon=? AND ts<=? ORDER BY ts LIMIT ?`,
		string(h), cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []struct {
		SymbolID int64
		Ts       int64
		Prob     float64
	}
	for rows.Next() {
		var r struct {
			SymbolID int64
			Ts       int64
			Prob     float64
		}
		if err := rows.Scan(&r.SymbolID, &r.Ts, &r.Prob); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolvePrediction records the realized up/down outcome.
func (s *Store) ResolvePrediction(ctx context.Context, symbolID int64, h md.Horizon, ts int64, fwdReturn float64) error {
	up := 0
	if fwdReturn > 0 {
		up = 1
	}
	_, err := s.w.ExecContext(ctx, `
		UPDATE prediction_outcomes SET up=?, fwd_return=?, resolved_at=?
		WHERE symbol_id=? AND horizon=? AND ts=?`,
		up, fwdReturn, time.Now().Unix(), symbolID, string(h), ts)
	return err
}

// ResolvedPredictionPairs returns (PUBLISHED prob, up) pairs — the calibrated
// probability frozen at prediction time against its realized outcome. This is
// the GRADING view: it answers "are our 70% calls actually 70%?" about the
// number users saw.
//
// Do NOT fit a recalibration map on it. The map is applied to the RAW blend
// probability, and prob here is the map's own previous output, so fitting on
// it is both a coordinate error and a recursion (2026-07-26 review, C3). Use
// ResolvedRawPredictionPairs to fit; use this to grade.
func (s *Store) ResolvedPredictionPairs(ctx context.Context, h md.Horizon, limit int) (probs []float64, ups []float64, err error) {
	rows, qerr := s.db.QueryContext(ctx, `
		SELECT prob, up FROM prediction_outcomes
		WHERE resolved_at IS NOT NULL AND horizon=? ORDER BY ts DESC LIMIT ?`,
		string(h), limit)
	if qerr != nil {
		return nil, nil, qerr
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var p float64
		var u int
		if err := rows.Scan(&p, &u); err != nil {
			return nil, nil, err
		}
		probs = append(probs, p)
		ups = append(ups, float64(u))
	}
	return probs, ups, rows.Err()
}

// ResolvedRawPredictionPairs returns (RAW blend prob, realized up) pairs for
// fitting the recalibration map — the newest `limit` resolved outcomes for one
// horizon.
//
// It exists because ResolvedPredictionPairs returns prediction_outcomes.prob,
// which UpsertPrediction seeds from CalProb: fitting a map on that column and
// then applying the map to raw is a coordinate error, and since cal_prob is the
// map's own previous output it also makes the fit recursive rather than out of
// sample (2026-07-26 review, C3). A map applied to raw must be fit on raw, so
// this joins predictions.raw_prob to the resolved outcome.
//
// Only resolved, non-voided rows are returned (resolved_at and up both NOT
// NULL), so a still-open prediction can never train the map that will be
// applied to it.
func (s *Store) ResolvedRawPredictionPairs(ctx context.Context, h md.Horizon, limit int) (raws []float64, ups []float64, err error) {
	rows, qerr := s.db.QueryContext(ctx, `
		SELECT p.raw_prob, o.up
		FROM prediction_outcomes o
		JOIN predictions p
		  ON p.symbol_id=o.symbol_id AND p.horizon=o.horizon AND p.ts=o.ts
		WHERE o.resolved_at IS NOT NULL AND o.up IS NOT NULL AND o.horizon=?
		ORDER BY o.ts DESC LIMIT ?`,
		string(h), limit)
	if qerr != nil {
		return nil, nil, qerr
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var p float64
		var u int
		if err := rows.Scan(&p, &u); err != nil {
			return nil, nil, err
		}
		raws = append(raws, p)
		ups = append(ups, float64(u))
	}
	return raws, ups, rows.Err()
}

// ── prequential-majority benchmark ──────────────────────────────────────

// SeedBenchmarkOutcome seeds an outcome row for a BENCHMARK pseudo-predictor
// (a namespaced horizon such as "1d#pm"). Benchmarks bypass the predictions
// table on purpose: they exist to be graded, never displayed, and must not
// enter calibration fits, dashboards or the ledger — every reader of
// predictions/prediction_outcomes filters on exact horizon values, so the
// namespaced horizon keeps benchmark rows out of all of those by construction
// while the registry's per-horizon grouping picks them up automatically.
func (s *Store) SeedBenchmarkOutcome(ctx context.Context, symbolID int64, h md.Horizon, ts int64, prob float64) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO prediction_outcomes (symbol_id, horizon, ts, prob)
		VALUES (?,?,?,?)`, symbolID, string(h), ts, prob)
	return err
}

// PrequentialMajorityProb returns the hindsight-free constant guess a
// majority-follower would commit RIGHT NOW for horizon h: 1 when the
// deduplicated resolved record over UTC days strictly before beforeDay runs
// majority-up, 0 when majority-down, 0.5 when empty or tied. (0.5 grades as a
// constant "up" guess under the >=0.5 rule — the closest committable analog
// of the registry null's expected coin flip.) Dedup mirrors DirectionalRecord
// and the accuracy registry: one row per (symbol, UTC-day), keeping the day's
// latest. sinceTs bounds the evidence window (the survivorship epoch — a
// majority learned from survivor-seeded rows would be a null in name only).
func (s *Store) PrequentialMajorityProb(ctx context.Context, h md.Horizon, beforeDay, sinceTs int64) (float64, error) {
	q := `
	WITH dedup AS (
	  SELECT up, ROW_NUMBER() OVER (PARTITION BY symbol_id, ts/86400 ORDER BY ts DESC) rn
	  FROM prediction_outcomes
	  WHERE horizon = ? AND resolved_at IS NOT NULL AND up IS NOT NULL
	    AND ts >= ? AND ts/86400 < ?
	)
	SELECT COUNT(*), COALESCE(SUM(up),0) FROM dedup WHERE rn = 1`
	var n, ups int
	if err := s.db.QueryRowContext(ctx, q, string(h), sinceTs, beforeDay).Scan(&n, &ups); err != nil {
		return 0.5, err
	}
	switch {
	case n == 0 || ups*2 == n:
		return 0.5, nil
	case ups*2 > n:
		return 1, nil
	default:
		return 0, nil
	}
}

// ── regime ──────────────────────────────────────────────────────────────

// UpsertRegime stores the latest regime for a symbol and logs a change row
// when the label differs from the previously-stored one.
func (s *Store) UpsertRegime(ctx context.Context, symbolID, ts int64, label string, strength float64, note string) error {
	var prev string
	err := s.db.QueryRowContext(ctx, `SELECT label FROM regime_state WHERE symbol_id=?`, symbolID).Scan(&prev)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO regime_state (symbol_id, ts, label, strength, note) VALUES (?,?,?,?,?)
		ON CONFLICT(symbol_id) DO UPDATE SET ts=excluded.ts, label=excluded.label, strength=excluded.strength, note=excluded.note`,
		symbolID, ts, label, strength, note); err != nil {
		return err
	}
	if prev != "" && prev != label {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO regime_changes (symbol_id, ts, from_lbl, to_lbl) VALUES (?,?,?,?)`,
			symbolID, ts, prev, label); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RegimeState is a stored regime snapshot.
type RegimeState struct {
	Symbol   string  `json:"symbol"`
	Market   string  `json:"market"`
	Ts       int64   `json:"ts"`
	Label    string  `json:"label"`
	Strength float64 `json:"strength"`
	Note     string  `json:"note"`
}

// Regimes returns the latest regime for every symbol.
func (s *Store) Regimes(ctx context.Context) ([]RegimeState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, sym.market, r.ts, r.label, r.strength, r.note
		FROM regime_state r JOIN symbols sym ON sym.id=r.symbol_id
		ORDER BY sym.market, sym.symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeState
	for rows.Next() {
		var r RegimeState
		if err := rows.Scan(&r.Symbol, &r.Market, &r.Ts, &r.Label, &r.Strength, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegimeChange is one logged transition.
type RegimeChange struct {
	Symbol string `json:"symbol"`
	Ts     int64  `json:"ts"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// RecentRegimeChanges returns the newest transitions across all symbols.
func (s *Store) RecentRegimeChanges(ctx context.Context, limit int) ([]RegimeChange, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, c.ts, c.from_lbl, c.to_lbl
		FROM regime_changes c JOIN symbols sym ON sym.id=c.symbol_id
		ORDER BY c.ts DESC, c.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeChange
	for rows.Next() {
		var c RegimeChange
		if err := rows.Scan(&c.Symbol, &c.Ts, &c.From, &c.To); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── rankings ────────────────────────────────────────────────────────────

// Ranked is one row of a ranking snapshot.
type Ranked struct {
	Symbol string  `json:"symbol"`
	Market string  `json:"market"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank"`
	Ret1M  float64 `json:"ret1m"`
	Ret3M  float64 `json:"ret3m"`
}

// ReplaceRanking swaps in a full ranking snapshot for timestamp ts.
func (s *Store) ReplaceRanking(ctx context.Context, ts int64, rows []struct {
	SymbolID     int64
	Score        float64
	Rank         int
	Ret1M, Ret3M float64
}) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM rankings WHERE ts=?`, ts); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rankings (ts, symbol_id, score, rank, ret1m, ret3m) VALUES (?,?,?,?,?,?)`,
			ts, r.SymbolID, r.Score, r.Rank, r.Ret1M, r.Ret3M); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestRanking returns the most recent ranking snapshot.
func (s *Store) LatestRanking(ctx context.Context) ([]Ranked, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, sym.market, r.score, r.rank, r.ret1m, r.ret3m
		FROM rankings r JOIN symbols sym ON sym.id=r.symbol_id
		WHERE r.ts=(SELECT MAX(ts) FROM rankings) ORDER BY r.rank`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []Ranked
	for rows.Next() {
		var r Ranked
		if err := rows.Scan(&r.Symbol, &r.Market, &r.Score, &r.Rank, &r.Ret1M, &r.Ret3M); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── breakouts ───────────────────────────────────────────────────────────

// InsertBreakout records a detected event (dedup handled by the caller).
func (s *Store) InsertBreakout(ctx context.Context, symbolID *int64, ts int64, kind, detail string, strength float64) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO breakouts (symbol_id, ts, kind, detail, strength) VALUES (?,?,?,?,?)`,
		symbolID, ts, kind, detail, strength)
	return err
}

// Breakout is a stored detection.
type Breakout struct {
	Symbol   string  `json:"symbol"`
	Ts       int64   `json:"ts"`
	Kind     string  `json:"kind"`
	Detail   string  `json:"detail"`
	Strength float64 `json:"strength"`
}

// RecentBreakouts returns the newest detections.
func (s *Store) RecentBreakouts(ctx context.Context, limit int) ([]Breakout, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(sym.symbol,''), b.ts, b.kind, b.detail, b.strength
		FROM breakouts b LEFT JOIN symbols sym ON sym.id=b.symbol_id
		ORDER BY b.ts DESC, b.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []Breakout
	for rows.Next() {
		var b Breakout
		if err := rows.Scan(&b.Symbol, &b.Ts, &b.Kind, &b.Detail, &b.Strength); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LastBreakoutTs returns the newest breakout time for a symbol+kind (dedup).
func (s *Store) LastBreakoutTs(ctx context.Context, symbolID int64, kind string) (int64, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(ts) FROM breakouts WHERE symbol_id=? AND kind=?`, symbolID, kind).Scan(&ts)
	return ts.Int64, err
}
