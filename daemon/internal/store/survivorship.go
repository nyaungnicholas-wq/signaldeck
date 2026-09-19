// Survivorship-clean universe accessors (2026-07-24).
//
// THE BIAS THIS CLOSES
// --------------------
// Symbols are never deleted here — unsubscribing sets active=0 and daily bars
// are protected from pruning in code. The history survives. What did NOT
// survive was its VISIBILITY: every research path iterates ActiveStockSymbols
// (active=1), so any name that was dropped, delisted or acquired silently left
// the study population. That is textbook survivorship bias, and it inflates
// exactly the strategies this platform researches most — oversold-bounce and
// mean-reversion, where the names that kept falling to zero are the ones
// missing.
//
// So the fix is not "keep the data" (already true) but "let research SEE the
// data": a universe accessor that returns everything ever tracked, plus a
// delisting marker so a point-in-time universe can be reconstructed rather
// than guessed.
//
// Honest limit, stated rather than hidden: the accessors below recover names
// the platform itself once tracked. Companies that died BEFORE they were ever
// added need an outside source.
//
// UPDATE 2026-08-02: that source turned out to be free after all. Alpaca's
// asset list carries ~19k INACTIVE securities and its bars endpoint still
// serves their history, so 650 exchange-listed companies that died between
// 2020 and 2026 were recoverable at no cost (see tools/alpha/fetch_delisted.py
// and UpsertHistoricalSymbol below). A paid point-in-time vendor is still
// better — the recovered set skews to 2021-2022 and holds only 33 outright
// collapses — but "only a paid vendor" was too pessimistic.
//
// The SEC route, for anyone tempted: Form 25 records that a delisting happened
// but NOT which ticker it happened to. company_tickers.json drops delisted
// names, per-CIK submissions return the post-delisting OTC ticker, and the
// filing document carries no symbol. Tested 2026-08-02; all three fail.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// UpsertHistoricalSymbol inserts a company that is ALREADY DEAD: never
// tradable, never streamed, present so research can see the names that failed.
//
// It REFUSES to touch a row that is currently active. Overwriting a live ticker
// with a dead company's record would delete a real company from every
// point-in-time universe built afterwards — the same failure the upstream
// ticker-reuse guard exists to prevent, and worth failing on twice. Exchanges
// recycle tickers constantly (COHR, CZR and ECHO all trade today under symbols
// a previous company was delisted from).
//
// An existing delisted_at is never overwritten: the live detector watched it
// happen, this import only inferred it from the last bar.
//
// addedAt MUST be when the company started trading (its first bar), not when we
// imported it. TradableAt filters `added_at <= ts`, so an observation-dated
// added_at makes a company that died in 2021 look like it was listed in 2026 —
// invisible to every point-in-time universe before today, which is the exact
// bias this file exists to remove.
func (s *Store) UpsertHistoricalSymbol(ctx context.Context, symbol string, market md.Market, name string, addedAt, delistedAt int64) (md.Symbol, error) {
	if delistedAt <= 0 {
		return md.Symbol{}, fmt.Errorf("%s: delistedAt is required — a historical symbol with no death date is indistinguishable from a live one", symbol)
	}
	if addedAt <= 0 || addedAt > delistedAt {
		return md.Symbol{}, fmt.Errorf("%s: addedAt (%d) must be positive and predate delistedAt (%d) — a company cannot be listed after it died", symbol, addedAt, delistedAt)
	}

	var active int
	err := s.db.QueryRowContext(ctx,
		`SELECT active FROM symbols WHERE symbol=? AND market=?`,
		symbol, string(market)).Scan(&active)
	switch {
	case err == nil && active == 1:
		return md.Symbol{}, fmt.Errorf("%s is ACTIVE: refusing to import it as delisted", symbol)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return md.Symbol{}, err
	}

	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO symbols (symbol, market, name, active, added_at, stream, delisted_at)
		VALUES (?,?,?,0,?,0,?)
		ON CONFLICT(symbol, market) DO UPDATE SET
			active=0, stream=0,
			added_at=MIN(symbols.added_at, excluded.added_at),
			delisted_at=COALESCE(NULLIF(symbols.delisted_at,0), excluded.delisted_at),
			name=CASE WHEN excluded.name != '' THEN excluded.name ELSE symbols.name END`,
		symbol, string(market), name, addedAt, delistedAt); err != nil {
		return md.Symbol{}, err
	}
	return s.GetSymbol(ctx, symbol, market)
}

// RepairAddedAtFromBars rewrites added_at to each symbol's FIRST daily bar.
//
// added_at was being set to time.Now() on insert — the date WE first saw the
// symbol, not the date it started trading. Since TradableAt filters
// `added_at <= ts`, that made the point-in-time universe empty for every
// historical date: measured 2026-08-02, TradableAt returned 0 symbols for
// 2021, 2023 and 2025 alike. The survivorship accessor was not merely biased,
// it returned nothing, silently.
//
// The first bar is the best listing-date evidence we hold. Symbols with no
// daily bars are left alone — there is nothing to infer from.
//
// Returns (repaired, skipped). Idempotent: re-running changes nothing.
func (s *Store) RepairAddedAtFromBars(ctx context.Context) (int64, int64, error) {
	res, err := s.w.ExecContext(ctx, `
		UPDATE symbols SET added_at = (
			SELECT MIN(b.ts) FROM bars b WHERE b.symbol_id = symbols.id AND b.tf = '1d')
		WHERE EXISTS (
			SELECT 1 FROM bars b WHERE b.symbol_id = symbols.id AND b.tf = '1d')
		  AND added_at <> (
			SELECT MIN(b.ts) FROM bars b WHERE b.symbol_id = symbols.id AND b.tf = '1d')`)
	if err != nil {
		return 0, 0, err
	}
	repaired, _ := res.RowsAffected()

	var skipped int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM symbols WHERE NOT EXISTS (
			SELECT 1 FROM bars b WHERE b.symbol_id = symbols.id AND b.tf = '1d')`).
		Scan(&skipped); err != nil {
		return repaired, 0, err
	}
	return repaired, skipped, nil
}

