package main

import "strconv"

func itoa(n int) string { return strconv.Itoa(n) }

// amendmentNote follows the chain's existing AMENDMENT convention: it names
// what changed, states that the prior record stands unaltered, and is written
// so a reader who trusts nothing else can still tell what happened.
const amendmentNote = "AMENDMENT — the registered FirstGradableOn (2026-08-07) is unreachable and no " +
	"structural verdict can exist on that date. It was derived by adding a CALENDAR-day horizon to a " +
	"resolver that indexes 21 TRADING bars, and it omitted the MIN_DISTINCT_BLOCKS=10 gate entirely. " +
	"NO claim, band table, accuracy figure, baseline or resolution rule changes — only the date on " +
	"which they become checkable. The frozen commitment stands unaltered above this record and is " +
	"deliberately NOT edited; a hash-chained commitment is never rewritten, so the correction is " +
	"appended instead. Corrected per-kind dates are in this record's spec. Filed 2026-08-04, before " +
	"ANY structural forecast had resolved, so nothing here was written with knowledge of an outcome."

// measured is the live state read at commit time. The record claims these are
// the numbers "at filing", so they are measured then rather than transcribed
// from an earlier session — the daemon is ingesting continuously and a
// hardcoded count is stale within minutes.
type measured struct {
	Rows, Resolved, Quarantined, Blocks int
}

// gradabilitySpec is the machine-readable body. It is assembled as a plain
// string rather than a marshalled struct so that what is hashed is exactly what
// a reader sees, with no field-ordering or escaping question in between.
func gradabilitySpec(m measured) string {
	return `{
  "kind": "gradability-correction",
  "filedOn": "2026-08-04",
  "amends": "prereg.FirstGradableOn = 2026-08-07",
  "correctionType": "schedule-only",
  "claimsChanged": false,
  "defect": {
    "one": "The horizon is 21 TRADING bars, not 21 calendar days. structregime.ResolveTrendAt, ResolveLiquidityAt and ResolveVol21At all index the bar array as closes[t+horizonDays], so 21 bars is ~30 calendar days. The registered date added 21 calendar days to the first call date (2026-07-18) and produced 2026-08-07; the earliest bar-correct resolution is 2026-08-17.",
    "two": "The registered date omitted the block gate. A published interval requires MIN_DISTINCT_BLOCKS=10 distinct non-overlapping horizon blocks, where a block is call_day // horizon_days. Ten 21-day blocks require 189 further days of CALLS, and the calls in the final block must themselves resolve. This is the larger of the two errors by an order of magnitude: 10 days versus 189."
  },
  "measuredStateAtFiling": {
    "regimeOutcomeRows": ` + itoa(m.Rows) + `,
    "resolved": ` + itoa(m.Resolved) + `,
    "distinctBlocksAccrued": ` + itoa(m.Blocks) + `,
    "distinctBlocksRequired": 10,
    "nullQuarantinedRows": ` + itoa(m.Quarantined) + `,
    "nullQuarantineNote": "rows written before the 2026-07-27 write-path guard carry no frozen naive-persistence baseline and are excluded from every structural benchmark denominator; they are NOT backfilled, because a persistence label computed after the outcome is known is a hindsight baseline. They are also the earliest-recorded and therefore earliest-resolving cohort."
  },
  "correctedDates": [
    {"kind": "trend21",            "horizonDays": 21, "firstCall": "2026-07-18", "earliestResolution": "2026-08-17", "firstPossibleVerdict": "2027-02-22"},
    {"kind": "vol21",              "horizonDays": 21, "firstCall": "2026-07-18", "earliestResolution": "2026-08-17", "firstPossibleVerdict": "2027-02-22"},
    {"kind": "liquidity21",        "horizonDays": 21, "firstCall": "2026-07-18", "earliestResolution": "2026-08-17", "firstPossibleVerdict": "2027-02-22"},
    {"kind": "trend21-crypto",     "horizonDays": 21, "firstCall": "2026-07-18", "earliestResolution": "2026-08-17", "firstPossibleVerdict": "2027-02-22"},
    {"kind": "liquidity21-crypto", "horizonDays": 21, "firstCall": "2026-07-18", "earliestResolution": "2026-08-17", "firstPossibleVerdict": "2027-02-22"},
    {"kind": "filingsdrift21",     "horizonDays": 21, "firstCall": "2026-07-22", "earliestResolution": "2026-08-21", "firstPossibleVerdict": "2027-02-26"},
    {"kind": "trend63",            "horizonDays": 63, "firstCall": "2026-07-18", "earliestResolution": "2026-10-15", "firstPossibleVerdict": "2028-05-04"}
  ],
  "derivation": {
    "tradingToCalendar": "ceil(bars * 7/5), an ESTIMATE that ignores holidays and is therefore a LOWER bound on the true calendar wait",
    "earliestResolution": "firstCallDay + tradingToCalendar(horizonDays)",
    "blockGateCallDay": "firstCallDay + (10 - 1) * horizonDays",
    "firstPossibleVerdict": "blockGateCallDay + tradingToCalendar(horizonDays)",
    "reproduce": "python tools/prereg_readiness.py --db data/signaldeck.db --claimed 2026-08-07 (exits 1 while any claimed date is unreachable; --self-check asserts the arithmetic)"
  },
  "whatHappensOnTheRegisteredDate": "tools/accuracy_registry.py will find zero resolved structural rows and return INSUFFICIENT for every kind. That is the refusal rule firing correctly, on schedule. It is not a verdict and must not be reported as one.",
  "doesNotChange": [
    "every frozen band table and claimed accuracy",
    "every resolution rule and stated baseline",
    "every known-weakness disclosure",
    "the refusal thresholds (min 30 independent observations, MIN_DISTINCT_BLOCKS=10)",
    "the verdict rules (DECAYED / HOLDING / WIDE / INSUFFICIENT)",
    "the null quarantine and its no-backfill rule"
  ],
  "consequence": "A pre-registered claim whose test date was wrong is still a pre-registered claim. The hypotheses remain frozen and checkable; only the calendar moves, and it moves LATER, which cannot flatter any result."
}`
}
