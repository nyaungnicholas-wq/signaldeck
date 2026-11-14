package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// StructSettledKind registers the settled-bar rule for the structural family.
//
// WHY. The structural runner loaded daily bars without the settled-bar trim
// every other runner applies. It runs every six hours, so the first call of a
// trading day was usually made mid-session from a bar still forming. The
// naive-persistence baseline was frozen at a later tick from whatever that bar
// had become, and the resolver checked only that enough forward bars existed,
// not that the last one had closed. Found by code audit on 2026-11-10 and
// confirmed independently by the owner the same week.
//
// WHAT CHANGES, for calls made at or after store.StructSettledEpochTS only:
// the call is built from the last daily bar settled at call time; the baseline
// is frozen at that same bar; the forward window is counted from that bar; and
// a row resolves only when the bar that closes its window has settled.
//
// WHAT DOES NOT. No claim, band table, horizon, kind, baseline definition,
// evidence floor or verdict rule. No stored row is rewritten: calls frozen
// before the cut-over keep the rule they were frozen under.
//
// The record files only while no call at or after the cut-over exists, so the
// rule cannot have been chosen with its own outcomes, or even its own calls,
// readable.
const StructSettledKind = "structural-settled-bar"

type structSettledMeasured struct {
	CallsFromEpoch int // regime_outcomes rows with ts at or after the cut-over
}

func measureStructSettled(ctx context.Context, db *sql.DB) (structSettledMeasured, error) {
	var m structSettledMeasured
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM regime_outcomes WHERE ts >= ?`, store.StructSettledEpochTS).Scan(&m.CallsFromEpoch)
	return m, err
}

const structSettledNote = "AMENDMENT - from 2026-11-16T00:00 New York, structural regime calls are built from, " +
	"frozen against and resolved on settled daily bars only. Forward-only: no claim changes, no stored row " +
	"is rewritten, and no call governed by the rule existed at filing."

func structSettledSpec(m structSettledMeasured) string {
	return `{
  "kind": "` + StructSettledKind + `",
  "filedOn": "` + time.Now().UTC().Format("2006-01-02") + `",
  "amends": "pipeline.VolRegimeRunner (structural predictor input), pipeline.RegimeOutcomeWorker (naive-persistence baseline freeze and resolution)",
  "correctionType": "structural-call-base-bar-and-resolution-settlement",
  "cutoverTs": ` + itoa(int(store.StructSettledEpochTS)) + `,
  "cutover": "2026-11-16T00:00:00-05:00",
  "claimsChanged": false,
  "storedRowsRewritten": false,
  "filedWithKnowledgeOfOutcome": false,
  "defect": {
    "mechanism": "The structural runner read daily bars up to now with no settled-bar trim and runs every six hours, so the first call of a trading day was usually built from a bar still forming. The baseline was frozen at a later tick from the same bar in whatever state it had reached, and the resolver required only that enough forward bars existed, not that the closing bar of the window had settled.",
    "foundBy": "code audit 2026-11-10; confirmed independently by the owner"
  },
  "rule": {
    "predictor": "daily bars are trimmed to the last settled bar before the call is computed",
    "baseline": "frozen at the last daily bar settled as of the call timestamp, the bar the predictor used",
    "forwardWindow": "counted in bars from that same base bar",
    "resolution": "a row resolves only when the bar that closes its window has settled; until then it stays pending",
    "appliesTo": "calls with ts at or after cutoverTs; earlier calls keep the rule they were frozen under"
  },
  "measuredStateAtFiling": {
    "structural_calls_at_or_after_cutover": ` + itoa(m.CallsFromEpoch) + `,
    "note": "the record refuses to file unless this is zero"
  },
  "whyThisCutover": "the start of the first trading week after the owner's decision of 2026-11-13; chosen by the calendar, not by any figure",
  "notOutcomeShopping": "forward-only; chosen from the mechanism of the defect, not from any accuracy figure; filed before any call the rule governs existed",
  "whatThisCannotChange": [
    "every registered claim and band table",
    "the horizons, kinds and baseline definitions",
    "the evidence floors and verdict rules",
    "any row frozen before the cut-over"
  ],
  "expectedConsequence": "calls made mid-session are built from the previous session's close instead of the forming bar; rows whose closing bar is still forming wait one more pass before resolving"
}`
}
