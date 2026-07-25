// Signal8 wave — Stage 1 store methods: SEC filings feed, Form 4 insider
// trades, 13F institutional holdings, and derived dilution flags. All of this
// is public-domain US government data (SEC EDGAR) — free to store and show.
// Honesty notes live with the API handlers: the data lags by law (Form 4 ~2
// business days; 13F quarterly + up to 45 days) and is labeled as such.
// All writes go through the single write connection s.w; reads use s.db.
package store

import (
	"context"
	"database/sql"
)

// FilingRow is one SEC filing in the per-symbol feed.
type FilingRow struct {
	ID       string `json:"id"` // SEC accession number
	SymbolID int64  `json:"symbolId"`
	Symbol   string `json:"symbol,omitempty"`
	Form     string `json:"form"`
	FiledTs  int64  `json:"filedTs"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Label    string `json:"label"`
}

// InsiderTradeRow is one parsed Form 4 filing (aggregated to its dominant
// transaction code; see schema comment).
type InsiderTradeRow struct {
	Accession string  `json:"accession"`
	SymbolID  int64   `json:"symbolId"`
	Symbol    string  `json:"symbol,omitempty"`
	Insider   string  `json:"insider"`
	Title     string  `json:"title"`
	Code      string  `json:"code"`
	Shares    float64 `json:"shares"`
	Price     float64 `json:"price"`
	Value     float64 `json:"value"`
	TxTs      int64   `json:"txTs"`
	FiledTs   int64   `json:"filedTs"`
}

// InstHoldingRow is one 13F information-table position.
type InstHoldingRow struct {
	CIK      string  `json:"cik"`
	Manager  string  `json:"manager"`
	Period   string  `json:"period"`
	SymbolID *int64  `json:"symbolId"` // nil = unmatched (honest)
	Symbol   string  `json:"symbol,omitempty"`
	CUSIP    string  `json:"cusip"`
	Name     string  `json:"name"`
	Value    float64 `json:"value"`
	Shares   float64 `json:"shares"`
}

// DilutionFlagRow is the derived per-symbol dilution signal.
type DilutionFlagRow struct {
	SymbolID  int64  `json:"symbolId"`
	Symbol    string `json:"symbol,omitempty"`
	Level     string `json:"level"`   // low | elevated | high
	Reasons   string `json:"reasons"` // JSON array of evidence strings
	UpdatedTs int64  `json:"updatedTs"`
}

// ── filings ───────────────────────────────────────────────────────────────

// InsertFiling records one filing; INSERT OR IGNORE on the accession PK makes
// every re-sweep idempotent. Returns true when the row is NEW (the poller uses
// this to decide which Form 4 documents to fetch and parse).
func (s *Store) InsertFiling(ctx context.Context, f FilingRow) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO filings (id, symbol_id, form, filed_ts, title, url, label)
		VALUES (?,?,?,?,?,?,?)`,
		f.ID, f.SymbolID, f.Form, f.FiledTs, f.Title, f.URL, f.Label)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Filings returns the filings feed, newest first. symbolID=0 means fleet-wide;
