package store

// RE-FETCHING A COHORT ONTO ONE PRICE BASIS.
//
// ~650 symbols were imported through `sdmaint import-delisted` from a staging DB
// that requested adjustment=all — split PLUS dividends — while every other
// ingest path requests adjustment=split. The bars table holds ONE series per
// symbol, so those 650 histories sit on a different price convention from the
// rest of the column.
//
// The size of that was measured before any of this was written, on ten sample
// symbols fetched fresh at adjustment=split and compared close-by-close against
// what is stored: CADE 97.7% of bars differ (worst 26.9%), CIVI 97.1% (49.4%),
// ELON 99.3% (47.3%), PRMW 99.6% (10.5%), PPEM 39.0% (66.2%). It is not a tail
// effect on a handful of dividend payers; it is most of the cohort's history.
//
// THE OLD BARS ARE NOT DELETED. They move into bars_quarantine under a run id,
// which is the same reversible mechanism the flat-pad work already uses and
// already has a tested restore. Re-fetching without removing them first would
// leave a MIXTURE: UpsertBars replaces matching timestamps, so any stored bar
// the fresh series does not contain would survive on the old basis. PPEM alone
// stores 123 bars where a fresh fetch returns 799, and ATC stores 457 against
// 383 — those differences are exactly the rows that would be left behind.

import (
	"context"
	"fmt"
)

// CohortSymbol is one symbol scheduled for re-fetch.
type CohortSymbol struct {
	SymbolID  int64
	Symbol    string
	Bars      int
	FirstDay  string // YYYY-MM-DD of its earliest stored daily bar
	LastDay   string
	Refetched bool // already moved under this run id — skip it
}

// CohortForRefetch resolves symbol names to live rows and reports what each one
// currently stores, marking the ones a previous pass already handled.
//
// The Refetched flag is what makes the migration resumable: a symbol whose bars
// are already in bars_quarantine under this run id has been done, and re-doing
// it would quarantine its FRESH bars on top of its stale ones — turning a repair
// into data loss on the second run.
func (s *Store) CohortForRefetch(ctx context.Context, tf string, symbols []string, runID string) ([]CohortSymbol, error) {
	if runID == "" {
		return nil, fmt.Errorf("runID must not be empty: an unidentifiable run cannot be resumed or undone")
	}
	out := make([]CohortSymbol, 0, len(symbols))
	for _, sym := range symbols {
		var c CohortSymbol
		c.Symbol = sym
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM symbols WHERE symbol = ? AND market = 'stocks'`, sym).Scan(&c.SymbolID)
		if err != nil {
			continue // not a live symbol; the caller reports the shortfall
		}
		var first, last any
		if err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*), MIN(date(ts,'unixepoch')), MAX(date(ts,'unixepoch'))
			FROM bars WHERE symbol_id = ? AND tf = ?`, c.SymbolID, tf).
			Scan(&c.Bars, &first, &last); err != nil {
			return nil, err
		}
		if sf, ok := first.(string); ok {
			c.FirstDay = sf
		}
		if sl, ok := last.(string); ok {
			c.LastDay = sl
		}
		var done int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM bars_quarantine WHERE run_id = ? AND symbol_id = ?`,
			runID, c.SymbolID).Scan(&done); err != nil {
			return nil, err
		}
		c.Refetched = done > 0
		out = append(out, c)
	}
	return out, nil
}

// QuarantineSymbolBars moves ONE symbol's bars for a timeframe into
// bars_quarantine under runID, and returns how many moved.
//
// One transaction, so a crash between the copy and the delete cannot lose bars.
// INSERT OR IGNORE on the quarantine side means re-running against a symbol
// already moved is a no-op rather than a duplicate-key failure — but callers
// should skip such symbols anyway (see CohortForRefetch.Refetched), because by
// then the bars in the table are the FRESH ones and quarantining those would
// discard the repair.
func (s *Store) QuarantineSymbolBars(ctx context.Context, symbolID int64, tf, runID, reason string, now int64) (int64, error) {
	if runID == "" {
		return 0, fmt.Errorf("runID must not be empty")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO bars_quarantine
  (symbol_id, tf, ts, open, high, low, close, volume, run_id, reason, quarantined_at)
SELECT symbol_id, tf, ts, open, high, low, close, volume, ?, ?, ?
FROM bars WHERE symbol_id = ? AND tf = ?`, runID, reason, now, symbolID, tf); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM bars WHERE symbol_id = ? AND tf = ?`, symbolID, tf)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// CohortRefetchDelta compares what a run quarantined against what replaced it,
// per symbol, so the migration reports what it actually changed rather than
// asserting success.
type CohortRefetchDelta struct {
	Symbol    string  `json:"symbol"`
	SymbolID  int64   `json:"symbolId"`
	OldBars   int     `json:"oldBars"`
	NewBars   int     `json:"newBars"`
	Common    int     `json:"commonDays"`
	Differing int     `json:"differingCloses"`
	WorstPct  float64 `json:"worstRelDiffPct"`
}

// CohortRefetchDeltas measures every symbol touched by a run.
func (s *Store) CohortRefetchDeltas(ctx context.Context, tf, runID string) ([]CohortRefetchDelta, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT q.symbol_id, COALESCE(sy.symbol,''),
       COUNT(*) AS old_bars,
       (SELECT COUNT(*) FROM bars b WHERE b.symbol_id = q.symbol_id AND b.tf = ?) AS new_bars,
       SUM(CASE WHEN nb.close IS NOT NULL THEN 1 ELSE 0 END) AS common,
       SUM(CASE WHEN nb.close IS NOT NULL AND q.close > 0
                 AND ABS(nb.close / q.close - 1) > 1e-6 THEN 1 ELSE 0 END) AS differing,
       COALESCE(MAX(CASE WHEN nb.close IS NOT NULL AND q.close > 0
                          THEN ABS(nb.close / q.close - 1) END), 0) AS worst
FROM bars_quarantine q
LEFT JOIN symbols sy ON sy.id = q.symbol_id
LEFT JOIN bars nb ON nb.symbol_id = q.symbol_id AND nb.tf = q.tf AND nb.ts = q.ts
WHERE q.run_id = ? AND q.tf = ?
GROUP BY q.symbol_id
ORDER BY differing DESC`, tf, runID, tf)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []CohortRefetchDelta{}
	for rows.Next() {
		var d CohortRefetchDelta
		var worst float64
		if err := rows.Scan(&d.SymbolID, &d.Symbol, &d.OldBars, &d.NewBars,
			&d.Common, &d.Differing, &worst); err != nil {
			return nil, err
		}
		d.WorstPct = 100 * worst
		out = append(out, d)
	}
	return out, rows.Err()
}

