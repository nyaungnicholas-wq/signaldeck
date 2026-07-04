package store

// ── STAGE 7: LIVE OUT-OF-SAMPLE TRACK RECORD (read accessors) ────────────────
//
// The /track-record surface grades the platform's OWN calibrated predictions
// (prediction_outcomes) against what the market actually did — the strongest
// honest evidence the project can produce, because prediction_outcomes.prob is
// the calibrated probability RECORDED AT PREDICTION TIME and up/fwd_return are
// filled in later by the resolver from realized bars. There is no lookahead: a
// row's prob is frozen when the prediction is made, and it becomes eligible for
// grading only once resolved_at is set.
//
// These are pure reads; the caller (api/trackrecord.go) does the independent-N
// dedup and the honesty gating. The store stays policy-free — it just returns
// the resolved (prob, up, fwd_return, symbol, market, day) rows.

import (
	"context"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ResolvedPredictionOutcome is one graded calibrated prediction: the calibrated
// probability recorded at prediction time joined to its realized outcome. Symbol
// + Market are carried so the API can group by market and dedupe to one
// independent observation per (symbol, forward-period).
type ResolvedPredictionOutcome struct {
	SymbolID  int64      `json:"-"`
	Symbol    string     `json:"symbol"`
	Market    md.Market  `json:"market"`
	Horizon   md.Horizon `json:"horizon"`
	Ts        int64      `json:"ts"`        // the prediction's bar timestamp (unix s)
	Prob      float64    `json:"prob"`      // calibrated P(up) recorded at prediction time
	Up        int        `json:"up"`        // realized 1/0
	FwdReturn float64    `json:"fwdReturn"` // realized forward return over the horizon
}

// ResolvedPredictionOutcomes returns every RESOLVED calibrated prediction for a
// horizon, newest-first, joined to its symbol + market. limit caps the row
// count. Only rows whose resolved_at is set are returned (no lookahead — an
// unresolved row has no realized outcome to grade against yet).
func (s *Store) ResolvedPredictionOutcomes(ctx context.Context, h md.Horizon, limit int) ([]ResolvedPredictionOutcome, error) {
	if limit <= 0 {
		limit = 20000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT po.symbol_id, sym.symbol, sym.market, po.ts, po.prob, po.up, po.fwd_return
		FROM prediction_outcomes po
		JOIN symbols sym ON sym.id = po.symbol_id
		WHERE po.resolved_at IS NOT NULL AND po.horizon = ? AND po.up IS NOT NULL
		ORDER BY po.ts DESC
		LIMIT ?`, string(h), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ResolvedPredictionOutcome
	for rows.Next() {
		o := ResolvedPredictionOutcome{Horizon: h}
		var mkt string
		if err := rows.Scan(&o.SymbolID, &o.Symbol, &mkt, &o.Ts, &o.Prob, &o.Up, &o.FwdReturn); err != nil {
			return nil, err
		}
		o.Market = md.Market(mkt)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ── STAGE 7: per-symbol chart-overlay markers ────────────────────────────────
//
// These power the candlestick overlays: regime changes and breakouts for one
// symbol within a bar window, so the chart can annotate WHERE the platform saw
// regime transitions or breakouts. (Score extremes come from ScoreHistory,
// which already exists.) Both are pure reads bounded by [from, to] unix seconds.

// RegimeChangeMarker is one regime transition for a symbol.
type RegimeChangeMarker struct {
	Ts   int64  `json:"ts"`
	From string `json:"from"`
	To   string `json:"to"`
}

// RegimeChangesForSymbol returns regime transitions for one symbol in [from,to],
// ascending by ts.
func (s *Store) RegimeChangesForSymbol(ctx context.Context, symbolID, from, to int64) ([]RegimeChangeMarker, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, from_lbl, to_lbl FROM regime_changes
		WHERE symbol_id=? AND ts>=? AND ts<=? ORDER BY ts`, symbolID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeChangeMarker
	for rows.Next() {
		var m RegimeChangeMarker
		if err := rows.Scan(&m.Ts, &m.From, &m.To); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// BreakoutMarker is one detected breakout event for a symbol.
type BreakoutMarker struct {
	Ts       int64   `json:"ts"`
	Kind     string  `json:"kind"`
	Detail   string  `json:"detail"`
	Strength float64 `json:"strength"`
}

// BreakoutsForSymbol returns breakout detections for one symbol in [from,to],
// ascending by ts.
func (s *Store) BreakoutsForSymbol(ctx context.Context, symbolID, from, to int64) ([]BreakoutMarker, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, kind, detail, strength FROM breakouts
		WHERE symbol_id=? AND ts>=? AND ts<=? ORDER BY ts`, symbolID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []BreakoutMarker
	for rows.Next() {
		var m BreakoutMarker
		if err := rows.Scan(&m.Ts, &m.Kind, &m.Detail, &m.Strength); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ResolvedPredictionCounts returns, per horizon, how many prediction_outcomes
// rows are resolved (up IS NOT NULL) vs total. Used by the track-record page to
// show, honestly, how thin the live record still is for each horizon.
func (s *Store) ResolvedPredictionCounts(ctx context.Context) (map[md.Horizon][2]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT horizon,
		       SUM(CASE WHEN resolved_at IS NOT NULL AND up IS NOT NULL THEN 1 ELSE 0 END) AS resolved,
		       COUNT(*) AS total
		FROM prediction_outcomes
		GROUP BY horizon`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[md.Horizon][2]int{}
	for rows.Next() {
		var hz string
		var resolved, total int
		if err := rows.Scan(&hz, &resolved, &total); err != nil {
			return nil, err
		}
		out[md.Horizon(hz)] = [2]int{resolved, total}
	}
	return out, rows.Err()
}
