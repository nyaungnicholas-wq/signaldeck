package store

// ── STAGE 2: MAKE THE PROOF VISIBLE (read accessors) ─────────────────────────
//
// Small pure reads that feed the gate countdown on /api/track-record and the
// weekly self-report worker (internal/briefing). All policy (the honesty
// gating, the estimate math, the report wording) lives in the callers — this
// layer only measures what is stored.

import (
	"context"
	"database/sql"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ResolutionAccrual is how many prediction outcomes RESOLVED inside a window
// for one horizon: raw rows vs distinct (symbol, UTC-day) — the independent
// count that actually moves the track-record gate.
type ResolutionAccrual struct {
	Raw         int `json:"raw"`
	Independent int `json:"independent"`
}

// ResolutionsSince counts, per horizon, the prediction_outcomes rows whose
// resolved_at falls at/after since: raw rows and distinct (symbol, SETTLED-MOVE)
// observations. This is the accrual-rate input for the gate countdown and the
// weekly self-report ("resolutions added this week"). The independent count keys
// on the PREDICTION's settled move — the same dedup rule the track record itself
// uses — not on when the resolver happened to run.
//
// It must stay the same rule as the track record's: this number is the accrual
// the gate counts down, so folding it more coarsely than the record it feeds
// would let the gate open on observations the record does not credit.
func (s *Store) ResolutionsSince(ctx context.Context, since int64) (map[md.Horizon]ResolutionAccrual, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT horizon,
		       COUNT(*),
		       COUNT(DISTINCT symbol_id || ':' || CAST(settle_day(settle_ts, ts) AS INTEGER))
		FROM prediction_outcomes
		WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND resolved_at >= ?
		GROUP BY horizon`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[md.Horizon]ResolutionAccrual{}
	for rows.Next() {
		var hz string
		var a ResolutionAccrual
		if err := rows.Scan(&hz, &a.Raw, &a.Independent); err != nil {
			return nil, err
		}
		out[md.Horizon(hz)] = a
	}
	return out, rows.Err()
}

// EarliestUnresolvedTs returns the oldest still-open prediction timestamp for
// a horizon (the first row that will resolve once its forward window closes).
// ok=false when the horizon has no unresolved predictions at all — then no
// first-resolution ETA can honestly be estimated.
func (s *Store) EarliestUnresolvedTs(ctx context.Context, h md.Horizon) (int64, bool, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(ts) FROM prediction_outcomes
		WHERE horizon=? AND resolved_at IS NULL`, string(h)).Scan(&ts)
	if err != nil {
		return 0, false, err
	}
	return ts.Int64, ts.Valid, nil
}

// SymbolModelProgress is one per-symbol agent's march toward the personal-model
// graduation gate (symbolagent.MinPersonal own resolved outcomes).
type SymbolModelProgress struct {
	Symbol   string `json:"symbol"`
	Market   string `json:"market"`
	Horizon  string `json:"horizon"`
	NSamples int    `json:"nSamples"`
	Tier     string `json:"tier"`
}

// SymbolModelsNearGraduation returns the not-yet-graduated symbol models
// (n_samples < target) closest to the personal-model gate, ordered by
// n_samples descending. Feeds the weekly self-report's "nearest graduation"
// list.
func (s *Store) SymbolModelsNearGraduation(ctx context.Context, target, limit int) ([]SymbolModelProgress, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, sym.market, m.horizon, m.n_samples, m.tier
		FROM symbol_models m
		JOIN symbols sym ON sym.id = m.symbol_id
		WHERE m.n_samples < ?
		ORDER BY m.n_samples DESC, sym.symbol ASC, m.horizon ASC
		LIMIT ?`, target, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []SymbolModelProgress
	for rows.Next() {
		var p SymbolModelProgress
		if err := rows.Scan(&p.Symbol, &p.Market, &p.Horizon, &p.NSamples, &p.Tier); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountSymbolModelsByTier counts stored per-symbol models on one evidence tier
// (e.g. symbolagent.TierPersonal for "graduated").
func (s *Store) CountSymbolModelsByTier(ctx context.Context, tier string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbol_models WHERE tier=?`, tier).Scan(&n)
	return n, err
}

// SentimentSymbolsSince counts the distinct symbols with at least one
// sentiment_daily aggregate on/after sinceDay (UTC YYYY-MM-DD; ISO strings
// compare correctly). Feeds the weekly report's sentiment-coverage line.
func (s *Store) SentimentSymbolsSince(ctx context.Context, sinceDay string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT symbol_id) FROM sentiment_daily WHERE day >= ?`, sinceDay).Scan(&n)
	return n, err
}
