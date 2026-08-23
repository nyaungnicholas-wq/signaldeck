package main

import (
	"context"
	"database/sql"
)

// The first record this command files that is not an amendment. It registers a
// NEW forward test: a claim whose evidence does not exist yet and cannot be
// assembled from anything already on disk.
//
// WHY IT IS ITS OWN KIND. Every other kind here corrects something already on
// the chain. This one commits to a measurement that has not been taken, so its
// pre-flight asks the opposite question: not "is the defect I describe real?"
// but "is the outcome still unknown?". A forward test filed after its own
// window has opened is not a forward test.

// ForwardTestKind registers one falsifiable hypothesis and the exact rule that
// will decide it, before any of its evidence accrues.
const ForwardTestKind = "forward-test-registration"

const forwardTestNote = "REGISTRATION — a new forward test, not an amendment. It commits, before the " +
	"evidence exists, to the rule that will decide whether the confluence long book at or above $20 " +
	"is worth trading. FILED WITH FULL KNOWLEDGE OF AN IN-SAMPLE RESULT, stated here rather than " +
	"buried: the hypothesis was SELECTED after slicing five price buckets against two directions, " +
	"roughly ten comparisons, and keeping the cell that looked best. A selected cell is exactly what " +
	"in-sample statistics cannot validate, which is the whole reason this record exists. The in-sample " +
	"reading is also not the encouraging one: weighted by TRADE the excess over a matched universe is " +
	"+0.332%, but weighted by SESSION — the honest unit, since trades within a session are one market " +
	"call repeated — it is -0.435%, beating the benchmark on 10 of 24 sessions. A single session, " +
	"2026-07-15, supplies 290 of the 791 episodes, so the trade-weighted figure is very nearly one day " +
	"counted 290 times. The registered expectation is therefore REFUTATION, and the decision rule is " +
	"written so that a null result is the default outcome rather than a disappointment to be explained " +
	"away. This record changes nothing already on the chain: no existing claim, band table, accuracy " +
	"figure, null, interval, evidence floor, retire rule or verdict moves, and no published number is " +
	"recomputed. It only binds a future reading of data that does not yet exist."

// forwardTestMeasured is the live state read at commit time. It is measured
// rather than transcribed for the same reason every other kind measures: the
// daemon writes continuously and a number typed into a spec is stale the moment
// it is typed.
type forwardTestMeasured struct {
	// PriorSessions is how many sessions of this population are ALREADY on
	// record. The pre-flight uses it to prove the window has not opened.
	PriorSessions int
	// EligibleBets is the episode-deduped long-book count at or above $20 that
	// already exists — the in-sample body this test deliberately does not use.
	EligibleBets int
	// NewestSession bounds that body, so a reader can see exactly where the
	// registered window has to start to avoid overlapping it.
	NewestSession string
	// ObservedRows is how many GRADED forward observations already exist for
	// this test id. It must be zero. A forward test whose results can already
	// be read is not a registration, it is a report with the dates rearranged,
	// and the chain cannot tell the two apart after the fact.
	ObservedRows int
}

