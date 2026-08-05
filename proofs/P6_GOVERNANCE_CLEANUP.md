# P6 — COMPLIANCE & GOVERNANCE CLEANUP

**Filed:** 2026-08-04
**Phase:** P6
**Status:** COMPLETE
**Authority:** Remediation & Proof Plan, P6. Operates under the freeze filed in
`proofs/P0_FREEZE.md`, which this document does **not** lift.

---

## 0. What this phase did and did not do

P6 repairs *attestations*: it makes every status claim in the corpus checkable, and
removes verdicts the underlying data cannot support. It does not fix any measured
defect, does not re-grade any model, and does not lift the freeze.

**Phase status, re-measured 2026-08-04 at the close of P6.** P1–P5 were landing in
parallel with this phase, so this section states what is *filed* rather than what is
*claimed*:

| Phase | Proof artifact filed | Work observable in the repo |
|---|---|---|
| P0 | `proofs/P0_FREEZE.md` | yes |
| P1 | `proofs/P1_GRADER_AMENDMENT.md`, `proofs/P1_GRADER_STATUS.txt` | yes |
| P2 | `proofs/P2_LIVE_RECORD_RECONCILIATION.md`, `P2_GENERATED_TABLE_HASH.txt`, `P2_INDEPENDENT_VERIFICATION.md` — **filed 2026-08-04**, after this row was first written | yes — `partials/live_accuracy.md` generated, included by the documents that used to type the record |
| P3A | `proofs/P3A_SURVIVORSHIP_BACKFILL.md` + three data artifacts — **filed 2026-08-04**, after this row was first written | yes — `universe_membership` populated, 1,854,228 rows; `delisted_at` stamps 16 → 716 |
| P3B | **not filed** | not assessed by this phase |
| P4C | `proofs/P4C_KILL_SWITCH_CORRECTION.md` — **filed 2026-08-04**, after this row was first written | yes — `daemon/internal/killswitch` wired and tested |
| P4A | `proofs/P4A_RISK_POLICY.md` + `RISK_POLICY.md` | yes — `MaxCorrToBook`, `MaxGrossExposure` added and tested |
| P4B | `proofs/P5_EV_EXECUTION_SPEC.md` §2 | yes — `riskgate.Admit` gates before `ev.Decide`; refusals ledgered |
| P5 | **not filed** | not assessed by this phase |

**A defect this phase found in itself.** Three times during P6, a document — including
this one — cited a proof artifact that did not exist. `P4C_KILL_SWITCH_CORRECTION.md`,
`P2_LIVE_RECORD_RECONCILIATION.md` and `P3A_SURVIVORSHIP_BACKFILL.md` were each cited
before they were filed, and each was filed shortly afterwards. The work was always
real and independently measurable; the record of it lagged. A citation to an unfiled
artifact is exactly the class of defect this phase exists to remove, so it is recorded
here rather than dropped once it resolved itself.

The reverse defect is now guarded. `tools/check_strategy_deck.py` fails if any
document claims a `proofs/` artifact is missing while that file exists, so a
statement of absence cannot quietly go stale the way these did.

**On the freeze.** `P0_FREEZE.md` §8 states four lift conditions — filed `P1_*`,
`P2_*`, `P3A_*` and `P3B_*` artifacts. By the close of P6 all four exist. **This phase
does not lift the freeze**, and did not treat the condition count as authority to.
§1 of `P0_FREEZE.md` requires an explicit successor artifact to lift it; conditions
being met is a prerequisite, not the act. Lifting authorises external publication,
which is the one thing this corpus was frozen to prevent, and it is a decision to be
taken deliberately rather than inferred from a directory listing.

Independently, §5 below blocks external publication while the chain is unanchored.
That gate is unaffected by the phase count and remains closed.

### Evidence standard used here

A claim is recorded as **BUILT AND IN FORCE** only when all three hold:

1. the artifact lives in **this** repository;
2. it is **testable** — a test file exercises it, or a scheduled job writes a log
   line that would differ if it stopped working;
