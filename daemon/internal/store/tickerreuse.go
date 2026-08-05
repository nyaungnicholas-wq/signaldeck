package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// TICKER REUSE (2026-08-04)
//
// An exchange recycles a ticker. Our symbols row is keyed on (symbol, market),
// so the new company's bars land on the dead company's row and the two price
// series are spliced into one name with a multi-year hole in the middle.
// Measured here on 2026-08-04: row 1122 `ATC` carries Atotech's 2021-02 →
// 2022-08 history AND a GraniteShares ETF that began printing on the recycled
// ticker 2026-05-12. `TradableAt` then deletes the LIVE ETF from every
// point-in-time universe after 2022-08-16, and any backtest touching ATC reads
// a 1,365-day gap and a discontinuous price as if it were one company.
//
// WHICH ROW KEEPS THE TICKER. The LIVE security does. Ingestion resolves by
// ticker — quotes, bars and news all arrive addressed to "ATC" — so moving the
// live company off that row would break the feed on the next poll and quietly
// re-create the same row on the one after. The DEAD company is the one that
// moves, to a disambiguated ticker it can keep forever.
//
// WHAT IS NOT THIS. A stamp that is one to three days early is NOT reuse: the
// delisting date is recorded from a filing and the last tape print can follow
// it by a settlement day or two. Measured on the same date, exactly four
// symbols had any bar after their delisting DAY, and three of them (ACACU,
// ELON, TRIL) were a single bar one to three days later. Splitting those would
// invent a second company out of a rounding difference. Only a long gap plus
// sustained later trading is reuse; the planner separates the two and this
// function refuses anything it is not given explicitly.

// ReusedTickerSplit is one row to be split in two. Every field is stated by the
// caller rather than inferred here, because this function's job is to apply a
// reviewed plan atomically, not to decide what a plan should say.
type ReusedTickerSplit struct {
	SymbolID         int64  // the row currently holding two securities
	Symbol           string // its ticker, re-verified against the live row
	HistoricalTicker string // ticker the DEAD company moves to
	HistoricalName   string
	DelistedAt       int64 // the boundary; rows on or before this UTC day are the dead company's
	LiveAddedAt      int64 // first bar of the security that keeps the ticker
}

// SplitResult reports what moved, so a plan can be audited against what
// actually happened rather than against what it intended.
type SplitResult struct {
	NewSymbolID int64
	Moved       map[string]int64
	Deleted     map[string]int64
}

// eraTables are the tables partitioned by time: rows at or before the delisting
// day belong to the dead company and move with it. The column is named per
// table because this schema is not consistent about it.
var eraTables = []struct{ table, tsCol string }{
	{"bars", "ts"},
	{"breakouts", "ts"},
	{"confluence_setups", "ts"},
	{"dq_events", "ts"},
	{"forecasts", "ts"},
	{"news", "ts"},
	{"rankings", "ts"},
	{"regime_state", "ts"},
	{"research_weeks", "ts"},
	{"tv_ratings", "ts"},
}

// fitTables hold aggregates and model fits computed OVER the spliced series —
// an expectancy cell keyed by state, a per-symbol model's weights. There is no
// timestamp to partition them by and no honest way to attribute them to one
// company or the other, because they were computed from both. They are deleted
// rather than moved: both workers rebuild them from bars on their next pass,
// and a wrong fit that survives is worse than a missing one that regenerates.
var fitTables = []string{"expectancy", "symbol_models"}

// splicedLatest is the third category, and the one that is easy to miss because
// its rows sit on the RIGHT side of the boundary. They are latest-state rows
// (or a recent window) that POST-date the delisting and would therefore look
// like they belong to the live security — but they were computed from a
// trailing window that reached back across the splice into the dead company's
// tape. They are wrong, and unlike the fit tables they cannot self-heal,
// because every refresh path gates on a bar count the live security does not
// have yet. Measured on ATC 2026-08-04, with 58 daily bars against
// histfeat.minTrailingBars=60, regime.MinBars=60 and forecast's warmup=50 plus
// a 5-fold evaluation: every one of those gates fails, the worker returns
// ok=false, and the stale row is never overwritten. `forecasts` proved it
// outright — n_train=389 needs 441 daily bars, which is 383 Atotech + 58 ETF
// exactly.
//
// So they are deleted. A missing row regenerates when the live security has
// enough history; a spliced row would have been served forever.
var splicedLatest = []struct{ table, tsCol string }{
	{"research_weeks", "ts"},
	{"forecasts", "ts"},
	{"regime_state", "ts"},
	{"confluence_setups", "ts"},
}