// SurvivorshipEpoch is 2026-07-24T00:00:00Z as Unix seconds: the instant before
// which every graded row was scored against a survivor-seeded universe.
//
// It lives here, exported, because three consumers need the SAME instant and
// each had been free to hold its own copy — the accuracy registry
// (SURVIVORSHIP_EPOCH in tools/accuracy_registry.py), the live
// prequential-majority benchmark (internal/pipeline), and the re-admission gate
// that decides whether a retired model may emit again (internal/api). A
// boundary duplicated three times can drift in two of them without anything
// failing. TestSurvivorshipEpochMatchesRegistryBoundary pins it to the
// registry's value.
//
// Any evidence window that DECIDES something must start here. A gate graded on
// earlier rows is graded on a sample the platform's own grader refuses to
// publish.
const SurvivorshipEpoch int64 = 1784851200

// ResearchUniverse returns EVERY stock symbol ever tracked, active or not, for
// backtests and statistical studies. Live trading paths must keep using
// ActiveStockSymbols — this one deliberately includes the dead.
func (s *Store) ResearchUniverse(ctx context.Context) ([]md.Symbol, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol, market, name, active, added_at
		FROM symbols WHERE market=? ORDER BY symbol`, string(md.Stocks))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Symbol
	for rows.Next() {
		var v md.Symbol
		var mkt string
		var active int
		if err := rows.Scan(&v.ID, &v.Symbol, &mkt, &v.Name, &active, &v.AddedAt); err != nil {
			return nil, err
		}
		v.Market = md.Market(mkt)
		v.Active = active == 1
		out = append(out, v)
	}
	return out, rows.Err()
}

// UniverseCoverage counts the universe by liveness so a study can DECLARE how
// much of its population is inactive rather than quietly omitting it.
func (s *Store) UniverseCoverage(ctx context.Context) (total, active, inactive int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(active),0)
		FROM symbols WHERE market=?`, string(md.Stocks)).Scan(&total, &active)
	if err != nil {
		return 0, 0, 0, err
	}
	return total, active, total - active, nil
}

// MarkDelisted records that a symbol stopped trading, so a point-in-time
// universe can be rebuilt. Deactivating is a SUBSCRIPTION decision (the user
// stopped watching); delisting is a MARKET fact — conflating them is what made
// the bias invisible, so they are stored separately.
func (s *Store) MarkDelisted(ctx context.Context, symbolID, ts int64) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE symbols SET delisted_at=? WHERE id=? AND (delisted_at IS NULL OR delisted_at=0)`,
		ts, symbolID)
	return err
}

// TradableAt returns the symbols that were tradable at ts — added by then and
// not yet delisted. This is the population a point-in-time backtest should
// draw from at that date.
func (s *Store) TradableAt(ctx context.Context, ts int64) ([]md.Symbol, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol, market, name, active, added_at
		FROM symbols
		WHERE market=? AND added_at <= ?
		  AND (delisted_at IS NULL OR delisted_at = 0 OR delisted_at > ?)
		ORDER BY symbol`, string(md.Stocks), ts, ts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Symbol
	for rows.Next() {
		var v md.Symbol
		var mkt string
		var active int
		if err := rows.Scan(&v.ID, &v.Symbol, &mkt, &v.Name, &active, &v.AddedAt); err != nil {
			return nil, err
		}
		v.Market = md.Market(mkt)
		v.Active = active == 1
		out = append(out, v)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────
// DELISTING DETECTION (appended block).

// StockLastBar is the newest daily bar timestamp held for one stock symbol.
type StockLastBar struct {
	SymbolID int64
	Symbol   string
	Active   bool
	// LastTs is 0 when the symbol has no daily bars at all.
	LastTs int64
	// DelistedAt is 0 when the symbol is not marked delisted.
	DelistedAt int64
}

// StockLastBars returns the newest tf='1d' bar per stock symbol, including
// symbols with no bars at all, plus the current delisted marker.
//
// Daily bars are pruning-protected in code, so "no recent bar" is a statement
// about the MARKET rather than about our retention — which is what makes this
// query usable as delisting evidence at all.
func (s *Store) StockLastBars(ctx context.Context) ([]StockLastBar, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sy.id, sy.symbol, sy.active,
		       COALESCE(MAX(b.ts), 0)          AS last_ts,
		       COALESCE(sy.delisted_at, 0)     AS delisted_at
		FROM symbols sy
		LEFT JOIN bars b ON b.symbol_id = sy.id AND b.tf = '1d'
		WHERE sy.market = ?
		GROUP BY sy.id, sy.symbol, sy.active, sy.delisted_at`, string(md.Stocks))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []StockLastBar
	for rows.Next() {
		var r StockLastBar
		var active int
		if err := rows.Scan(&r.SymbolID, &r.Symbol, &active, &r.LastTs, &r.DelistedAt); err != nil {
			return nil, err
		}
		r.Active = active == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClearDelisted removes a delisting marker. A symbol that prints a bar again was
// never delisted — it was halted, or our fetch was failing — and a permanent
// marker would silently shrink every point-in-time universe built afterwards.
// Delisting must therefore be REVERSIBLE on evidence, or a false positive
// becomes indistinguishable from a market fact.
func (s *Store) ClearDelisted(ctx context.Context, symbolID int64) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE symbols SET delisted_at=NULL WHERE id=?`, symbolID)
	return err
}

