package main

import (
	"context"
	"database/sql"
	"strconv"
)

// The FOURTH amendment this command can file, and the first that is about the
// DATA rather than the schedule, the attribution window, or the protocol's own
// provenance.
//
// WHY IT IS NEEDED. Every frozen structural claim — trend21 73.10%, vol21
// 55.80%, liquidity21 59.50%, trend63 70.00%, filingsdrift21 50.00% and the two
// crypto legs — was registered against a corpus that no longer exists. It had 21
// delisted names and an EMPTY universe_membership table, which means a
// survivor-seeded universe and look-ahead in every cross-sectional denominator.
// P3A backfilled 706 confirmed-dead names and P3B populated the point-in-time
// universe. The claims will therefore be graded against a different corpus than
// the one they were committed to, and the chain must say so.
//
// WHY NOT SIMPLY RE-BASELINE THE CLAIMS. Because the measurement says not to.
// Every cross-sectional leg moved less than 0.55pp and no verdict changed;
// restating the claims would be a look at the data dressed up as housekeeping.
// The honest instrument records the corpus change and leaves the claims exactly
// where they were committed.

// DataIntegrityKind is a record ABOUT the corpus the frozen claims will be
// graded against, never about their content. Like the three kinds before it, it
// is deliberately its own kind: amending a predictor's kind would move that
// claim's spec hash and read as "the claim changed", and no claim, band table,
// accuracy figure, null, floor or verdict rule changes here.
const DataIntegrityKind = "data-integrity-amendment"

const dataIntegrityNote = "AMENDMENT — the corpus every frozen structural claim was registered " +
	"against has been repaired and is no longer the corpus those claims were committed to. " +
	"Delisted names 21 -> 716 (P3A); universe_membership 0 rows -> populated point-in-time " +
	"membership (P3B). NO CLAIM, band table, accuracy figure, null, evidence floor or verdict rule " +
	"is altered by this record, and none is re-baselined. The measurement is why: every " +
	"cross-sectional leg moved under 0.55pp, no verdict changed, and the trend21 survivorship " +
	"effect was +0.8pp — meaning the ORIGINAL claims were inflated by excluding dead names, so " +
	"the repaired corpus is STRICTLY HARDER to satisfy than the one registered. This amendment " +
	"therefore cannot flatter any claim; its only possible effect is to make the frozen numbers " +
	"more difficult to meet. It is filed BEFORE any structural forecast has resolved, which the " +
	"pre-flight guard enforces, so it commits to the repaired corpus in advance of every outcome. " +
	"The residual is disclosed rather than smoothed: delisting stamps are thin and unverified for " +
	"2023-2025 and survivorship completeness stands at 97.9%, not 100%. The prior registrations " +
	"stand unaltered above this record and are deliberately NOT edited; a hash-chained commitment " +
	"is never rewritten."

// dataIntegrityMeasured is the live corpus state read at commit time. Never
// transcribed, for the same reason the other measures are not: the daemon writes
// continuously and a hand-copied count is stale within minutes — and a record
// asserting a corpus state it did not measure would be exactly the defect this
// whole remediation exists to abolish.
type dataIntegrityMeasured struct {
	UniverseRows   int
	UniverseDays   int
	StocksTotal    int
	StocksDelisted int
	SpanYears      float64
	DelistRate     float64

	OutcomeRows       int
	Resolved          int
	Quarantined       int
	DistinctBlocksMax int
}

// minPlausibleDelistRate is the floor below which the survivorship repair cannot
// be said to have happened. Real broad-universe US delisting runs several
// percent per YEAR; the pre-repair corpus ran 21 names over 1,077 across 7.5
// years, roughly 0.26%/yr, which is the number that made the universe
// survivor-seeded in the first place. Mirrors the same floor the docs gate
// enforces in ops/docs-registry.json.
const minPlausibleDelistRate = 0.02

// measureDataIntegrity reads the corpus state this record reports. Any query
// that cannot be answered is an error, never a zero: a record that silently
// filed "0 rows" because a table was missing would assert the opposite of the
// truth it exists to record.
func measureDataIntegrity(ctx context.Context, db *sql.DB) (dataIntegrityMeasured, error) {
	var m dataIntegrityMeasured

	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT day) FROM universe_membership`).
		Scan(&m.UniverseRows, &m.UniverseDays); err != nil {
		return m, err
	}

	var oldest sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN delisted_at IS NOT NULL THEN 1 ELSE 0 END),
		       MIN(added_at)
		  FROM symbols WHERE market='stocks'`).
		Scan(&m.StocksTotal, &m.StocksDelisted, &oldest); err != nil {
		return m, err
	}

	// Seconds in a Julian year, matching tools/docs_gate.py so the two reports of
	// this rate cannot disagree.
	if oldest.Valid && m.StocksTotal > 0 {
		var now int64
		if err := db.QueryRowContext(ctx, `SELECT strftime('%s','now')`).Scan(&now); err != nil {
			return m, err
		}
		m.SpanYears = float64(now-oldest.Int64) / 31557600.0
		if m.SpanYears < 0.5 {
			m.SpanYears = 0.5
		}
		m.DelistRate = float64(m.StocksDelisted) / float64(m.StocksTotal) / m.SpanYears
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN resolved_at IS NOT NULL THEN 1 ELSE 0 END),
		       SUM(CASE WHEN naive_label IS NULL THEN 1 ELSE 0 END)
		  FROM regime_outcomes`).
		Scan(&m.OutcomeRows, &m.Resolved, &m.Quarantined); err != nil {
		return m, err
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(b), 0) FROM (
		  SELECT COUNT(DISTINCT day / horizon_days) AS b
		    FROM regime_outcomes GROUP BY kind)`).Scan(&m.DistinctBlocksMax); err != nil {
		return m, err
	}

	return m, nil
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 4, 64) }

