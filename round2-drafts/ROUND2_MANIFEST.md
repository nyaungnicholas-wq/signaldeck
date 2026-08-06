# Round 2 Completion Manifest — 2026-08-06

**Run:** `wf_3c8529fe-fa3` · **7/7 agents DONE**, 0 errors · 1,214,052 subagent tokens · 518 tool uses
**Artifacts:** 14 · **All 9 patches pass `git apply --check`** (verified by the orchestrator, not self-reported)
**Invariants:** held — `git status` shows 13 modified source files, all from prior rounds. Zero round-2 edits to source; no DB write, no deploy, no git state change.

---

## C1 — quarantine redesign · **RESOLVED**

The round-1 draft stamped `prediction_outcomes.basis_epoch`. Independently re-confirmed dead:
**0 non-test readers in `daemon/`, 0 in `tools/accuracy_registry.py`, 0 in `web/`, and the column is NULL on every row.**

The replacement is a **structural, per-row exclusion** injected into the two SQL statements that actually
select the graded population, self-describing in console, JSON and per-row output. It touches no
`basis_epoch` and uses **no timestamp cutoff**. Predicate derived from the guard at HEAD
(`predict.go:1089-1105`): base bar via `BarAtOrBefore`, `target = base.Ts + horizonSecs(h)`, forward bar
via `BarAtOrAfter`; a row is contaminated when it was frozen before that forward bar's true session close.

→ `round2-drafts/patches/grader-quarantine.patch`

**Scope correction on the internal objection:** `audits/2026-08-06-1d-label-disagreements.md` does **not**
object to *excluding* a `resolved_at`-selected subset — it objects to *relabelling* one. The patch excludes;
it does not relabel. The objection does not apply.

## C3 — e2e credential isolation · **RESOLVED, and the leak is wider than reported**

→ `round2-drafts/patches/e2e-credential-isolation.patch`

Two corrections to the brief:

1. **`SIGNALDECK_ROOT` already exists** as the first rung of `projectRoot()` (`config.go:34`), checked before
   any probing. No new override was needed. The real defect is that `e2e_test.go` never *sets* it — and
   `projectRoot()`'s probe is load-bearing for legitimate use (three existing tests depend on it), so
   changing the probe would have been the wrong fix.
2. **The leak reaches two credential stores, not one.** `config.go:214` also reads
   `<root>/stock-trader/.env` for `ALPACA_KEY` / `ALPACA_SECRET` — **a different repository**, verified
   present. The e2e suite would have loaded credentials from outside SignalDeck entirely.

Fix is proved with a before/after Go test: fails before the change, passes after.

## S-C2 — design effect · **DOWNGRADED to Low; the published intervals were never affected**

**Part A (the half that mattered) — parity claim VERIFIED TRUE.** `tools/accuracy_registry.py:1009-1013`
claims to match `clusterstat.DesignEffect` in the Go daemon. It does. The Go version has **both** guards:
degenerate `p<=0 || p>=1 -> n/k` at `clusterstat.go:328-330`, and the `max(1.0, …)` floor at `:346-348`.
Verified numerically over **614 shared cases** including every degenerate shape, running the real registry
function against a Go binary built from a byte-identical copy — **zero mismatches**.

**No `clusterstat` patch was produced.** A clean bill with evidence, not an invented discrepancy.

**Part B — `h0221.py` genuinely did collapse to 1.0 at maximal clustering**, and is fixed to mirror the
grader rather than inventing a third estimator. **This changes no published number** — nothing imports
`h0221` except the round-1 draft tests.

→ `round2-drafts/patches/h0221-design-effect-fix.patch`

**Self-correction:** my own round-1 draft test `drafts/research_tests/test_research_math.py:437` asserts
`f(allhit) == 1.0` — it *endorses the very defect just fixed*. That draft test must be corrected or
discarded before use.

---

## THE CORRECTION THAT MATTERS MOST — contamination is latent, not live

I stated repeatedly that rev `0499416` "mints contaminated rows every ~10 minutes." **That was inferred
from the guard's absence, never measured.** Measured read-only, contaminated rows by resolution date:

| date | contaminated / total |
|---|---|
| 08-02 | 620 / 15,052 |
| 08-03 | 1,487 / 2,123 |
| 08-04 | **0** / 20,840 |
| 08-05 | **0** / 21,836 |
| 08-06 | **0** / 33,264 |

**Zero in three days.** Cause: the resolver is **~2 days behind on 1d and ~8 days on 1w, with 180,090
unresolved rows**, so it never reaches a prediction whose forward bar is the current session.

