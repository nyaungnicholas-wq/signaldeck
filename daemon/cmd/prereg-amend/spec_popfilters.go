package main

import (
	"context"
	"database/sql"
)

const PopFiltersKind = "forward-test-population-filters"

const popFiltersNote = `This amendment to seq 87 (testId "confluence-long-liquid-2026-08") is filed before any graded session, so it does not duplicate the benchmark-floor record. It adds three screens that change which rows are in the population:` +
	`
- market='stocks' on both legs. The registration said "every symbol carrying a 1d bar with close >= 20"; the database holds 7 crypto symbols against 2,943 stocks, so crypto sat silently in the benchmark and the book. On Fridays the benchmark also averaged 1-day crypto returns against 3-day stock returns, because LEAD is per-symbol and crypto prints Saturdays.` +
	`
- ABS(next/close - 1) <= 0.30 on the benchmark forward leg. The book side is already guarded; the benchmark had nothing. 29 rows since 2026-07-15 exceed 30% in one day, and DFNS alone contributes +109%, +69%, -67%, +122% across 07-28..07-31. This is not an unrepaired split: the split repairer inspected DFNS nine times and recorded a real move each time. It is a microcap squeeze equal-weighted against AAPL in a benchmark with no liquidity screen, and the asymmetry is the defect (such a name is ~0.15% of a 660-name benchmark and up to 12.5% of an 8-bet book session).` +
	`
- the grader's own stale-feed quarantine, dq_events(kind='stale'), which tools/accuracy_registry.py already applies and this did not, so the two disagreed about what a live feed is. 391 symbols stopped printing on 2026-08-05, halving the benchmark universe mid-window, and all had been flagged stale.` +
	`
Why now: forward_test_daily holds no rows; after a session grades the same screens would be a post-hoc population change an append-only log cannot distinguish.` +
	`
What it cannot change: the session unit, the 60-session floor, the 5-bet rule, the Bonferroni decision rule, the start date, and the consequence of failure: the book is not traded and no execution layer is built.` +
	`
Direction of effect: the screens narrow the population and cannot flatter it — measured, the 2026-08-11 benchmark moves from +1.090% to +0.895%, making that session's excess worse for the book.`

type popFiltersMeasured struct {
	RegistrationSeq int // seq of the forward-test registration being amended
	ObservedRows    int // number of rows observed for the testId
	CryptoSymbols   int // count of symbols with market='crypto'
	ExtremeRows     int // count of 1d bars where close>=20 and ABS(next/close-1)>0.30
	StaleFlagged    int // distinct symbols flagged stale in dq_events
	BenchNamesWeek  int // minimum daily count of benchmark names (stocks) from 2026-08-14 onward
	BenchNamesRest  int // maximum daily count of benchmark names (stocks) from 2026-08-14 onward
}

func measurePopFilters(ctx context.Context, db *sql.DB) (popFiltersMeasured, error) {
	var m popFiltersMeasured
	var err error

	err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM prereg_records WHERE kind = ?`, ForwardTestKind).Scan(&m.RegistrationSeq)
	if err != nil {
		return m, err
	}

	m.ObservedRows, err = countObservedRows(ctx, db, "confluence-long-liquid-2026-08")
	if err != nil {
		return m, err
	}

	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM symbols WHERE market = 'crypto'`).Scan(&m.CryptoSymbols)
	if err != nil {
		return m, err
	}

	err = db.QueryRowContext(ctx, `
		WITH bar_data AS (
			SELECT
				symbol_id,
				close,
				LEAD(close) OVER (PARTITION BY symbol_id ORDER BY ts) AS nxt
			FROM bars
			WHERE tf = '1d' AND ts >= strftime('%s', '2026-07-15')
		)
		SELECT COUNT(*) FROM bar_data
		WHERE close >= 20 AND nxt IS NOT NULL AND ABS(nxt/close - 1) > 0.30
	`).Scan(&m.ExtremeRows)
	if err != nil {
		return m, err
	}

	err = db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT symbol_id) FROM dq_events WHERE kind = 'stale' AND symbol_id IS NOT NULL`).Scan(&m.StaleFlagged)
	if err != nil {
		return m, err
	}

	err = db.QueryRowContext(ctx, `
		WITH b AS (
			  SELECT date(ts,'unixepoch') AS day, close,
			         LEAD(close) OVER (PARTITION BY symbol_id ORDER BY ts) AS nxt
			    FROM bars WHERE tf = '1d'
			),
			daily_counts AS (
			  -- The REGISTERED benchmark predicate, deliberately WITHOUT the
			  -- market screen this amendment adds: the point is to record what
			  -- the population looked like BEFORE the change, so a reader can
			  -- check the note's claim that weekend sessions carry three names.
			  SELECT day, COUNT(*) AS cnt FROM b
			   WHERE close >= 20 AND nxt IS NOT NULL AND day >= '2026-08-14'
			   GROUP BY day
			)
		SELECT MIN(cnt), MAX(cnt) FROM daily_counts`).
		Scan(&m.BenchNamesWeek, &m.BenchNamesRest)
	if err != nil {
		return m, err
	}

	return m, nil
}

func popFiltersSpec(m popFiltersMeasured) string {
	return "{\"kind\":\"" + PopFiltersKind + "\",\"amends\":" + itoa(m.RegistrationSeq) +
		",\"testId\":\"confluence-long-liquid-2026-08\",\"filedBeforeAnyGradedSession\":true," +
		"\"screensAdded\":{\"market\":\"stocks\",\"extremeMove\":true,\"staleFeed\":true}," +
		"\"measuredStateAtFiling\":{\"RegistrationSeq\":" + itoa(m.RegistrationSeq) +
		",\"ObservedRows\":" + itoa(m.ObservedRows) +
		",\"CryptoSymbols\":" + itoa(m.CryptoSymbols) +
		",\"ExtremeRows\":" + itoa(m.ExtremeRows) +
		",\"StaleFlagged\":" + itoa(m.StaleFlagged) +
		",\"BenchNamesWeek\":" + itoa(m.BenchNamesWeek) +
		",\"BenchNamesRest\":" + itoa(m.BenchNamesRest) +
		"},\"directionOfEffect\":\"narrows\",\"whatThisCannotChange\":[\"session unit\",\"60-session floor\",\"5-bet rule\",\"Bonferroni decision rule\",\"start date\",\"consequence of failure: the book is not traded and no execution layer is built\"]}"
}
