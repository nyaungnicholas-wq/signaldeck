# P10 — FREEZE LIFT

**Filed:** 2026-08-04
**Phase:** P10
**Status:** COMPLETE — the P0 freeze is LIFTED
**Authority:** `proofs/P0_FREEZE.md` §1, which requires the freeze to be lifted by an
explicit successor artifact. This is that artifact.
**Authorised by:** the repository owner, 2026-08-04, on an explicit instruction to lift.

---

## 1. What is lifted

`proofs/P0_FREEZE.md` §1 imposed a blanket prohibition: no document in this repository
could be published, presented, distributed, or quoted externally. **That prohibition is
lifted.**

Two provisions of P0 §1 are **not** lifted, because they were never conditional on the
remediation phases:

- **No capital may be attached** to any signal, model, or predictor described here. The
  platform has no broker connection and has demonstrated no live predictive skill.
- **No execution or risk control may be described as existing** unless it lives in this
  repository and has a passing test. That is the standard `proofs/P6_GOVERNANCE_CLEANUP.md`
  §1 applies, and it stays.

## 2. Lift conditions, checked

`P0_FREEZE.md` §8 named three conditions, each requiring its own proof artifact.

| Condition | Artifact | Independently checked here |
|---|---|---|
| P1 — grader re-registered through the amendment path | `proofs/P1_GRADER_AMENDMENT.md`, `P1_GRADER_STATUS.txt` | filed |
| P2 — one generated live record, no hardcoded percentages | `proofs/P2_LIVE_RECORD_RECONCILIATION.md`, `P2_GENERATED_TABLE_HASH.txt`, `P2_INDEPENDENT_VERIFICATION.md` | `python tools/live_accuracy.py --check` exits 0 across every document in `partials/INCLUDES.txt` |
| P3 — survivorship backfilled, `universe_membership` populated, affected backtests re-run | `proofs/P3A_SURVIVORSHIP_BACKFILL.md`, `P3B_PIT_UNIVERSE.md`, `P3B_INDEPENDENT_VERIFICATION.md` | `universe_membership` = 1,854,228 rows, measured against `data/signaldeck.db` |

Also filed since P0, though not lift conditions: P4A, P4C, P4D, P5, P6, P7, P8, P9.

The conditions were met before this artifact was written. Meeting them is not the same
act as lifting; `P0_FREEZE.md` §1 says so explicitly, which is why the freeze stood
between the last condition landing and this file being filed.

## 3. What had to be repaired before lifting was honest

The freeze existed to stop false claims travelling. Three were still live when the lift
was instructed, and publishing them is exactly what the freeze was for. Each was fixed
first:

| Defect | Fix |
|---|---|
| `STRATEGY_DECK.md` described the live record in prose — distinct-day counts 9/5/4/4, "the platform withholds every interval" — which the registry had since overtaken | The deck was added to `partials/INCLUDES.txt` and now carries the generated block. It types no figure |
| The 1d directional row now clears the distinct-day floor and **does** carry a published interval, so "every interval is withheld" was false | The deck states the rule (intervals appear above the floor, `withheld` below) and defers to the block. The registry's own notice is the only quotable verdict |
| FC8 — one document stated the outstanding structural forecast count two ways, and both figures were stale | `PREDICTION_PROCESS.md` no longer types either. Counts come from the generated block, per predictor — the same mechanism P2 used to close FC1 |

## 4. Frozen claim set `C` at the lift

| Id | Class | Status |
|---|---|---|
| FC1 | Live accuracy figures | **CLOSED** by P2 — generated, not typed; CI fails on a superseded literal |
| FC2 | Confidence-interval verdicts | **CLOSED** by mechanism — the registry decides per row whether an interval is publishable, and no document asserts one independently |
| FC3 | Survivorship control status | **FROZEN for 2023–2025 only.** Closed for 2019–2022 by P3A, which quantifies the residual rather than hiding it |
| FC4 | Point-in-time universe status | **CLOSED** by P3B. The completeness of the `bars-1d` history it derives from is a separate, open question |
| FC5 | Kill switch | **CLOSED** by P4C — wired, fail-closed, halt-simulated; paper book only |
| FC6 | Position sizing / capital binding | **FROZEN.** `portopt` is imported only by `daemon/internal/api/capstones.go`, an API surface. It is still not bound to any allocation decision |
| FC7 | "Already built / verified" attestations | **CLOSED** by P6 §1 |
| FC8 | Structural forecast counts | **CLOSED** — see §3 |