// countObservedRows reports how many graded rows the forward test already has.
// A missing table is zero, not an error: the grading tool creates it on first
// run, so "not there yet" is the expected state at registration time and the
// only honest reading of it.
func countObservedRows(ctx context.Context, db *sql.DB, testID string) (int, error) {
	var present int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='forward_test_daily'`,
	).Scan(&present); err != nil {
		return 0, err
	}
	if present == 0 {
		return 0, nil
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM forward_test_daily WHERE test_id = ?`, testID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// measureForwardTest reads the in-sample body this registration excludes. The
// population predicate is identical to the one the spec registers, so the two
// cannot drift apart: if this query is wrong, the test grades a different book
// than the one described.
func measureForwardTest(ctx context.Context, db *sql.DB) (forwardTestMeasured, error) {
	const query = `
		WITH ep AS (
		  SELECT symbol_id, direction, episode_ts, MIN(ts) t
		    FROM confluence_outcomes
		   WHERE fwd_return IS NOT NULL AND ungradable IS NULL AND episode_ts IS NOT NULL
		   GROUP BY symbol_id, direction, episode_ts
		)
		SELECT COUNT(DISTINCT date(o.ts,'unixepoch')),
		       COUNT(*),
		       COALESCE(MAX(date(o.ts,'unixepoch')),'')
		  FROM ep JOIN confluence_outcomes o
		    ON o.symbol_id=ep.symbol_id AND o.ts=ep.t
		 WHERE o.direction=1 AND o.entry_px >= 20`
	var m forwardTestMeasured
	if err := db.QueryRowContext(ctx, query).Scan(
		&m.PriorSessions, &m.EligibleBets, &m.NewestSession); err != nil {
		return forwardTestMeasured{}, err
	}
	n, err := countObservedRows(ctx, db, "confluence-long-liquid-2026-08")
	if err != nil {
		return forwardTestMeasured{}, err
	}
	m.ObservedRows = n
	return m, nil
}

func forwardTestSpec(m forwardTestMeasured) string {
	return `{
  "kind": "forward-test-registration",
  "testId": "confluence-long-liquid-2026-08",
  "question": "Does the confluence long book, restricted to entries at or above $20, produce a POSITIVE DAY-WEIGHTED mean excess return over a matched same-session universe benchmark?",
  "unitOfObservation": "The trading SESSION, never the trade. Bets placed within one session share one market call, so averaging over trades counts that call once per position and manufactures significance out of position count. Trade-weighting is precisely what produced the in-sample illusion this test exists to check.",
  "population": "confluence_outcomes, episode-deduped to one bet per (symbol, direction, episode_ts), direction = +1, entry_px >= 20, ungradable IS NULL, horizon 1d.",
  "benchmark": "Equal-weighted mean next-session return of EVERY symbol carrying a 1d bar with close >= 20 on that same session. The book must beat holding its own universe, not zero.",
  "dailyStatistic": "mean(book signed return for the session) - mean(benchmark for the session).",
  "testStatistic": "The unweighted mean of the daily excess across all eligible sessions. Every session counts once, whatever its position count.",
  "startRule": "The first session STRICTLY AFTER this record's timestamp. No backfill, ever. Sessions on or before the timestamp are in-sample by construction and are excluded even if they would help.",
  "minimumEvidence": "60 distinct eligible sessions before any verdict may be stated. A session is eligible only if it carries at least 5 bets; sessions below that are recorded with eligible=0 and excluded from the statistic. Both thresholds are fixed HERE, before the data exists, so that neither can be chosen later to suit a result.",
  "decisionRule": "PASS only if the mean daily excess is greater than zero AND the one-sided 95% lower confidence bound, Bonferroni-corrected across the registered family, is also greater than zero. Anything else is REFUTED. There is no third outcome and no extension of the window to reach one.",
  "consequenceOfFailure": "The book is not traded and no execution layer is built for it. A refuted test ends the line of work rather than starting a search for a better subset.",
  "knownWeakness": "The hypothesis is a SELECTED cell: five price buckets crossed with two directions, roughly ten comparisons, best cell kept. In sample it reads +0.332% excess per trade but -0.435% per session, beating the benchmark on 10 of 24 sessions, and 2026-07-15 alone contributes 290 of 791 episodes. The short book is excluded for a separate measured reason: its loss is tail-driven (median +0.372%, six episodes worse than -50%, worst -533%), not directional.",
  "measuredStateAtFiling": {
    "priorSessions": ` + itoa(m.PriorSessions) + `,
    "eligibleBets": ` + itoa(m.EligibleBets) + `,
    "newestSession": "` + m.NewestSession + `",
    "note": "This is the in-sample body the test does NOT use. It is recorded so a reader can verify the registered window does not overlap it."
  },
  "whatThisCannotChange": [
    "every published accuracy, null, interval and skill figure",
    "the frozen predictor specs and their band tables",
    "the evidence floors of 30 independent observations and 10 distinct UTC days",
    "the auto-retire rule and its thresholds",
    "the Bonferroni family and looks accounting",
    "the publication gate and its collapsed cross-section refusal"
  ]
}`
}
