package store

import (
	"context"
	"database/sql"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

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
	rows, err := s.db.QueryContext(ctx, `
        SELECT symbol_id, horizon, ts, score, fwd_return, resolved_at, settle_ts, grp_n FROM (
            SELECT symbol_id, horizon, ts, score, fwd_return, resolved_at, settle_ts,
                   COUNT(*) OVER (PARTITION BY symbol_id, settle_day(settle_ts, ts)) AS grp_n,
                   ROW_NUMBER() OVER (PARTITION BY symbol_id, settle_day(settle_ts, ts) ORDER BY ts DESC) AS rn
            FROM score_outcomes WHERE resolved_at IS NOT NULL AND horizon=?
        ) WHERE rn=1 ORDER BY ts DESC LIMIT ?`, string(h), limit)
	if err != nil {
		return nil, 0, err
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
			return nil, 0, err
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
		rawRows += grpN
	}
	return out, rawRows, rows.Err()
}