// form="" means all forms (a non-empty form matches the raw form type prefix,
// so "424B" catches 424B1..424B5 and "S-3" catches S-3ASR).
func (s *Store) Filings(ctx context.Context, symbolID int64, form string, limit int) ([]FilingRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT f.id, f.symbol_id, y.symbol, f.form, f.filed_ts, f.title, f.url, f.label
	      FROM filings f JOIN symbols y ON y.id = f.symbol_id WHERE 1=1`
	args := []any{}
	if symbolID != 0 {
		q += ` AND f.symbol_id = ?`
		args = append(args, symbolID)
	}
	if form != "" {
		q += ` AND f.form LIKE ? || '%'`
		args = append(args, form)
	}
	q += ` ORDER BY f.filed_ts DESC, f.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []FilingRow
	for rows.Next() {
		var r FilingRow
		if err := rows.Scan(&r.ID, &r.SymbolID, &r.Symbol, &r.Form, &r.FiledTs, &r.Title, &r.URL, &r.Label); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FilingsSince returns a symbol's filings with filed_ts >= since whose form
// matches any of the given prefixes (dilution derivation: S-1/S-3/424B).
func (s *Store) FilingsSince(ctx context.Context, symbolID int64, formPrefixes []string, since int64) ([]FilingRow, error) {
	if len(formPrefixes) == 0 {
		return nil, nil
	}
	q := `SELECT id, symbol_id, form, filed_ts, title, url, label FROM filings
	      WHERE symbol_id=? AND filed_ts>=? AND (`
	args := []any{symbolID, since}
	for i, p := range formPrefixes {
		if i > 0 {
			q += ` OR `
		}
		q += `form LIKE ? || '%'`
		args = append(args, p)
	}
	q += `) ORDER BY filed_ts DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []FilingRow
	for rows.Next() {
		var r FilingRow
		if err := rows.Scan(&r.ID, &r.SymbolID, &r.Form, &r.FiledTs, &r.Title, &r.URL, &r.Label); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UnparsedForm4s returns Form 4 filings that have no insider_trades row yet —
// the fetch/parse backlog (bounded by limit so one run never floods EDGAR).
// Sentinel rows (code=”) count as parsed here BY DESIGN: a derivative-only or
// permanently-unfetchable Form 4 leaves the backlog via its sentinel instead
// of being re-fetched from EDGAR every run forever.
func (s *Store) UnparsedForm4s(ctx context.Context, limit int) ([]FilingRow, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.symbol_id, f.form, f.filed_ts, f.title, f.url, f.label
		FROM filings f
		LEFT JOIN insider_trades t ON t.accession = f.id
		WHERE f.form = '4' AND t.accession IS NULL
		ORDER BY f.filed_ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []FilingRow
	for rows.Next() {
		var r FilingRow
		if err := rows.Scan(&r.ID, &r.SymbolID, &r.Form, &r.FiledTs, &r.Title, &r.URL, &r.Label); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── insider trades (Form 4) ───────────────────────────────────────────────

// InsertInsiderTrade records one parsed Form 4; INSERT OR IGNORE on the
// accession PK keeps re-parses idempotent.
func (s *Store) InsertInsiderTrade(ctx context.Context, t InsiderTradeRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO insider_trades
		  (accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		t.Accession, t.SymbolID, t.Insider, t.Title, t.Code, t.Shares, t.Price, t.Value, t.TxTs, t.FiledTs)
	return err
}

// InsiderTrades returns parsed insider transactions newest-first. symbolID=0
// means fleet-wide recent; code="" means all transaction codes. Sentinel rows
// (code=” — parsed-empty markers for derivative-only/abandoned Form 4s, see
// the filings-poller) are ALWAYS excluded: they exist only to take an
// accession out of the UnparsedForm4s backlog, never to be shown.
func (s *Store) InsiderTrades(ctx context.Context, symbolID int64, code string, limit int) ([]InsiderTradeRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT t.accession, t.symbol_id, y.symbol, t.insider, t.title, t.code,
	             t.shares, t.price, t.value, t.tx_ts, t.filed_ts
	      FROM insider_trades t JOIN symbols y ON y.id = t.symbol_id
	      WHERE t.code != ''`
	args := []any{}
	if symbolID != 0 {
		q += ` AND t.symbol_id = ?`
		args = append(args, symbolID)
	}
	if code != "" {
		q += ` AND t.code = ?`
		args = append(args, code)
	}
	q += ` ORDER BY t.filed_ts DESC, t.accession DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []InsiderTradeRow
	for rows.Next() {
		var r InsiderTradeRow
		if err := rows.Scan(&r.Accession, &r.SymbolID, &r.Symbol, &r.Insider, &r.Title, &r.Code,
			&r.Shares, &r.Price, &r.Value, &r.TxTs, &r.FiledTs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── institutional holdings (13F) ──────────────────────────────────────────

// UpsertInstHolding writes one 13F position. ON CONFLICT replace keeps a
// re-parse of the same (cik, period, cusip) idempotent while letting a
// later, better symbol match improve symbol_id.
func (s *Store) UpsertInstHolding(ctx context.Context, h InstHoldingRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO inst_holdings (cik, manager, period, symbol_id, cusip, name, value, shares)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(cik, period, cusip) DO UPDATE SET
		  manager = excluded.manager,
		  symbol_id = excluded.symbol_id,
		  name = excluded.name,
		  value = excluded.value,
		  shares = excluded.shares`,
		h.CIK, h.Manager, h.Period, h.SymbolID, h.CUSIP, h.Name, h.Value, h.Shares)
	return err
}

// HasInstPeriod reports whether a manager's holdings for a report period are
// already stored (the 13F poller's skip-if-done gate).
func (s *Store) HasInstPeriod(ctx context.Context, cik, period string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM inst_holdings WHERE cik=? AND period=?`, cik, period).Scan(&n)
	return n > 0, err
}

// InstHoldingsBySymbol returns, for one symbol, each manager's LATEST-period
// position (who holds it now, per our stored data), largest value first.
func (s *Store) InstHoldingsBySymbol(ctx context.Context, symbolID int64, limit int) ([]InstHoldingRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT h.cik, h.manager, h.period, h.symbol_id, h.cusip, h.name, h.value, h.shares
		FROM inst_holdings h
		JOIN (SELECT cik, MAX(period) AS mx FROM inst_holdings GROUP BY cik) t
		  ON t.cik = h.cik AND t.mx = h.period
		WHERE h.symbol_id = ?
		ORDER BY h.value DESC LIMIT ?`, symbolID, limit)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

// InstHoldingsByManager returns one manager's latest-period holdings, largest
// first. cik matches exactly; manager display names are resolved by the API.
func (s *Store) InstHoldingsByManager(ctx context.Context, cik string, limit int) ([]InstHoldingRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT h.cik, h.manager, h.period, h.symbol_id, h.cusip, h.name, h.value, h.shares
		FROM inst_holdings h
		WHERE h.cik = ? AND h.period = (SELECT MAX(period) FROM inst_holdings WHERE cik = ?)
		ORDER BY h.value DESC LIMIT ?`, cik, cik, limit)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

// InstManagers returns each stored manager with its latest period and position
// count — the /api/institutions overview.
func (s *Store) InstManagers(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT h.cik, h.manager, h.period, COUNT(*), SUM(h.value)
		FROM inst_holdings h
		JOIN (SELECT cik, MAX(period) AS mx FROM inst_holdings GROUP BY cik) t
		  ON t.cik = h.cik AND t.mx = h.period
		GROUP BY h.cik ORDER BY SUM(h.value) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []map[string]any
	for rows.Next() {
		var cik, manager, period string
		var n int64
		var total float64
		if err := rows.Scan(&cik, &manager, &period, &n, &total); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"cik": cik, "manager": manager, "period": period,
			"positions": n, "totalValue": total,
		})
	}
	return out, rows.Err()
}

func scanInstRows(rows *sql.Rows) ([]InstHoldingRow, error) {
	defer rows.Close() //nolint:errcheck
	var out []InstHoldingRow
	for rows.Next() {
		var r InstHoldingRow
		var sid sql.NullInt64
		if err := rows.Scan(&r.CIK, &r.Manager, &r.Period, &sid, &r.CUSIP, &r.Name, &r.Value, &r.Shares); err != nil {
			return nil, err
		}
		if sid.Valid {
			v := sid.Int64
			r.SymbolID = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── dilution flags ────────────────────────────────────────────────────────

// UpsertDilutionFlag writes (or refreshes) one symbol's derived dilution flag.
func (s *Store) UpsertDilutionFlag(ctx context.Context, f DilutionFlagRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO dilution_flags (symbol_id, level, reasons, updated_ts)
		VALUES (?,?,?,?)
		ON CONFLICT(symbol_id) DO UPDATE SET
		  level = excluded.level,
		  reasons = excluded.reasons,
		  updated_ts = excluded.updated_ts`,
		f.SymbolID, f.Level, f.Reasons, f.UpdatedTs)
	return err
}

