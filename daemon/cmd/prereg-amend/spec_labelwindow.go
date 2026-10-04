package main

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"
)

// LabelWindowKind amends the label base and grading window start to fix SD-30.
// WHY. The directional 1-day and 1-week outcome labels leaked because the
// resolver's base bar was the newest SETTLED daily bar at issue, but stock bars
// only settle at 22:00 ET. The broad-universe pass at 00:00-02:00 UTC used the
// previous session as base, grading a move that had already happened.
// Measured: 93.8% of kept stocks 1d outcomes matched the move already visible
// at issue (crypto 78.6%). At 1w, stocks accuracy was 0.713 when the call
// agreed with the visible move vs 0.232 when it disagreed.
// Since 2026-10-02 every 1d/1w directional figure is WITHHELD on all public
// and member surfaces (publication.SD30Withheld).
// Owner's decision (2026-10-02/03): option (b), "label from the issue day's
// session close to the next close", read as: base = the issue day's own daily
// bar (the latest 1d bar at or before issue, the bar settle_ts already names
// and the grader groups by). This fixes stock rows issued after the close,
// stock rows issued in-session, and crypto.
// 1w = calendar week from the new base (5 sessions, 4 in a holiday week; 7
// days crypto), the arithmetic the grader's settlement check already uses.
// Residual accepted by the owner: a stock row issued after the close still
// sees the 16:00 ET-to-issue after-hours move. Expected visible-move
// agreement is about 55% instead of 93.8% (the score_outcomes probe measured
// 54-57%).
// Rejected alternatives: (a) grade only pre-open rows (drops ~62% of rows);
// (c) keep the figures withheld indefinitely.
// Fix: pipeline.labelBaseSinceTs = 1791072000 (2026-10-04T00:00:00Z) in
// daemon/internal/pipeline/settledbase.go. For rows at/after it the base is
// the issue day's own bar. If that bar is missing (a whole stock session
// closed after the at-or-before bar and before issue, or a crypto bar a day
// or more old), the row stays pending instead of grading across a visible
// session. Rows before the cutoff keep the rule they were frozen under.
// No label is ever rewritten and nothing is backfilled.
// Window: store.GradingEpoch / tools/accuracy_registry.py GRADING_EPOCH move
// from 2026-09-25 (seq 130, grading-window-reregistration-2) to 2026-10-04,
// so no row graded on the leaking label is in the graded window. The grader's
// only code change is that constant (GRADING_EPOCH and the GRADING_EPOCH_TS derived from it); the registrar re-pins it automatically
// from the committed tree at deploy.
// What this alters: no accuracy figure, null, interval, evidence floor (30
// independent observations, 10 distinct credible days), auto-retire rule,
// verdict map, collapse detector, or the survivorship epoch (stays 2026-07-24).
// Seq 117 and seq 130 stay on the chain unaltered. The retirement already on
// record for the directional ensemble stands.
// Answer the "no swapping the resolution rule after seeing outcomes"
// principle head-on:
//   - the change is forward-only: only rows issued at/after 2026-10-04 use it;
//   - it was chosen by measuring the leak's mechanism (visible-move agreement),
//     not by any accuracy figure;
//   - the guard proves no row in the new window had resolved at filing.
//
// Expected consequences:
//   - no directional row at first, then INSUFFICIENT (n/30), then
//     INSUFFICIENT DAYS until 10 credible days accrue in the new window;
//   - the SD-30 withhold stays on until then AND the owner says yes;
//   - calibration (fit on the graded window) starts uncalibrated on the new
//     labels;
//   - the prequential-majority benchmark commits nothing for a horizon
//     until that horizon has a resolved row in the new window (about 2 days
//     at 1d, about 8 at 1w); issue days in that gap get no benchmark rows;
//   - the grader refuses until the registrar's pass completes at deploy.
const LabelWindowKind = "label-window-reregistration"

const (
	labelWindowOldEpochTS int64 = 1790294400 // 2026-09-25T00:00:00Z, set by seq 130
	labelWindowNewEpochTS int64 = 1791072000 // 2026-10-04T00:00:00Z
)

type labelWindowMeasured struct {
	Resolved map[string]int
	Pending  map[string]int
}

func measureLabelWindow(ctx context.Context, db *sql.DB) (labelWindowMeasured, error) {
	m := labelWindowMeasured{
		Resolved: make(map[string]int),
		Pending:  make(map[string]int),
	}
	query := `SELECT horizon, COUNT(*) - COALESCE(SUM(resolved_at IS NOT NULL), 0), COALESCE(SUM(resolved_at IS NOT NULL), 0) FROM prediction_outcomes WHERE ts >= ? AND (horizon LIKE '1d%' OR horizon LIKE '1w%') GROUP BY horizon`
	rows, err := db.QueryContext(ctx, query, labelWindowNewEpochTS)
	if err != nil {
		return m, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var horizon string
		var pending, resolved int
		if err := rows.Scan(&horizon, &pending, &resolved); err != nil {
			return m, err
		}
		m.Pending[horizon] = pending
		m.Resolved[horizon] = resolved
	}
	return m, rows.Err()
}

func (m labelWindowMeasured) resolvedTotal() int {
	total := 0
	for _, v := range m.Resolved {
		total += v
	}
	return total
}

func jsonIntMap(m map[string]int) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`"` + k + `": ` + itoa(m[k]))
	}
	b.WriteString("}")
	return b.String()
}

