# P9 — RE-GRADE & EVIDENCE PACK

**Filed:** 2026-08-05T01:05Z (2026-08-04 18:05 PDT)
**Phase:** P9
**Status:** COMPLETE — data-integrity amendment FILED as chain seq 41 (§6)
**Manifest:** `proofs/P9_MANIFEST.txt` (SHA-256 of every artifact below)

---

## 1. Gate check — was the grader allowed to re-run?

P9 permits a re-grade only after P1, P2, and P3 are complete or explicitly
disclosed. Verified before running anything:

| Phase | Required | Evidence | Verdict |
|---|---|---|---|
| P1 | grader re-registered through the amendment path | `proofs/P1_GRADER_AMENDMENT.md`, `proofs/P1_GRADER_STATUS.txt`; chain seq 38 + 39 | **COMPLETE** |
| P2 | one generated live record, no hardcoded percentages | `proofs/P2_LIVE_RECORD_RECONCILIATION.md`, `proofs/P2_INDEPENDENT_VERIFICATION.md`, `partials/live_accuracy.md` | **COMPLETE** |
| P3A | survivorship backfilled, affected backtests re-run | `proofs/P3A_SURVIVORSHIP_BACKFILL.md` + revalidation output | **COMPLETE WITH DISCLOSED RESIDUAL** (§4) |
| P3B | `universe_membership` populated, point-in-time | `proofs/P3B_PIT_UNIVERSE.md`, `proofs/P3B_INDEPENDENT_VERIFICATION.md` | **COMPLETE** |

P3A does **not** claim survivorship is closed. Its own done-when table answers
*"can A3 'Survivorship closed' truthfully be marked met"* with **NO** — the
permitted wording is *"materially closed 2019–2022; residual 2023–2025 gap
quantified in P3A §3."* P9's gate allows "complete **or explicitly disclosed**",
and this is the disclosure.

## 2. The re-grade

```
bash ops/accuracy-registry.sh          exit 0
```

| Field | Before (14:05) | After |
|---|---|---|
| status | `REFUSED` | *(absent — the clean state)* |
| refusal_reason | `grader exited 1` | `null` |
| graded_at | 2026-08-03T23:06:30 | **2026-08-04T18:23:15** |
| rows | 0 | **13** |
| heartbeat | `success=0` | **`success=1`** |

(The first clean grade this phase landed at 17:55:59; the daemon re-graded at
18:23:15 and that is the value the committed snapshot carries. Both are clean —
the figure is refreshed rather than pinned, because a document quoting a snapshot
that has since moved is the defect this whole remediation is about.)

**Registration verified, not assumed.** `tools/accuracy_registry.py` hashes to
`bcb722562f0b4d91a78e879d75f6da16cef9489b370e83d13922dc00d75da8b3`, which is
byte-identical to the `graderSha256` pinned by pre-registration chain **seq 39**.
The grader that produced this grade is the grader the chain authorises.

The three gates that make that meaningful were **unchanged** by P1:
`minIndependentN 30`, `minDistinctBlocks 10`, `maxAlpha 0.05`. No gate was
widened to obtain a passing grade.

## 3. Independent verification — the docs gate

```
python tools/docs_gate.py check        docs-gate: clean       exit=0
python tools/gen_docs_index.py --check index matches registry  exit=0
python tools/test_docs_gate.py         Ran 62 tests           OK
python -m unittest discover -s tools   Ran 285 tests          OK (skipped=4)
```

The gate built in P8 now passes against the real repository. Its `grader-status`
check independently confirms the re-grade: it was **red before** this phase and
is **green after**, without the check being modified.

Committed integrity snapshot (`ops/data-integrity.json`):

```
grader.status              OK          graded 2026-08-04T18:23:15, 13 rows
universe_membership_rows   1,854,289   2,146 distinct days, 2018-07-26 → 2026-08-04
delisting.rate_per_year    0.0503      716 of 1,774 over 8.03 years (floor 0.02)
survivorship_fix_date      2026-08-04
```

