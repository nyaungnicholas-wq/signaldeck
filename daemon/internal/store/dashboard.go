// Stage 3 (visual kit) — batched store reads for the ONE-call GET
// /api/dashboard payload. Every method here is a single SQL statement over
// the read pool (s.db) so the dashboard endpoint never issues per-symbol
// queries (no N+1): sparklines for a whole watchlist come back from one
// window-function scan, and the gauge inputs are single aggregates.
// Kept in this file — separate from store.go — so the dashboard wave never
// touches the core write paths.
package store

import (
	"context"
	"strings"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// LastNDailyCloses returns, for EACH requested symbol id, its most recent n
// daily closes in CHRONOLOGICAL order (oldest→newest) — in ONE query. This is
// the watchlist-sparkline batch: the dashboard needs ~60 closes per watched
// symbol, and issuing LastBars per symbol would be a query-per-row walk.
// Symbols with no daily bars are simply absent from the map (honest absence —
// the frontend renders "no data yet", never a fabricated flat line).
func (s *Store) LastNDailyCloses(ctx context.Context, symbolIDs []int64, n int) (map[int64][]float64, error) {
	out := map[int64][]float64{}
	if len(symbolIDs) == 0 || n <= 0 {
		return out, nil
	}
	// Build the IN (?,...) list; ids are bound params, never interpolated.
	ph := strings.TrimSuffix(strings.Repeat("?,", len(symbolIDs)), ",")
	args := make([]any, 0, len(symbolIDs)+1)
	for _, id := range symbolIDs {
		args = append(args, id)
	}
	args = append(args, n)
	// rn = 1 is the NEWEST bar; keep rn <= n, then emit ascending ts so each
	// slice is chart-ready without a per-symbol reverse pass.
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, close FROM (
			SELECT symbol_id, ts, close,
			       ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts DESC) AS rn
			FROM bars WHERE tf='1d' AND symbol_id IN (`+ph+`)
		) WHERE rn <= ? ORDER BY symbol_id, ts`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var id int64
		var close float64
		if err := rows.Scan(&id, &close); err != nil {
			return nil, err
		}
		out[id] = append(out[id], close)
	}
	return out, rows.Err()
}

// AnomalyCountSince returns how many anomalies were recorded at/after since —
// the dashboard "anomalies (24h)" gauge input. Anomalies are hour-deduped at
// insert time (idx_anomalies_dedup), so this is a count of DISTINCT
// (symbol, kind, hour) events, not raw scan hits.
func (s *Store) AnomalyCountSince(ctx context.Context, since int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM anomalies WHERE ts >= ?`, since).Scan(&n)
	return n, err
}

// LatestPredictionStats returns, over the LATEST prediction per symbol for one
// horizon, the average calibrated-probability confidence (mean of
// |cal_prob − 0.5| × 2, i.e. 0 = coin flip, 1 = certainty) and how many
// symbols have a prediction — in ONE query. Feeds the dashboard confidence
// gauge; the gate flag comes from ResolvedPredictionCount.
func (s *Store) LatestPredictionStats(ctx context.Context, h md.Horizon) (avgConf float64, n int, err error) {
	// 2026-07-24 perf pass: the old GROUP BY + self-join form re-aggregated the
	// whole predictions table (~240k rows, ~3.7s cold). Driving from symbols
	// with one idx_predictions_horizon_sym_ts probe per symbol (~50ms) returns
	// the identical latest-per-symbol stats.
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(ABS(cal_prob - 0.5) * 2), 0), COUNT(*)
		FROM (
			SELECT (SELECT p.cal_prob FROM predictions p
			        WHERE p.horizon=? AND p.symbol_id=s.id AND p.n_used > 0
			        ORDER BY p.ts DESC LIMIT 1) AS cal_prob
			FROM symbols s
		) WHERE cal_prob IS NOT NULL`, string(h)).Scan(&avgConf, &n)
	return avgConf, n, err
}

// ResolvedPredictionCount returns how many prediction outcomes have resolved
// for one horizon — the honesty gate input for the confidence gauge (below
// the minimum the gauge is labeled "not significant", mirroring the /honesty
// independent-N gate).
func (s *Store) ResolvedPredictionCount(ctx context.Context, h md.Horizon) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM prediction_outcomes
		WHERE horizon=? AND resolved_at IS NOT NULL AND up IS NOT NULL`, string(h)).Scan(&n)
	return n, err
}

// LiveDirectionalRecord summarizes the LIVE forward record of the calibrated
// directional predictions for one horizon over the SAME population the
// published grader grades — see gradeablepop.go for the definition and for the
// measurement that forced this.
//
// It used to fold on date(ts,'unixepoch') over every resolved row, with none of
// the grader's filters: no survivorship epoch, no settlement quarantine, no
// stale-feed exclusion. That published 17,099 observations at 47.7% on symbol
// pages while /accuracy published 2,399 at 41.5% and a FAILED verdict for the
// same predictor — the app overstating its own evidence 7.1x and its accuracy
// by 6.1pp against its own scoreboard. Two populations meant two truths; there
// is now one.
func (s *Store) LiveDirectionalRecord(ctx context.Context, h md.Horizon) (independentN int, winRate float64, err error) {
	applicable, err := s.SettlementApplicable(ctx)
	if err != nil {
		return 0, 0, err
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(AVG(CASE WHEN (prob>=0.5)=(up=1) THEN 1.0 ELSE 0.0 END),0)
		FROM (`+gradeableDedupSQL(applicable)+`) WHERE rn = 1 AND horizon = ?`,
		string(h)).Scan(&independentN, &winRate)
	return independentN, winRate, err
}

// RegimeBreadth aggregates the validated regime forecasts fleet-wide for the
// dashboard gauges: how many stocks read uptrend (of trend21 rows) and how
// many read elevated (of vol21 rows).
func (s *Store) RegimeBreadth(ctx context.Context) (uptrend, trendN, elevated, volN int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN kind='trend21' AND regime='uptrend' THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN kind='trend21' THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN kind='vol21' AND regime='elevated' THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN kind='vol21' THEN 1 ELSE 0 END),0)
		FROM regime_forecasts`).Scan(&uptrend, &trendN, &elevated, &volN)
	return uptrend, trendN, elevated, volN, err
}

// UnseenAlertCount returns one user's unseen-alert count (the dashboard
// sidebar badge) without paging the full alert list.
func (s *Store) UnseenAlertCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alerts WHERE user_id=? AND seen=0`, userID).Scan(&n)
	return n, err
}