3. it is **currently binding** — something imports, schedules, or gates on it.

Failing (3) alone gives **BUILT BUT NOT IN FORCE**. Failing (1) gives **NOT BUILT**,
regardless of what exists in a sibling repository. The word *verified* is not used
anywhere in this corpus except where all three hold and the evidence column names the
file that proves it.

---

## 1. P6A — corrected status table

`INSTITUTIONAL_GAP.md` carried a table headed **"Already built (verified, not
claimed)"**. Twenty-two rows, none of them status-qualified, several of them false.
The heading itself was the defect: it asserted verification as a property of the
whole table.

The table is replaced by the classification below. Path corrections are part of the
finding: eleven rows cited `internal/<pkg>`, but the Go module root is `daemon/`, so
none of those paths resolved as written.

### BUILT AND IN FORCE

| Claim | Evidence in this repo |
|---|---|
| No look-ahead in feature and backtest code | Go tests in `daemon/internal/backtest/backtest_test.go`, `daemon/internal/alphax/alphax_test.go`, `daemon/internal/api/trackrecord_test.go`, and 5 more; all run in CI |
| Execution simulation — next-bar-open fills, single round-trip cost proxy | `daemon/internal/backtest/backtest.go` + `backtest_test.go` |
| Model registry and governance | `daemon/internal/modelhealth` (3 importers); gate in `daemon/internal/pipeline/modelhealth.go` + `modelhealth_gate_test.go`, `modelhealth_registry_test.go` |
| Automatic model retirement | `daemon/internal/pipeline/modelhealth_gate_test.go`; verdicts persisted, non-emitting models suppressed |
| Concept/feature drift monitoring | `daemon/internal/modelhealth/drift.go` + `drift_test.go` — two-sample KS, n-adjusted critical value |
| Calibration monitoring | `GET /api/calibration` registered at `daemon/internal/api/predict.go:160`; `cachekey_test.go` |
| Explainability | `AuditTrend` in `daemon/internal/structregime/explain.go` + `explain_test.go`; `daemon/internal/api/explain.go` |
| Data quality / freshness checks | `dq-auditor` wired in `daemon/internal/maintain/maintain.go` |
| Corporate actions | `daemon/internal/splitfix` + `splitfix_test.go`, imported by the repair worker |
| Pre-trade risk limits — **paper book only** | `daemon/internal/riskgate` + `riskgate_test.go`; importers `pipeline/paper.go`, `pipeline/paperrisk.go`, `stresslab/replay.go`, `api/stress.go`. No real capital is attached to any of them |
| Data-client rate limits, backoff, reconnect | `daemon/internal/ingest/alpaca/client.go` — 429 backoff. Alpaca is a **market-data** client here, not an execution client |
| CI with unit and integration tests | `.github/workflows/ci.yml` — `go build`, `go vet`, `go test -race ./...`, a clone-only rebuild from `git archive HEAD`, and `npm ci && npm run lint && npm run build` |
| Secrets management | `.env` and `.env.*` gitignored (`.gitignore:6-7`); constant-time token compare at `daemon/internal/api/auth.go:27` and `tvwebhook.go:101` |
| Data licensing controls | `daemon/internal/datalicense` + HTTP 451 guard at `daemon/internal/api/api.go:606`, asserted by `daemon/internal/api/export_test.go:62` |
| Local database backup | `ops/signaldeck-backup-offline.sh`; `logs/backup-offline.log` 2026-08-04 — 3843 MB written, SHA-256 recorded, retention pruning ran |
| Multi-source price validation | `daemon/internal/pricecheck` + `pricecheck_test.go`, imported by `pipeline/honestygaps.go` |
| Canary / staged model rollout | `daemon/internal/canary` + `canary_test.go`, `cluster_test.go`, `readmit_test.go`; 5 importers including `api/modelhealth.go` |
| Dataset versioning and checksums | `daemon/internal/datasetver` + `datasetver_test.go`, imported by `pipeline/honestygaps.go` |
| Nightly bias regression | Scheduled task `SignalDeck Bias`; `logs/nightly-bias.log` last line `nightly bias regression: PASS` |

