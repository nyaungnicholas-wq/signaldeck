package main

import (
	"context"
	"database/sql"
)

// This is an amendment to a forward test registered at seq 87, filed before its
// window has produced a single graded session. WHY IT EXISTS: the registered
// benchmark is every symbol carrying a 1d bar with close >= 20 on that same
// session, with no floor on how many symbols that is. In sample it is about 650
// names on every session the book actually trades, and the thinnest graded
// session carries 320. But the benchmark table also contains sessions of THREE
// names - weekends, when only crypto prints - and nothing in the registered rule
// would stop such a session grading if the book ever traded into one. A forward
// window is 60 sessions long and a feed outage is not hypothetical; one degenerate
// session would enter a permanent record and could not be removed. WHY IT IS
// HONEST NOW AND NOT LATER: the window opens on the first session strictly after
// seq 87 and no forward row has been graded yet. A breadth floor is
// direction-neutral: it cannot prefer the book because it says nothing about the
// book. Filed once evidence exists, the same amendment would be indistinguishable
// from dropping sessions that read badly.
const BenchFloorKind = "forward-test-benchmark-floor"

// the floor is set from the STRUCTURE of the two populations rather than from any
// statistic - graded sessions carry 320 names at their thinnest and degenerate
// sessions carry 3, so 100 sits roughly 3x below everything the book has ever
// been graded against and 33x above everything it has not. No value in that gap
// changes the in-sample result, which is the point: a threshold that could be
// tuned is a threshold that will be.
const MinBenchmarkNames = 100

const benchFloorNote = "AMENDMENT to the forward test at seq 87, filed BEFORE its first graded session " +
	"exists. It ADDS one eligibility criterion and relaxes nothing: a session counts " +
	"toward the statistic and the 60-session floor only if its benchmark carries at " +
	"least 100 distinct symbols, in addition to the registered requirement of at " +
	"least 5 bets. The registered benchmark DEFINITION, population, daily statistic, " +
	"test statistic, start rule, decision rule, Bonferroni family and consequence-of-failure " +
	"are untouched, and no published figure is recomputed. WHAT WAS DELIBERATELY NOT " +
	"FILED, and why it matters more than what was: an audit proposed also screening " +
	"crypto out of the benchmark and trimming single-session moves beyond 30 percent. " +
	"Both were measured first. Neither reaches the graded population - the thinnest " +
	"benchmark on any session the book actually trades is 320 names, the book has never " +
	"traded a weekend, and applying both screens moves the in-sample day-weighted mean " +
	"excess from -0.0725 percent to -0.0584 percent while leaving the sign, the verdict " +
	"and the 10-of-20 beat count unchanged. Having now MEASURED their direction, filing " +
	"them would be choosing a benchmark with a result in view, which is the exact " +
	"failure this record exists to prevent. They are recorded as considered and rejected " +
	"so that a later reader cannot mistake their absence for an oversight."

// benchFloorMeasured is read at commit time rather than transcribed because every
// number in the spec is a claim about THIS database and a number typed by hand is
// stale the moment it is typed.
type benchFloorMeasured struct {
	// RegistrationSeq is the record being amended; zero means nothing to amend.
	RegistrationSeq int
	// ObservedRows is how many forward sessions have graded so far. It must be
	// zero: an eligibility rule written with results in view is not a rule, it
	// is a selection.
	ObservedRows int
	// GradedMinNames is the thinnest benchmark across the in-sample sessions the
	// book actually traded at the 5-bet floor.
	GradedMinNames int
	// GradedSessions is how many such sessions there were.
	GradedSessions int
	// DegenerateSessions is how many benchmark sessions carry fewer names than
	// the floor. These are the ones this amendment exists to exclude.
	DegenerateSessions int
	// WouldDropNow is how many currently graded in-sample sessions the floor
	// would remove. It must be zero, or the floor is not neutral on the
	// evidence already in hand.
	WouldDropNow int
}

const benchBreadthSQL = `
WITH b AS (
  SELECT symbol_id, date(ts,'unixepoch') d, close,
         LEAD(close) OVER (PARTITION BY symbol_id ORDER BY ts) nxt
    FROM bars WHERE tf='1d'
),
bench AS (
  SELECT d, COUNT(*) n FROM b
   WHERE close >= 20 AND nxt IS NOT NULL GROUP BY d
),
ep AS (
  SELECT symbol_id, direction, episode_ts, MIN(ts) t
    FROM confluence_outcomes
   WHERE fwd_return IS NOT NULL AND ungradable IS NULL AND episode_ts IS NOT NULL
   GROUP BY symbol_id, direction, episode_ts
),
book AS (
  SELECT date(o.ts,'unixepoch') d, COUNT(*) n
    FROM ep JOIN confluence_outcomes o
      ON o.symbol_id=ep.symbol_id AND o.ts=ep.t
   WHERE o.direction=1 AND o.entry_px >= 20
   GROUP BY d
   HAVING COUNT(*) >= 5
)
SELECT COALESCE(MIN(bench.n),0), COUNT(*),
       COALESCE(SUM(CASE WHEN bench.n < ? THEN 1 ELSE 0 END),0)
  FROM book JOIN bench ON bench.d = book.d
`