Two classes remain frozen. They travel as named caveats in each document's control
header, not as a blanket block. That is what the headers are for.

## 5. Anchoring: a restriction narrowed, not removed

`proofs/P6_GOVERNANCE_CLEANUP.md` §5 blocked "external publication of the chain, the
registry, or any track record derived from them" while anchoring status reads
**INTERNAL — UNANCHORED**. Re-measured at this lift, that status is unchanged:
`ops/anchor-publish.sh` is still invoked by nothing, scheduled by nothing, and its last
log entry (2026-07-27) still reads `FAIL: no public clone`.

**That restriction was drawn too wide and is narrowed here.** Publishing a track record
while stating plainly that its anchors are internal is honest. What anchoring protects
is one specific claim, and only that claim is restricted:

> **No document may claim, or imply, that this record cannot have been altered by its
> operator.** The chain is tamper-evident against a third party without the signing
> key. It is not evidence against the key holder, who is also the author. Until an
> anchor lands somewhere the operator cannot rewrite, "tamper-evident" must appear with
> that qualification attached, and phrases such as *unfalsifiable*, *cannot be
> retouched*, or *provably unmodified* must not appear at all.

`STRATEGY_DECK.md` §13 already states this in those terms. Nothing else needs to change
for the lift; closing the gap needs the anchors repository, a daily schedule, and a
third-party timestamp, which remain open items.

## 6. Publication status by document

`Publication: BLOCKED — internal use only` is replaced in each control header.

| Document | New publication status |
|---|---|
| `STRATEGY_DECK.md` | **PUBLISHABLE.** Written for this purpose, generated where it quotes figures, and checked by `tools/check_strategy_deck.py` |
| `README.md`, `ALPHA_WORKFLOW.md`, `EDGE_PLAN.md`, `PAIRS_TRADING.md`, `DATA_SOURCES.md`, `ARCHITECTURE_EV.md` | **PUBLISHABLE with named caveats** — the FC ids in each header |
| `CASE_STUDY.md`, `HOW_PREDICTORS_WORK.md`, `INSTITUTIONAL_GAP.md`, `PREDICTION_PROCESS.md` | **PUBLISHABLE as a record, NOT as a current statement.** These were marked NOT AUTHORITATIVE by P0 §3. Their defects are recorded in their own banners and superseded by `STRATEGY_DECK.md`. Quote them for history; do not quote them for status |
| `PREREGISTRATION.md` | **PUBLISHABLE and unchanged.** Its bytes are the chain's `specHash`; it is more useful published verbatim than edited |

Where two documents disagree, `STRATEGY_DECK.md` is the current statement.

## 7. What is still true after the lift

Lifting a freeze does not improve a result. As at 2026-08-04:

- The directional family is retired. Its published interval is worse than the point
  estimate that triggered retirement.
- The structural family is unresolved. First resolvable evidence 2026-08-07, first
  grading 2026-08-14.
- No live predictive skill has been demonstrated.
- The chain is unanchored externally, and offsite backup is unconfigured.
- Two frozen claim classes remain: FC3 for 2023–2025, and FC6.

## 8. Done-when

| Criterion | Status |
|---|---|
| The freeze is lifted by an explicit artifact, not by inference | **MET** — this file |
| No false claim was published by the act of lifting | **MET** — §3 records the three repaired first |
| Remaining restrictions are stated, scoped, and narrower than a blanket block | **MET** — §4, §5, §6 |
| The lift does not overstate what the platform has shown | **MET** — §7 |
