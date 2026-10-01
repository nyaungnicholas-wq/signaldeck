package store

import (
	"context"
	"strings"
)

// readThenWrite runs a heavy SELECT on the READ pool and writes its rows on
// the writer in short multi-row INSERTs, so the SQLite write lock is held for
// the inserts only, never for the scan.
//
// Why: a single INSERT ... SELECT holds the write lock for its whole read.
// Measured live 2026-09-30, three of them (ReconcileNewsSymbols 12.6s,
// UpsertSentimentDaily 11.3s, Rollup 5-7s, all full scans) kept the lock long
// enough that every sign-in hit its 12s busy_timeout and failed.
//
// insert is everything before VALUES ("INSERT OR IGNORE INTO t (a, b)"); tail
// is anything after the rows (an ON CONFLICT clause) or "". Batches are not
// one transaction: callers must be idempotent recomputes, which all of them
// are, so a failure part-way is repaired by the next pass. Returns the summed
// RowsAffected.
func (s *Store) readThenWrite(ctx context.Context, query string, args []any, ncols int, insert, tail string) (int64, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	var vals []any
	for rows.Next() {
		row := make([]any, ncols)
		ptrs := make([]any, ncols)
		for i := range row {
			ptrs[i] = &row[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			rows.Close() //nolint:errcheck
			return 0, err
		}
		vals = append(vals, row...)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck
		return 0, err
	}
	rows.Close() //nolint:errcheck

	const batchRows = 200
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", ncols), ",") + ")"
	var total int64
	for start := 0; start < len(vals); start += batchRows * ncols {
		end := min(start+batchRows*ncols, len(vals))
		n := (end - start) / ncols
		stmt := insert + " VALUES " + strings.TrimSuffix(strings.Repeat(tuple+",", n), ",") + " " + tail
		res, err := s.w.ExecContext(ctx, stmt, vals[start:end]...)
		if err != nil {
			return total, err
		}
		k, _ := res.RowsAffected()
		total += k
	}
	return total, nil
}

// deleteInBatches runs del, a DELETE whose rows are picked by a
// "key IN (SELECT key ... LIMIT ?)" subquery (args fill every placeholder
// before that LIMIT), as repeated short autocommit statements until one
// deletes fewer than a full batch. The SQLite write lock is free between
// batches, which is where the priority gate lets account writes in.
//
// Why: each retention prune was ONE statement however many rows it matched.
// Logged live 2026-09-30: DeleteFilingsBefore held the writer 18.6s on one
// pass and 10.0s on the next (39,796 filings in one statement); sign-in's
// account writes give up after 12s.
//
// Batches commit separately. Every caller archives before it prunes, so a
// failure part-way leaves archived rows in place for the next pass to archive
// and prune again: duplicated in the cold archive, never lost.
func (s *Store) deleteInBatches(ctx context.Context, del string, args ...any) (int64, error) {
	const batchRows = 1000
	var total int64
	for {
		res, err := s.w.ExecContext(ctx, del, append(args, batchRows)...)
		if err != nil {
			return total, err
		}
		k, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += k
		if k < batchRows {
			return total, nil
		}
	}
}