The last four rows were listed in `INSTITUTIONAL_GAP.md` as **"genuinely missing and
genuinely worth building"**. They have since been built. That section was stale in the
opposite direction — understating rather than overstating — and is corrected too.

### BUILT BUT NOT IN FORCE

| Claim | What is actually true |
|---|---|
| Point-in-time universe | ~~`universe_membership` holds **0 rows**~~ — **AMENDED 2026-08-04.** Re-measured at the close of P6: **1,854,228 rows**, 2,146 distinct days, 1,777 distinct symbols, spanning epoch days 1532563200–1785888000. The table is populated. Every row carries `source = 'bars-1d'`, i.e. membership is derived from the retained daily bars, so the table is only as point-in-time as that bar history is complete. Reclassified **BUILT AND POPULATED — coverage not independently assessed by this phase.** The assessment belongs to P3A, whose proof artifact is not filed |
| Survivorship-bias control | `ResearchUniverse` / `TradableAt` live in `daemon/internal/pipeline/delisting.go` + `delisting_test.go` — not in `store` as cited. `ALPHA_WORKFLOW.md` §B2 measured this control **open**. **AMENDED 2026-08-04**: `proofs/P3A_SURVIVORSHIP_BACKFILL.md` is now filed and records `delisted_at` stamps rising 16 → 716, with the verdict *substantially closed for 2019–2022* and a residual it quantifies rather than hides — 2023–2025 is under-covered because the upstream endpoint under-reports recent years and the live detector only began watching in 2026. Reclassified **PARTIALLY IN FORCE**: FC3 narrows to the 2023–2025 window instead of lifting |
| Walk-forward, non-overlapping validation | `tools/revalidate_structural.py` exists and runs on demand. Nothing schedules it and no gate consumes its output. The "54,969 obs, quarter-block CIs" figure describes one past run, not a standing measurement |
| Kill switch | ~~`daemon/internal/killswitch/killswitch.go` exists, is well-specified, and has **zero importers and no test file**. Nothing calls it. It cannot halt anything in its current state~~ — **AMENDED 2026-08-04 by P4C.** True as measured; the package was mid-flight when this row was written. It is now imported by `daemon/internal/pipeline/paper.go`, read before every order, and exercised by `killswitch_test.go` plus an end-to-end halt simulation (`pipeline.TestKillSwitch_HaltsEntriesAndLedgersTheRefusal`). Reclassified **BUILT AND IN FORCE — paper book only**. Evidence: `proofs/P4C_KILL_SWITCH_CORRECTION.md` |
| Offsite backup | `logs/backup-offline.log`, 2026-08-04: `offsite SKIPPED: no destination configured`. The iCloud path in the script is macOS-only and this platform is Windows; `SIGNALDECK_OFFSITE_DIR` is unset. Backups exist on one machine, on one volume |
| Forensic audit trail — external tamper-evidence | `prediction_ledger` holds 322,515 rows and `ledger_anchors` 5, both hash-chained and signed. Both live in the same SQLite file the operator controls. The chain is tamper-evident against an actor **without** the signing key and not against the operator. See §5 |

### NOT BUILT

| Claim | What is actually true |
|---|---|
| Kill switch cited as `stock-trader/trader/risk_gate.py` | That file is in a **different repository**. No claim in this corpus may rest on it, and none now does. The in-repo replacement is `daemon/internal/killswitch` — **wired and tested 2026-08-04 under P4C**, see the amended row above |
| Slippage measurement (`slippage_log.jsonl`, executed-qty ledger reconciled against the broker) | No such ledger exists in this repository. This describes a `stock-trader` artifact. This repository has no broker connection and executes nothing |
| The three named point-in-time tests | `test_pit_fundamentals`, `test_no_lookahead` and `test_fill_timing` do not exist anywhere in this repository. The only occurrences of those strings are in prose — `INSTITUTIONAL_GAP.md` itself and `audits/2026-07-26-reaudit.md`. Look-ahead coverage does exist under other names (first row of the in-force table); the cited tests do not |

