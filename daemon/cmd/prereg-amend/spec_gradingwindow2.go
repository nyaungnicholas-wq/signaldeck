package main

import (
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// GradingWindow2Kind re-registers the directional grading window a SECOND
// time, from 2026-08-07 to 2026-09-25. Its own kind because the first record
// (seq 117, GradingWindowKind) is a one-time fact the duplicate guard will not
// file twice, and this one states a different fact.
//
// WHY. Two defects compounded, both fixed before filing:
//  1. The fleet calibration fit (store.ResolvedRawPredictionPairs) had no
//     grading-epoch filter, so it fit 53 days instead of the graded window and
//     its near-flat map squeezed 1d probabilities into ~1.8pp around 0.48:
//     every symbol was called DOWN, one market-wide call. Fixed by a9383b0,
//     live from 2026-09-25 03:47:47 UTC (the first prediction-runner run on
//     c38f712).
//  2. The 1d resolver read one oldest-first batch of 1500 rows that its gap
//     guard skipped for ever, so from ~2026-09-10 no squeezed row was graded
//     and the gate never saw one. Fixed by 5c9940b on 2026-09-30; the first
//     rows it graded put 1d 2026-09-14 (28 distinct across 324 symbols) in the
//     window and the gate refused the whole directional table.
//
// The new epoch is the first UTC day the repaired calibration served. Every
// symbol's latest 2026-09-25 forecast (the row the grader keeps) is after
// 03:47:47 UTC; the guard re-measures cleanliness at filing time.
const GradingWindow2Kind = "grading-window-reregistration-2"

const (
	window2OldEpochTS int64 = 1786060800         // 2026-08-07T00:00:00Z, set by seq 117
	window2NewEpochTS       = store.GradingEpoch // 2026-09-25T00:00:00Z
)

const gradingWindow2Note = "AMENDMENT — the directional grading window's start moves a second time, from " +
	"2026-08-07 to 2026-09-25, the first UTC day the repaired calibration served. FILED WITH KNOWLEDGE OF " +
	"AN OUTCOME, stated plainly: at filing the publication gate reads REFUSED over a collapsed 1d " +
	"cross-section (2026-09-14: every symbol called DOWN by a calibration map squeezed to ~1.8pp), the " +
	"directional ensemble is already retired on its own record, and the last published grade read NO " +
	"SKILL for 1d and INSUFFICIENT DAYS for 1w. This alters no accuracy figure, null, interval, evidence " +
	"floor, auto-retire rule or verdict map. It removes the days whose forecasts were one repeated " +
	"market-wide call produced by a now-fixed defect, and with them the dispersed 08-07..09-24 days, " +
	"which is the cost of a single window start. Measured at filing, the window it opens holds ZERO " +
	"collapsed cross-sections on the gate's own ruler. The expected consequence is INSUFFICIENT DAYS for " +
	"both horizons until the credible-day floor (10) is met again; nothing publishes before then. The " +
	"survivorship epoch does not move, and the 2026-09-21 record (seq 117) stands unaltered above this one."

func gradingWindow2Spec(m gradingWindowMeasured) string {
	return `{
  "kind": "` + GradingWindow2Kind + `",
  "filedOn": "` + time.Now().UTC().Format("2006-01-02") + `",
  "amends": "store.GradingEpoch / tools/accuracy_registry.py GRADING_EPOCH = 2026-08-07 (grading-window-reregistration, seq 117)",
  "correctedTo": "store.GradingEpoch / tools/accuracy_registry.py GRADING_EPOCH = 2026-09-25",
  "correctionType": "grading-window-start-only",
  "claimsChanged": false,
  "filedWithKnowledgeOfOutcome": true,
  "survivorshipEpochMoves": false,
  "defect": {
    "one": "The fleet calibration fit read every resolved pair since the survivorship epoch (no grading-epoch filter), so its near-flat map squeezed 1d probabilities into ~1.8pp around 0.48 and every symbol was called DOWN: one market-wide call per day, which the publication gate classifies as a collapsed cross-section.",
    "two": "The 1d resolver re-read one oldest-first batch of 1500 permanently skipped rows from ~2026-09-10, so the squeezed days were never graded and the gate could not see them until that was fixed."
  },
  "fixedBy": {
    "calibration": "a9383b0 (calibration fit on the graded window), live from 2026-09-25T03:47:47Z (first prediction-runner run on c38f712)",
    "resolver": "5c9940b (resolver reads past permanently skipped rows), 2026-09-30"
  },
  "measuredStateAtFiling": {
    "graded_days_from_old_epoch": ` + itoa(m.TotalDaysOld) + `,
    "collapsed_from_old_epoch": {
      "1d": ` + jsonStringList(m.CollapsedOld["1d"]) + `,
      "1w": ` + jsonStringList(m.CollapsedOld["1w"]) + `
    },
    "collapsed_from_new_epoch": {
      "1d": ` + jsonStringList(m.CollapsedNew["1d"]) + `,
      "1w": ` + jsonStringList(m.CollapsedNew["1w"]) + `
    },
    "raw_forecast_days_from_new_epoch": {
      "1d": ` + itoa(m.RawDaysNew["1d"]) + `,
      "1w": ` + itoa(m.RawDaysNew["1w"]) + `,
      "note": "raw distinct call days on the gate's own dedup; the grader further drops thin, unsettled and stale-feed days, so credible days will be fewer"
    }
  },
  "whyThisEpoch": "2026-09-25 is the first UTC day served by the repaired calibration (live 03:47:47Z); every symbol's latest forecast that day, the row the grader keeps, postdates the fix. The epoch is chosen by when the defect ended and by the collapse table on the gate's ruler, not by any accuracy number; measuredStateAtFiling.collapsed_from_new_epoch must be empty for this record to file.",
  "whatThisCannotChange": [
    "every accuracy, null, interval and skill figure",
    "the evidence floors (30 independent observations, 10 distinct credible days)",
    "the auto-retire rule and its frozen thresholds",
    "the collapse detector (forecastmon.MinDistinctRatio 0.15, MinSymbolsForCollapse 30)",
    "the survivorship epoch used for listing-status reconstruction, which stays 2026-07-24",
    "the retirement already on record for the directional ensemble",
    "the first re-registration (seq 117), which stays on the chain unaltered"
  ],
  "expectedConsequence": "INSUFFICIENT DAYS for both horizons on the rows in hand: the new window holds only the days since 2026-09-25, below the 10-credible-day floor. That is a count, not a verdict; nothing publishes until it clears, and when it does the number published is whatever the record says."
}`
}
