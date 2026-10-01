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
// These are pure reads. IndependentPredictionOutcomes does the independent-N
// collapse (one row per symbol per settle day) in SQL for the published
// callers; the API (api/trackrecord.go, api/composite.go) does the honesty
// gating. The store stays policy-free about publication.

import (
	"context"
	"database/sql"
	"strconv"

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
	// SettleTs is the base bar this row was graded from — the independence unit
	// (md.SettleDay). 0 means unknown and folds back to the calendar day. Carried
	// so the Go-side dedup in the API agrees with the SQL-side dedup in
	// DirectionalRecord; two halves of one record folding differently is the
	// defect this column exists to prevent.
	SettleTs int64 `json:"-"`
}

// ResolvedPredictionOutcomes returns every RESOLVED calibrated prediction for a
// horizon, newest-first, joined to its symbol + market. limit caps the row
// count. Only rows whose resolved_at is set are returned (no lookahead — an
// unresolved row has no realized outcome to grade against yet).
//
// The survivorship-epoch floor (2026-07-24 00:00:00 UTC) is CONCATENATED into
// the query, not bound, because it is a schema constant, not a user value.
// Without it the endpoint fed /api/track-record — "THE honest scoreboard the
// whole project exists to earn" per its file header — and fleetEdgeSkill with
// every resolved row ever under ORDER BY ts DESC LIMIT 120000. On 2026-08-12
// 198,002 resolved 1d rows existed so ~39% were silently dropped while the
// payload still published rawN: 120000 as the record size. The surviving
// window began NINE DAYS BEFORE the 2026-07-24 survivorship boundary, so it
// graded survivor-seeded rows from a 1,059-symbol universe that no longer
// exists, and it rolled forward every day with no start date in the payload.
// It therefore published 48.8% where the registry published 41.5% for the same
// predictor. The epoch also makes the cap NON-BINDING again: 77,904 post-epoch
// 1d rows and 33,175 1w against a 120,000 limit, so the truncation is gone
// rather than merely smaller. Forward-looking: if those counts approach the
// cap the truncation returns silently; len(result) == limit is the signal, and
// the fix is to page, not to raise the number.
//
// It came back: 1w crossed 120,000 graded rows around 2026-09-29 and the cap
// began dropping the window's first days from the public track record, ~10k
// more rows a day. limit < 0 now reads the WHOLE graded window (the epoch is
// the bound). limit == 0 keeps the old 20,000 default for callers that want a
// recent window. The published callers read the whole window COLLAPSED, through
// IndependentPredictionOutcomes below.
func (s *Store) ResolvedPredictionOutcomes(ctx context.Context, h md.Horizon, limit int) ([]ResolvedPredictionOutcome, error) {
	if limit == 0 {
		limit = 20000
	}
	q := `
		SELECT po.symbol_id, sym.symbol, sym.market, po.ts, po.prob, po.up, po.fwd_return, po.settle_ts
		FROM prediction_outcomes po
		JOIN symbols sym ON sym.id = po.symbol_id
		WHERE po.resolved_at IS NOT NULL AND po.horizon = ? AND po.up IS NOT NULL
		  AND po.ts >= ` + strconv.Itoa(GradingEpochTS) + `
		ORDER BY po.ts DESC`
	args := []any{string(h)}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ResolvedPredictionOutcome
	for rows.Next() {
		o := ResolvedPredictionOutcome{Horizon: h}
		var mkt string
		var settle sql.NullInt64
		if err := rows.Scan(&o.SymbolID, &o.Symbol, &mkt, &o.Ts, &o.Prob, &o.Up, &o.FwdReturn, &settle); err != nil {
			return nil, err
		}
		o.SettleTs = settle.Int64 // 0 when NULL — md.SettleDay reads that as unknown
		o.Market = md.Market(mkt)
		out = append(out, o)
	}
	return out, rows.Err()
}

