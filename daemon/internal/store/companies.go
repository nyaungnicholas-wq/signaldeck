// Signal8 wave — Stage 5 store methods: the COMPANIES DIRECTORY. The
// companies table mirrors the free EDGAR company_tickers_exchange.json map
// (~10.4k SEC registrants), synced daily by the companies-sync worker and
// SIC-enriched opportunistically by the filings-poller (no added requests).
// Everything here is a single SQL statement (batched — never a query per
// company): the directory API joins the whole table to the tracked-data maps
// (LatestDailyAll + LatestMetricAll) built once per request.
// Writes go through the single write connection s.w; reads use s.db.
package store

import (
	"context"
	"strings"
)

// CompanyRow is one SEC-registered company (one ticker) in the directory.
// SIC/SICDesc are '' until the filings-poller has seen the company's
// submissions JSON (honest absence, never a guessed sector).
type CompanyRow struct {
	CIK       int64  `json:"cik"`
	Ticker    string `json:"ticker"`
	Name      string `json:"name"`
	Exchange  string `json:"exchange"` // may be '' — SEC lists some with null
	SIC       string `json:"sic"`
	SICDesc   string `json:"sicDesc"`
	UpdatedTs int64  `json:"updatedTs"`
}

// UpsertCompanies writes the directory in ONE transaction (the sync worker
// hands it the whole ~10.4k-row map). ON CONFLICT(ticker) refreshes
// cik/name/exchange/updated_ts while PRESERVING sic/sic_desc when the incoming
// row has none — the SEC exchange file never carries SIC, so a daily sync must
// not wipe the filings-poller's enrichment. Blank incoming name/exchange also
// preserve the stored value (the poller's minimal fallback rows carry no
// exchange).
func (s *Store) UpsertCompanies(ctx context.Context, rows []CompanyRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO companies (cik, ticker, name, exchange, sic, sic_desc, updated_ts)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(ticker) DO UPDATE SET
		  cik        = excluded.cik,
		  name       = CASE WHEN excluded.name     = '' THEN companies.name     ELSE excluded.name     END,
		  exchange   = CASE WHEN excluded.exchange = '' THEN companies.exchange ELSE excluded.exchange END,
		  sic        = CASE WHEN excluded.sic      = '' THEN companies.sic      ELSE excluded.sic      END,
		  sic_desc   = CASE WHEN excluded.sic_desc = '' THEN companies.sic_desc ELSE excluded.sic_desc END,
		  updated_ts = excluded.updated_ts`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck
	for _, r := range rows {
		ticker := strings.ToUpper(strings.TrimSpace(r.Ticker))
		if ticker == "" || r.CIK == 0 {
			continue // an unkeyable row is dropped, never stored half-blank
		}
		if _, err := stmt.ExecContext(ctx, r.CIK, ticker, r.Name, r.Exchange,
			r.SIC, r.SICDesc, r.UpdatedTs); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateCompanySICByCIK sets the SIC industry code+description on EVERY
// directory row sharing a CIK (share classes list under one registrant).
// Returns rows affected — 0 means the directory has no row for that CIK yet
// (the caller may insert a minimal fallback row so enrichment isn't lost).
func (s *Store) UpdateCompanySICByCIK(ctx context.Context, cik int64, sic, sicDesc string, ts int64) (int64, error) {
	res, err := s.w.ExecContext(ctx, `
		UPDATE companies SET sic=?, sic_desc=?, updated_ts=? WHERE cik=?`,
		sic, sicDesc, ts, cik)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListCompanies returns directory rows matching the SQL-pushable filters, in
// stable ticker order. q matches a ticker PREFIX or a name SUBSTRING
// (case-insensitive); sector matches sic_desc exactly; exchange matches
// exactly. The result is bounded by the directory itself (~10.4k rows) — the
// API layer joins tracked data, applies the mcap/tracked filters (which need
// bar+fundamentals context), and paginates.
func (s *Store) ListCompanies(ctx context.Context, q, sector, exchange string) ([]CompanyRow, error) {
	sql := `SELECT cik, ticker, name, exchange, sic, sic_desc, updated_ts
	        FROM companies WHERE 1=1`
	args := []any{}
	if q = strings.TrimSpace(q); q != "" {
		sql += ` AND (ticker LIKE ? || '%' OR name LIKE '%' || ? || '%')`
		args = append(args, strings.ToUpper(q), q)
	}
	if sector = strings.TrimSpace(sector); sector != "" {
		sql += ` AND sic_desc = ?`
		args = append(args, sector)
	}
	if exchange = strings.TrimSpace(exchange); exchange != "" {
		sql += ` AND exchange = ?`
		args = append(args, exchange)
	}
	sql += ` ORDER BY ticker`
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []CompanyRow
	for rows.Next() {
		var r CompanyRow
		if err := rows.Scan(&r.CIK, &r.Ticker, &r.Name, &r.Exchange, &r.SIC, &r.SICDesc, &r.UpdatedTs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CompanyCount returns how many directory rows are stored (0 = the
// companies-sync worker hasn't completed a run yet — the API says so).
func (s *Store) CompanyCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM companies`).Scan(&n)
	return n, err
}

