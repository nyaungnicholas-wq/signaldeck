package main

import (
	"context"
	"database/sql"
	"os/exec"
	"strings"
	"time"
)

// The SECOND amendment this command can file. It follows the same convention as
// amendmentNote/gradabilitySpec: name what changed, state that the prior record
// stands unaltered, and write it so a reader who trusts nothing else can still
// tell what happened and judge whether it flatters anything.

// RevisionEpochKind is a record ABOUT which rows carry an attribution
// guarantee, never about what any predictor claims. Like GradabilityKind it is
// deliberately its own kind: amending a predictor's kind would move that
// claim's spec hash and read as "the claim changed", and no claim, band table,
// accuracy figure, null or threshold changes here.
const RevisionEpochKind = "revision-epoch-correction"

const revisionEpochNote = "AMENDMENT — the revision gate's attribution epoch moves from 2026-07-27 " +
	"to 2026-08-04. FILED WITH KNOWLEDGE OF AN OUTCOME, and that is stated plainly rather than " +
	"buried: at filing the directional rows were already published showing 46.26% against a 52.88% " +
	"prequential null, and their verdicts were already stripped by this very gate. A correction made " +
	"after seeing a result must justify itself on what it CANNOT change, so: advancing this epoch " +
	"alters no accuracy figure, no null, no interval, no evidence floor and no verdict rule. The " +
	"graded sample is bounded by SURVIVORSHIP_EPOCH (2026-07-24), which does NOT move. The verdict " +
	"this restores will be read off exactly the numbers already published, and on those numbers it " +
	"reads FAILED, not clean — so the correction cannot flatter the model it re-enables, and the most " +
	"likely consequence of filing it is that the flagship is retired. The prior epoch stands unaltered " +
	"above this record and is deliberately NOT edited; a hash-chained commitment is never rewritten."

// revisionMeasured is the live state read at commit time, for the same reason
// measured is: the daemon writes continuously and a transcribed count is stale
// within minutes.
type revisionMeasured struct {
	Attributable   int
	Unattributable int
	OffendingDays  int
	NewestBadDay   string
	// OnOrAfterNewEpoch is the pre-flight guard's subject: unattributable rows
	// at or past the new boundary. It must be zero, or the record's central
	// claim — that the boundary clears every contaminated row — is false.
	OnOrAfterNewEpoch int
}

const (
	oldRevisionEpochTS = 1785456000 // 2026-07-27T00:00:00Z
	newRevisionEpochTS = 1786089600 // 2026-08-04T00:00:00Z
)

