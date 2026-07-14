// DATA-EXPANSION WAVE store methods: seven free external context datasets —
// FINRA bi-monthly short interest, Hyperliquid crypto perp funding/OI, CFTC
// COT weekly positioning, StockTwits page-snapshot sentiment, Wikipedia
// attention (resolution cache + daily views), and CBOE daily put/call ratios.
// All writes go through the single write connection s.w; reads use s.db.
//
// HONESTY: everything in this file is DESCRIPTIVE context served with
// explicit caveats — nothing here is a scored factor in this wave.
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ── FINRA bi-monthly short interest ───────────────────────────────────────

// ShortInterestRow is one symbol's bi-monthly short interest for one
// settlement date. Symbol is populated only by queries that join symbols.
type ShortInterestRow struct {
	SymbolID    int64   `json:"symbolId"`
	Symbol      string  `json:"symbol,omitempty"`
	Settlement  string  `json:"settlementDate"` // YYYY-MM-DD (15th or EOM)
	ShortQty    float64 `json:"shortQty"`
	PrevQty     float64 `json:"prevQty"`
	ADV         float64 `json:"adv"`
	DaysToCover float64 `json:"daysToCover"`
	ChangePct   float64 `json:"changePct"`
}

// UpsertShortInterest writes a settlement date's tracked rows in ONE
// transaction. INSERT OR REPLACE on (symbol_id, settlement_date) makes
// re-ingesting a file (FINRA re-posts revisions) idempotent — newest wins.
func (s *Store) UpsertShortInterest(ctx context.Context, rows []ShortInterestRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO short_interest
		  (symbol_id, settlement_date, short_qty, prev_qty, adv, days_to_cover, change_pct)
		VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.SymbolID, r.Settlement,
			r.ShortQty, r.PrevQty, r.ADV, r.DaysToCover, r.ChangePct); err != nil {
			return fmt.Errorf("upsert short_interest %d/%s: %w", r.SymbolID, r.Settlement, err)
		}
	}
	return tx.Commit()
}

// ShortInterestRecent returns one symbol's newest `n` settlement rows,
// newest first (the API serves latest + previous from the first two).
func (s *Store) ShortInterestRecent(ctx context.Context, symbolID int64, n int) ([]ShortInterestRow, error) {
	if n <= 0 || n > 48 {
		n = 2
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, settlement_date, short_qty, prev_qty, adv, days_to_cover, change_pct
		FROM short_interest WHERE symbol_id = ?
		ORDER BY settlement_date DESC LIMIT ?`, symbolID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ShortInterestRow
	for rows.Next() {
		var r ShortInterestRow
		if err := rows.Scan(&r.SymbolID, &r.Settlement, &r.ShortQty, &r.PrevQty,
			&r.ADV, &r.DaysToCover, &r.ChangePct); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Hyperliquid crypto perp funding + open interest ───────────────────────

// CryptoPerpRow is one crypto symbol's perp snapshot at one time.
type CryptoPerpRow struct {
	SymbolID     int64   `json:"symbolId"`
	Ts           int64   `json:"ts"`
	Funding      float64 `json:"funding"`
	OpenInterest float64 `json:"openInterest"`
	MarkPx       float64 `json:"markPx"`
}

// InsertCryptoPerp writes one snapshot row (INSERT OR REPLACE — a re-run in
// the same second overwrites rather than erroring).
func (s *Store) InsertCryptoPerp(ctx context.Context, r CryptoPerpRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO crypto_perp (symbol_id, ts, funding, open_interest, mark_px)
		VALUES (?,?,?,?,?)`, r.SymbolID, r.Ts, r.Funding, r.OpenInterest, r.MarkPx)
	return err
}

