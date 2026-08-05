# P9 — RE-GRADE & EVIDENCE PACK

**Filed:** 2026-08-05T01:05Z (2026-08-04 18:05 PDT)
**Phase:** P9
**Status:** COMPLETE — with one action deliberately NOT taken (§6)
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

| Field | Before (14:05) | After (17:55) |
|---|---|---|
| status | `REFUSED` | *(absent — the clean state)* |
| refusal_reason | `grader exited 1` | `null` |
| graded_at | 2026-08-03T23:06:30 | **2026-08-04T17:55:59** |
| rows | 0 | **13** |
| heartbeat | `success=0` | **`success=1`** @ 2026-08-05T00:55:59Z |

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
python tools/test_docs_gate.py         Ran 56 tests           OK
python -m unittest discover -s tools   Ran 276 tests          OK (skipped=4)
```

The gate built in P8 now passes against the real repository. Its `grader-status`
check independently confirms the re-grade: it was **red before** this phase and
is **green after**, without the check being modified.

Committed integrity snapshot (`ops/data-integrity.json`):

```
grader.status              OK          graded 2026-08-04T17:55:59, 13 rows
universe_membership_rows   1,854,228   2,146 distinct days, 2018-07-26 → 2026-08-04
delisting.rate_per_year    0.0503      716 of 1,773 over 8.03 years (floor 0.02)
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

Zero of 27,273 structural forecasts have resolved. The earliest gradable date is
**2026-08-07**, fixed by chain seq 37 (`gradability-correction`). Any structural
verdict published today would be manufactured, not measured.

### 5b. Pre-registration must be amended, because the corpus changed

The frozen structural claims (trend21 73.10%, vol21 55.80%, liquidity21 59.50%,
trend63 70.00%, filingsdrift21 50.00%, and the two crypto legs) were registered
against a corpus that **no longer exists**: it had 21 delistings and no
point-in-time universe. It now has 716 and 1.85M membership rows.

That no verdict flipped is a *result*, not a reason to skip the amendment. The
claims will be graded on 2026-08-07 against a **different corpus** than the one
they were registered against, and the record must say so. Per P9's own closing
instruction: *do not pretend the original frozen claims are still valid without
amendment.*

**A re-baselined claim set is NOT warranted** — the measured movement (<0.55pp,
no verdict change) does not justify restating claims, and restating them would
itself be a look at the data. The correct instrument is a data-integrity
amendment that records the corpus change and leaves the claims where they are.

### Verdict

> **Delayed grading event (2026-08-07) + a data-integrity amendment recording the
> corpus change. Claims unchanged. No structural verdict may be published before
> 2026-08-07.**

## 6. What was NOT done, and why

**The data-integrity amendment has been drafted but NOT filed.** Two reasons,
both stated rather than worked around:

1. **`daemon/cmd/prereg-amend` cannot file it.** It accepts exactly two kinds —
   `gradability-correction` and `revision-epoch-correction` — and rejects
   anything else (`main.go:48`). A corpus amendment needs a new kind, with tests,
   in the tool that writes the integrity ledger.
2. **The write is permanent.** `prereg_records` is hash-chained and append-only.
   A mis-specified amendment cannot be withdrawn, only superseded. That is an
   authorisation decision, not an implementation detail.

Drafted spec, for review:

```json
{
  "kind": "data-integrity-amendment",
  "filedOn": "2026-08-04",
  "amends": "the corpus every frozen structural claim was registered against",
  "reason": "survivorship backfill (P3A) and point-in-time universe population (P3B)",
  "corpusBefore": { "delistedNames": 21, "universeMembershipRows": 0 },
  "corpusAfter":  { "delistedNames": 716, "universeMembershipRows": 1854228 },
  "claimsChanged": false,
  "measuredEffect": "every cross-sectional leg moved <0.55pp; no verdict changed; trend21 survivorship effect +0.8pp",
  "residual": "delisting stamps thin and unverified 2023-2025; survivorship completeness 97.9%",
  "studiesNotReRun": ["tools/alpha/xsection_ic.json", "tools/alpha/xscore_result.json"],
  "firstGradableOn": "2026-08-07"
}
```

To proceed, the `prereg-amend` tool needs a third kind added and tested; then the
record is filed with `-commit`. **Say the word and I will build and file it** —
I stopped here because it permanently writes the project's highest-integrity
artifact.

## 7. Freeze status

`proofs/P0_FREEZE.md` §8 lifts the freeze only when P1, P2 and P3 are each
complete with their own proof artifact. All three now are, with P3's residual
disclosed per §1.

**The freeze is therefore liftable — but it should not lift silently, and not by
this phase.** Two conditions ride with it:

1. Every document reproducing a PRE study must carry P3A §7's survivor-seeded
   label. Two such studies remain un-re-run.
2. No structural verdict may be published before 2026-08-07 (§5a).

Lifting is a publication decision with the amendment of §6 as its natural
precondition, so it is left to the operator rather than taken here.

## 8. Evidence pack contents

| Artifact | What it proves |
|---|---|
| `proofs/P9_MANIFEST.txt` | SHA-256 of every proof artifact and every gate/generator source |
| `proofs/P0_FREEZE.md` … `proofs/P7_CORRECTED_DECK.md` | the remediation chain |
| `proofs/P8_DOCS_GATE.md`, `proofs/P8_TEST_OUTPUT.txt` | the harness that blocks recurrence, and its output |
| `ops/data-integrity.json` | the committed answers CI cannot ask the 4 GB database |
| `partials/live_accuracy.md` | the single generated live record |
| `.github/workflows/ci.yml` job `docs-gate` | enforcement |
| chain seq 37–39 | gradability, revision epoch, grader re-registration |

## 9. Done-when

| Criterion | Status |
|---|---|
| Grader re-run only after P1/P2/P3 | **MET** — gate checked before running (§1) |
| Final evidence pack generated | **MET** — this file + `P9_MANIFEST.txt` |
| Decision made between grading and amendment | **MET** — §5: delayed grading event + data-integrity amendment |
| Original frozen claims not pretended valid | **MET** — amendment required and drafted; claims explicitly not re-baselined |
| Corrupted corpus not graded | **MET** — the corpus is repaired, and the grade run was the directional family only; structural grading is blocked until 2026-08-07 |
