package store

import (
	"context"
	"fmt"
)

// NON-POSITIVE PRICES (2026-08-04)
//
// A bar whose close is zero is not a trade at zero — it is an ABSENT price the
// vendor returned anyway, usually with real volume attached. Alpaca does this
// for some pre-merger SPAC shells: LeddarTech (LDTC) carries 373 such bars from
// 2021-03 to 2023-12, each with genuine volume and OHLC all 0.0, and its real
// series only begins at 6.08 on 2023-12-22.
//
// They are not merely useless. Any return is close[k]/close[k-1] - 1, so a zero
// previous close is a division by zero — `tools/xsfactor_edge.py` died on
// exactly that immediately after these bars were imported. A model that instead
// guarded the division would read a -100% return followed by a +infinite one.
//
// So they are deleted rather than tolerated, and `added_at` is repaired
// afterwards: it is meant to be the symbol's first REAL bar, and a symbol whose
// leading bars were all priceless has been carrying a first-seen date that
// never had a price.

// PurgeNonPositiveBars deletes bars with a non-positive close (optionally for
// one symbol; symbolID <= 0 means every symbol), then re-dates added_at to each
// affected symbol's earliest surviving daily bar.
//
// Returns the bars deleted and the symbols re-dated. Idempotent: a second run
// deletes nothing and re-dates nothing.
// ONE SHORT TRANSACTION PER SYMBOL, not one long one over the whole table.
// `bars` is WITHOUT ROWID keyed (symbol_id, tf, ts) and holds 14.4M rows, so a
// single `DELETE ... WHERE close <= 0` is a full scan inside a write
// transaction. Against a live daemon that lost the writer every time: three
// consecutive runs died on `database is locked (517)` within three seconds.
// Scoping each delete to one symbol_id uses the primary key's leading column,
// so the write is short enough to fit between the daemon's own writes.
func (s *Store) PurgeNonPositiveBars(ctx context.Context, symbolID int64) (deleted, redated int64, err error) {
	// Discovery runs on the READ pool, outside any write transaction — it is
	// the expensive half and it does not need the writer.
	q, args := `SELECT DISTINCT symbol_id FROM bars WHERE close <= 0`, []any{}
	if symbolID > 0 {
		q, args = q+` AND symbol_id = ?`, []any{symbolID}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, 0, fmt.Errorf("find affected symbols: %w", err)
	}
	var affected []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close() //nolint:errcheck
			return 0, 0, err
		}
		affected = append(affected, id)
	}
	rows.Close() //nolint:errcheck
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	for _, id := range affected {
		tx, err := s.w.BeginTx(ctx, nil)
		if err != nil {
			return deleted, redated, fmt.Errorf("begin for symbol %d: %w", id, err)
		}
		r, err := tx.ExecContext(ctx,
			`DELETE FROM bars WHERE symbol_id = ? AND close <= 0`, id)
		if err != nil {
			tx.Rollback() //nolint:errcheck
			return deleted, redated, fmt.Errorf("delete non-positive bars for symbol %d: %w", id, err)
		}
		n, _ := r.RowsAffected()

		// COALESCE so a symbol left with no daily bars at all keeps its current
		// added_at rather than being re-dated to the epoch, which would put it
		// in every historical universe.
		res, err := tx.ExecContext(ctx, `
			UPDATE symbols SET added_at = (
			  SELECT COALESCE(MIN(ts), symbols.added_at) FROM bars
			  WHERE symbol_id = symbols.id AND tf = '1d')
			WHERE id = ? AND added_at <> (
			  SELECT COALESCE(MIN(ts), symbols.added_at) FROM bars
			  WHERE symbol_id = symbols.id AND tf = '1d')`, id)
		if err != nil {
			tx.Rollback() //nolint:errcheck
			return deleted, redated, fmt.Errorf("re-date symbol %d: %w", id, err)
		}
		m, _ := res.RowsAffected()
		if err := tx.Commit(); err != nil {
			return deleted, redated, fmt.Errorf("commit symbol %d: %w", id, err)
		}
		deleted += n
		redated += m
	}
	return deleted, redated, nil
}