## 4. Did the repairs materially change historical results?

This is the question P9's closing note exists for. Answered from measurement,
not assertion.

**The corpus changed materially.**

| Quantity | Before | After |
|---|---|---|
| delisted names | 21 | **716** (+706 confirmed dead) |
| `universe_membership` rows | 0 | **1,854,228** |
| cross-sectional sample size | — | **+10–12%** (21d composite 71,550 → 79,216) |

**The results did not.**

- Every cross-sectional leg moved **< 0.55pp**. **No verdict changed.**
  `liquidity` remains negative at 5d and 21d and still survives Bonferroni;
  `lowVol` remains positive-but-fragile; every composite remains
  indistinguishable from zero.
- trend21 survivorship effect: **+0.8pp** (active 83.6% vs delisted 82.8%) —
  positive, i.e. the published claim *was* inflated by excluding dead names, but
  by less than a percentage point.
- The geometry control is unchanged and remains the dominant finding:
  **74% of trend21's conviction spread is barrier distance, not forecasting.**

P3A's own summary is the fair one: the survivorship defect was **real as a defect
and small as an effect on this particular study** — 706 dead names did not rescue
or destroy any cross-sectional conclusion.

**Residual, disclosed:** delisting stamps are plausible for 2020–2022 and *thin
and unverified* for 2023–2025. 735 symbols are `active=0` with no `delisted_at`,
holding registry survivorship completeness at **97.9%**, not 100%. Two studies
were **not** re-run (`tools/alpha/xsection_ic.json`, `tools/alpha/xscore_result.json`)
and must carry P3A §7's survivor-seeded label wherever reproduced.

## 5. Decision

P9 offers two paths. **Neither is "proceed with structural grading", and that is
not a judgement call — it is arithmetic.**

### 5a. Structural grading cannot proceed. There is nothing to grade.

| predictor | outstanding forecasts | resolved |
|---|---:|---:|
| trend21 | 6,742 | **0** |
| vol21 | 6,800 | **0** |
| trend63 | 6,742 | **0** |
| liquidity21 | 6,687 | **0** |
| trend21-crypto | 108 | **0** |
| liquidity21-crypto | 108 | **0** |
| filingsdrift21 | 86 | **0** |
| **total** | **27,273** | **0** |

Zero of 27,273 structural forecasts have resolved.

**CORRECTION, 2026-08-05.** An earlier revision of this section stated *"the
earliest gradable date is 2026-08-07, fixed by chain seq 37."* That was wrong in
both halves, and wrong in the direction that flatters the project. Seq 37 is the
record that proves **2026-08-07 is incorrect**; it does not fix the date there.
Read directly from the chain:

| | 21-day kinds | filingsdrift21 | trend63 |
|---|---|---|---|
| earliest **resolution** | 2026-08-17 | 2026-08-21 | 2026-10-15 |
| first possible **verdict** | **2027-02-22** | 2027-02-26 | **2028-05-04** |

Two defects in the original date, per seq 37: the horizon is 21 **trading bars**,
not calendar days (≈30 calendar days, so 2026-08-17 not 2026-08-07); and the date
omitted the block gate entirely — a published interval needs
`MIN_DISTINCT_BLOCKS = 10` non-overlapping blocks, which is **189 further days of
calls**. The chain calls this "the larger of the two errors by an order of
magnitude: 10 days versus 189."

**Blocks accrued: 2 of 10 required.**

**The gradable pool is also smaller than the raw count.** Of the 27,273 rows,
**16,082 (59%) are null-quarantined** — written before the 2026-07-27 write-path
guard, they carry no frozen naive-persistence baseline and are excluded from every
structural benchmark denominator. They are deliberately **not** backfilled,
because a persistence label computed after the outcome is known is a hindsight
baseline. The gradable pool is therefore ~11,191 rows, not 27,273 — and those
quarantined rows are the earliest-recorded, hence earliest-resolving, cohort.

