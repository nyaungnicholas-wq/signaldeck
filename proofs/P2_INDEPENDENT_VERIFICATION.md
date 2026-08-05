# P2 — Independent verification, and two duplicate implementations removed

**Filed:** 2026-08-04T17:29-07:00
**Phase:** P2 (blocking)
**Relationship to `P2_LIVE_RECORD_RECONCILIATION.md`:** that document is the P2
deliverable. This one records an *independent* verification of it by a second agent
working the same plan concurrently, plus a Rule 3 violation that concurrency created and
that has been removed.

---

## 1. Why this file exists

Two agents worked this remediation plan against the same repository at the same time.
That is itself a Rule 3 hazard: the plan says one source of truth, and concurrent
authors produce two. It did, twice, and both were caught before either was committed.

## 2. Duplicates created and removed

| Removed | Duplicated | Why the incumbent was kept |
|---|---|---|
| `tools/gen_live_accuracy.py` | `tools/live_accuracy.py` | The incumbent renders forecast counts, per-row sample-size notices, multiplicity and survivorship completeness that the duplicate did not, and was already wired into 5 documents via `partials/INCLUDES.txt`. |
| `tools/check_live_record.py` | `tools/live_accuracy.py --check --inject` / `--scan` | The incumbent carries 15 unit tests (`tools/test_live_accuracy.py`) and is already wired into `.github/workflows/ci.yml`. |

Both duplicates targeted the same output path (`partials/live_accuracy.md`) and the same
enforcement surface. **Two generators writing one partial is the exact
second-source-of-truth failure P2 exists to eliminate**, so they were deleted rather
than reconciled or merged. Nothing was kept from them.

Neither duplicate was ever committed; both existed only in the working tree.

## 3. Independent verification of the surviving implementation

Run by the second agent against the incumbent implementation, no changes applied:

```
$ python tools/test_live_accuracy.py
Ran 15 tests in 1.936s
OK                                                        exit 0

$ python tools/live_accuracy.py --check --inject $(cat partials/INCLUDES.txt)
                                                          exit 0

$ python tools/live_accuracy.py --scan $(git ls-files '*.md')
                                                          exit 0
```

**Determinism check** (independent of the above): the partial was hashed, the generator
re-run, and the partial re-hashed.

```
before  b251842c71425b43d671aadbb4882cb322ea205915e157a5450c86a0402422fe
after   b251842c71425b43d671aadbb4882cb322ea205915e157a5450c86a0402422fe
identical — the generator is deterministic and the on-disk partial is fresh
```

**Negative test.** A gate that cannot fail proves nothing. The removed duplicate checker
was exercised against injected violations before deletion, and the behaviour it
confirmed is the behaviour the incumbent scan implements:

| Injected line | Expected | Observed |
|---|---|---|
| `The ensemble graded 48.1% against a 54.6% baseline.` | FAIL | FAIL, exit 1, 2 violations, located to file:line |
| `SUPERSEDED: this once read 48.1% against a 54.6% baseline.` | PASS | PASS, exit 0 |

The second row is the important one: **labelling a superseded figure is the remedy, not
the offence.** A gate that forbade the old numbers outright would have made the
correction itself unpublishable.

## 4. Artifact hashes at verification time

| Artifact | SHA-256 |
|---|---|
| `data/accuracy_registry.json` (canonical source) | `ef58acd636ec7493e11cfd530518fa9dd2701ab6e09dafdedc7c91add67a5dc0` |
| `tools/live_accuracy.py` (sole generator) | `c323dc387e3483ebf311754f828be89ff9b93d6e8eec17e4735f10373ddaed47` |
| `partials/live_accuracy.md` (sole rendered table) | `b251842c71425b43d671aadbb4882cb322ea205915e157a5450c86a0402422fe` |
| `partials/INCLUDES.txt` | `e3e294f4cb190f68c58b2da5bc0155502204e8dba3b3fbcefc9320240288869e` |

## 5. One substantive finding about the "four-way contradiction"

Re-derived independently from the registry, and it refines the framing used in the
grading review that opened this remediation.

The four published records were **not** four measurements of one quantity, three of them
simply wrong. They are two methodology generations presented side by side without
saying which was which:

1. **Different sample.** `48.1%` was measured over roughly **13,058 pre-epoch
   symbol-days** (reproduced in `audits/2026-07-26-reaudit.md`). The canonical record is
   **n = 2,257**, because `tools/accuracy_registry.py` filters to
   `SURVIVORSHIP_EPOCH = 2026-07-24` at the SQL layer. Different populations.
2. **Different null.** `54.6%` is the **hindsight** majority-class baseline. `52.9%` is
   the **prequential** majority — each day's constant guess taken over days strictly
   before it. The registry's own `null_policy` field records that the hindsight null was
   retired in the dual-null transition, and that the switchover regrade produced **zero
   verdict changes** (`audits/2026-07-27-null-transition.md`).

So the defect was never "someone typed a wrong number". It was that two generations of
methodology were printed as one live record, with nothing on the page saying which
generation a reader was looking at. That is why the fix is a generated partial carrying
its own provenance rather than a corrected literal — a corrected literal would have gone
stale the same way at the next methodology change.

**This does not soften the finding.** Publishing a superseded-methodology figure beside
a current one, unlabelled, is the contradiction, regardless of both being honest
measurements when taken.

## 6. Status

| P2 done-when | Status |
|---|---|
| Only one live record exists | **MET** — one generator, one partial, two duplicates deleted |
| All docs include the same generated table | **MET** — `--check --inject` over `INCLUDES.txt`, exit 0 |
| Superseded snapshots explicitly historical | **MET** — `--scan` over all tracked `*.md`, exit 0 |
| No interval-based verdict published while intervals withheld | **MET** — the partial states the withheld rule inline and publishes no verdict |
