# P2 — Single source of truth for the live record

**Date:** 2026-08-04 · **Closes:** FC1 · **Artifacts:** `partials/live_accuracy.md`,
`tools/live_accuracy.py`, `tools/test_live_accuracy.py`, `partials/INCLUDES.txt`,
`proofs/P2_GENERATED_TABLE_HASH.txt`

---

## 1. The defect

Four mutually contradictory live directional records were in circulation
simultaneously, across fourteen tracked markdown documents:

| record | n | printed in |
|---|---|---|
| 48.1% / 54.6% | 13,058 (also written 13,044) | `PREDICTION_PROCESS.md`, `CASE_STUDY.md`, `DEEP_REPORT_2026-08-03.md`, `REMEDIATION_SYNTHESIS_2026-08-04.md`, `HOSTILE_REVIEW_FIX_SUPERPROMPT.md`, `HANDOFF_PROMPT.md` |
| 48.0% / 54.4% | 12,696 | `SHIP_READINESS.md`, `INSTITUTIONAL_GAP.md`, `REMEDIATION_2026-08-03.md` |
| 46.7% (Brier skill −25.2%) | 8,191 | `MCP_SERVER.md`, `MCP_SERVER_SUPERPROMPT.md`, `LANDING_PAGE_SUPERPROMPT.md`, `SCORE_LOOP_SUPERPROMPT.md` |
| 46.3% / 52.9% | 2,257 | `HOW_PREDICTORS_WORK.md` |

Two further conflicts rode along with them: `CASE_STUDY.md` printed a 1w row of
46.2% / 54.4% and a high-conviction row of 48.6% / 56.2% that appear nowhere
else, and the documents disagreed about whether an interval verdict existed at
all — several read "the whole interval below the null" as significant negative
skill while the platform withholds every interval for this claim.

**The important property of that table is that every row was correct when it was
typed.** This was never a wrongness problem. It was a provenance problem: four
hand-copied snapshots of a re-grading registry, with nothing on any of them to
say which was current. A reader could not tell, and neither could a reviewer.

## 2. The authoritative source, and why

**Selected:** `data/accuracy_registry.json`, and within it the last successful
grade recorded at **2026-08-03T23:06:30**.

Why that file:

- It is the **output of the pinned grader**, not a transcription of one. The
  grading script is registered by sha256 in a pre-registration chain inside the
  database, so the numbers in it cannot be improved after seeing them without a
  committed, chained amendment.
- It is the file the **daemon itself reads** (`internal/modelhealth` takes the
  `retire` flag from it), so the published record and the operational
  consequence come from one place. A document that disagreed with it was already
  disagreeing with the running system.
- It carries the **multiplicity accounting** (family_size 13 × looks 8 =
  divisor 104, corrected_alpha 4.81e-4), the **survivorship completeness**
  measurement, and the **interval method** per row. No hand-typed table carried
  any of that, which is how "the interval is below the null" survived in prose
  while `ci_method` said `withheld`.

Why the 2026-08-03T23:06:30 snapshot and not the top-level object: the registry's
current top level is

```
status              REFUSED
refused_since       2026-08-04T14:05:05
refusal_reason      grader exited 1
refusal_stderr      UNREGISTERED GRADER: … pins graderSha256 6908c6f9… at commit
                    1a8c67ea…, but this file hashes 44e0294b…
rows                []
```

That refusal is the system working — the grader was edited and is no longer the
registered one. But it means the top level has **zero publishable rows**, and a
generator that rendered it literally would silently delete the live record from
every document. The registry preserves the last successful grade under
`stale_last_registry`; that is what is rendered, **labelled stale, with the
refusal reason and the age of the grade printed inside the block itself**.

`tools/test_live_accuracy.py::test_falls_back_to_last_successful_grade_and_says_it_is_stale`
and `::test_never_publishes_an_empty_table_from_a_refused_registry` pin both
halves of that behaviour.

## 3. The canonical record

Rendered to `partials/live_accuracy.md`. Reproduced here for the record — this
is the only copy in this repository that is not machine-inserted, and it is
inside a proof document, which is a dated artifact by definition:

```
| Predictor                                  | Band            |     n | Live acc |  Null | Skill  | Days | Interval |
| directional-ensemble (1d)                  | all             | 2,257 |    46.3% | 52.9% | -6.6pp |    9 | withheld |
| prequential-majority (1d)                  | all             | 1,644 |    55.5% | 51.1% | +4.4pp |    6 | withheld |
| directional-ensemble (1w)                  | all             |   912 |    47.8% | 50.2% | -2.4pp |    4 | withheld |
| prequential-majority (1w)                  | all             |     7 |    28.6% | 50.0% | -21.4pp|    1 | withheld |
| directional-ensemble (1d, high conviction) | |p-0.5|>=0.15   |   297 |    55.2% | 60.3% | -5.1pp |    5 | withheld |
| directional-ensemble (1w, high conviction) | |p-0.5|>=0.15   |    63 |    47.6% | 46.8% | +0.8pp |    4 | withheld |
```

Graded 2026-08-03T23:06:30. Every interval is `withheld`, so **no pass/fail
verdict is published from one.** The two `INSUFFICIENT` strings that appear
below the table are the registry's own statements about *sample size* — they say
a verdict cannot be reached, which is the opposite of reaching one.

The four earlier records reconcile against it as follows. The n collapse from
13,058 to 2,257 is not a correction; it is the **survivorship epoch**. The
registry grades only observations after 2026-07-24, the date from which listing
status is resolvable for the symbols contributing rows (currently 328 of 335,
97.9%). The larger n values predate that gate and pooled observations whose
survivorship status could not be established.