const labelWindowNote = "AMENDMENT — the label base moves from the newest SETTLED daily bar at issue to the issue day's own daily bar, and the grading window start moves from 2026-09-25 to 2026-10-04. FILED WITH KNOWLEDGE OF AN OUTCOME, stated plainly: the directional ensemble is already retired on its own record and figures are withheld under SD-30. This alters no accuracy figure, null, interval, evidence floor, auto-retire rule, verdict map, collapse detector, or survivorship epoch. The expected consequence is no directional row at first (none resolved), then INSUFFICIENT (n/30) until 30 independent observations, then INSUFFICIENT DAYS until 10 credible days accrue in the new window; nothing publishes before then."

func labelWindowSpec(m labelWindowMeasured) string {
	return `{
  "kind": "` + LabelWindowKind + `",
  "filedOn": "` + time.Now().UTC().Format("2006-01-02") + `",
  "amends": "pipeline.settledBase label base (settled-only step back, settledBaseSinceTs 2026-09-07) and store.GradingEpoch / tools/accuracy_registry.py GRADING_EPOCH = 2026-09-25 (grading-window-reregistration-2, seq 130)",
  "correctedTo": "pipeline.settledBase label base (issue day's own bar from pipeline.labelBaseSinceTs 2026-10-04) and store.GradingEpoch / tools/accuracy_registry.py GRADING_EPOCH = 2026-10-04",
  "correctionType": "label-base-and-grading-window-start",
  "oldGradingEpochTs": ` + itoa(int(labelWindowOldEpochTS)) + `,
  "newGradingEpochTs": ` + itoa(int(labelWindowNewEpochTS)) + `,
  "claimsChanged": false,
  "filedWithKnowledgeOfOutcome": true,
  "survivorshipEpochMoves": false,
  "defect": {
    "mechanism": "The resolver's base bar was the newest SETTLED daily bar at issue. Stock bars count as settled only from 22:00 ET, so the broad-universe pass at 00:00-02:00 UTC took the PREVIOUS session as base and graded a move that had already happened.",
    "measured": {
      "kept_1d_stocks_outcome_matches_visible_move_pct": 93.8,
      "kept_1d_crypto_outcome_matches_visible_move_pct": 78.6,
      "1w_stocks_accuracy_when_call_agrees_with_visible_move": 0.713,
      "1w_stocks_accuracy_when_call_disagrees_with_visible_move": 0.232
    }
  },
  "fix": {
    "resolver": "base = the issue day's own daily bar (the latest 1d bar at or before issue, the bar settle_ts already names and the grader groups by)",
    "missingBarGuard": "If that bar is missing (a whole stock session closed after the at-or-before bar and before issue, or a crypto bar a day or more old), the row stays pending instead of grading across a visible session",
    "cutoff": "pipeline.labelBaseSinceTs = 1791072000 (2026-10-04T00:00:00Z); for rows at/after it the new base applies; rows before keep the rule they were frozen under"
  },
  "label": {
    "old": "newest SETTLED daily bar at issue",
    "new": "issue day's own daily bar",
    "oneWeek": "calendar week from the new base (5 sessions, 4 in a holiday week; 7 days crypto)",
    "crypto": "one bar per UTC day: the base is the issue day's bar, so 1d runs to the next day's close and 1w 7 days on; the old base (the previous day's bar) left a window 99% elapsed at issue",
    "residualAccepted": "a stock row issued after the close still sees the 16:00 ET-to-issue after-hours move; expected visible-move agreement is about 55% instead of 93.8%"
  },
  "alternativesRejected": ` + jsonStringList([]string{
		"grade only pre-open rows (drops ~62% of rows)",
		"keep the figures withheld indefinitely",
	}) + `,
  "measuredStateAtFiling": {
    "resolved_from_new_epoch": ` + jsonIntMap(m.Resolved) + `,
    "pending_from_new_epoch": ` + jsonIntMap(m.Pending) + `,
    "note": "1d and 1w prediction_outcomes rows, benchmark twins (#pm) included, issued at or after 2026-10-04 and read at filing; the record refuses to file unless every resolved count is zero"
  },
  "whyThisEpoch": "2026-10-04 is the first UTC day after the owner's decision (2026-10-02/03) and the resolver change (pipeline.labelBaseSinceTs). It is chosen by when the defect ends and by the measured leak mechanism, not by any accuracy figure; measuredStateAtFiling must show no resolved rows in the new window for this record to file (the guard proves no row in the new window had resolved at filing).",
  "notOutcomeShopping": "the change is forward-only: only rows issued at/after 2026-10-04 use it; it was chosen by measuring the leak's mechanism (visible-move agreement), not by any accuracy figure; the guard proves no row in the new window had resolved at filing.",
  "whatThisCannotChange": [
    "every accuracy, null, interval and skill figure",
    "the evidence floors (30 independent observations, 10 distinct credible days)",
    "the auto-retire rule and its frozen thresholds",
    "the collapse detector (forecastmon.MinDistinctRatio 0.15, MinSymbolsForCollapse 30)",
    "the survivorship epoch used for listing-status reconstruction, which stays 2026-07-24",
    "the retirement already on record for the directional ensemble",
    "seq 117 and seq 130, which stay on the chain unaltered"
  ],
  "expectedConsequence": "No directional row at first (none resolved), then INSUFFICIENT (n/30) until 30 independent observations, then INSUFFICIENT DAYS until 10 credible days: the new window holds only the days since 2026-10-04. That is a count, not a verdict; nothing publishes until it clears, and when it does the number published is whatever the record says.",
  "expectedSideEffects": [
    "calibration (fit on the graded window) starts uncalibrated on the new labels",
    "the prequential-majority benchmark commits nothing for a horizon until that horizon has a resolved row in the new window, about 2 days at 1d and about 8 days at 1w, and issue days in that gap never get benchmark rows",
    "the grader refuses until the registrar's pass completes at deploy"
  ]
}`
}