Two consequences: the deploy is **less time-critical than I said**, and there is a **new finding** — a
180,090-row resolver backlog. It also strengthens the design choice: a timestamp cutoff would have looked
correct exactly as long as the backlog lasted, then silently stopped excluding.

---

## Patch inventory — all verified `APPLIES CLEAN`

```
daemon-guard-provenance-preflight.patch
dq-auditor-delisted.patch
e2e-credential-isolation.patch
grader-quarantine.patch
h0221-design-effect-fix.patch
install-windows-tasks-cadence.patch
test-coverage-calibration.patch
```

Scripts (draft, do-not-execute headers):
`devops/run-daemon-with-provenance.ps1`, `devops/register-daemon-task.ps1`,
`devops/test-run-daemon-with-provenance.ps1`, `swarm3_partA_parity_check.py`

## Verify every patch yourself

```bash
cd "C:/Users/Nicholas_N/Desktop/claude code/signaldeck"
for p in round2-drafts/patches/*.patch; do echo "== $p"; git apply --check "$p" && echo CLEAN; done
```

## Human execution runbook — suggested order

Apply in dependency order; each step is independently revertible with `git checkout -- <files>`.

```bash
# 1. Safety first — stops the e2e suite reading real credentials from two repos
git apply round2-drafts/patches/e2e-credential-isolation.patch

# 2. Observability — stops the dq_events flood blinding the DQ signal
git apply round2-drafts/patches/dq-auditor-delisted.patch

# 3. Scheduling correctness
git apply round2-drafts/patches/install-windows-tasks-cadence.patch
git apply round2-drafts/patches/daemon-guard-provenance-preflight.patch

# 4. Grader honesty — changes a PUBLISHED number; read the measured before/after first
git apply round2-drafts/patches/grader-quarantine.patch

# 5. Research hygiene — changes no published number
git apply round2-drafts/patches/h0221-design-effect-fix.patch

# 6. Tests
git apply round2-drafts/patches/test-coverage-calibration.patch

# then, in daemon/
go build ./... && go vet ./... && go test -short -count=1 ./...
```

**Step 4 warning:** the quarantine patch alters the published accuracy figure. Read the agent's measured
before/after in the run journal before applying, and decide deliberately — including if it makes the
model look *better*.

**Still gated on approval (unchanged):** BLOCKED-1 (commit → rebuild → restart), BLOCKED-2 (DB write),
BLOCKED-3 (web task), BLOCKED-4 (governed doc), BLOCKED-5 (alert transport), BLOCKED-6 (universe prune).
`devops/register-daemon-task.ps1` is drafted, **not registered**.

**Status: NOT COMPLETE** — all 7 agents finished; six items still require human approval.

---

## FINAL RESULTS (6/7 agents; 9 patches, all APPLIES CLEAN)

### Stale-feed exclusion — measured, and the direction matters

| predictor | n before → after | acc before → after |
|---|---|---|
| directional-ensemble (1d, high conviction) | 621 → 435 | 48.631% → **45.287%** (−3.344 pp) |
| directional-ensemble (1w) | 1,656 → 1,649 | 41.063% → 40.934% |
| prequential-majority (1d) baseline | 2,622 → 1,376 | 57.742% → **59.084%** |

Rows excluded: **16,726 of 101,302** resolved post-epoch (16.5%), 0 unverifiable.
Independent observations lost: 2,866 of 8,567 symbol-days — **for 1d alone, 1,613 of 3,322 (48.6%)**.

**MATERIAL FINDING: the contamination was NOT flattering the model.** Cleaning the graded population
makes the model look *worse*, not better, and the baseline it is measured against gets *stronger*.
Verdict FAILED → FAILED. The negative skill is not an artifact of dirty data.

Second-order consequence to weigh before applying: losing 48.6% of 1d independent observations roughly
halves effective_n, which **widens** the published interval. A wider interval on a worse point estimate
is the honest outcome, but it is a visible change to the published surface.

### Coverage — real tests, honest gain

```
BEFORE  total 70.0%  (27,925 / 39,890 statements)  0 failures
AFTER   total 70.2%  (28,013 / 39,890 statements)  0 failures   (+88 statements)
```
Above the 70.1 ci.yml floor, achieved with tests written to catch real defects, not padding.

### Patch inventory (9, all CLEAN)

Grader lane ships three: `grader-quarantine.patch` (C1 alone), `grader-stale-feed.patch` (stale alone),
and `grader-quarantine-stale.patch` — the **combined** patch. They touch the same SQL.
**Use the combined patch; do not apply the two singles together.**