### Stale figures removed

| Figure as printed | Measured 2026-08-04 |
|---|---|
| `prediction_ledger`, 235k rows | 322,515 rows |
| directional ensemble "48.0% vs 54.4% baseline" | frozen under **FC1** — see §4. Four documents print four different pairs of numbers for this. No figure is restated here |

---

## 2. P6B — document control headers

Every strategy document now carries a seven-line control header immediately under its
title. The template:

```md
<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** <FROZEN — NOT AUTHORITATIVE | FROZEN — AUTHORITATIVE | ACTIVE>
> **Scope:** <one line: what this document is the record of>
> **Frozen claim classes:** <FC ids, or NONE> — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4
> **Authority:** `proofs/P0_FREEZE.md` (freeze) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** BLOCKED — internal use only
```

Applied to: `ALPHA_WORKFLOW.md`, `ARCHITECTURE_EV.md`, `CASE_STUDY.md`,
`DATA_SOURCES.md`, `EDGE_PLAN.md`, `HOW_PREDICTORS_WORK.md`, `INSTITUTIONAL_GAP.md`,
`PAIRS_TRADING.md`, `PREDICTION_PROCESS.md`, `README.md`, `STRATEGY_DECK.md`.

**`PREREGISTRATION.md` is deliberately excluded**, for the reason recorded in
`P0_FREEZE.md` §5: its SHA-256 is the `specHash` of the chain's `prereg-document`
record, and `daemon/internal/prereg/document_claims_test.go` parses its prose against
the chain. Any edit — including a governance header — breaks that digest and forces an
unintended amendment. Its control metadata is registered here instead:

| Field | Value |
|---|---|
| Owner | Nicholas Nyaung |
| Status | ACTIVE — chain-digested, immutable outside the amendment path |
| Scope | The frozen 2026-08-14 structural grading protocol |
| Frozen claim classes | NONE — this document *is* the pre-registration |
| Publication | BLOCKED by reference, per `P0_FREEZE.md` §5 |
| SHA-256 at P0 | `01d6bcdfe1451b6f2c84185478f9c8f3c405ef3ae845a11a937d865eacf2a724` |

---

## 3. P6C — interval language

The platform withholds every confidence interval for the directional record: the
distinct-day block counts are 9, 5, 4 and 4 against a floor of
`min_distinct_blocks = 10`. A verdict that quantifies an interval cannot be stated
where no interval is published. Two documents stated one anyway.

| File | Claim removed |
|---|---|
| `PREDICTION_PROCESS.md` §"Layer 6" | "the whole confidence interval below the null, which is significant *negative* skill, not merely no edge" |
| `CASE_STUDY.md` | "with the entire day-clustered interval below the baseline" |

Both are replaced by the single sanctioned line:

```md
> No interval verdict is available: the platform withholds every interval for this claim (distinct-day blocks below `min_distinct_blocks = 10`).
```

What survives is the point estimate with its status label, and the operational fact
that the retirement rule fired. What does not survive is any statement about
significance, sign of skill, or interval position. "Significant negative skill" is a
verdict that requires the interval it was drawn from; it is withdrawn, not softened.

This does not make the directional family look better. It makes the claim match the
evidence that is actually published.

---

## 4. P6D — definition of `C`

`C` is the frozen claim set. Eleven documents each declared their own "frozen claim
classes" list in their own wording — "intervals" in one, "confidence-interval
verdicts" in another, "point-in-time data" against "point-in-time universe" — with no
canonical list anywhere. Two other documents use `C1`/`C2`/`C3` as finding labels,
which read as members of the same set and are not.

**Definition, stated once and nowhere else:**