// SplitReusedTicker moves one security's history off a row that holds two,
// atomically, and returns what it moved.
//
// Reversibility, since this is the kind of change that has to be undoable: the
// new symbol id is returned and every moved row is addressable as
// `symbol_id = <new id>`, so the inverse is one UPDATE per table back to the
// old id plus restoring delisted_at. The rows are not deleted, only re-pointed.
// The two fit tables ARE deleted and regenerate; that is the only part that is
// not a pure inverse, which is why it is limited to derived data.
func (s *Store) SplitReusedTicker(ctx context.Context, spec ReusedTickerSplit) (SplitResult, error) {
	res := SplitResult{Moved: map[string]int64{}, Deleted: map[string]int64{}}
	if spec.SymbolID <= 0 || spec.Symbol == "" || spec.HistoricalTicker == "" || spec.DelistedAt <= 0 {
		return res, fmt.Errorf("incomplete split spec: %+v", spec)
	}
	if spec.HistoricalTicker == spec.Symbol {
		return res, fmt.Errorf("historical ticker must differ from %q", spec.Symbol)
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Re-verify identity against the LIVE row, the way applyDelistings does: a
	// plan built against an older copy of the database must not be able to
	// operate on whatever row now owns that id.
	var haveSym, market string
	var haveDelisted, addedAt int64
	if err := tx.QueryRowContext(ctx,
		`SELECT symbol, market, COALESCE(delisted_at,0), added_at FROM symbols WHERE id=?`,
		spec.SymbolID).Scan(&haveSym, &market, &haveDelisted, &addedAt); err != nil {
		return res, fmt.Errorf("load symbol %d: %w", spec.SymbolID, err)
	}
	if haveSym != spec.Symbol {
		return res, fmt.Errorf("id %d is %q, plan says %q — refusing", spec.SymbolID, haveSym, spec.Symbol)
	}
	if haveDelisted != spec.DelistedAt {
		return res, fmt.Errorf("id %d delisted_at is %d, plan says %d — refusing",
			spec.SymbolID, haveDelisted, spec.DelistedAt)
	}

	var clash int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols WHERE symbol=? AND market=?`,
		spec.HistoricalTicker, market).Scan(&clash); err != nil {
		return res, fmt.Errorf("check historical ticker: %w", err)
	}
	if clash != 0 {
		return res, fmt.Errorf("historical ticker %q already exists in %s — refusing", spec.HistoricalTicker, market)
	}

	r, err := tx.ExecContext(ctx, `
		INSERT INTO symbols (symbol, market, name, active, added_at, stream, delisted_at)
		VALUES (?,?,?,0,?,0,?)`,
		spec.HistoricalTicker, market, spec.HistoricalName, addedAt, spec.DelistedAt)
	if err != nil {
		return res, fmt.Errorf("create historical row: %w", err)
	}
	newID, err := r.LastInsertId()
	if err != nil {
		return res, fmt.Errorf("new symbol id: %w", err)
	}
	res.NewSymbolID = newID

	// The boundary is the END of the delisting UTC day, not the stamp itself:
	// daily bars carry 00:00/04:00/05:00 alignments, so the dead company's own
	// last bar can be stamped hours after a midnight-aligned delisted_at.
	boundary := (spec.DelistedAt/86400)*86400 + 86399

	// news_symbols has no timestamp of its own, so it is partitioned by the
	// ARTICLE's timestamp. The subquery deliberately does NOT also require
	// news.symbol_id = the row being split: news_symbols is many-to-many, an
	// article about several tickers is owned by whichever symbol filed it, and
	// on the real data three of eight rows for the split symbol pointed at
	// articles owned by other symbols. Constraining on ownership left those
	// three attached to the LIVE company while describing the dead one.
	if r, err := tx.ExecContext(ctx, `
		UPDATE news_symbols SET symbol_id=?
		WHERE symbol_id=? AND news_id IN (SELECT id FROM news WHERE ts<=?)`,
		newID, spec.SymbolID, boundary); err != nil {
		return res, fmt.Errorf("move news_symbols: %w", err)
	} else if n, _ := r.RowsAffected(); n > 0 {
		res.Moved["news_symbols"] = n
	}

	for _, t := range eraTables {
		r, err := tx.ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s SET symbol_id=? WHERE symbol_id=? AND %s<=?`, t.table, t.tsCol),
			newID, spec.SymbolID, boundary)
		if err != nil {
			return res, fmt.Errorf("move %s: %w", t.table, err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			res.Moved[t.table] = n
		}
	}

	for _, t := range fitTables {
		r, err := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE symbol_id=?`, t), spec.SymbolID)
		if err != nil {
			return res, fmt.Errorf("clear %s: %w", t, err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			res.Deleted[t] = n
		}
	}

	if _, err := purgeSplicedLatest(ctx, tx, spec.SymbolID, boundary, res.Deleted); err != nil {
		return res, err
	}

	// The surviving row is the LIVE security: no longer delisted, and dated from
	// its own first bar rather than the dead company's.
	if _, err := tx.ExecContext(ctx,
		`UPDATE symbols SET delisted_at=NULL, added_at=? WHERE id=?`,
		spec.LiveAddedAt, spec.SymbolID); err != nil {
		return res, fmt.Errorf("reset live row: %w", err)
	}

	// Nothing of the dead company may remain addressable as the live row, and
	// nothing of the live company may have moved. Checked inside the
	// transaction so a violation rolls the whole split back.
	var strays int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bars WHERE symbol_id=? AND ts<=?`, spec.SymbolID, boundary).Scan(&strays); err != nil {
		return res, fmt.Errorf("post-check live row: %w", err)
	}
	if strays != 0 {
		return res, fmt.Errorf("post-check: %d pre-delisting bars still on the live row", strays)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bars WHERE symbol_id=? AND ts>?`, newID, boundary).Scan(&strays); err != nil {
		return res, fmt.Errorf("post-check historical row: %w", err)
	}
	if strays != 0 {
		return res, fmt.Errorf("post-check: %d post-delisting bars moved to the historical row", strays)
	}

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// execer is whatever can run a statement — a *sql.DB or a *sql.Tx — so the
// purge is callable both inside a split's transaction and on its own, for a
// split that was already applied before this category was identified.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func purgeSplicedLatest(ctx context.Context, x execer, symbolID, boundary int64, into map[string]int64) (int64, error) {
	var total int64
	for _, t := range splicedLatest {
		r, err := x.ExecContext(ctx, fmt.Sprintf(
			`DELETE FROM %s WHERE symbol_id=? AND %s>?`, t.table, t.tsCol), symbolID, boundary)
		if err != nil {
			return total, fmt.Errorf("purge spliced %s: %w", t.table, err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			into[t.table] = into[t.table] + n
			total += n
		}
	}
	return total, nil
}

// PurgeSplicedLatest deletes the post-boundary latest-state rows that were
// computed across a splice, for a symbol whose split has ALREADY been applied.
//
// It exists because the category was identified after the first split ran: the
// rows sit on the right side of the boundary and look like the live security's
// own, so a migration that only moved and re-pointed left them behind. Safe to
// re-run — a second call deletes nothing.
func (s *Store) PurgeSplicedLatest(ctx context.Context, symbolID, boundary int64) (map[string]int64, error) {
	out := map[string]int64{}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := purgeSplicedLatest(ctx, tx, symbolID, boundary, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// NudgeDelisting moves a delisting stamp forward to the symbol's last bar day.
//
// The stamp comes from a filing; the tape can print for a settlement day or two
// afterwards. That is a stamp being early, not a second company, and correcting
// it is what makes "no member after its delisting" an exact statement instead
// of one carrying a grace period. Refuses to move a stamp BACKWARD, and refuses
// a gap wide enough to be reuse — that case belongs to SplitReusedTicker.
func (s *Store) NudgeDelisting(ctx context.Context, symbolID, newTs, maxGapDays int64) (bool, error) {
	var cur int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(delisted_at,0) FROM symbols WHERE id=?`, symbolID).Scan(&cur); err != nil {
		return false, err
	}
	if cur <= 0 {
		return false, fmt.Errorf("symbol %d carries no delisting stamp", symbolID)
	}
	if newTs <= cur {
		return false, nil
	}
	if gap := (newTs - cur) / 86400; gap > maxGapDays {
		return false, fmt.Errorf("symbol %d: last bar is %d days after the stamp — too wide to be a settlement lag, treat as ticker reuse", symbolID, gap)
	}
	if _, err := s.w.ExecContext(ctx,
		`UPDATE symbols SET delisted_at=? WHERE id=?`, newTs, symbolID); err != nil {
		return false, err
	}
	return true, nil
}

// SortedKeys is a small helper so callers print a deterministic report.
func SortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
