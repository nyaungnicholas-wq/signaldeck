package store

import (
	"context"
	"database/sql"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const independentWindowSecs = 35 * 86400 // 35-day window for bounded scan

// ResolvedOutcomesIndependent returns the most recent resolved outcome per
// symbol/horizon/settle-day group, along with the total number of raw rows
// those observations represent. The caller reports both counts; the difference
// between them is the honesty message — "we read N raw rows and only k are
// independent evidence". Deduping in SQL would erase that difference, because
// the collapsed rows never reach Go. So the query reports how many raw rows
// each surviving observation stands for, and rawRows is their SUM over the
// returned rows. That is strictly more truthful than counting rows the reader
// happened to fetch: it is the raw evidence actually behind the returned
// observations.
//
// KNOWN AND DELIBERATE: the OLDEST day returned is usually PARTIAL. The limit
// cuts mid-day, so that day is truncated by the row budget rather than by
// reality — measured 2026-08-13 at limit 5000, the boundary day held 172 rows
// against a 322-row median for 1d, and 200 against 317 for 1w. A caller that
// resamples WHOLE DAYS (the /api/honesty bootstrap does) treats it as a
// genuinely small day, because nothing distinguishes the two.
//
// NOT fixed by dropping that day: real trading days already vary in size, the
// percentile bootstrap assumes no uniformity, and discarding ~172 real
// observations to tidy one cluster out of 23 costs more evidence than the bias
// is worth. Recorded so the asymmetry is not later mistaken for a short market
// day. This deliberately differs from LabeledFeaturesRecentDays, which DOES
// guarantee whole days: there the row cap is a safety ceiling and wholeness is
// free, whereas here the limit IS the window, so wholeness would cost a day.
func (s *Store) ResolvedOutcomesIndependent(ctx context.Context, h md.Horizon, limit int) (out []md.ScoreOutcome, rawRows int, err error) {
	if limit <= 0 {
		return nil, 0, fmt.Errorf("limit must be positive, got %d", limit)
	}

	// A bounded result with `limit` rows holds exactly the newest `limit` groups:
	// a group it misses has its newest row below the cut-off. Its counts can still
	// be short, because a group may have older rows below the cut-off (a symbol
	// whose bars stop keeps adding rows to one settle day). addRowsBelow adds
	// those rows back, scanning only the symbols where an overlap is possible.
	// Measured 2026-11-18: the unbounded query held a read snapshot 60 to 163
	// seconds, 16 times that day.
	var maxTs sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(ts) FROM score_outcomes WHERE resolved_at IS NOT NULL AND horizon=?`, string(h)).Scan(&maxTs); err != nil {
		return nil, 0, err
	}
	if maxTs.Valid {
		since := maxTs.Int64 - independentWindowSecs
		out, counts, err := s.resolvedOutcomesIndependentCounts(ctx, h, limit, since)
		if err != nil {
			return nil, 0, err
		}
		if len(out) == limit {
			if err := s.addRowsBelow(ctx, h, since, out, counts); err != nil {
				return nil, 0, err
			}
			sum := 0
			for _, c := range counts {
				sum += c
			}
			return out, sum, nil
		}
	}
	return s.resolvedOutcomesIndependentSince(ctx, h, limit, 0)
}

func (s *Store) resolvedOutcomesIndependentCounts(ctx context.Context, h md.Horizon, limit int, since int64) (out []md.ScoreOutcome, counts []int, err error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT symbol_id, horizon, ts, score, fwd_return, resolved_at, settle_ts, grp_n FROM (
            SELECT symbol_id, horizon, ts, score, fwd_return, resolved_at, settle_ts,
                   COUNT(*) OVER (PARTITION BY symbol_id, settle_day(settle_ts, ts)) AS grp_n,
                   ROW_NUMBER() OVER (PARTITION BY symbol_id, settle_day(settle_ts, ts) ORDER BY ts DESC) AS rn
            FROM score_outcomes WHERE resolved_at IS NOT NULL AND horizon=? AND ts>=?
        ) WHERE rn=1 ORDER BY ts DESC LIMIT ?`, string(h), since, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var o md.ScoreOutcome
		var hz string
		var fwd sql.NullFloat64
		var res sql.NullInt64
		var settle sql.NullInt64
		var grpN int
		if err := rows.Scan(&o.SymbolID, &hz, &o.Ts, &o.Score, &fwd, &res, &settle, &grpN); err != nil {
			return nil, nil, err
		}
		o.SettleTs = settle.Int64
		o.Horizon = md.Horizon(hz)
		if fwd.Valid {
			o.FwdReturn = &fwd.Float64
		}
		if res.Valid {
			o.ResolvedAt = &res.Int64
		}
		out = append(out, o)
		counts = append(counts, grpN)
	}
	return out, counts, rows.Err()
}

func (s *Store) resolvedOutcomesIndependentSince(ctx context.Context, h md.Horizon, limit int, since int64) (out []md.ScoreOutcome, rawRows int, err error) {
	out, counts, err := s.resolvedOutcomesIndependentCounts(ctx, h, limit, since)
	if err != nil {
		return nil, 0, err
	}
	sum := 0
	for _, c := range counts {
		sum += c
	}
	return out, sum, nil
}

func (s *Store) addRowsBelow(ctx context.Context, h md.Horizon, since int64, out []md.ScoreOutcome, counts []int) error {
	idx := make(map[int64]map[int64]int)
	minDay := make(map[int64]int64)
	for i, o := range out {
		day := md.SettleDay(o.SettleTs, o.Ts)
		if idx[o.SymbolID] == nil {
			idx[o.SymbolID] = make(map[int64]int)
		}
		idx[o.SymbolID][day] = i
		if d, ok := minDay[o.SymbolID]; !ok || day < d {
			minDay[o.SymbolID] = day
		}
	}

	maxStmt, err := s.db.PrepareContext(ctx, `SELECT MAX(CASE WHEN settle_ts > 0 THEN settle_ts ELSE ts END) FROM score_outcomes WHERE symbol_id=? AND horizon=? AND ts<? AND resolved_at IS NOT NULL`)
	if err != nil {
		return err
	}
	defer maxStmt.Close() //nolint:errcheck

	groupStmt, err := s.db.PrepareContext(ctx, `SELECT settle_day(settle_ts, ts), COUNT(*) FROM score_outcomes WHERE symbol_id=? AND horizon=? AND ts<? AND resolved_at IS NOT NULL GROUP BY 1`)
	if err != nil {
		return err
	}
	defer groupStmt.Close() //nolint:errcheck

	hStr := string(h)
	for symbol, days := range idx {
		var maxTs sql.NullInt64
		if err := maxStmt.QueryRowContext(ctx, symbol, hStr, since).Scan(&maxTs); err != nil {
			return err
		}
		if !maxTs.Valid {
			continue
		}
		if md.TradingDay(maxTs.Int64) < minDay[symbol] {
			continue
		}
		rows, err := groupStmt.QueryContext(ctx, symbol, hStr, since)
		if err != nil {
			return err
		}
		for rows.Next() {
			var day int64
			var n int
			if err := rows.Scan(&day, &n); err != nil {
				rows.Close() //nolint:errcheck
				return err
			}
			if i, ok := days[day]; ok {
				counts[i] += n
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}