func dataIntegritySpec(m dataIntegrityMeasured) string {
	return `{
  "kind": "data-integrity-amendment",
  "filedOn": "2026-08-04",
  "amends": "the CORPUS every frozen structural claim was registered against, not any claim",
  "correctionType": "corpus-only",
  "claimsChanged": false,
  "claimsRebaselined": false,
  "filedBeforeAnyStructuralOutcome": true,
  "reason": "P3A survivorship backfill and P3B point-in-time universe population repaired two defects that were open when every structural claim was registered.",
  "defect": {
    "one": "Survivorship. The registered corpus identified dead names by the 'active' subscription flag rather than by 'delisted_at', a market fact. It held 21 delistings across 1,077 names over 7.5 years — roughly 0.26% per year, where real broad-universe US delisting runs several percent per year. Every band table computed over it was computed over a universe that had already dropped its losers.",
    "two": "Point-in-time membership. 'universe_membership' held ZERO rows, so every cross-sectional rank was computed against a universe including names not yet listed on the day being ranked. That is look-ahead in the denominator of every cross-sectional feature."
  },
  "corpusBefore": {
    "delistedNames": 21,
    "universeMembershipRows": 0,
    "note": "as measured in ALPHA_WORKFLOW.md sections B2 and B3, the corroborated authority on both defects"
  },
  "corpusAfter": {
    "stocksTotal": ` + itoa(m.StocksTotal) + `,
    "delistedNames": ` + itoa(m.StocksDelisted) + `,
    "delistRatePerYear": ` + ftoa(m.DelistRate) + `,
    "spanYears": ` + ftoa(m.SpanYears) + `,
    "universeMembershipRows": ` + itoa(m.UniverseRows) + `,
    "universeMembershipDistinctDays": ` + itoa(m.UniverseDays) + `
  },
  "measuredStateAtFiling": {
    "regimeOutcomeRows": ` + itoa(m.OutcomeRows) + `,
    "resolved": ` + itoa(m.Resolved) + `,
    "nullQuarantinedRows": ` + itoa(m.Quarantined) + `,
    "distinctBlocksAccrued": ` + itoa(m.DistinctBlocksMax) + `,
    "distinctBlocksRequired": 10
  },
  "measuredEffectOfTheRepair": {
    "crossSectionalLegs": "every leg moved less than 0.55pp; NO verdict changed. liquidity remains negative at 5d and 21d and still survives Bonferroni; lowVol remains positive-but-fragile with an interval touching zero; every composite remains indistinguishable from zero.",
    "sampleSizeGrowth": "10-12% (21d composite 71,550 -> 79,216)",
    "trend21SurvivorshipEffect": "+0.8pp (active 83.6% vs delisted 82.8%) — POSITIVE, meaning the published claim was INFLATED by excluding dead names",
    "geometryControlUnchanged": "74% of trend21's conviction spread remains barrier distance rather than forecasting",
    "honestReading": "The survivorship defect was real as a defect and small as an effect on these particular studies. 706 confirmed-dead names did not rescue or destroy any cross-sectional conclusion."
  },
  "whyNotRebaselined": "Restating a claim after measuring the repaired corpus would be a look at the data. The measurement does not support restatement in any case: sub-0.55pp movement with no verdict change is not a new claim, it is the same claim on better data. The claims stay exactly where they were committed and will be graded, later, against the corpus described here.",
  "whyThisCannotFlatter": "Both repairs move AGAINST the claims. Removing survivorship inflation lowers realized accuracy (+0.8pp of the original figure was bias); removing look-ahead from cross-sectional denominators removes information the model previously had for free. A repaired corpus is strictly harder to satisfy than the survivor-seeded one these claims were registered against, so no claim can be made easier to meet by this record.",
  "whatThisCannotChange": [
    "every frozen band table and claimed accuracy",
    "every resolution rule and stated baseline",
    "the refusal thresholds (min 30 independent observations, MIN_DISTINCT_BLOCKS = 10)",
    "the verdict rules (DECAYED / HOLDING / WIDE / INSUFFICIENT)",
    "the null quarantine and its no-backfill rule",
    "SURVIVORSHIP_EPOCH, which does not move"
  ],
  "residualDisclosed": {
    "delistingCoverage": "plausible 2020-2022; thin and unverified 2023-2025",
    "survivorshipCompleteness": "97.9%, not 100% — 735 symbols are active=0 with no delisted_at and are not yet separated into genuinely-delisted versus merely-unwatched",
    "studiesNotReRun": [
      "tools/alpha/xsection_ic.json",
      "tools/alpha/xscore_result.json"
    ],
    "requiredLabel": "Any document reproducing a result from the studies above must carry the survivor-seeded label defined in proofs/P3A_SURVIVORSHIP_BACKFILL.md section 7."
  },
  "expectedConsequence": "No immediate change to any published figure. Zero structural forecasts have resolved and none can resolve before 2026-08-17 (2026-10-15 for trend63); the first date any structural VERDICT is reachable is 2027-02-22, per the gradability-correction at chain seq 37. When those verdicts arrive they will be read against the corpus described in this record, not the one the claims were registered against, and this record is what makes that difference auditable.",
  "proofArtifacts": [
    "proofs/P3A_SURVIVORSHIP_BACKFILL.md",
    "proofs/P3A_revalidate_structural_2026-08-04.txt",
    "proofs/P3B_PIT_UNIVERSE.md",
    "proofs/P9_EVIDENCE_PACK.md"
  ]
}`
}