```md
C = the frozen claim set. Its members are FC1-FC8, enumerated in `proofs/P6_GOVERNANCE_CLEANUP.md` §4.
No other document may define, extend, re-word, or re-order C; document headers cite FC ids only.
```

| Id | Class | Why frozen |
|---|---|---|
| FC1 | Live accuracy figures | Four mutually inconsistent versions across four documents |
| FC2 | Confidence-interval verdicts | Published while the platform withholds every interval — see §3 |
| FC3 | Survivorship control status | Asserted closed in three documents; measured open in `ALPHA_WORKFLOW.md` §B2. **Narrowed 2026-08-04 by P3A** — substantially closed for 2019–2022; stays frozen for 2023–2025, where the backfill's own artifact records the coverage gap |
| FC4 | Point-in-time universe status | Asserted as established while `universe_membership` held 0 rows. **Partially resolved 2026-08-04** — the table is populated (1,854,228 rows). It stays frozen for the narrower claim that the membership is point-in-time, because every row derives from `bars-1d` and that derivation was not audited here |
| FC5 | Kill switch existence and enforcement | Claimed via a different repository; the in-repo package was unwired. **RESOLVED 2026-08-04 (P4C)** — wired, enforced before every order, halt-simulated. FC5 lifts for the paper book only; no live order path exists to halt |
| FC6 | Position sizing / capital binding | Marked "Exists"; not bound to any allocation decision |
| FC7 | "Already built / verified" attestations | Superseded by §1 of this document |
| FC8 | Structural forecast counts | ~12,529 stated where the per-kind table sums to 11,853 |

**Namespace note.** The `C1`/`C2`/`C3` labels in `HOSTILE_REVIEW_FIX_SUPERPROMPT.md`
and `REMEDIATION_SYNTHESIS_2026-08-04.md` are finding identifiers in a separate
namespace and are **not** members of `C`. Members of `C` carry the `FC` prefix
specifically so the two cannot be confused.

---

## 5. P6E — pre-registration chain anchoring

**Status: INTERNAL — UNANCHORED.**

| Fact | Evidence |
|---|---|
| The chain is signed and hash-linked | `daemon/internal/ledgeranchor` + `ledgeranchor_test.go`; 5 rows in `ledger_anchors` |
| Every anchor lives in the operator's own SQLite file | `data/signaldeck.db` — the same file whose contents the anchors attest |
| An external publication path exists | `ops/anchor-publish.sh` |
| Nothing invokes it | No caller in `ops/`, `tools/`, `daemon/` or `.github/`; no `SignalDeck Anchor` scheduled task among the 12 registered |
| It has never succeeded | `logs/anchor-publish.log`, last entry 2026-07-27: `FAIL: no public clone at /nonexistent — nothing was externally timestamped`. The entry before it: `PUSH FAILED — commit exists locally only` |
| The public clone does not exist | `~/.signaldeck/anchor-publish` absent; `SIGNALDECK_ANCHOR_REPO` unset |

### What this means for the tamper-evidence claim

The chain is tamper-evident against an actor who does not hold the signing key. It is
**not** evidence against the operator, who holds the key and the database, and who can
therefore regenerate any history and re-sign it. No timestamp exists that the operator
did not create. Any claim of an unfalsifiable track record is unsupported until an
anchor lands somewhere the operator cannot rewrite.

### In force now

**External publication of the chain, the registry, or any track record derived from
them is blocked**, and remains blocked while this status reads UNANCHORED. This is
narrower than but consistent with the P0 freeze: even after P0 lifts, anchoring is an
independent gate on the specific claim that the record cannot be retouched.

### To reach ANCHORED — INTERNAL + EXTERNAL

1. Create the public anchors repository and clone it to `~/.signaldeck/anchor-publish`;
   set `SIGNALDECK_ANCHOR_REPO`. `ops/anchor-publish.sh` never creates or re-points a
   remote by design, so this step is manual and deliberate.