// CryptoPerpSeries returns one symbol's snapshots with ts ≥ since, ascending
// (chart-ready).
func (s *Store) CryptoPerpSeries(ctx context.Context, symbolID, since int64) ([]CryptoPerpRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, funding, open_interest, mark_px
		FROM crypto_perp WHERE symbol_id = ? AND ts >= ?
		ORDER BY ts ASC`, symbolID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []CryptoPerpRow
	for rows.Next() {
		var r CryptoPerpRow
		if err := rows.Scan(&r.SymbolID, &r.Ts, &r.Funding, &r.OpenInterest, &r.MarkPx); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── CFTC COT weekly positioning ───────────────────────────────────────────

// COTRow is one contract's legacy futures-only COT report for one week.
type COTRow struct {
	Contract     string  `json:"contract"`
	ReportDate   string  `json:"reportDate"` // YYYY-MM-DD
	NoncommLong  float64 `json:"noncommLong"`
	NoncommShort float64 `json:"noncommShort"`
	CommLong     float64 `json:"commLong"`
	CommShort    float64 `json:"commShort"`
	OpenInterest float64 `json:"openInterest"`
}

// UpsertCOT writes a batch of weekly rows in ONE transaction (INSERT OR
// REPLACE on (contract, report_date) — CFTC revisions overwrite cleanly).
func (s *Store) UpsertCOT(ctx context.Context, rows []COTRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO cot_reports
		  (contract, report_date, noncomm_long, noncomm_short, comm_long, comm_short, open_interest)
		VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.Contract, r.ReportDate,
			r.NoncommLong, r.NoncommShort, r.CommLong, r.CommShort, r.OpenInterest); err != nil {
			return fmt.Errorf("upsert cot_reports %s/%s: %w", r.Contract, r.ReportDate, err)
		}
	}
	return tx.Commit()
}

// COTSince returns every stored row with report_date ≥ since (YYYY-MM-DD),
// ordered contract then date ASC — the API groups per contract.
func (s *Store) COTSince(ctx context.Context, since string) ([]COTRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT contract, report_date, noncomm_long, noncomm_short, comm_long, comm_short, open_interest
		FROM cot_reports WHERE report_date >= ?
		ORDER BY contract ASC, report_date ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []COTRow
	for rows.Next() {
		var r COTRow
		if err := rows.Scan(&r.Contract, &r.ReportDate, &r.NoncommLong, &r.NoncommShort,
			&r.CommLong, &r.CommShort, &r.OpenInterest); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestCOTDate is the newest stored report date ("" when the table is empty
// — honest absence, not an error).
func (s *Store) LatestCOTDate(ctx context.Context) (string, error) {
	var day string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(report_date), '') FROM cot_reports`).Scan(&day)
	return day, err
}

// ── StockTwits page-snapshot sentiment ────────────────────────────────────

// StocktwitsRow is one symbol's page-snapshot tally at one time.
type StocktwitsRow struct {
	SymbolID int64 `json:"symbolId"`
	Ts       int64 `json:"ts"`
	Bullish  int   `json:"bullish"`
	Bearish  int   `json:"bearish"`
	Untagged int   `json:"untagged"`
	Total    int   `json:"total"`
}