// DilutionFlag returns one symbol's flag (ok=false when never derived).
func (s *Store) DilutionFlag(ctx context.Context, symbolID int64) (DilutionFlagRow, bool, error) {
	var r DilutionFlagRow
	r.SymbolID = symbolID
	err := s.db.QueryRowContext(ctx, `
		SELECT level, reasons, updated_ts FROM dilution_flags WHERE symbol_id=?`,
		symbolID).Scan(&r.Level, &r.Reasons, &r.UpdatedTs)
	if err == sql.ErrNoRows {
		return DilutionFlagRow{}, false, nil
	}
	if err != nil {
		return DilutionFlagRow{}, false, err
	}
	return r, true, nil
}

// DilutionFlagged returns every symbol whose flag is elevated or high (the
// fleet-wide dilution watch), highest level first, then most recent.
func (s *Store) DilutionFlagged(ctx context.Context, limit int) ([]DilutionFlagRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.symbol_id, y.symbol, d.level, d.reasons, d.updated_ts
		FROM dilution_flags d JOIN symbols y ON y.id = d.symbol_id
		WHERE d.level IN ('elevated','high')
		ORDER BY CASE d.level WHEN 'high' THEN 0 ELSE 1 END, d.updated_ts DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []DilutionFlagRow
	for rows.Next() {
		var r DilutionFlagRow
		if err := rows.Scan(&r.SymbolID, &r.Symbol, &r.Level, &r.Reasons, &r.UpdatedTs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
