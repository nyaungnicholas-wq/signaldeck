// Cross-sectional leg observations, for the day-clustered fleet veto.
package store

import (
	"context"
	"time"
)

// LegDailyObs is one symbol's leg values on one trading day, with the realized
// direction. Components is the raw JSON the predictor froze at prediction time;
// the caller decodes the legs it cares about.
type LegDailyObs struct {
	Day        string
	SymbolID   int64
	Components string
	Up         int
}

// LegDailyObservations returns resolved predictions since `since`, DEDUPED to one
// row per symbol per trading day, for computing WITHIN-DAY cross-sectional AUC.
//
// The dedup is the same independence rule the rest of the tree grades with: the
// runner re-scores a symbol many times a day and every row inside one day
// carries the identical outcome, so pooling them would let the busiest-scored
// symbols dominate a cross-section that is supposed to have one vote each.
//
// n_used > 0 filters the evidence-only rows a legless blend writes. Those are
// deliberately not forecasts and must not enter a ranking measurement.
func (s *Store) LegDailyObservations(ctx context.Context, horizon string, since time.Time) ([]LegDailyObs, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH dedup AS (
		  SELECT date(p.ts,'unixepoch') AS d, p.symbol_id, p.components, o.up,
		         ROW_NUMBER() OVER (PARTITION BY p.symbol_id, date(p.ts,'unixepoch')
		                            ORDER BY p.ts DESC) rn
		  FROM predictions p
		  JOIN prediction_outcomes o
		    ON o.symbol_id = p.symbol_id AND o.horizon = p.horizon AND o.ts = p.ts
		  WHERE p.horizon = ? AND p.n_used > 0 AND p.ts >= ?
		    AND o.resolved_at IS NOT NULL AND o.up IS NOT NULL
		)
		SELECT d, symbol_id, components, up FROM dedup WHERE rn = 1 ORDER BY d`,
		horizon, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []LegDailyObs
	for rows.Next() {
		var o LegDailyObs
		if err := rows.Scan(&o.Day, &o.SymbolID, &o.Components, &o.Up); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
