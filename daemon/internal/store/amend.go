package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type form4Row struct {
	accession    string
	insider      string
	code         string
	amendsDay    string
	supersededBy string // computed this call
	stored       string // superseded_by as read
	filedTs      int64
}

// migrateAmendments adds amends_day and superseded_by columns to insider_trades if missing,
// and creates an index on superseded_by.
func migrateAmendments(w *sql.DB) error {
	for _, name := range []string{"amends_day", "superseded_by"} {
		var n int
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('insider_trades') WHERE name=?`, name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec("ALTER TABLE insider_trades ADD COLUMN " + name + " TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
	}
	if _, err := w.Exec(`CREATE INDEX IF NOT EXISTS idx_insider_superseded ON insider_trades (superseded_by)`); err != nil {
		return err
	}
	return nil
}

// SupersedeAmendedForm4s marks restated Form 4 rows as superseded by their amendments.
// It processes amendments in filed_ts order, matching each to exactly one original
// by same insider (case-insensitive, trimmed) within a [-1 day, +3 day] window around
// the amendment's amends_day. Returns count of superseded rows and accessions of
// ambiguous amendments (multiple candidates).
func (s *Store) SupersedeAmendedForm4s(ctx context.Context, symbolID int64) (superseded int, ambiguous []string, err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.QueryContext(ctx, `SELECT accession, insider, code, filed_ts, amends_day, superseded_by
		FROM insider_trades WHERE symbol_id=? ORDER BY filed_ts, accession`, symbolID)
	if err != nil {
		return 0, nil, err
	}
	var all []form4Row
	for rows.Next() {
		var r form4Row
		if err := rows.Scan(&r.accession, &r.insider, &r.code, &r.filedTs, &r.amendsDay, &r.stored); err != nil {
			rows.Close() //nolint:errcheck,gosec
			return 0, nil, err
		}
		all = append(all, r)
	}
	rows.Close() //nolint:errcheck,gosec
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}

	// The links are recomputed from scratch, oldest amendment first, every
	// call: the result is then the same whatever order the filings arrived in
	// (an original, then its second 4/A, then its first must still end with
	// only the second counting).
	byAcc := make(map[string]*form4Row, len(all))
	for i := range all {
		byAcc[all[i].accession] = &all[i]
	}
	head := func(r *form4Row) *form4Row { // O -> A1 -> A2 resolves to A2
		for i := 0; i < len(all) && r.supersededBy != ""; i++ {
			next, ok := byAcc[r.supersededBy]
			if !ok {
				break
			}
			r = next
		}
		return r
	}
	for i := range all {
		a := &all[i]
		if a.amendsDay == "" || strings.TrimSpace(a.insider) == "" {
			continue
		}
		day, perr := time.Parse("2006-01-02", strings.TrimSpace(a.amendsDay))
		if perr != nil {
			continue
		}
		day0 := day.Unix()
		heads := map[string]*form4Row{}
		for k := range all {
			o := &all[k]
			// Same insider, filed on the date the 4/A names (acceptance-time
			// slack), before it. Parsed-empty rows (code '') hold no trade to
			// replace and would only make a real match look ambiguous.
			if o.accession == a.accession || o.filedTs >= a.filedTs || o.code == "" ||
				!strings.EqualFold(strings.TrimSpace(o.insider), strings.TrimSpace(a.insider)) ||
				o.filedTs < day0-86400 || o.filedTs >= day0+3*86400 {
				continue
			}
			if h := head(o); h.accession != a.accession && h.filedTs < a.filedTs {
				heads[h.accession] = h
			}
		}
		switch len(heads) {
		case 0:
		case 1:
			for _, h := range heads {
				h.supersededBy = a.accession
			}
		default:
			ambiguous = append(ambiguous, a.accession)
		}
	}
	for i := range all {
		r := &all[i]
		if r.supersededBy == r.stored {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE insider_trades SET superseded_by=? WHERE accession=?`,
			r.supersededBy, r.accession); err != nil {
			return 0, nil, err
		}
		if r.supersededBy != "" {
			superseded++
		}
	}
	return superseded, ambiguous, tx.Commit()
}

// ReplaceInstPeriod replaces all holdings for a CIK/period with the provided rows.
// Uses the cik and period arguments (not row fields) for the delete and insert.
// On conflict (cik, period, cusip), updates manager, symbol_id, name, and adds
// value and shares to existing.
func (s *Store) ReplaceInstPeriod(ctx context.Context, cik, period string, rows []InstHoldingRow) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `DELETE FROM inst_holdings WHERE cik=? AND period=?`, cik, period); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO inst_holdings (cik, manager, period, symbol_id, cusip, name, value, shares)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cik, period, cusip) DO UPDATE SET
			manager=excluded.manager,
			symbol_id=excluded.symbol_id,
			name=excluded.name,
			value=inst_holdings.value+excluded.value,
			shares=inst_holdings.shares+excluded.shares
	`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck

	for _, r := range rows {
		var symbolID *int64
		if r.SymbolID != nil {
			symbolID = r.SymbolID
		}
		if _, err := stmt.ExecContext(ctx, cik, r.Manager, period, symbolID, r.CUSIP, r.Name, r.Value, r.Shares); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ReplaceFundamentalSeries replaces all fundamental rows for a symbol_id/metric
// with the provided rows. Uses the symbolID and metric arguments (not row fields).
// On conflict (symbol_id, metric, as_of), updates value and fetched_at.
func (s *Store) ReplaceFundamentalSeries(ctx context.Context, symbolID int64, metric string, rows []FundamentalRow) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `DELETE FROM fundamentals WHERE symbol_id=? AND metric=?`, symbolID, metric); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO fundamentals (symbol_id, metric, value, as_of, fetched_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(symbol_id, metric, as_of) DO UPDATE SET
			value=excluded.value,
			fetched_at=excluded.fetched_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck

	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, symbolID, metric, r.Value, r.AsOf, r.FetchedAt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// InstPeriods lists the report periods stored for one 13F manager, ascending.
func (s *Store) InstPeriods(ctx context.Context, cik string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT period FROM inst_holdings WHERE cik=? ORDER BY period`, cik)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