// IndependentPredictionOutcomes is the WHOLE graded window of
// ResolvedPredictionOutcomes(h, -1) collapsed in SQL to what its published
// callers (api buildTrackRecord, fleetEdgeSkill) actually grade: ONE row per
// (symbol, SETTLED MOVE — md.SettleDay), the newest. rawN is the graded row
// count behind those rows (each group's COUNT(*), summed), i.e. the old
// len(rows), from the same snapshot. No cap: the epoch is the bound.
//
// Both callers used to receive every graded row and drop all but the newest of
// each group in Go. Live, read-only, 2026-10-01: 1w 128,834 rows for 9,120
// groups, 1d 37,399 for 1,420.
//
// GROUP BY with a bare-column MAX(ts), NOT the ROW_NUMBER window the other
// settle_day collapses use. SQLite takes every bare column from the row holding
// the single max() (documented min/max special case; ts is unique per group by
// the PK). MEASURED through this driver, process CPU, 7 interleaved runs on a
// 1w-shaped 127,400-row window, median: old read + Go dedup 547ms; this 391ms;
// the two-window form 891ms and one window 625ms — the windows' extra sort
// costs more in modernc than shipping the rows did. (The native sqlite3 CLI
// ranks the window form 2x faster than the old read; it is the wrong ruler.)
// The read was ~70% of a whole 1w buildTrackRecord's CPU on that fixture.
//
// ORDER BY ts DESC, symbol_id is the order the old query produced through its
// plan (idx_predoutcomes_resolved_hts keys (horizon, ts DESC, symbol_id)), now
// stated rather than inherited: the callers' float sums run in row order.
func (s *Store) IndependentPredictionOutcomes(ctx context.Context, h md.Horizon) (out []ResolvedPredictionOutcome, rawN int, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.symbol_id, sym.symbol, sym.market, g.ts, g.prob, g.up, g.fwd_return, g.settle_ts, g.grp_n FROM (
		  SELECT po.symbol_id, MAX(po.ts) AS ts, po.prob, po.up, po.fwd_return, po.settle_ts, COUNT(*) AS grp_n
		  FROM prediction_outcomes po
		  WHERE po.resolved_at IS NOT NULL AND po.horizon = ? AND po.up IS NOT NULL
		    AND po.ts >= `+strconv.Itoa(GradingEpochTS)+`
		  GROUP BY po.symbol_id, settle_day(po.settle_ts, po.ts)
		) g
		JOIN symbols sym ON sym.id = g.symbol_id
		ORDER BY g.ts DESC, g.symbol_id`, string(h))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		o := ResolvedPredictionOutcome{Horizon: h}
		var mkt string
		var settle sql.NullInt64
		var grpN int
		if err := rows.Scan(&o.SymbolID, &o.Symbol, &mkt, &o.Ts, &o.Prob, &o.Up, &o.FwdReturn, &settle, &grpN); err != nil {
			return nil, 0, err
		}
		o.SettleTs = settle.Int64 // 0 when NULL — settle_day folds it the same way
		o.Market = md.Market(mkt)
		out = append(out, o)
		rawN += grpN
	}
	return out, rawN, rows.Err()
}

// DirectionalAccuracy is one symbol's realized directional record over its
// INDEPENDENT observations (at most one per SETTLED MOVE — see md.SettleDay;
// a Fri/Sat/Sun cluster resolving against one Friday bar is ONE observation).
type DirectionalAccuracy struct {
	N       int // independent (symbol, settled-move) observations
	Correct int // of those, how many had predUp == actualUp
}

// DirectionalAccuracyBySymbol grades the whole resolved ledger for a horizon in
// ONE query and returns the per-symbol record.
//
// Why not ResolvedPredictionOutcomes + a Go loop: /api/attribution measured
// ~29s doing exactly that — pulling the full 120k-row ledger and then
// discarding every row that wasn't the requested symbol. MEASURED on the live
// DB through this driver, per 1d grade: 2.4s for the raw scan, 1.6-2.3s for the
// same aggregate written as a MAX(ts) self-join, and 0.8-1.1s for the window
// form below (1w: 0.4s). Notably the driver's per-row Scan was NOT the bill —
// 120k rows scan in ~1.2s — so the win here is SQLite doing one partitioned
// pass instead of a join, not the smaller result set.
//
// Semantics: the newest row of each (symbol, SETTLED MOVE) is that move's single
// independent observation (the PK makes (symbol_id,
// horizon, ts) unique, so the ROW_NUMBER pick is deterministic), and it scores
// DIRECTION — predUp == actualUp — not the up-rate. It grades the FULL ledger
// rather than the newest 120k rows; 1d is already at 120,055 resolved rows, so
// the old limit had begun to silently drop the oldest of them.
func (s *Store) DirectionalAccuracyBySymbol(ctx context.Context, h md.Horizon) (map[int64]DirectionalAccuracy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, COUNT(*) AS n,
		       SUM(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END) AS correct
		FROM (
		  SELECT symbol_id, prob, up,
		         ROW_NUMBER() OVER (PARTITION BY symbol_id, settle_day(settle_ts, ts) ORDER BY ts DESC) AS rn
		  FROM prediction_outcomes
		  WHERE resolved_at IS NOT NULL AND horizon = ? AND up IS NOT NULL
		)
		WHERE rn = 1
		GROUP BY symbol_id`, string(h))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]DirectionalAccuracy{}
	for rows.Next() {
		var id int64
		var da DirectionalAccuracy
		if err := rows.Scan(&id, &da.N, &da.Correct); err != nil {
			return nil, err
		}
		out[id] = da
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
