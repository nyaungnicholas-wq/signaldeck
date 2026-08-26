package main

import (
	"context"
	"database/sql"
)

// BookExtremeKind puts the SAME extreme-move screen on the book leg that seq 90
// already put on the benchmark, and corrects that record's stated reason.
const BookExtremeKind = "forward-test-book-extreme-guard"

const bookExtremeNote = `This amendment to seq 87 (testId "confluence-long-liquid-2026-08") applies ABS(fwd_return) <= 0.30 to the BOOK leg, matching the screen seq 90 applied to the benchmark forward leg.` +
	`
It exists because seq 90's stated justification is FALSE, and an append-only chain cannot quietly become correct. Seq 90 reads: "ABS(next/close - 1) <= 0.30 on the benchmark forward leg. The book side is already guarded; the benchmark had nothing." The book side is NOT guarded and never was. There is no extreme-move screen anywhere on the book path: the only value confluence_outcomes.ungradable ever takes is the 2026-08-21 entry-bar retirement, and relWeakPct=0.30 in internal/confluence is a cross-sectional ranking percentile, not a return cap. One graded book episode moves +36.07%, uncapped. So seq 90 did not remove an asymmetry, it created one — benchmark winsorised, book not — while recording that the opposite was true.` +
	`
Why this is not a benchmark chosen with a result in view, which seq 89 rightly forbids. Seq 89 rejected the 30% trim because its direction of effect was already MEASURED and non-zero. Here the measured effect is exactly ZERO: mean daily excess stays -0.1340% and the beat count stays 10 of 19 eligible sessions. The reason is recorded below rather than hoped for — the single extreme episode falls in session 2026-07-15, which the registered 100-name benchmark breadth floor (seq 89) already refuses at 43 names, so it never reaches the statistic. Prospectively the direction is genuinely unknown: a future >30% winner excluded hurts the book, a >30% loser excluded helps it.` +
	`
This record therefore does two things a later reader must be able to separate. It adds a screen, and it retracts a factual claim made by seq 90. The screen is new; the claim was always wrong.` +
	`
Why now: forward_test_daily holds no rows. After a session grades, a population change is one an append-only log cannot distinguish from a post-hoc one, and this command refuses to file once any row exists.` +
	`
What it cannot change: the session unit, the 60-session floor, the 5-bet rule, the Bonferroni decision rule, the start date, and the consequence of failure: the book is not traded and no execution layer is built for it.`

type bookExtremeMeasured struct {
	RegistrationSeq int // seq of the forward-test registration being amended
	ObservedRows    int // forward_test_daily rows for the testId; must be 0
	GradedEpisodes  int // episode-deduped book episodes in the registered population
	ExtremeEpisodes int // of those, how many move more than 30%
	// ExtremeInGradableSession is the number that fall in a session whose
	// benchmark clears the registered 100-name breadth floor — i.e. the ones
	// that would actually MOVE the statistic. It must be 0, or this amendment
	// has a known direction and seq 89's objection applies to it in full.
	ExtremeInGradableSession int
}

// The registered population, in one place. This is BOOK_SQL's WHERE clause and
// must stay identical to tools/forward_test.py: a measurement taken over a
// different population than the grader uses would describe a different claim.
const bookEpisodeCTE = `
WITH ep AS (
  SELECT symbol_id, direction, episode_ts, MIN(ts) t
    FROM confluence_outcomes
   WHERE fwd_return IS NOT NULL AND ungradable IS NULL AND episode_ts IS NOT NULL
   GROUP BY symbol_id, direction, episode_ts
),
book AS (
  SELECT date(o.ts,'unixepoch') d, o.fwd_return r
    FROM ep JOIN confluence_outcomes o
           ON o.symbol_id = ep.symbol_id AND o.ts = ep.t
    JOIN symbols sy ON sy.id = o.symbol_id AND sy.market = 'stocks'
   WHERE o.direction = 1 AND o.entry_px >= 20
)`