// CompanyFacet is one filter option with its row count.
type CompanyFacet struct {
	Value string `json:"value"`
	N     int    `json:"n"`
}

// CompanySectors returns the distinct SIC industry descriptions present (the
// sector-filter options), most common first, capped. '' rows (not yet
// enriched) are excluded — they are absence, not a sector.
func (s *Store) CompanySectors(ctx context.Context, limit int) ([]CompanyFacet, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sic_desc, COUNT(*) FROM companies WHERE sic_desc != ''
		GROUP BY sic_desc ORDER BY COUNT(*) DESC, sic_desc LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanFacets(rows)
}

// CompanyExchanges returns the distinct exchanges present (filter options),
// most common first. '' (SEC null) is excluded from the options; rows keep it
// and render "—".
func (s *Store) CompanyExchanges(ctx context.Context) ([]CompanyFacet, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT exchange, COUNT(*) FROM companies WHERE exchange != ''
		GROUP BY exchange ORDER BY COUNT(*) DESC, exchange`)
	if err != nil {
		return nil, err
	}
	return scanFacets(rows)
}

func scanFacets(rows interface {
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}) ([]CompanyFacet, error) {
	defer rows.Close() //nolint:errcheck
	var out []CompanyFacet
	for rows.Next() {
		var f CompanyFacet
		if err := rows.Scan(&f.Value, &f.N); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DailyLast is one symbol's latest daily bar essentials for the directory
// join: last close (+ its ts + volume) and the previous close (0 when only
// one daily bar exists).
type DailyLast struct {
	Last   float64
	Prev   float64
	Volume float64
	Ts     int64
}

// LatestDailyAll returns, for EVERY symbol with daily bars, its latest daily
// close/volume/ts and the previous close — in ONE window-function query (the
// LastTwoDailyCloses pattern plus volume, so the directory endpoint never
// issues a per-symbol bar query over 10k companies).
func (s *Store) LatestDailyAll(ctx context.Context) (map[int64]DailyLast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, close, COALESCE(prev_close, 0), volume FROM (
			SELECT symbol_id, ts, close, volume,
			       LAG(close) OVER (PARTITION BY symbol_id ORDER BY ts) AS prev_close,
			       ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts DESC) AS rn
			FROM bars WHERE tf='1d'
		) WHERE rn = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]DailyLast{}
	for rows.Next() {
		var id int64
		var d DailyLast
		if err := rows.Scan(&id, &d.Ts, &d.Last, &d.Prev, &d.Volume); err != nil {
			return nil, err
		}
		out[id] = d
	}
	return out, rows.Err()
}

// PeriodicFiling is one symbol's most recent 10-Q/10-K (the earnings-estimate
// anchor: last periodic filing date + ~1 quarter).
type PeriodicFiling struct {
	Form    string
	FiledTs int64
}

// LatestPeriodicFilingAll returns, for EVERY symbol with a stored 10-Q or
// 10-K, its most recent one — in ONE query. Amendments (10-Q/A, 10-K/A) are
// deliberately EXCLUDED: an amendment re-files old paper months later and
// would corrupt the filing-cadence estimate.
func (s *Store) LatestPeriodicFilingAll(ctx context.Context) (map[int64]PeriodicFiling, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, f.form, f.filed_ts
		FROM filings f
		JOIN (SELECT symbol_id, MAX(filed_ts) AS mx FROM filings
		      WHERE form IN ('10-Q','10-K') GROUP BY symbol_id) t
		  ON t.symbol_id = f.symbol_id AND t.mx = f.filed_ts
		WHERE f.form IN ('10-Q','10-K')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]PeriodicFiling{}
	for rows.Next() {
		var id int64
		var p PeriodicFiling
		if err := rows.Scan(&id, &p.Form, &p.FiledTs); err != nil {
			return nil, err
		}
		// A 10-Q and 10-K filed at the identical instant both satisfy the join;
		// first row wins deterministically enough (same ts either way).
		if _, dup := out[id]; !dup {
			out[id] = p
		}
	}
	return out, rows.Err()
}
