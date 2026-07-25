// Signal8 wave — Stage 4: home-surface store helpers. One fleet-wide
// fundamentals lookup + one fleet-wide last-two-daily-closes lookup so the
// movers/calendar endpoints never issue a query per symbol. Reads only
// (s.db pool), matching the rest of the store.
package store

import "context"

// DailyCloses is one symbol's latest daily close (and its bar ts) plus the
// immediately preceding daily close (0 when only one daily bar exists).
type DailyCloses struct {
	Last float64
	Prev float64
	Ts   int64
}

// LastTwoDailyCloses returns, for EVERY symbol with daily bars, its latest
// close (+ts) and the previous close — in ONE query. /api/movers used to walk
// the active universe issuing a LastBars query per symbol (~2 queries per
// stock per request); this batches the whole sweep into a single statement.
//
// 2026-07-24 perf pass: the old window-function form scanned every tf='1d'
// bar (1.6M rows, ~7s cold and far worse under worker contention). Driving
// from symbols with correlated LIMIT-1 probes lets each symbol resolve via
// idx_bars_tf_sym_ts in a handful of index steps (~20ms measured) and stays
// flat as bar history grows.
func (s *Store) LastTwoDailyCloses(ctx context.Context) (map[int64]DailyCloses, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, last, COALESCE(prev, 0) FROM (
			SELECT s.id AS symbol_id,
			       (SELECT b.ts FROM bars b WHERE b.tf='1d' AND b.symbol_id=s.id
			        ORDER BY b.ts DESC LIMIT 1) AS ts,
			       (SELECT b.close FROM bars b WHERE b.tf='1d' AND b.symbol_id=s.id
			        ORDER BY b.ts DESC LIMIT 1) AS last,
			       (SELECT b.close FROM bars b WHERE b.tf='1d' AND b.symbol_id=s.id
			        ORDER BY b.ts DESC LIMIT 1 OFFSET 1) AS prev
			FROM symbols s
		) WHERE ts IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]DailyCloses{}
	for rows.Next() {
		var id int64
		var dc DailyCloses
		if err := rows.Scan(&id, &dc.Ts, &dc.Last, &dc.Prev); err != nil {
			return nil, err
		}
		out[id] = dc
	}
	return out, rows.Err()
}

// LatestMetricAll returns the newest (by as_of) row of ONE fundamentals
// metric for EVERY symbol that has it, keyed by symbol_id. Used by:
//   - /api/movers: metric "SharesOutstanding" → mcap = shares × last close
//     (best-effort; symbols EDGAR hasn't covered are simply absent — the
//     API labels them "mcap unavailable" rather than fabricating a number);
//   - /api/calendar: metric "LatestFilingDate" → the "reports soon (est)"
//     earnings heuristic (last SEC filing date + ~1 quarter, labeled EST).
func (s *Store) LatestMetricAll(ctx context.Context, metric string) (map[int64]FundamentalRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, f.value, f.as_of, f.fetched_at
		FROM fundamentals f
		JOIN (SELECT symbol_id, MAX(as_of) AS mx FROM fundamentals
		      WHERE metric=? GROUP BY symbol_id) t
		  ON t.symbol_id = f.symbol_id AND t.mx = f.as_of
		WHERE f.metric=?`, metric, metric)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]FundamentalRow{}
	for rows.Next() {
		r := FundamentalRow{Metric: metric}
		if err := rows.Scan(&r.SymbolID, &r.Value, &r.AsOf, &r.FetchedAt); err != nil {
			return nil, err
		}
		out[r.SymbolID] = r
	}
	return out, rows.Err()
}
