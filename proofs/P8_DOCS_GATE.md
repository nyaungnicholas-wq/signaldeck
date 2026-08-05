# P8 — AUTOMATED PROOF HARNESS / CI

**Filed:** 2026-08-05T01:05Z (2026-08-04 18:05 PDT)
**Phase:** P8
**Status:** COMPLETE
**Goal:** the repository itself blocks recurrence of the P0 defect class.

---

## 1. What was actually wrong

P0 froze ten strategy documents. The defects were not typos — they were a class:

| Defect | Nothing in the repo could contradict it because |
|---|---|
| Four inconsistent live-accuracy figures | each was typed by hand, correct on the day it was typed |
| Survivorship asserted closed, measured open | the assertion lived in prose, the measurement in another file |
| PIT universe asserted verified, table empty | no check ever asked the database |
| Kill switch cited from a **different repository** | nothing verified a cited path was in *this* repo |
| "Already built (verified)" rows contradicted | "verified" was a word in a table, not a link to proof |

Every one is a human-typed claim with no mechanical adversary. P8 is the adversary.

## 2. The six required checks, and where each is enforced

The spec named six minimum checks. Two were already owned by a sibling tool built
in P2; re-implementing them would have given the repository two definitions of the
same rule that can disagree — the single-source-of-truth defect reproduced inside
the gate itself. So they are **delegated, not duplicated**, and this table is the
honest accounting of who enforces what.

| # | Check | Enforced by | CI step |
|---|---|---|---|
| 1 | No hardcoded live accuracy | `tools/live_accuracy.py --scan` (precise: known registry literals) **+** `docs_gate.py` check `no-hardcoded-live-accuracy` (broad: any % in live-accuracy context) | `tools` job + `docs-gate` job |
| 2 | Grader status | `docs_gate.py` check `grader-status` | `docs-gate` job |
| 3 | Docs index (owner/version/data-as-of/status) | `docs_gate.py` check `docs-index` | `docs-gate` job |
| 4 | Forbidden claims | `docs_gate.py` check `forbidden-claims` | `docs-gate` job |
| 5 | Single source of truth / stale partials | `tools/live_accuracy.py --check --inject` (embedded copies) **+** `docs_gate.py` check `single-source-of-truth` (declared partials, malformed regions) | `tools` job + `docs-gate` job |
| 6 | Data integrity | `docs_gate.py` check `data-integrity` | `docs-gate` job |

The two checks on line 1 and 5 are **complementary, not redundant, and neither
should be deleted as duplicate.** `--scan` catches a number that matches a known
registry value and cannot catch an invented one; `docs_gate` catches any
percentage stated in live-accuracy context and cannot tell stale from current.

## 3. New artifacts

| Path | Role |
|---|---|
| `tools/docs_gate.py` | the gate — `check` / `build` / `write-integrity` |
| `tools/test_docs_gate.py` | 56-test contract; the specification, written before the implementation |
| `ops/docs-registry.json` | single source of truth for per-document metadata + the adjudicated allowlist |
| `ops/data-integrity.json` | the committed answer to what CI cannot ask the 4 GB database |
| `.github/workflows/ci.yml` → job `docs-gate` | self-test, then enforce |
| `.gitattributes` | LF pinned for `partials/*` and the snapshot |

## 4. The CI split, and why it is not a hole

`data/` is gitignored and `signaldeck.db` is ~4 GB. A runner can never ask it
whether the PIT universe is populated or the grader is refusing. This is the
identical split already used by `ops/ledger-provenance.sh`:

- `docs_gate.py write-integrity` runs **where the database lives**, and commits
  `ops/data-integrity.json`.
- `docs_gate.py check` runs **in CI** and reads only committed files.

**A missing or unparseable snapshot is a violation (`integrity-snapshot`), never a
skip.** That is the property that makes the split safe: it cannot degrade into
silence, which is how "we check that locally" becomes "nobody checks it".

Exit codes are three-valued on purpose: `0` clean, `1` the documents are wrong,
`2` the gate could not look. A gate that passes because it was handed an empty
registry reports green on a broken repository, so `load_registry` raises on a
missing file, a non-object, an empty `strategy_docs`, or a mistyped key.

## 5. First run over the real repository — 9 violations, adjudicated

The gate's first real run is the honest part of this report. It fired 9 times.
**Seven were false positives**, and they are recorded here because "the gate cried
wolf and was switched off" is the normal way this kind of work dies.

| Hit | Verdict | Action |
|---|---|---|
| `grader-status` REFUSED | **TRUE** — grader unregistered since P1's amendment | fixed by re-running the grader (P9) |
| `PAIRS_TRADING.md:69` `95% CI (block)` | false positive — confidence level | structural fix |
| `PREDICTION_PROCESS.md:437`, `PREREGISTRATION.md:111` `95% interval on live accuracy` | false positive — confidence level | structural fix |
| `PREDICTION_PROCESS.md:271` `never 50%` | false positive — the null being argued against | structural fix |
| `EDGE_PLAN.md:19,20,21` | false positive — industry context and a target range | allowlist, with reasons |
| `EXECUTION_SPEC.md:138` `guaranteed levy` | false positive — a guaranteed **cost**, the opposite of a return claim | allowlist, with reason |

