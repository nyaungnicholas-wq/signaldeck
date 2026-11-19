package store

import (
	"context"
)

// CorrectFilingTs repairs filed_ts values that were stored as US Eastern
// wall-clock time instead of UTC. Only exact +4h (14400s) or +5h (18000s)
// differences are corrected; the original values are preserved in
// filing_ts_corrections for auditability.
func (s *Store) CorrectFilingTs(ctx context.Context, symbolID int64, feed map[string]int64, nowTs int64) (int, error) {
	if len(feed) == 0 {
		return 0, nil
	}

	type row struct {
		accession string
		storedTs  int64
		tbl       string
	}

	var rows []row

	// Read filings
	filingRows, err := s.db.QueryContext(ctx, `SELECT id, filed_ts FROM filings WHERE symbol_id=?`, symbolID)
	if err != nil {
		return 0, err
	}
	for filingRows.Next() {
		var r row
		if err := filingRows.Scan(&r.accession, &r.storedTs); err != nil {
			filingRows.Close() //nolint:errcheck
			return 0, err
		}
		r.tbl = "filings"
		rows = append(rows, r)
	}
	if err := filingRows.Err(); err != nil {
		filingRows.Close() //nolint:errcheck
		return 0, err
	}
	if err := filingRows.Close(); err != nil {
		return 0, err
	}

	// Read insider_trades
	tradeRows, err := s.db.QueryContext(ctx, `SELECT accession, filed_ts FROM insider_trades WHERE symbol_id=?`, symbolID)
	if err != nil {
		return 0, err
	}
	for tradeRows.Next() {
		var r row
		if err := tradeRows.Scan(&r.accession, &r.storedTs); err != nil {
			tradeRows.Close() //nolint:errcheck
			return 0, err
		}
		r.tbl = "insider_trades"
		rows = append(rows, r)
	}
	if err := tradeRows.Err(); err != nil {
		tradeRows.Close() //nolint:errcheck
		return 0, err
	}
	if err := tradeRows.Close(); err != nil {
		return 0, err
	}

	// Find correctable rows
	type correction struct {
		accession string
		tbl       string
		oldTs     int64
		newTs     int64
	}
	var corrections []correction

	for _, r := range rows {
		feedTs, ok := feed[r.accession]
		if !ok {
			continue
		}
		if feedTs%86400 == 0 {
			continue
		}
		diff := feedTs - r.storedTs
		if diff != 14400 && diff != 18000 {
			continue
		}
		corrections = append(corrections, correction{
			accession: r.accession,
			tbl:       r.tbl,
			oldTs:     r.storedTs,
			newTs:     feedTs,
		})
	}

	if len(corrections) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Update first and archive only what the update changed: a row that
	// retention deleted since the read must not leave a correction on record.
	done := make(map[string]bool)
	for _, c := range corrections {
		q := `UPDATE insider_trades SET filed_ts=? WHERE accession=? AND filed_ts=?`
		if c.tbl == "filings" {
			q = `UPDATE filings SET filed_ts=? WHERE id=? AND filed_ts=?`
		}
		res, err := tx.ExecContext(ctx, q, c.newTs, c.accession, c.oldTs)
		if err != nil {
			return 0, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return 0, err
		} else if n == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO filing_ts_corrections (accession, tbl, old_filed_ts, new_filed_ts, corrected_ts)
			VALUES (?,?,?,?,?)`, c.accession, c.tbl, c.oldTs, c.newTs, nowTs); err != nil {
			return 0, err
		}
		done[c.accession] = true
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return len(done), nil
}
