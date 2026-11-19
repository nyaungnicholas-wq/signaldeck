package store

import "context"

// EarningsEvent is one 8-K (or 8-K/A) carrying Item 2.02, as listed in a
// company's EDGAR submissions feed.
type EarningsEvent struct {
	Accession string
	Form      string // "8-K" or "8-K/A"
	FiledTs   int64  // EDGAR acceptance time, unix seconds UTC
	Items     string // raw item list, e.g. "2.02,9.01"
}

// RecordEarningsEvents keeps every Item 2.02 filing the sweep sees, at any age.
// Rows are never updated or deleted here: the first value seen is kept, so the
// table records what was known and when (first_seen_ts). Returns rows inserted.
// Known accessions are filtered on the read handle first, so a sweep that
// finds nothing new never takes the write lock.
func (s *Store) RecordEarningsEvents(ctx context.Context, symbolID int64, evs []EarningsEvent, nowTs int64) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT accession FROM earnings_events WHERE symbol_id=?`, symbolID)
	if err != nil {
		return 0, err
	}
	known := make(map[string]bool)
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close() //nolint:errcheck
			return 0, err
		}
		known[a] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var fresh []EarningsEvent
	for _, e := range evs {
		if !known[e.Accession] {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	n := 0
	for _, e := range fresh {
		// OR IGNORE still guards a row another symbol (or a racing sweep) wrote.
		res, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO earnings_events (accession, symbol_id, form, filed_ts, items, first_seen_ts)
			VALUES (?,?,?,?,?,?)`, e.Accession, symbolID, e.Form, e.FiledTs, e.Items, nowTs)
		if err != nil {
			return 0, err
		}
		k, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		n += int(k)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
