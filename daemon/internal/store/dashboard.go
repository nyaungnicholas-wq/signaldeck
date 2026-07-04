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
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(ABS(p.cal_prob - 0.5) * 2), 0), COUNT(*)
		FROM predictions p
		JOIN (SELECT symbol_id, MAX(ts) AS mx FROM predictions
		      WHERE horizon=? GROUP BY symbol_id) t
		  ON t.symbol_id = p.symbol_id AND t.mx = p.ts
		WHERE p.horizon=?`, string(h), string(h)).Scan(&avgConf, &n)
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
		WHERE horizon=? AND resolved_at IS NOT NULL`, string(h)).Scan(&n)
	return n, err
}

// UnseenAlertCount returns one user's unseen-alert count (the dashboard
// sidebar badge) without paging the full alert list.
func (s *Store) UnseenAlertCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alerts WHERE user_id=? AND seen=0`, userID).Scan(&n)
	return n, err
}