// revResolvable mirrors tools/accuracy_registry.py revision_resolvable: a stamp
// is worth something only when it names a commit THIS repository contains. A
// dirty or empty stamp needs no lookup, and a git failure is False rather than
// True — an unverifiable stamp is not a verified one.
func revResolvable(ctx context.Context, rev string) bool {
	if rev == "" || strings.HasSuffix(rev, "+dirty") || len(rev) != 40 {
		return false
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(c, "git", "cat-file", "-e", rev+"^{commit}").Run() == nil
}

// measureRevisionState reads the live attribution state of the window the old
// epoch opened. Measured at commit time, never transcribed: the daemon writes
// continuously and these counts move every pass.
func measureRevisionState(ctx context.Context, db *sql.DB) (revisionMeasured, error) {
	var m revisionMeasured
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(revision,''), COUNT(*),
		       COUNT(DISTINCT trading_day(predicted_at)),
		       MAX(date(predicted_at,'unixepoch')),
		       SUM(CASE WHEN predicted_at >= ? THEN 1 ELSE 0 END)
		  FROM prediction_ledger WHERE predicted_at >= ? GROUP BY 1`,
		newRevisionEpochTS, oldRevisionEpochTS)
	if err != nil {
		return m, err
	}
	defer rows.Close() //nolint:errcheck

	badDays := map[int]bool{}
	for rows.Next() {
		var rev, newest string
		var n, days, recent int
		if err := rows.Scan(&rev, &n, &days, &newest, &recent); err != nil {
			return m, err
		}
		if revResolvable(ctx, rev) {
			m.Attributable += n
			continue
		}
		m.Unattributable += n
		m.OnOrAfterNewEpoch += recent
		if newest > m.NewestBadDay {
			m.NewestBadDay = newest
		}
		dayRows, err := db.QueryContext(ctx, `
			SELECT DISTINCT trading_day(predicted_at) FROM prediction_ledger
			 WHERE predicted_at >= ? AND COALESCE(revision,'') = ?`,
			oldRevisionEpochTS, rev)
		if err != nil {
			return m, err
		}
		for dayRows.Next() {
			var d int
			if err := dayRows.Scan(&d); err != nil {
				dayRows.Close() //nolint:errcheck
				return m, err
			}
			badDays[d] = true
		}
		dayRows.Close() //nolint:errcheck
	}
	m.OffendingDays = len(badDays)
	return m, rows.Err()
}

func revisionEpochSpec(m revisionMeasured) string {
	return `{
  "kind": "revision-epoch-correction",
  "filedOn": "2026-08-04",
  "amends": "tools/accuracy_registry.py REVISION_EPOCH = 2026-07-27",
  "correctedTo": "2026-08-04",
  "correctionType": "attribution-window-only",
  "claimsChanged": false,
  "filedWithKnowledgeOfOutcome": true,
  "defect": {
    "one": "The 2026-07-27 epoch opened attribution enforcement on the GRADER side, but the daemon-side guard added in the same wave (cmd/signaldeckd/main.go) only refused builds that were DIRTY (vcs.modified) or UNSTAMPED (no embedded revision). It never asked the third question: does my commit still EXIST? A clean build stamping a real 40-hex commit passed the guard, and a later rebase or amend then removed that commit from the repository, converting every row it had written into evidence the grader must refuse.",
    "two": "The refusal is all-or-nothing and permanent. apply_revision_gate strips the verdict for the whole predictor family when ANY contributing row is unattributable, and the window is anchored to a FIXED epoch, so offending rows never age out. 5.3% of the window disqualified 100% of the directional verdicts, forever."
  },
  "measuredStateAtFiling": {
    "postEpochLedgerRowsAttributable": ` + itoa(m.Attributable) + `,
    "postEpochLedgerRowsUnattributable": ` + itoa(m.Unattributable) + `,
    "distinctDaysTouched": ` + itoa(m.OffendingDays) + `,
    "newestUnattributableDay": "` + m.NewestBadDay + `",
    "offendingBuilds": [
      "(unstamped)",
      "256caf4cfc975cb2ca46befdac5c9a7c0143a3ef+dirty",
      "4c377f59875547fa5bfca6d1222d956dc583bba1+dirty",
      "6c876c148c8a672118a106218578c153613436b6+dirty",
      "706b37c8ebe9535a47835c41ce693e071db78e6a+dirty",
      "f71f58486dabe39853b4ff2ba2d4850acc8d0359+dirty",
      "3020f0585f4b864af6a2b736f88696b48b1b9c7d",
      "3b80a09c7cd1a839f6abcb4c114efdb8d095c207",
      "3c096b33c1962cd0e5b771b6df9e6ec130bbcf58"
    ],
    "offendingBuildsNote": "The last three carry NO +dirty suffix. They are clean builds naming commits this repository no longer contains — the failure mode the old guard could not see, and the reason this is a correction rather than a retry."
  },
  "whyForwardOnly": "The epoch moves past the last contaminated row (2026-08-03) and no further. Rows before it are not attribution-checked, which is exactly the posture rows before the ORIGINAL epoch already had: their provenance is what it is, and inventing a guarantee for them would be worse than declining to claim one. Nothing is deleted, quarantined or reweighted; 3,255 rows stay in the ledger and stay readable.",
  "whatThisCannotChange": [
    "every published accuracy, null, interval and skill figure",
    "the graded sample, which is bounded by SURVIVORSHIP_EPOCH = 2026-07-24 and does not move",
    "the evidence floors (30 independent observations, 10 distinct UTC days)",
    "the auto-retire rule and its frozen thresholds",
    "the Bonferroni family/looks accounting"
  ],
  "runtimeFixThatMakesThisMeaningful": {
    "what": "lineage.BuildReachable, wired into the daemon startup gate",
    "behaviour": "refuses to start when git can be consulted AND proves the running build's commit is absent from the repository; an UNPROVABLE answer never refuses, so deploy-path builds (git archive HEAD, no .git, provenance certain by construction) are unaffected",
    "why": "Advancing the epoch without closing the hole would only buy time before the same contamination recurred and disqualified the family again."
  },
  "expectedConsequence": "The next grade recomputes the directional rows with their verdicts intact. On the numbers already published (1d: 46.26% live vs 52.88% prequential null over 2257 observations) the honest reading is FAILED once the 10-distinct-day floor is met, which sets retire=true and stops the horizon publishing. This correction is therefore expected to RETIRE the flagship, not to rescue it."
}`
}