func measureBookExtreme(ctx context.Context, db *sql.DB) (bookExtremeMeasured, error) {
	var m bookExtremeMeasured
	var err error

	if err = db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0) FROM prereg_records WHERE kind = ?`,
		ForwardTestKind).Scan(&m.RegistrationSeq); err != nil {
		return m, err
	}
	if m.ObservedRows, err = countObservedRows(ctx, db, "confluence-long-liquid-2026-08"); err != nil {
		return m, err
	}

	if err = db.QueryRowContext(ctx, bookEpisodeCTE+`
		SELECT COUNT(*), COALESCE(SUM(ABS(r) > 0.30), 0) FROM book
	`).Scan(&m.GradedEpisodes, &m.ExtremeEpisodes); err != nil {
		return m, err
	}

	// Count the extreme episodes that sit in a session the benchmark can
	// actually grade. This mirrors BENCH_SQL's breadth count EXACTLY — the
	// LEAD, the price floor, the extreme screen and the stale-feed screen.
	//
	// A first version dropped the last three on the reasoning that "only the
	// SIZE matters", and its own guard caught it: it counted session
	// 2026-07-15's benchmark as clearing 100 when the grader counts 43, and so
	// refused to file over a number no grader will ever use. Two spellings of
	// one quantity is the exact defect class this amendment exists to correct
	// on the other leg, and writing a comment to justify the divergence did not
	// make the divergence true.
	err = db.QueryRowContext(ctx, `
		WITH stale_feed AS (
		  SELECT DISTINCT symbol_id, date(ts - 18000, 'unixepoch') AS sd
		    FROM dq_events WHERE kind = 'stale' AND symbol_id IS NOT NULL
		),
		bb AS (
		  SELECT b.symbol_id, b.ts, date(b.ts,'unixepoch') d, b.close,
		         LEAD(b.close) OVER (PARTITION BY b.symbol_id ORDER BY b.ts) nxt
		    FROM bars b
		    JOIN symbols sy ON sy.id = b.symbol_id AND sy.market = 'stocks'
		   WHERE b.tf = '1d'
		),
		bench AS (
		  SELECT d, COUNT(*) n
		    FROM bb
		   WHERE close >= 20 AND nxt IS NOT NULL
		     AND ABS(nxt/close - 1) <= 0.30
		     AND NOT EXISTS (SELECT 1 FROM stale_feed f
		                      WHERE f.symbol_id = bb.symbol_id AND f.sd = bb.d)
		   GROUP BY d
		),
		ep AS (
		  SELECT symbol_id, direction, episode_ts, MIN(ts) t
		    FROM confluence_outcomes
		   WHERE fwd_return IS NOT NULL AND ungradable IS NULL AND episode_ts IS NOT NULL
		   GROUP BY symbol_id, direction, episode_ts
		),
		book AS (
		  SELECT date(o.ts,'unixepoch') d, o.fwd_return r
		    FROM ep JOIN confluence_outcomes o
		           ON o.symbol_id = ep.symbol_id AND o.ts = ep.t
		    JOIN symbols sy ON sy.id = o.symbol_id AND sy.market = 'stocks'
		   WHERE o.direction = 1 AND o.entry_px >= 20
		)
		SELECT COUNT(*) FROM book
		  JOIN bench ON bench.d = book.d
		 WHERE ABS(book.r) > 0.30 AND bench.n >= 100
	`).Scan(&m.ExtremeInGradableSession)
	return m, err
}

func bookExtremeSpec(m bookExtremeMeasured) string {
	return "{\"kind\":\"" + BookExtremeKind + "\",\"amends\":" + itoa(m.RegistrationSeq) +
		",\"testId\":\"confluence-long-liquid-2026-08\",\"filedBeforeAnyGradedSession\":true," +
		"\"screenAdded\":{\"leg\":\"book\",\"rule\":\"ABS(fwd_return) <= 0.30\"}," +
		"\"retracts\":{\"seq\":90,\"claim\":\"The book side is already guarded\"," +
		"\"why\":\"no extreme-move screen exists on the book path; one graded episode moves +36.07%\"}," +
		"\"measuredStateAtFiling\":{\"RegistrationSeq\":" + itoa(m.RegistrationSeq) +
		",\"ObservedRows\":" + itoa(m.ObservedRows) +
		",\"GradedEpisodes\":" + itoa(m.GradedEpisodes) +
		",\"ExtremeEpisodes\":" + itoa(m.ExtremeEpisodes) +
		",\"ExtremeInGradableSession\":" + itoa(m.ExtremeInGradableSession) +
		"},\"directionOfEffect\":\"none measured; unknown prospectively\"," +
		"\"whatThisCannotChange\":[\"session unit\",\"60-session floor\",\"5-bet rule\",\"Bonferroni decision rule\",\"start date\",\"consequence of failure: the book is not traded and no execution layer is built\"]}"
}