## 4. What was built

| artifact | what it does |
|---|---|
| `tools/live_accuracy.py` | reads the registry, renders one block; `--write` the partial, `--inject` it into documents, `--check` for drift, `--scan` for hand-typed figures |
| `partials/live_accuracy.md` | the generated block, delimited by `<!-- BEGIN GENERATED live_accuracy -->` / `<!-- END GENERATED live_accuracy -->` |
| `partials/INCLUDES.txt` | the documents that carry the block |
| `tools/test_live_accuracy.py` | 15 tests over the generator, the refusal fallback, the withheld-interval rule, injection and the scan |
| `.github/workflows/ci.yml` | runs the tests on every job, and the drift/scan gates whenever a registry is present |

Documents now carrying the generated block (`partials/INCLUDES.txt`):
`CASE_STUDY.md`, `HOW_PREDICTORS_WORK.md`, `INSTITUTIONAL_GAP.md`,
`PREDICTION_PROCESS.md`, `SHIP_READINESS.md`.

Documents marked `<!-- SUPERSEDED-SNAPSHOT -->` with a banner naming the
authoritative partial: `DEEP_REPORT_2026-08-03.md`, `REMEDIATION_2026-08-03.md`,
`REMEDIATION_SYNTHESIS_2026-08-04.md`, `HOSTILE_REVIEW_FIX_SUPERPROMPT.md`,
`LANDING_PAGE_SUPERPROMPT.md`, `SCORE_LOOP_SUPERPROMPT.md`,
`MCP_SERVER_SUPERPROMPT.md`, `HANDOFF_PROMPT.md`. The four superprompts carry an
extra line, because they are briefs a future session will follow: *take every
accuracy figure from the partial and include it rather than restating it.*

`MCP_SERVER.md` states the failure qualitatively and points at the partial
instead of restating figures. Everything under `audits/` is exempt by path — a
dated audit is evidence, and rewriting one would destroy the thing that makes it
useful.

## 5. The CI gate, and the two rules it enforces

**Rule 1 — no superseded literal in a live-claim document.**
`SUPERSEDED_LITERALS` in `tools/live_accuracy.py` lists the figures this
repository no longer stands behind: `48.1% 54.6% 48.0% 54.4% 46.7% 46.2% 48.6%
56.2% 13,058 13,044 12,696 8,191`.

**Rule 2 — no hand-typed CURRENT figure either.** The scan additionally derives
the current grade's own figures from the registry and bans those outside a
generated block. This is the rule that actually prevents recurrence: FC1 was not
caused by wrong numbers, it was caused by *typed* numbers, and a correct figure
typed by hand today is a superseded figure typed by hand tomorrow. Two
exclusions keep it from becoming noise a reviewer learns to ignore — rows below
the registry's own `min_independent_n = 30`, and the value `50.0%`, which is the
coin flip and appears legitimately anywhere a baseline is discussed.

## 6. Verification

```
$ python3 tools/test_live_accuracy.py
...............
Ran 15 tests in 2.0s
OK

$ python3 tools/live_accuracy.py --check --inject $(cat partials/INCLUDES.txt)
$ echo $?
0

$ python3 tools/live_accuracy.py --scan $(git ls-files '*.md')
$ echo $?
0
```

Before the fix, that last command reported 27 hits across 14 files. The
per-file counts were: `PREDICTION_PROCESS.md` 12, `CASE_STUDY.md` 6,
`HOSTILE_REVIEW_FIX_SUPERPROMPT.md` 5, `LANDING_PAGE_SUPERPROMPT.md` 4,
`SHIP_READINESS.md` 3, and 1–2 each in `SCORE_LOOP_SUPERPROMPT.md`,
`REMEDIATION_SYNTHESIS_2026-08-04.md`, `MCP_SERVER.md`, `INSTITUTIONAL_GAP.md`,
`HOW_PREDICTORS_WORK.md`, `DEEP_REPORT_2026-08-03.md`,
`REMEDIATION_2026-08-03.md`, `MCP_SERVER_SUPERPROMPT.md`, `HANDOFF_PROMPT.md`.

Hashes: `proofs/P2_GENERATED_TABLE_HASH.txt`.

## 7. Done-when, against the brief

| criterion | status |
|---|---|
| only one live record exists | **met** — one generator, one partial, scan proves no other copy |
| all docs include the same generated table | **met** — 5 documents, `--check` proves byte equality |
| all superseded snapshots explicitly historical | **met** — 8 documents banner-marked; `audits/` exempt by path |
| no interval-based verdict while intervals withheld | **met** — Interval column reads `withheld`, the block says no verdict is published from it, and a test asserts no verdict word appears on a withheld row |

## 8. Open, and stated rather than hidden

- **The grader is still unregistered.** Every figure above is 15h+ stale and
  labelled so in the block. P2 makes the staleness impossible to hide; it does
  not fix it. That is P1's job (`proofs/P1_GRADER_AMENDMENT.md`).
- **A parallel session built a second generator.** `tools/gen_live_accuracy.py`
  appeared in the working tree at 17:25 on 2026-08-04 with a different marker
  convention (`LIVE-ACCURACY-PARTIAL:BEGIN`) and briefly overwrote
  `partials/live_accuracy.md`. Only one generator can own this file — two tools
  rendering one record is FC1 in a new costume. The version documented here is
  the one wired into the five documents and into CI; the other is not wired into
  any document. **This needs an explicit decision before either is committed.**
- **The registry's own survivorship completeness is 97.9%, not 100%** — 7 graded
  symbols are inactive with no delisting date. The block prints that line
  verbatim. See `proofs/P3A_SURVIVORSHIP_BACKFILL.md`.