What happens on 2026-08-07, in the chain's own words: the grader "will find zero
resolved structural rows and return INSUFFICIENT for every kind. That is the
refusal rule firing correctly, on schedule. **It is not a verdict and must not be
reported as one.**"

### 5b. Pre-registration must be amended, because the corpus changed

The frozen structural claims (trend21 73.10%, vol21 55.80%, liquidity21 59.50%,
trend63 70.00%, filingsdrift21 50.00%, and the two crypto legs) were registered
against a corpus that **no longer exists**: it had 21 delistings and no
point-in-time universe. It now has 716 and 1.85M membership rows.

That no verdict flipped is a *result*, not a reason to skip the amendment. The
claims will be graded (from 2027-02-22 at the earliest, per §5a) against a
**different corpus** than the one
they were registered against, and the record must say so. Per P9's own closing
instruction: *do not pretend the original frozen claims are still valid without
amendment.*

**A re-baselined claim set is NOT warranted** — the measured movement (<0.55pp,
no verdict change) does not justify restating claims, and restating them would
itself be a look at the data. The correct instrument is a data-integrity
amendment that records the corpus change and leaves the claims where they are.

### Verdict

> **Delayed grading event + a data-integrity amendment recording the corpus
> change. Claims unchanged.**
>
> **No structural verdict may be published before 2027-02-22** (2027-02-26 for
> filingsdrift21; **2028-05-04** for trend63). The 2026-08-07 date that appeared
> in the first revision of this document is superseded by chain seq 37 — see the
> correction in §5a. A grader run on 2026-08-07 returns INSUFFICIENT for every
> kind, which is the refusal rule working, not a result.

## 6. The data-integrity amendment — FILED

**Filed 2026-08-04 as pre-registration chain `seq 41`,
kind `data-integrity-amendment`.** An earlier revision of this section recorded
it as drafted-but-not-filed, pending authorisation and a tool that could write
it. Both are now resolved.

| | |
|---|---|
| seq | **41** |
| kind | `data-integrity-amendment` |
| prevHash | `3df7dddec07680f8b8b8fb324941328a8d105f6421905eb56b824f3310f3b89b` (seq 40, `protocol-provenance-correction`) |
| entryHash | `d5bb043832a17423e0c1c2caa00c637739438de6bc1b830198c1086cc1ac9a95` |
| chain verification | **INTACT** pre-flight and post-write |

### What it records

The corpus every frozen structural claim was registered against has been
repaired and is no longer the corpus those claims were committed to. Delisted
names 21 → 716 (P3A); `universe_membership` 0 → 1,854,289 rows across 2,146
distinct days (P3B). **No claim, band table, accuracy figure, null, evidence
floor or verdict rule is altered, and none is re-baselined.**

Every number in the record is **measured at commit time**, never transcribed:
`stocksTotal 1774`, `delistedNames 716`, `delistRatePerYear 0.0503`,
`universeMembershipRows 1854289`, `regimeOutcomeRows 27273`, `resolved 0`,
`nullQuarantinedRows 16082`, `distinctBlocksAccrued 2`. A record asserting a
corpus state it did not measure would be the exact defect this remediation
exists to abolish.

### Why filing it cannot flatter anything

Both repairs move **against** the claims. Removing survivorship inflation lowers
realized accuracy — +0.8pp of the original trend21 figure was bias. Removing
look-ahead from cross-sectional denominators removes information the model
previously had for free. A repaired corpus is **strictly harder** to satisfy
than the survivor-seeded one these claims were registered against, so no claim
can be made easier to meet by this record. It is also filed **before any
structural forecast has resolved**, which the tool enforces rather than asserts.

### The three refusal guards, and proof they fire