// InsertStocktwits writes one snapshot row (INSERT OR REPLACE — same-second
// re-runs overwrite rather than erroring).
func (s *Store) InsertStocktwits(ctx context.Context, r StocktwitsRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO stocktwits_sentiment (symbol_id, ts, bullish, bearish, untagged, total)
		VALUES (?,?,?,?,?,?)`, r.SymbolID, r.Ts, r.Bullish, r.Bearish, r.Untagged, r.Total)
	return err
}

// StocktwitsSeries returns one symbol's snapshots with ts ≥ since, ascending.
func (s *Store) StocktwitsSeries(ctx context.Context, symbolID, since int64) ([]StocktwitsRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, bullish, bearish, untagged, total
		FROM stocktwits_sentiment WHERE symbol_id = ? AND ts >= ?
		ORDER BY ts ASC`, symbolID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []StocktwitsRow
	for rows.Next() {
		var r StocktwitsRow
		if err := rows.Scan(&r.SymbolID, &r.Ts, &r.Bullish, &r.Bearish, &r.Untagged, &r.Total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Wikipedia attention (resolution cache + daily views) ──────────────────

// WikiArticle is one symbol's cached article resolution. OK=false rows
// remember a FAILED resolution so the worker never re-hammers Wikimedia.
type WikiArticle struct {
	SymbolID   int64  `json:"symbolId"`
	Article    string `json:"article"` // '' when OK=false
	OK         bool   `json:"ok"`
	ResolvedAt int64  `json:"resolvedAt"`
}

// GetWikiArticle returns the cached resolution (found=false when the symbol
// has never been attempted).
func (s *Store) GetWikiArticle(ctx context.Context, symbolID int64) (WikiArticle, bool, error) {
	var wa WikiArticle
	var ok int
	err := s.db.QueryRowContext(ctx, `
		SELECT symbol_id, article, ok, resolved_at FROM wiki_article WHERE symbol_id = ?`,
		symbolID).Scan(&wa.SymbolID, &wa.Article, &ok, &wa.ResolvedAt)
	if err == sql.ErrNoRows {
		return WikiArticle{}, false, nil
	}
	if err != nil {
		return WikiArticle{}, false, err
	}
	wa.OK = ok == 1
	return wa, true, nil
}

// SetWikiArticle records a resolution outcome (success OR failure).
func (s *Store) SetWikiArticle(ctx context.Context, wa WikiArticle) error {
	ok := 0
	if wa.OK {
		ok = 1
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO wiki_article (symbol_id, article, ok, resolved_at)
		VALUES (?,?,?,?)`, wa.SymbolID, wa.Article, ok, wa.ResolvedAt)
	return err
}

// WikiViewRow is one symbol-day's page views.
type WikiViewRow struct {
	SymbolID int64  `json:"symbolId"`
	Day      string `json:"day"` // YYYY-MM-DD
	Views    int64  `json:"views"`
}

// UpsertWikiViews writes a batch of day rows in ONE transaction (INSERT OR
// REPLACE — overlapping refetch windows are idempotent).
func (s *Store) UpsertWikiViews(ctx context.Context, rows []WikiViewRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO wiki_views (symbol_id, day, views) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.SymbolID, r.Day, r.Views); err != nil {
			return fmt.Errorf("upsert wiki_views %d/%s: %w", r.SymbolID, r.Day, err)
		}
	}
	return tx.Commit()
}

// WikiViewsSeries returns one symbol's last `days` day rows in ASCENDING day
// order (chart-ready).
func (s *Store) WikiViewsSeries(ctx context.Context, symbolID int64, days int) ([]WikiViewRow, error) {
	if days <= 0 || days > 365 {
		days = 90
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, day, views FROM wiki_views WHERE symbol_id = ?
		ORDER BY day DESC LIMIT ?`, symbolID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []WikiViewRow
	for rows.Next() {
		var r WikiViewRow
		if err := rows.Scan(&r.SymbolID, &r.Day, &r.Views); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// CompanyNamesByTickers returns ticker → company name from the companies
// directory for the given tickers (ONE query — never per-symbol). Tickers
// with no directory row are simply absent (honest absence).
func (s *Store) CompanyNamesByTickers(ctx context.Context, tickers []string) (map[string]string, error) {
	out := make(map[string]string, len(tickers))
	if len(tickers) == 0 {
		return out, nil
	}
	q := `SELECT ticker, name FROM companies WHERE ticker IN (?` +
		repeatPlaceholder(len(tickers)-1) + `)`
	args := make([]any, len(tickers))
	for i, t := range tickers {
		args[i] = t
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var ticker, name string
		if err := rows.Scan(&ticker, &name); err != nil {
			return nil, err
		}
		if name != "" {
			out[ticker] = name
		}
	}
	return out, rows.Err()
}

// repeatPlaceholder returns n copies of ",?" (IN-clause helper).
func repeatPlaceholder(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		b = append(b, ',', '?')
	}
	return string(b)
}

// ── CBOE daily put/call ratios ────────────────────────────────────────────

// CboePCRow is one trade date's market-wide put/call statistics.
type CboePCRow struct {
	Day      string  `json:"day"` // YYYY-MM-DD
	TotalPC  float64 `json:"totalPC"`
	IndexPC  float64 `json:"indexPC"`
	EquityPC float64 `json:"equityPC"`
	VIXPC    float64 `json:"vixPC"`
	CallVol  float64 `json:"callVol"`
	PutVol   float64 `json:"putVol"`
	TotalVol float64 `json:"totalVol"`
}

// UpsertCboePC writes one day's statistics (INSERT OR REPLACE — CBOE
// re-posts are idempotent, newest wins).
func (s *Store) UpsertCboePC(ctx context.Context, r CboePCRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO cboe_pc
		  (day, total_pc, index_pc, equity_pc, vix_pc, call_vol, put_vol, total_vol)
		VALUES (?,?,?,?,?,?,?,?)`,
		r.Day, r.TotalPC, r.IndexPC, r.EquityPC, r.VIXPC, r.CallVol, r.PutVol, r.TotalVol)
	return err
}

// CboePCSeries returns the last `days` day rows in ASCENDING day order.
func (s *Store) CboePCSeries(ctx context.Context, days int) ([]CboePCRow, error) {
	if days <= 0 || days > 365 {
		days = 90
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT day, total_pc, index_pc, equity_pc, vix_pc, call_vol, put_vol, total_vol
		FROM cboe_pc ORDER BY day DESC LIMIT ?`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []CboePCRow
	for rows.Next() {
		var r CboePCRow
		if err := rows.Scan(&r.Day, &r.TotalPC, &r.IndexPC, &r.EquityPC, &r.VIXPC,
			&r.CallVol, &r.PutVol, &r.TotalVol); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// HasCboePCDay reports whether a day's statistics are stored — the worker's
// cheap idempotence check beyond the meta day-key.
func (s *Store) HasCboePCDay(ctx context.Context, day string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cboe_pc WHERE day = ?`, day).Scan(&n)
	return n > 0, err
}

// ── TradingView near-real-time quote tape ─────────────────────────────────

// TVQuoteRow is one symbol's scanner quote at one fetch time. Realtime marks
// whether Price is the rtc real-time composite (true) or the 15-min-delayed
// close (false) — the API surfaces the flag so nothing delayed masquerades
// as live.
type TVQuoteRow struct {
	SymbolID     int64   `json:"symbolId"`
	Ts           int64   `json:"ts"`
	Price        float64 `json:"price"`
	DelayedClose float64 `json:"delayedClose"`
	ChangePct    float64 `json:"changePct"`
	DayVolume    float64 `json:"dayVolume"`
	Realtime     bool    `json:"realtime"`
}

// InsertTVQuote writes one quote row (INSERT OR REPLACE — same-second re-runs
// overwrite rather than erroring).
func (s *Store) InsertTVQuote(ctx context.Context, r TVQuoteRow) error {
	rt := 0
	if r.Realtime {
		rt = 1
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO tv_quotes
		  (symbol_id, ts, price, delayed_close, change_pct, day_volume, realtime)
		VALUES (?,?,?,?,?,?,?)`,
		r.SymbolID, r.Ts, r.Price, r.DelayedClose, r.ChangePct, r.DayVolume, rt)
	return err
}

// LatestTVQuote returns a symbol's newest quote (ok=false when none stored).
func (s *Store) LatestTVQuote(ctx context.Context, symbolID int64) (TVQuoteRow, bool, error) {
	var r TVQuoteRow
	var rt int
	err := s.db.QueryRowContext(ctx, `
		SELECT symbol_id, ts, price, delayed_close, change_pct, day_volume, realtime
		FROM tv_quotes WHERE symbol_id = ? ORDER BY ts DESC LIMIT 1`,
		symbolID).Scan(&r.SymbolID, &r.Ts, &r.Price, &r.DelayedClose, &r.ChangePct, &r.DayVolume, &rt)
	if err == sql.ErrNoRows {
		return TVQuoteRow{}, false, nil
	}
	if err != nil {
		return TVQuoteRow{}, false, err
	}
	r.Realtime = rt == 1
	return r, true, nil
}

// PruneTVQuotes deletes quote rows older than `before` (unix seconds) — the
// tape keeps only a trailing window by design; bars are the durable record.
func (s *Store) PruneTVQuotes(ctx context.Context, before int64) (int64, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM tv_quotes WHERE ts < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ─────────────────────────────────────────────────────────────────────────
// CROSS-SECTIONAL ALPHA wave (appended block) — minimal "latest row" reads
// the featureVersion-5 vector builder needs from this file's datasets. Each
// mirrors an existing accessor's shape ((row, bool, error) honest-absence
// idiom); freshness/thinness gating stays in the pipeline so the store keeps
// no policy.

// LatestCryptoPerp returns a crypto symbol's newest perp snapshot (ok=false
// when none is stored — honest absence).
func (s *Store) LatestCryptoPerp(ctx context.Context, symbolID int64) (CryptoPerpRow, bool, error) {
	var r CryptoPerpRow
	err := s.db.QueryRowContext(ctx, `
		SELECT symbol_id, ts, funding, open_interest, mark_px
		FROM crypto_perp WHERE symbol_id = ? ORDER BY ts DESC LIMIT 1`,
		symbolID).Scan(&r.SymbolID, &r.Ts, &r.Funding, &r.OpenInterest, &r.MarkPx)
	if err == sql.ErrNoRows {
		return CryptoPerpRow{}, false, nil
	}
	if err != nil {
		return CryptoPerpRow{}, false, err
	}
	return r, true, nil
}

// LatestStocktwits returns a symbol's newest page-snapshot tally (ok=false
// when none is stored — honest absence).
func (s *Store) LatestStocktwits(ctx context.Context, symbolID int64) (StocktwitsRow, bool, error) {
	var r StocktwitsRow
	err := s.db.QueryRowContext(ctx, `
		SELECT symbol_id, ts, bullish, bearish, untagged, total
		FROM stocktwits_sentiment WHERE symbol_id = ? ORDER BY ts DESC LIMIT 1`,
		symbolID).Scan(&r.SymbolID, &r.Ts, &r.Bullish, &r.Bearish, &r.Untagged, &r.Total)
	if err == sql.ErrNoRows {
		return StocktwitsRow{}, false, nil
	}
	if err != nil {
		return StocktwitsRow{}, false, err
	}
	return r, true, nil
}

// LatestCOTByContract returns the newest stored COT row whose contract name
// matches the LIKE pattern (case-insensitive; ok=false when none matches —
// honest absence). The pipeline passes a curated pattern (e.g. the E-mini
// S&P 500), never user input.
func (s *Store) LatestCOTByContract(ctx context.Context, likePattern string) (COTRow, bool, error) {
	var r COTRow
	err := s.db.QueryRowContext(ctx, `
		SELECT contract, report_date, noncomm_long, noncomm_short, comm_long, comm_short, open_interest
		FROM cot_reports WHERE UPPER(contract) LIKE UPPER(?)
		ORDER BY report_date DESC LIMIT 1`, likePattern).
		Scan(&r.Contract, &r.ReportDate, &r.NoncommLong, &r.NoncommShort,
			&r.CommLong, &r.CommShort, &r.OpenInterest)
	if err == sql.ErrNoRows {
		return COTRow{}, false, nil
	}
	if err != nil {
		return COTRow{}, false, err
	}
	return r, true, nil
}