**Structural fix** (`scrub_non_accuracy_percentages`): a percentage that states an
interval's *confidence level*, or the coin-flip *null* a document argues against,
is scrubbed from the line before the accuracy rule reads it. Scrubbing is
per-token, never per-line — a real figure beside a confidence level on the same
line still fires (`test_c1_still_fires_on_a_real_figure_beside_a_confidence_level`).

**Allowlist discipline.** Four entries remain, each scoped by `line_contains`
rather than to a whole file, so a genuine claim appearing later in the same
document still fails the build. **An entry without a `reason` suppresses nothing**
— the gate enforces that (`test_c1_allowlist_requires_a_reason`), so the list
cannot rot into a silent bypass.

## 6. Bypasses found in review and closed

Three independent worker reviews ran against the implementation before it was
accepted. What they found, and what was done:

| Finding | Severity | Resolution |
|---|---|---|
| One unterminated `<!-- BEGIN GENERATED -->` exempted **every remaining line** of a document from the live-accuracy check | HIGH | region parsing now fails closed; a malformed, nested, or mismatched marker is itself a `single-source-of-truth` violation and exempts nothing |
| CRLF checkout on Windows vs LF generation would report every partial "stale" | HIGH | newline-normalised comparison + `.gitattributes` |
| `| Control | Not verified |` wrongly flagged as an unproven attestation | MEDIUM | negation-aware; `not/never/no/yet/un-` verified do not fire |
| Allowlist `line_contains` compared against an empty string, so it silently narrowed nothing | MEDIUM | source line text now carried on the violation and matched |
| Missing/empty registry passed the gate | MEDIUM | exit 2 |

A defect the *contract* had, found by running it: the clean fixture declared a
partial it never built, so "clean" and "a partial is missing" were the same state
and neither could be tested. Fixed by building in the fixture.

## 7. A correctness trap worth recording

`tools/accuracy_registry.py` writes **no `status` key at all** on a clean grade —
`status: REFUSED` is written only by `ops/accuracy-registry.sh`, and only when it
refuses. Reading a missing key as "not OK" would publish a **permanent false
refusal** on a healthy repository: the same shape as the defect where a stale
2026-07-29 refusal was served as current, which is worse than a loud failure
because it looks like the honesty machinery working.

`grader_status_of()` encodes this, with its own tests. Zero published rows is
`EMPTY`, not `OK` — a grade that graded nothing must not pass by declining to
complain.

## 8. Proof

Full transcript: `proofs/P8_TEST_OUTPUT.txt`.

```
python tools/test_docs_gate.py             Ran 56 tests   OK
python -m unittest discover -s tools       Ran 276 tests  OK (skipped=4)
python tools/docs_gate.py check            docs-gate: clean       exit=0
```

Committed snapshot at the time of filing:

```
grader.status              OK          (graded 2026-08-04T17:55:59, 13 rows)
universe_membership_rows   1,854,228   (2,146 distinct days)
delisting.rate_per_year    0.0503      (716 of 1,773 over 8.03 years; floor 0.02)
survivorship_fix_date      2026-08-04
```

## 9. Done-when

| Criterion | Status |
|---|---|
| Hardcoded live accuracy fails a build | **MET** — two complementary checks, both in CI |
| Refusing grader fails the docs build | **MET** — `grader-status`, and it genuinely fired |
| A doc without owner/version/data-as-of/status fails | **MET** — and an unregistered new doc fails too |
| Forbidden claims fail | **MET** — including unproven "verified" in control tables |
| Missing/stale generated partials fail | **MET** — plus malformed generated regions |
| Data-integrity failures block | **MET** — empty PIT universe, implausible delisting rate, unlabelled pre-fix backtest |
| The repo itself blocks recurrence | **MET** — a new root `.md` that is neither registered nor exempt fails CI, so a new strategy document cannot dodge the gate by being new |

## 10. Known limitations, stated rather than hidden

1. **The metadata lives in `ops/docs-registry.json`, not in the documents.**
   Adding front-matter to the ten frozen documents would change the SHA-256
   hashes `proofs/P0_FREEZE.md` §3–§4 recorded, invalidating that proof artifact,
   and `PREREGISTRATION.md` is digested into the hash-chained pre-registration
   record — editing it forces an unintended amendment. The gate accepts in-document
   metadata too, so this can move into the documents through the amendment path
   without changing the gate.
2. **`docs_gate.py` declares no partials of its own** (`"partials": []`). The live
   record's partial is owned by `tools/live_accuracy.py`. C5's generic machinery
   is tested and live but currently guards an empty set here; it engages the moment
   a partial is declared.
3. **The C1 rule is deliberately over-broad**, mitigated by the scrubber and a
   reasoned allowlist. It will flag a legitimate backtest hit rate stated on the
   same line as its label. That is the intended trade: a false positive costs one
   adjudicated allowlist entry; a false negative is how P0 happened.
