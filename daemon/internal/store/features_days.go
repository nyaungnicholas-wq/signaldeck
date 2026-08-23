package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func (s *Store) LabeledFeaturesRecentDays(ctx context.Context, h md.Horizon, days int, maxRows int) ([]LabeledFeature, bool, error) {
	if days <= 0 || maxRows <= 0 {
		return nil, false, errors.New("days and maxRows must be positive")
	}

	// First query: find distinct day buckets and their row counts for this horizon,
	// newest day first, limited to the requested number of days.
	// dayFold, not f.ts/86400. The consumer (adaptive.Compute) counts distinct
	// days with md.TradingDay = (ts-18000)/86400, and this folded on raw UTC
	// midnight -- two spellings of one fold, which is how this class of defect
	// returns. Measured over a 30-day window the two disagreed by one day
	// (30 vs 31), so no outcome moved, but the gate and its input must be
	// measured in the same unit by construction rather than by luck.
	dayRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s AS d, COUNT(*)
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		WHERE f.horizon=? AND o.resolved_at IS NOT NULL
		  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL
		GROUP BY d ORDER BY d DESC LIMIT ?`, dayFold("f.ts")),
		string(h), days)
	if err != nil {
		return nil, false, err
	}
	defer dayRows.Close() //nolint:errcheck

	var dayBuckets []struct {
		day   int64
		count int
	}
	for dayRows.Next() {
		var d int64
		var c int
		if err := dayRows.Scan(&d, &c); err != nil {
			return nil, false, err
		}
		dayBuckets = append(dayBuckets, struct {
			day   int64
			count int
		}{d, c})
	}
	if err := dayRows.Err(); err != nil {
		return nil, false, err
	}

	// Accumulate from newest day; stop before the day that would exceed maxRows.
	// ceilingBound is true iff we dropped at least one whole day because of the ceiling.
	var cutoffDay int64
	var totalRows int
	ceilingBound := false
	for _, b := range dayBuckets {
		if totalRows+b.count > maxRows {
			// This day and every older one are dropped WHOLE. Reaching this
			// branch is itself the proof that the ceiling — not a short
			// history — is what shortened the span.
			ceilingBound = true
			break
		}
		totalRows += b.count
		cutoffDay = b.day
	}

	// If no days fit (e.g., first day alone exceeds maxRows), return empty with ceilingBound.
	if totalRows == 0 {
		return []LabeledFeature{}, ceilingBound, nil
	}

	cutoffTs := cutoffDay*86400 + tradingDayOffsetSecs

	// Second query: fetch all labeled rows on or after the cutoff day, newest first.
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, f.ts, f.version, f.vec, o.up, o.fwd_return
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		WHERE f.horizon=? AND o.resolved_at IS NOT NULL
		  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL
		  AND f.ts >= ?
		ORDER BY f.ts DESC`,
		string(h), cutoffTs)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close() //nolint:errcheck

	out := make([]LabeledFeature, 0, totalRows)
	for rows.Next() {
		var lf LabeledFeature
		var vecJSON []byte
		if err := rows.Scan(&lf.SymbolID, &lf.Ts, &lf.Version, &vecJSON, &lf.Up, &lf.FwdReturn); err != nil {
			return nil, false, err
		}
		lf.Horizon = h
		if err := json.Unmarshal(vecJSON, &lf.Vec); err != nil {
			return nil, false, err
		}
		out = append(out, lf)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	return out, ceilingBound, nil
}
