// Store methods for the composite SignalScore (SIGNALS-hub overhaul; see
// internal/composite + pipeline/compositescorer.go). Kept in this file —
// separate from store.go — so the composite wave never touches the core
// write paths. All writes go through the single write connection s.w; reads
// use s.db.
package store

import (
	"context"
	"database/sql"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// CompositeScore is one stored composite SignalScore row. Symbol/Market are
// populated only by queries that join symbols.
type CompositeScore struct {
	SymbolID int64   `json:"-"`
	Symbol   string  `json:"symbol,omitempty"`
	Market   string  `json:"market,omitempty"`
	Horizon  string  `json:"horizon"`
	Ts       int64   `json:"ts"`
	Score    int     `json:"score"`
	CurvePct float64 `json:"curvePct"`
	Edge     float64 `json:"edge"`
	Payload  string  `json:"-"` // composite.Payload JSON (the API re-encodes it typed)
}

// UpsertCompositeScore stores one composite row; INSERT OR REPLACE on the
// (symbol_id, ts, horizon) PK makes a re-run of the same pass idempotent.
func (s *Store) UpsertCompositeScore(ctx context.Context, c CompositeScore) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO composite_scores
		  (symbol_id, ts, horizon, score, curve_pct, edge, payload)
		VALUES (?,?,?,?,?,?,?)`,
		c.SymbolID, c.Ts, c.Horizon, c.Score, c.CurvePct, c.Edge, c.Payload)
	return err
}

// LatestCompositeScore returns a symbol's newest composite row FOR THE GIVEN
// HORIZON (ok=false when the symbol has never been scored at that horizon —
// honest absence, not an error). horizon="" means "any horizon, newest wins" —
// kept for callers (desk.go) that don't distinguish 1d vs 1w.
func (s *Store) LatestCompositeScore(ctx context.Context, symbolID int64, horizon string) (CompositeScore, bool, error) {
	c := CompositeScore{SymbolID: symbolID}
	q := `SELECT ts, horizon, score, curve_pct, edge, payload FROM composite_scores WHERE symbol_id=?`
	args := []any{symbolID}
	if horizon != "" {
		q += ` AND horizon=?`
		args = append(args, horizon)
	}
	q += ` ORDER BY ts DESC LIMIT 1`
	err := s.db.QueryRowContext(ctx, q, args...).
		Scan(&c.Ts, &c.Horizon, &c.Score, &c.CurvePct, &c.Edge, &c.Payload)
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

// TopCompositeScores returns each ACTIVE symbol's newest composite row AT THE
// GIVEN HORIZON, joined to its symbol, best score first (score DESC, then
// curve_pct DESC, then symbol for determinism). horizon="" means any horizon
// (back-compat for callers that don't distinguish); market "" means both
// markets; limit <= 0 means all rows (the API ranks over the full set before
// truncating).
func (s *Store) TopCompositeScores(ctx context.Context, limit int, market, horizon string) ([]CompositeScore, error) {
	return s.compositeLatestRows(ctx, 1<<62, limit, market, horizon)
}

// CompositeScoresBefore returns each active symbol's newest composite row AT
// THE GIVEN HORIZON with ts < cutoff — the previous pass set the API computes
// rank-change against (e.g. cutoff = start of today UTC for "vs previous day").
func (s *Store) CompositeScoresBefore(ctx context.Context, cutoff int64, market, horizon string) ([]CompositeScore, error) {
	return s.compositeLatestRows(ctx, cutoff, 0, market, horizon)
}

// compositeLatestRows is the shared newest-row-per-symbol read (same window
// pattern as LatestPredictionsAll): rows with ts < cutoff, optional market AND
// horizon filter, ordered best score first. The MAX(ts) subquery is itself
// horizon-scoped so a symbol's 1d and 1w rows never leak into each other's
// "latest" — without this a fresher 1w write would silently shadow a 1d row.
func (s *Store) compositeLatestRows(ctx context.Context, cutoff int64, limit int, market, horizon string) ([]CompositeScore, error) {
	sub := `SELECT symbol_id, MAX(ts) AS mx FROM composite_scores WHERE ts < ?`
	subArgs := []any{cutoff}
	if horizon != "" {
		sub += ` AND horizon=?`
		subArgs = append(subArgs, horizon)
	}
	sub += ` GROUP BY symbol_id`

	q := `
		SELECT c.symbol_id, sy.symbol, sy.market, c.ts, c.horizon, c.score, c.curve_pct, c.edge, c.payload
		FROM composite_scores c
		JOIN (` + sub + `) t ON t.symbol_id = c.symbol_id AND t.mx = c.ts
		JOIN symbols sy ON sy.id = c.symbol_id AND sy.active = 1`
	args := append([]any{}, subArgs...)
	where := []string{}
	if horizon != "" {
		where = append(where, `c.horizon=?`)
		args = append(args, horizon)
	}
	if market != "" {
		where = append(where, `sy.market=?`)
		args = append(args, market)
	}
	if len(where) > 0 {
		q += ` WHERE ` + where[0]
		for _, w := range where[1:] {
			q += ` AND ` + w
		}
	}
	q += ` ORDER BY c.score DESC, c.curve_pct DESC, sy.symbol`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []CompositeScore{}
	for rows.Next() {
		var c CompositeScore
		if err := rows.Scan(&c.SymbolID, &c.Symbol, &c.Market, &c.Ts, &c.Horizon,
			&c.Score, &c.CurvePct, &c.Edge, &c.Payload); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── composite-pass input reads ────────────────────────────────────────────

// PredForScoring is one active symbol's newest prediction for the composite
// pass: identity + cadence flag + the full stored probabilities/components.
type PredForScoring struct {
	SymbolID   int64
	Symbol     string
	Market     md.Market
	Stream     bool
	Ts         int64
	RawProb    float64
	CalProb    float64
	NUsed      int
	Components string
}

// LatestPredictionsForScoring returns, for EVERY active symbol with at least
// one stored prediction on horizon h, its newest prediction WITH the
// components JSON — in ONE query (no N+1), same window pattern as
// LatestPredictionsAll. The composite engine consumes these as-is; it never
// recomputes the ensemble.
func (s *Store) LatestPredictionsForScoring(ctx context.Context, h md.Horizon) ([]PredForScoring, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.symbol_id, sy.symbol, sy.market, sy.stream, p.ts, p.raw_prob, p.cal_prob, p.n_used, p.components
		FROM predictions p
		JOIN (SELECT symbol_id, MAX(ts) AS mx FROM predictions
		      WHERE horizon=? GROUP BY symbol_id) t
		  ON t.symbol_id = p.symbol_id AND t.mx = p.ts
		JOIN symbols sy ON sy.id = p.symbol_id AND sy.active = 1
		WHERE p.horizon=?
		ORDER BY sy.symbol`, string(h), string(h))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PredForScoring
	for rows.Next() {
		var r PredForScoring
		var market string
		var stream int
		if err := rows.Scan(&r.SymbolID, &r.Symbol, &market, &stream, &r.Ts,
			&r.RawProb, &r.CalProb, &r.NUsed, &r.Components); err != nil {
			return nil, err
		}
		r.Market = md.Market(market)
		r.Stream = stream == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsiderNetActivity sums a symbol's OPEN-MARKET Form 4 activity since
// sinceTs (transaction date): dollar buys (code P) and sells (code S) with
// their trade counts. Grants/exercises/gifts (A/M/G/F/…) are deliberately
// excluded — only P/S carry conviction (see the insider_trades schema note).
func (s *Store) InsiderNetActivity(ctx context.Context, symbolID, sinceTs int64) (buys, sells float64, nBuys, nSells int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN code='P' THEN value ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN code='S' THEN value ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN code='P' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN code='S' THEN 1 ELSE 0 END), 0)
		FROM insider_trades
		WHERE symbol_id=? AND tx_ts>=? AND code IN ('P','S')`, symbolID, sinceTs).
		Scan(&buys, &sells, &nBuys, &nSells)
	return buys, sells, nBuys, nSells, err
}

// LatestBreakoutFor returns a symbol's newest breakout event (ok=false when
// none is stored). The caller applies its own recency window.
func (s *Store) LatestBreakoutFor(ctx context.Context, symbolID int64) (Breakout, bool, error) {
	var b Breakout
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, kind, detail, strength FROM breakouts
		WHERE symbol_id=? ORDER BY ts DESC, id DESC LIMIT 1`, symbolID).
		Scan(&b.Ts, &b.Kind, &b.Detail, &b.Strength)
	if err == sql.ErrNoRows {
		return b, false, nil
	}
	return b, err == nil, err
}
