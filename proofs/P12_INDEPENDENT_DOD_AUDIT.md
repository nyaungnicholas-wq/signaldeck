# P12 — Independent Definition-of-Done audit, and three items closed

**Owner:** Nicholas Nyaung
**Version:** 1.0.0
**Data as of:** 2026-08-04
**Status:** ACTIVE

A second session ran the P0–P11 remediation. This record is the independent
audit of that work, plus the three items the audit left open, closed here. It
deliberately does not restate P0–P11; it states what was checked, what failed
checking, and what changed as a result.

## The harness

`tools/verify_dod.py` — read-only, stdlib only, opens the database with
`mode=ro`. Twenty checks over the Definition of Done: seven on truth and
grading, four on data integrity, three on risk, three on governance, three on
presentation reported as MANUAL because they are judgement calls a regex should
not assert.

```
python tools/verify_dod.py --repo .
```

Result at filing: **16 passed, 0 failed, 4 manual.**

```
GRADER_REGISTERED            PASS   bcb722562f0b matches bcb722562f0b
CHAIN_INTACT                 PASS   all rows verified
NO_MANUAL_REPIN              PASS   seq 20 entered off-path, explained on-chain
GRADER_HEALTHY               PASS   last success 2026-08-05T01:23:16.337Z
REGISTRY_NOT_REFUSED         PASS   status key absent (healthy)
NO_SUPERSEDED_SNAPSHOT       PASS   no stale snapshot present
NO_VERDICT_WITHOUT_INTERVAL  PASS   13 rows judged
SURVIVORSHIP_DISCLOSED       PASS   [live] clean=0, disclosed=13
UNIVERSE_POPULATED           PASS   1,854,289 rows, 2018-07-26..2026-08-05
PIT_NO_FUTURE_ROWS           PASS   no future rows
BACKTEST_NOT_LABELLED_LIVE   PASS   [live] 7/13 are backtests, all labelled
RISK_POLICY_EXISTS           PASS   27335 bytes
NO_CROSS_REPO_RISK_CLAIM     MANUAL 3 lines pair a control with a foreign path
KILLSWITCH_IN_REPO           PASS   1 file, 6 references
DOCS_INDEX_EXISTS            PASS   3576 bytes
DOC_HEADERS                  PASS   all headers present
PREREG_ANCHORING_EXPLICIT    PASS   PREREGISTRATION.md:7
DECK_FROM_TRUTH              MANUAL STRATEGY_DECK.md, CASE_STUDY.md, SHIP_READINESS.md
NO_HYPE                      MANUAL 7 hype phrases in root *.md
TABLES_HAVE_STATUS_LABELS    MANUAL 133 markdown tables
```

## What was verified rather than taken on trust

**The grader re-registration is real.** `prereg_records` seq 39, kind
`grading-protocol`, note begins `AMENDMENT`, pinning `graderSha256 bcb72256…`
at commit `328d8c4` — byte-identical to the grader on disk.

**No record was hand-written.** Every chain row reproduces under the documented
preimage `sha256(prev_hash ‖ 0x1E ‖ "ts=…|kind=…|specHash=…|note=…")`, checked
against the implementation at `daemon/internal/prereg/prereg.go:140`, with each
row's `prev_hash` equal to its predecessor's `entry_hash`.

**The one substantive verdict is arithmetically correct.**
`directional-ensemble (1d)` publishes `FAILED` with `ci [0.3109, 0.5594]`,
`day-clustered-wilson`, `retire: true`. Upper bound 0.5594 is below the
prequential null 0.5677, which is the registered retirement criterion. The
interval is Bonferroni-corrected (`ci_z 3.5226`, `divisor 117` = family 13 ×
looks 9) over an effective n of 185 clustered from 2911 raw rows. The other
twelve rows decline to grade and publish no interval.

## Finding — seq 20, and how it was closed

Every `grading-protocol` record after the seq 8 genesis carries an `AMENDMENT`
note, except seq 20, whose note begins `APPEND-19` and which was appended by the
single-purpose `cmd/append-prereg19` rather than by `cmd/prereg-amend`. By note
convention alone it is indistinguishable from a hand re-pin of the grading
protocol, which is exactly what the "no manual hash re-pin" criterion exists to
catch.