// RestoreCohortRefetch undoes a re-fetch run EXACTLY, and returns how many bars
// it put back.
//
// WHY RestoreQuarantinedBars IS NOT ENOUGH HERE. That one is additive: it
// re-inserts the quarantined rows and leaves everything else alone, which is
// exact for the flat-pad runs it was written for, because nothing REPLACED the
// bars it removed. A re-fetch does replace them, and the fresh series routinely
// contains days the old one did not — ACACU came back with 318 bars where 248
// went in. An additive restore therefore leaves a UNION of the two series:
// measured on the canary, ACACU restored to 396 bars against an original 248,
// mixing both price conventions in one symbol. That is a worse state than
// either series alone, and it is what a rollback is supposed to prevent.
//
// So this deletes the symbol's current bars for the timeframe first, then
// re-inserts the quarantined ones — replace, not merge. One transaction per run
// so a crash cannot leave a symbol emptied.
//
// onlySymbol, when non-empty, undoes just that one symbol and leaves the rest of
// the run in place. That is needed because a re-fetch can be right for 649
// symbols and wrong for one: SIC's fresh bars are real market data, but they
// belong to whatever security holds the ticker NOW, and appending them to a row
// whose history is a 2021 SPAC splices two companies across a 4.8-year hole.
// Undoing the whole run to fix one symbol would discard 649 correct repairs.
//
// It is idempotent: a second call finds no quarantine rows and changes nothing.
func (s *Store) RestoreCohortRefetch(ctx context.Context, tf, runID, onlySymbol string) (int64, error) {
	if runID == "" {
		return 0, fmt.Errorf("runID must not be empty")
	}
	// One extra predicate, spelled the same way in all three statements below so
	// they cannot select different sets: an empty filter matches everything.
	const symFilter = ` AND (? = '' OR symbol_id = (SELECT id FROM symbols WHERE symbol = ? AND market = 'stocks'))`
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Clear only the symbols this run touched, only for its timeframe.
	if _, err := tx.ExecContext(ctx, `
DELETE FROM bars
 WHERE tf = ?
   AND symbol_id IN (SELECT DISTINCT symbol_id FROM bars_quarantine
                      WHERE run_id = ? AND tf = ?`+symFilter+`)`,
		tf, runID, tf, onlySymbol, onlySymbol); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `
INSERT OR REPLACE INTO bars (symbol_id, tf, ts, open, high, low, close, volume)
SELECT symbol_id, tf, ts, open, high, low, close, volume
FROM bars_quarantine WHERE run_id = ? AND tf = ?`+symFilter, runID, tf, onlySymbol, onlySymbol)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM bars_quarantine WHERE run_id = ? AND tf = ?`+symFilter,
		runID, tf, onlySymbol, onlySymbol); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
