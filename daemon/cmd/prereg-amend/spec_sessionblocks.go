package main

import (
	"context"
	"database/sql"
	"time"
)

// SessionBlocksKind registers two corrections to how the grader clusters and
// benchmarks structural regime calls, for calls at or after SessionBlocksEpochTS.
const SessionBlocksKind = "structural-session-blocks"

// SessionBlocksEpochTS is 2026-11-23T00:00:00-05:00 (05:00:00Z).
const SessionBlocksEpochTS int64 = 1795410000

type sessionBlocksMeasured struct {
	CallsFromEpoch int
}

func measureSessionBlocks(ctx context.Context, db *sql.DB) (sessionBlocksMeasured, error) {
	var m sessionBlocksMeasured
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM regime_outcomes WHERE ts >= ?`, SessionBlocksEpochTS).Scan(&m.CallsFromEpoch)
	return m, err
}

const sessionBlocksNote = "AMENDMENT - from 2026-11-23T00:00 New York, structural horizon blocks are counted in NYSE sessions, not calendar days, and the naive-persistence null excludes superseded calls as the model always has. Forward-only: no claim changes, no stored row is rewritten, and no call governed by the rule existed at filing."

func sessionBlocksSpec(m sessionBlocksMeasured) string {
	return `{` +
		`"kind":` + `"` + SessionBlocksKind + `"` + `,` +
		`"filedOn":` + `"` + time.Now().UTC().Format("2006-01-02") + `"` + `,` +
		`"amends":` + `"tools/accuracy_registry.py horizon_blocks (structural block clustering) and the naive-persistence null tally"` + `,` +
		`"correctionType":` + `"structural-block-unit-and-null-row-set"` + `,` +
		`"cutoverTs":` + itoa(int(SessionBlocksEpochTS)) + `,` +
		`"cutover":` + `"2026-11-23T00:00:00-05:00"` + `,` +
		`"claimsChanged":false,` +
		`"storedRowsRewritten":false,` +
		`"filedWithKnowledgeOfOutcome":false,` +
		`"defects":[` +
		`{"mechanism":"regime_outcomes.day is an epoch-day number (md.TradingDay), so consecutive call days jump by 3 over a weekend. horizon_blocks closed a 21-day block at day >= anchor + 21, which is 21 calendar days, about 15 NYSE sessions. Forward windows of adjacent blocks overlapped and the clustered interval counted more independent blocks than exist.","foundBy":"code audit 2026-11-10; reproduced against the database 2026-11-15"},` +
		`{"mechanism":"The model's per-day tally excluded superseded rows; the naive-persistence null's tally did not, so a superseded call counted toward the null but not the model. (Rows with no frozen naive_label are left as they are: the existing null-coverage gate discloses them and refuses a skill verdict on partial coverage, and dropping them from the model would hide that self-selection.)","foundBy":"code audit 2026-11-10; reproduced against the database 2026-11-15"}` +
		`],` +
		`"rule":{` +
		`"blocks":"for the stock structural kinds only, a block whose anchor day (regime_outcomes.day, the settle bar's trading day) is on or after the cut-over date closes when nyse_sessions_after(anchor, day) >= horizon sessions, counted on the NYSE full-holiday calendar ported from daemon/internal/marketcal; earlier blocks, the crypto structural kinds (which trade every day) and every directional horizon keep the calendar-day rule",` +
		`"nullRowSet":"for calls at or after the cut-over, the null tally excludes superseded rows exactly as the model tally does; the null-coverage gate is unchanged",` +
		`"appliesTo":"blocks: anchor day on or after 2026-11-23; null row set: calls with ts at or after cutoverTs; everything earlier keeps the rule it was frozen under"` +
		`},` +
		`"measuredStateAtFiling":{` +
		`"structural_calls_at_or_after_cutover":` + itoa(m.CallsFromEpoch) + `,` +
		`"note":"the record refuses to file unless this is zero"` +
		`},` +
		`"whyThisCutover":"the first Monday after the owner's instruction of 2026-11-16 to finish the audit's items; chosen by the calendar, not by any figure",` +
		`"notOutcomeShopping":"forward-only; chosen from the mechanism of each defect, not from any accuracy figure; filed before any call the rule governs existed",` +
		`"whatThisCannotChange":[` +
		`"every registered claim and band table",` +
		`"the horizons, kinds and baseline definitions",` +
		`"the evidence floors (minIndependentN, minDistinctDays, minDistinctBlocks), maxAlpha and the verdict rules",` +
		`"any row or block anchored before the cut-over"` +
		`],` +
		`"expectedConsequence":"post-cut-over structural blocks are about 1.4x wider, so fewer distinct blocks accrue per month and verdicts need longer to clear minDistinctBlocks; a superseded call no longer moves the null"` +
		`}`
}