2. Register a daily scheduled task that runs it, and treat a `PUSH FAILED` line in
   `logs/anchor-publish.log` as a failure with an alert, not a log entry. The current
   script already logs the failure; nothing reads it.
3. Weekly third-party timestamping — an OpenTimestamps commitment over the chain head
   is the cheapest form that does not depend on a repository the operator also
   controls. Not yet implemented; recorded here as the open item, not as a plan that
   has been executed.

Step 3 is what actually closes the gap. Steps 1 and 2 move the evidence to a host the
operator still controls the credentials for — better than nothing, weaker than a
timestamp authority.

---

## 6. Change proof

| File | Change |
|---|---|
| `INSTITUTIONAL_GAP.md` | Attestation table replaced by a three-status classification; stale "genuinely missing" section corrected; control header added |
| `PREDICTION_PROCESS.md` | Interval verdict removed; control header added |
| `CASE_STUDY.md` | Interval verdict removed; control header added |
| `ALPHA_WORKFLOW.md`, `ARCHITECTURE_EV.md`, `DATA_SOURCES.md`, `EDGE_PLAN.md`, `HOW_PREDICTORS_WORK.md`, `PAIRS_TRADING.md`, `README.md` | Control headers added; per-document frozen-class prose replaced by FC ids |
| `PREREGISTRATION.md` | **Unchanged by design** — digest preserved |
| `proofs/P6_GOVERNANCE_CLEANUP.md` | This file |

---

## 7. Done-when criteria

| Criterion | Status | Basis |
|---|---|---|
| No false "verified" claims remain | **MET** | Every row of §1 carries a status with a named in-repo artifact. The word *verified* appears in no status column. Three rows moved to NOT BUILT; six to BUILT BUT NOT IN FORCE, of which two were later re-measured and amended in place rather than silently upgraded |
| Every document has owner / version / status | **MET** | Eleven documents carry the §2 header; `PREREGISTRATION.md`'s metadata is registered in §2 rather than embedded, for the digest reason recorded there |
| No interval verdict where intervals are withheld | **MET** | Both occurrences removed; §3 records what was removed and what replaced it |
| Chain anchoring status is explicit | **MET** | §5 — INTERNAL — UNANCHORED, with the failing log line as evidence and external publication blocked |
| Citations resolve to artifacts that exist | **MET, and guarded** | Three citations pointed at unfiled artifacts during this phase; all three were filed by its close (§0). The recurrence is guarded by `tools/check_strategy_deck.py`, which fails on a claim that a `proofs/` artifact is missing when it exists |

## 8. What P6 did not fix

Recorded so the next phase does not inherit a false floor:

- ~~`universe_membership` is still empty.~~ **Populated 2026-08-04** — 1,854,228 rows.
  FC4 is partially resolved. FC3 is narrowed, not lifted: P3A closes survivorship for
  2019–2022 and quantifies a 2023–2025 residual that remains open.
- ~~`daemon/internal/killswitch` is still unwired and still has no test.~~ **Closed 2026-08-04 by P4C**: wired into the paper worker, read before every order, halt-simulated end to end. It halts the only executor this repository has; there is still no live order path.
- Offsite backup is still a single volume on a single machine.
- The chain is still unanchored externally. This is the largest unclosed item: it is
  the difference between a record that is internally consistent and a record an
  outsider can check.
- The `bars-1d` history that `universe_membership` derives from has not itself been
  audited for completeness. `proofs/P3B_PIT_UNIVERSE.md` establishes that the
  membership is point-in-time and that the predicted §B3 look-ahead is not present;
  the completeness of the underlying bar history is a separate question it does not
  answer.
- **The freeze's lift conditions are met and the freeze has not been lifted.** No
  successor artifact exists, and the anchoring gate in §5 is independent of the phase
  count. Deciding it is the open governance item.

P6 changed what the corpus *claims*. Where the platform changed underneath it — the
universe backfill and the kill switch — the rows were re-measured rather than
inherited, and both are labelled with what was and was not checked.
