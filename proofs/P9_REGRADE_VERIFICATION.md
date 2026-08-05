# P9 — Re-grade verification: the flagship graded FAILED, as pre-committed

**Filed:** 2026-08-04T18:2x-07:00
**Phase:** P9 (final gate)
**Status:** **RE-GRADE RAN AND PRODUCED A VERDICT.** Certification incomplete — §5.
**Relationship to `P9_EVIDENCE_PACK.md` / `P9_MANIFEST.txt`:** those are the evidence
pack. This verifies the grade itself and records what the verification changed.

---

## 1. The result

The grader ran, and for the first time a directional row cleared the evidence floor and
published an interval.

| predictor | n | acc | prequential null | skill | interval | verdict |
|---|---:|---:|---:|---:|---|---|
| **directional-ensemble (1d)** | **2,911** | **43.08%** | **56.77%** | **−13.69pp** | **day-clustered Wilson, 10 distinct days** | **FAILED — significantly worse than the naive baseline** · `retire=true` |
| directional-ensemble (1w) | 938 | 45.63% | 52.19% | −6.56pp | withheld (5/10 days) | INSUFFICIENT DAYS — no verdict |
| directional-ensemble (1d, high conviction) | 300 | 55.33% | 60.00% | −4.67pp | withheld (6/10 days) | INSUFFICIENT DAYS — no verdict |
| directional-ensemble (1w, high conviction) | 62 | 45.16% | 44.35% | +0.81pp | withheld (4/10 days) | INSUFFICIENT DAYS — no verdict |

The 1d interval is **[31.1%, 55.9%]** at effective n = 185. Its upper bound sits below
the 56.77% baseline, which is what makes FAILED a verdict rather than a point estimate.

All seven structural predictors remain **PENDING**, 0 resolved.

## 2. The grade is legitimate — provenance checked, not assumed

| Field | Value | Why it matters |
|---|---|---|
| `grader_sha256` | `bcb722562f0b4d91…` | the grader committed in `328d8c4`, not a working-tree edit |
| `grading_protocol_seq` | **39** | the chain record the registrar appended in P1 |
| `revision_epoch` | 2026-08-04 | the amended epoch, in force |
| `survivorship_epoch` | 2026-07-24 | **unmoved**, as the amendment promised |
| `divisor` | **117** (was 104) | another look was charged |
| `ci_z` | **3.5226** (was 3.4912) | the interval got *wider*, not narrower |

The multiplicity counters moved in the only direction they are allowed to move. A
re-grade that spent a look and did not pay for it would be the tell; this one paid.

## 3. The pre-commitment was honoured

`prereg_records` seq 38, filed **before** this grade, states:

> "The verdict this restores will be read off exactly the numbers already published, and
> on those numbers it reads FAILED, not clean — so the correction cannot flatter the
> model it re-enables, and the most likely consequence of filing it is that the flagship
> is retired."

It reads **FAILED**. `retire=true`.

One honest deviation: the amendment said the verdict would be read off "exactly the
numbers already published" (46.26% vs 52.88%, n=2,257). The grade came in at 43.08% vs
56.77%, n=2,911. The sample accrued between filing and grading — the daemon writes
continuously — and the revision-epoch correction restored rows the gate had been
stripping. **The figures moved against the model, not for it**, so nothing about the
change flatters the claim. But the amendment's wording promised a stability the
mechanism does not provide, and that is worth saying plainly rather than letting the
matching verdict paper over it.

## 4. What this verification changed

Three defects found while verifying, each fixed at its source:

**4.1 — `ops/accuracy-registry.sh` hardcoded a superseded record inside a generated
block.** Its preamble stated the pre-retirement figures ("1d 48.1% vs 54.6% over 13,058
independent obs" and two more) and asserted *"the entire day-clustered CI below the
majority-class baseline"* for every directional row. Both were wrong to print: the
figures are a superseded pre-epoch population hand-typed on top of a block whose purpose
is to be generated, and three of the four rows have **withheld** intervals, so the
sentence claimed evidence the grader refuses to publish. The preamble now states the
retirement and points at the reconciliation, carrying no figures.

**4.2 — the scan reported `proofs/` as violations.** A proof artifact that records "this
figure was 48.1% over 13,058 pre-epoch symbol-days, and here is why it is not the
current record" is doing the job the scan exists to enforce. `proofs/` is now exempt on
the same footing as `audits/` — both are dated evidence, never live claims.

**4.3 — the scan reported README's own generated table as hand-typed.** README carries a
second generated block under different markers (`LIVE-ACCURACY:BEGIN`), rendered by
`ops/accuracy-registry.sh` from the same registry. The scan knew only one marker pair
and named every figure in an auto-generated table. It now recognises both.

After all three: `--scan` exit 0, `--check --inject` exit 0, generator suite 15/15.

## 5. Why P9 is not certified

`python tools/docs_gate.py check` exits **1** with 10 violations, and they are not
about the grade:

| rule | count | cause |
|---|---:|---|
| `single-source-of-truth` | 5 | expects `partials/live_record.md`; the repository has `partials/live_accuracy.md` |
| `no-hardcoded-live-accuracy` | 4 | includes `PREREGISTRATION.md:111`, a chain-digested document |
| `grader-status` | 1 | snapshot records `MISSING`; the gate wants `OK` |

**Two incompatible single-source mechanisms now exist**, both added during this
remediation:

- `tools/live_accuracy.py` → `partials/live_accuracy.md`, markers
  `BEGIN GENERATED live_accuracy`, manifest `partials/INCLUDES.txt`
- `tools/docs_gate.py` → `partials/live_record.md`, its own contract

They disagree about which partial is canonical, so satisfying one leaves the other red.
That is a Rule 3 violation *in the enforcement layer* — the precise failure this phase
was built to prevent, reproduced one level up, exactly as the untracked kill switch
reproduced P4C's own finding one level up.

It is **not** resolved here, deliberately. `tools/docs_gate.py` was under active edit by
a concurrent session throughout this phase (it was unparseable twice, at 17:42 and
18:23), the choice of canonical partial is a design decision its author owns, and
resolving it by unilaterally deleting one mechanism mid-write is how the next defect
gets created.

The `grader-status: MISSING` row is a smaller instance of the same thing: a successful
grade omits the `status` key entirely, and the snapshot reader records the absence as
`MISSING` rather than `OK`.

## 6. Status

| P9 done-when | Status |
|---|---|
| Grader runs | **MET** — graded 2026-08-04T18:23:15 under protocol seq 39 |
| A verdict is produced | **MET** — 1d FAILED with a published day-clustered interval |
| Verdict provenance verifiable | **MET** — §2, committed grader, look charged, epochs correct |
| Pre-registration honoured | **MET** — §3, with the wording deviation stated |
| Live record propagated to every document | **MET** — drift gate exit 0 |
| Evidence pack certified clean by the docs gate | **NOT MET** — §5 |

**The substantive result of this remediation is that the system graded its own flagship
and killed it, on a pre-registered rule, with an interval it had to earn.** That is the
outcome the plan existed to make possible, and it happened. The certification gap is a
tooling conflict, not a claim about the market.
