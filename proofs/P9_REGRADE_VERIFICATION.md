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

### 5.1 The gate is failing its own specification

`tools/docs_gate.py` does not pass its own suite: **56 tests, 5 failures, 4 errors**,
static since 18:23:54. The failures are its own false-positive controls:

| failing test | what it asserts |
|---|---|
| `test_c1_ignores_a_confidence_level` | "`95% CI` is the interval's confidence, not an accuracy figure" |
| `test_c1_ignores_the_coin_flip_baseline` | a 50% baseline is not a live-accuracy claim |
| `test_c1_allows_a_figure_inside_a_generated_region` | "A generated number is the fix, not the defect — it must not fire" |
| 4 × `GraderStatusTest` | the grader-status helper errors outright |

This is decisive for certification. Take the gate's own reported violation at
`PREREGISTRATION.md:111`:

```
With `[lo, hi]` the day-clustered 95% interval on live accuracy and `C` the frozen
```

That line is **verbatim the fixture in `test_c1_ignores_a_confidence_level`**, the test
that asserts it must *not* be flagged. The gate is firing on a string its own
specification exempts.

So the ten violations are not evidence that the corpus is wrong. They are evidence that
the instrument is wrong, and its author's tests say so explicitly.

Two consequences worth stating:

1. **Certifying P9 through this gate would be certifying with a broken instrument** —
   the same error as reading a verdict off a grader that refuses to run, which is what
   P1 existed to prevent.
2. **Complying with it would damage the repository.** The remedy it prints for
   `PREREGISTRATION.md:111` is to move the figure into a generated partial or delete the
   claim. `PREREGISTRATION.md` is digested into the pre-registration chain as
   `prereg-document`; editing that line to satisfy a false positive changes its digest
   and forces an unintended chained amendment. The gate is asking for a change that Rule
   2 exists to forbid.

The CI job wired in `10875b2` runs `python3 tools/test_docs_gate.py` **before**
`docs_gate.py check`, deliberately — "self-test the gate before trusting it". CI will
therefore fail at the self-test step and name the gate, not the corpus. That is the
harness behaving correctly: it is reporting that this gate is not yet trustworthy, which
is true.

### 5.2 Why the repair was not attempted — the file is still being written

`tools/docs_gate.py` was unparseable **three separate times** during this phase, each
time caught within minutes of the edit:

| observed | error |
|---|---|
| 17:42 | `SyntaxError: '{' was never closed` (line 721) |
| 18:23 | `SyntaxError: unterminated string literal` (line 454) |
| 18:31 | `SyntaxError: expected 'except' or 'finally' block` (line 776) — file ends inside an open `try` |

The last was observed 90 seconds after the file's mtime. This is not a settled module
with nine known bugs; it is a module mid-rewrite by a concurrent author.

Repairing it would have been overwritten, would have conflicted, or — worst — would have
merged a half-written module with a hand-patched one and produced a gate nobody could
reason about. The decision it needs is a design decision (**which partial is canonical**)
and it belongs to whoever is writing it.

### 5.3 The actual blocker, isolated to one line — and it is already being fixed

Running **HEAD's** committed `docs_gate.py` (52 tests, OK) against the current repository
gives **one** violation, not ten:

```
docs-gate: grader-status: ops/data-integrity.json:
           Grader status is 'None', not OK.
```

The cause is a round-trip inconsistency inside the gate:

| function | behaviour |
|---|---|
| `check_grader_status` | `status = str(grader.get("status","")).strip().upper()` → violation unless it is exactly `OK` |
| `write-integrity` | copies the registry's `status` verbatim, defaulting to `MISSING` |

**A successful grade omits the `status` key entirely.** Only a refusal sets it
(`status: REFUSED`). So the round trip is:

- refused grade → `REFUSED` → check fails ✓ *correct*
- **successful grade → key absent → `None`/`MISSING` → check fails ✗ *wrong***

**This gate cannot pass on a successful grade.** It has only ever been exercised against
refused registries, because until 2026-08-04T18:23:15 this repository had no successful
grade for it to see. The first real success is what exposed it.

That is also the explanation for the concurrent rewrite. HEAD carries **52** tests; the
working tree carries **56**. The four added are `GraderStatusTest`, including
`test_clean_registry_without_a_status_key_is_ok` — a test asserting exactly the case
above. The other author found this same bug and is mid-fix on it.

**So the single line blocking P9 certification is the single line already being
repaired.** Patching it here would collide head-on with that work, and the collision
would be in the function that decides whether the repository may publish a number.
Holding is not deference; it is the only way the fix lands once instead of twice.

**Recommendation, unambiguous:** `partials/live_accuracy.md` should be canonical. It
exists, is deterministic, carries 15 passing tests, is injected into all five documents
in `partials/INCLUDES.txt`, and is enforced by both a drift gate and a scan gate that
currently pass. `partials/live_record.md` does not exist. Point `docs_gate`'s
`single-source-of-truth` rule at the former and delete the latter's contract — the same
resolution applied earlier in this remediation when a duplicate generator and a duplicate
checker were deleted rather than reconciled.

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