Measured against seq 18, what it actually did:

```
clusterUnit        None -> "non-overlapping horizon blocks (anchored at the first call day)…"
maxAlpha           None -> 0.05
minDistinctBlocks  None -> 10
multiplicityRule   None -> "Published intervals are Bonferroni-corrected…"
```

Seq 18 had dropped four frozen floors to null; seq 20 restored them. Restoring a
Bonferroni correction and a minimum block count makes a verdict harder to
obtain, so the change cannot have flattered any result.

**Closed by explanation, not by rewriting.** The chain is append-only, so seq
20's note can never be corrected in place — the only honest remedy is a later
record naming it. `daemon/cmd/prereg-amend` gained a third kind,
`protocol-provenance-correction`, filed at **seq 40**, spec hash
`6e26c23a265a042cafbc8f3316615c5265103654d865274076d2953f4a63ff0b`. It changes
no claim, threshold or grader hash; it records provenance only. Like the two
existing kinds it refuses to file when its premise is false — if no off-path
record exists, if the floors were not restored, or if any floor was dropped
instead. Chain verified INTACT both pre-flight and post-write.

```
prereg-amend -db data/signaldeck.db -kind protocol-provenance-correction        # dry run
prereg-amend -db data/signaldeck.db -kind protocol-provenance-correction -commit
```

## Finding — the document-header failure was mine, not the repository's

The audit initially reported 31 root documents lacking owner/version/status.
That was wrong. This repository keeps that metadata in
`ops/docs-registry.json`, with an explicit exempt list, and that registry was
already complete: 15 governed documents with all five required fields, 31
exempt, zero root `.md` unregistered. `verify_dod.py` was corrected to test the
real contract.

`DOCS_INDEX.md` genuinely was missing and now exists — **generated** by
`tools/gen_docs_index.py` from the registry, never hand-maintained, so it cannot
drift from the thing it describes. `--check` exits 1 when the committed file is
stale, which is the form CI wants.

```
python tools/gen_docs_index.py --repo .            # write
python tools/gen_docs_index.py --repo . --check    # exit 1 if stale
```

## Four harness defects found and fixed before any result was trusted

Recorded because an audit that hides its own false positives is worth nothing.

1. **`CHAIN_INTACT` falsely reported the chain broken at seq 1** — the separator
   was `\x0e` (shift-out) where the spec and the Go source use `\x1e` (record
   separator). Corrected, 39/39 rows reproduced.
2. **Three per-row checks passed vacuously.** While the registry was refused,
   `rows` was empty, so `SURVIVORSHIP_DISCLOSED`, `BACKTEST_NOT_LABELLED_LIVE`
   and `NO_VERDICT_WITHOUT_INTERVAL` "passed" without inspecting anything. They
   now fall back to `stale_last_registry.rows` and label which source they judged.
3. **`NO_VERDICT_WITHOUT_INTERVAL` flagged all 7 structural predictors** whose
   verdict is `PENDING (first grade 2026-08-14, 0/30 resolved)` — a statement
   that no grade exists, not a claim about performance.
4. **`NO_CROSS_REPO_RISK_CLAIM` cannot tell a claim from its retraction.** All
   three current hits are retractions, and they span lines. Downgraded to MANUAL
   with the lines named, rather than reporting a false failure.

## Limits of this audit

- `UNIVERSE_POPULATED` proves the table has rows and no future-dated ones. It
  does **not** prove any cross-sectional feature reads it. Population is not
  PIT-validity.
- `KILLSWITCH_IN_REPO` proves the package exists and is called from
  `pipeline/paper.go:193` and `:412`. That is the paper-trading path; there is
  no live-order path in this repository for it to gate.
- The three MANUAL presentation checks are not assertions. `NO_HYPE` counts
  phrases; it does not judge them.
- The corpus was moving during the first freeze (a second writer was active), so
  `freeze/2026-08-04-remediation/` pins a moment, not a baseline.
  `freeze/2026-08-04-post/` was taken against a quiet tree and is the one to use.
  Note `freeze/` is gitignored — these snapshots are local, not committed.

## Reproduce

```
bash ops/freeze-corpus.sh <label>
python tools/verify_dod.py --repo .
python tools/gen_docs_index.py --repo . --check
python tools/docs_gate.py check
```