const degenerateBenchSQL = `
WITH b AS (
  SELECT symbol_id, date(ts,'unixepoch') d, close,
         LEAD(close) OVER (PARTITION BY symbol_id ORDER BY ts) nxt
    FROM bars WHERE tf='1d'
)
SELECT COUNT(*) FROM (
  SELECT d, COUNT(*) n FROM b
   WHERE close >= 20 AND nxt IS NOT NULL GROUP BY d HAVING COUNT(*) < ?)
`

func measureBenchFloor(ctx context.Context, db *sql.DB) (benchFloorMeasured, error) {
	var m benchFloorMeasured

	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM prereg_records WHERE kind = ?`, ForwardTestKind).Scan(&m.RegistrationSeq)
	if err != nil {
		return benchFloorMeasured{}, err
	}

	n, err := countObservedRows(ctx, db, "confluence-long-liquid-2026-08")
	if err != nil {
		return benchFloorMeasured{}, err
	}
	m.ObservedRows = n

	err = db.QueryRowContext(ctx, benchBreadthSQL, MinBenchmarkNames).Scan(&m.GradedMinNames, &m.GradedSessions, &m.WouldDropNow)
	if err != nil {
		return benchFloorMeasured{}, err
	}

	err = db.QueryRowContext(ctx, degenerateBenchSQL, MinBenchmarkNames).Scan(&m.DegenerateSessions)
	if err != nil {
		return benchFloorMeasured{}, err
	}

	return m, nil
}

func benchFloorSpec(m benchFloorMeasured) string {
	return `{
  "kind": "forward-test-benchmark-floor",
  "amends": "forward-test-registration at seq ` + itoa(m.RegistrationSeq) + `, testId confluence-long-liquid-2026-08",
  "change": "ADDS one eligibility criterion. A session counts toward the test statistic and toward the 60-session evidence floor only if its benchmark carries at least ` + itoa(MinBenchmarkNames) + ` distinct symbols, IN ADDITION to the registered requirement that it carry at least 5 bets. Sessions failing the new criterion are recorded with eligible=0, exactly as under-populated sessions already are.",
  "reason": "The registered benchmark places no floor on its own breadth. The benchmark table contains sessions of as few as 3 symbols - weekends, when only crypto prints - and the registered rule would grade one if the book ever traded into it. A single degenerate session cannot be removed from a 60-session record once written.",
  "directionNeutral": "The criterion refers only to the benchmark symbol count and says nothing about the book, its returns, or their difference. It cannot prefer one outcome over another.",
  "whyFiledNow": "No forward session has been graded yet. An eligibility rule added after evidence accrues is a selection rule, and an append-only log cannot tell the two apart after the fact.",
  "whatThisDoesNotChange": [
    "the benchmark DEFINITION - still the equal-weighted mean next-session return of every symbol with a 1d bar and close >= 20 on that session",
    "the population, the episode dedup, the entry_px >= 20 floor and the direction = +1 restriction",
    "the daily statistic, the test statistic, and the SESSION as the unit of observation",
    "the start rule and the prohibition on backfill",
    "the 5-bet eligibility floor and the 60-session evidence floor, both of which still apply in full",
    "the decision rule, the Bonferroni family size and the consequence of failure",
    "every published accuracy, null, interval and skill figure"
  ],
  "consideredAndRejected": "An audit also proposed excluding crypto from the benchmark and trimming single-session moves beyond 30 percent. Both were measured before this record was drafted: neither reaches the graded population, and applying both moves the in-sample day-weighted mean excess from -0.0725 percent to -0.0584 percent with the sign, the verdict and the 10-of-20 beat count unchanged. Their direction is therefore KNOWN, and filing them would be selecting a benchmark with a result in view. They are rejected on that ground, not on their merits as data hygiene.",
  "measuredStateAtFiling": {
    "registrationSeq": ` + itoa(m.RegistrationSeq) + `,
    "observedForwardRows": ` + itoa(m.ObservedRows) + `,
    "inSampleGradedSessions": ` + itoa(m.GradedSessions) + `,
    "thinnestBenchmarkOnAGradedSession": ` + itoa(m.GradedMinNames) + `,
    "benchmarkSessionsBelowTheFloor": ` + itoa(m.DegenerateSessions) + `,
    "gradedSessionsThisWouldDropToday": ` + itoa(m.WouldDropNow) + `,
    "note": "The floor sits far below every benchmark the book has ever been graded against and far above every degenerate one. It drops nothing currently graded, which is why it cannot have been chosen for its effect."
  }
}`
}