// DQSilencedSymbols returns the symbol ids whose FRESHNESS checks carry no
// information: names recorded as delisted that nothing downstream still needs.
//
// Why this exists. A delisted ticker stops printing bars because the company
// stopped trading, so "last daily bar is old" is a tautology on it, not an
// incident. Measured on the live DB 2026-08-06: 5,599 of the 5,719 `stale`
// dq_events in a trailing 24h window (97.9%) named a delisted symbol, arriving
// at ~1,868/hour — one per delisted row per rate-limit bucket — against ~31/hour
// from the live universe. The one data-quality signal this platform has was
// reading 98% noise.
//
// Two populations are DELIBERATELY NOT silenced, because a feed fault on them
// is still actionable:
//
//   - Names the paper book still HOLDS. A position outlives the universe
//     (pipeline.PaperTrader re-admits held-but-inactive symbols exit-only), and
//     an exit needs a price. A dead feed under an open position IS the incident.
//   - Names still inside the GRADED population — an unresolved score_outcomes
//     or prediction_outcomes row. Those are pending accuracy measurements, so a
//     feed fault there corrupts a published number rather than merely a chart.
//     prediction_outcomes is the table tools/accuracy_registry.py grades, which
//     is why it gets its own clause instead of riding on score_outcomes: the
//     two happen to name the same 16 symbols today, but they are written by
//     different paths (InsertScore vs UpsertPrediction) and a guard on the
//     published number must not depend on that coincidence holding.
//
// Measured 2026-08-06 on the live DB: 0 delisted symbols held in the paper book
// and 16 carrying unresolved outcomes, so the exemption costs at most 16
// events/hour against the ~1,868 it removes.
//
// ponytail: paper_positions is keyed (strategy, symbol_id), so the NOT EXISTS
// scans it rather than seeking — it holds 2 rows today. Add an index on
// symbol_id if the book ever carries thousands of open positions.
func (s *Store) DQSilencedSymbols(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sy.id FROM symbols sy
		WHERE COALESCE(sy.delisted_at, 0) > 0
		  AND NOT EXISTS (SELECT 1 FROM paper_positions p WHERE p.symbol_id = sy.id)
		  AND NOT EXISTS (SELECT 1 FROM score_outcomes o
		                  WHERE o.symbol_id = sy.id AND o.resolved_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM prediction_outcomes po
		                  WHERE po.symbol_id = sy.id AND po.resolved_at IS NULL)`)
	if err != nil {
		return nil, err
	}
	return scanIDSet(rows)
}

// scanIDSet converts *sql.Rows of int64 IDs into a set.
func scanIDSet(rows *sql.Rows) (map[int64]bool, error) {
	defer rows.Close() //nolint:errcheck
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// DelistedSymbolIDs returns a set of symbol IDs that have a delisting stamp.
// Every symbol carrying a delisting stamp, held or not; the outcome resolver
// voids rows on these at once because a delisted name never prints a forward
// bar and the 30-day grace only parked them at the head of the oldest-first
// queue (measured 2026-09-09: 19,499 score outcomes, 1.95M rows behind them).
func (s *Store) DelistedSymbolIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM symbols WHERE COALESCE(delisted_at, 0) > 0`)
	if err != nil {
		return nil, err
	}
	return scanIDSet(rows)
}