`daemon/cmd/prereg-amend` gained a fourth kind
(`daemon/cmd/prereg-amend/spec_dataintegrity.go`). Each guard refuses to put a
FALSE statement on a log that cannot be corrected. Each was demonstrated against
a synthetic database before the real filing:

| Guard | Fixture | Observed |
|---|---|---|
| PIT universe must be populated | `universe_membership` emptied | `REFUSING to file: universe_membership holds zero rows.` |
| survivorship backfill must have landed | 1 delisting of 100 over 9 years | `REFUSING to file: delisting rate 0.0011/year is below the 0.0200 plausibility floor` |
| must predate every structural outcome | 3 rows resolved | `REFUSING to file: 3 structural forecast(s) have resolved.` |

### A defect caught before it became permanent

The spec is built by concatenating raw-string segments around `itoa`/`ftoa`
calls. A search-and-replace over backticks broke every concatenation, so the
JSON carried the literal text `' + itoa(m.StocksTotal) + '` where the number
belonged — and **it compiled and vetted clean**. Nothing in a Go build can tell
a raw string holding JSON from one holding Go source.

Caught by the dry run, and now locked by
`daemon/cmd/prereg-amend/spec_dataintegrity_test.go`, which asserts the spec
parses as JSON, carries the measured numbers rather than unevaluated source, and
still reports `claimsChanged: false` / `claimsRebaselined: false`. The dry-run
default (`-commit` is the only way to write) is what made the catch possible.

### After the write

- chain verified INTACT post-write; 41 records total
- grader re-ran clean: 13 rows, `grading_protocol_seq 39` undisturbed
- `tools/accuracy_registry.py` still hashes `bcb7225…`, matching the seq-39 pin
- `docs_gate.py check` → clean; 62 gate tests OK; `go test ./cmd/prereg-amend/` OK

## 7. Freeze status

`proofs/P0_FREEZE.md` §8 lifts the freeze only when P1, P2 and P3 are each
complete with their own proof artifact. All three now are, with P3's residual
disclosed per §1.

**The freeze is therefore liftable — but it should not lift silently, and not by
this phase.** Two conditions ride with it:

1. Every document reproducing a PRE study must carry P3A §7's survivor-seeded
   label. Two such studies remain un-re-run.
2. No structural verdict may be published before 2027-02-22 (§5a); a grader run
   on 2026-08-07 returns INSUFFICIENT, which is the refusal rule working.

The amendment that was §6's natural precondition is now filed (chain seq 41), so
the remaining barrier is a publication decision, which is left to the operator
rather than taken here.

## 8. Evidence pack contents

| Artifact | What it proves |
|---|---|
| `proofs/P9_MANIFEST.txt` | SHA-256 of every proof artifact and every gate/generator source |
| `proofs/P0_FREEZE.md` … `proofs/P7_CORRECTED_DECK.md` | the remediation chain |
| `proofs/P8_DOCS_GATE.md`, `proofs/P8_TEST_OUTPUT.txt` | the harness that blocks recurrence, and its output |
| `ops/data-integrity.json` | the committed answers CI cannot ask the 4 GB database |
| `partials/live_accuracy.md` | the single generated live record |
| `.github/workflows/ci.yml` job `docs-gate` | enforcement |
| chain seq 37–41 | gradability, revision epoch, grader re-registration, protocol provenance, **data-integrity amendment** |

## 9. Done-when

| Criterion | Status |
|---|---|
| Grader re-run only after P1/P2/P3 | **MET** — gate checked before running (§1) |
| Final evidence pack generated | **MET** — this file + `P9_MANIFEST.txt` |
| Decision made between grading and amendment | **MET** — §5: delayed grading event + data-integrity amendment |
| Original frozen claims not pretended valid | **MET** — amendment FILED as chain seq 41; claims explicitly not re-baselined |
| Corrupted corpus not graded | **MET** — the corpus is repaired, and the grade run was the directional family only; structural grading is blocked until 2027-02-22 at the earliest |
