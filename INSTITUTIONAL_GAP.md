# Institutional spec — what's covered, what's missing, what's cargo cult

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** SUPERSEDED — historical record; current status is `STRATEGY_DECK.md`
> **Scope:** Coverage assessment against an institutional quant-platform specification. Its status table is superseded by `proofs/P6_GOVERNANCE_CLEANUP.md` §1.
> **Frozen claim classes:** FC6 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, statuses in `proofs/P10_FREEZE_LIFT.md` §4
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE as a record, NOT as a current statement — see `STRATEGY_DECK.md`

> ## ⛔ SUPERSEDED — publishable as a record, not as current status
> **P0 freeze 2026-08-04, lifted 2026-08-04** (`proofs/P10_FREEZE_LIFT.md`). Remediation
> is complete. This document is publishable as a record of what was measured and when;
> it **must not be quoted as *current* status** — quote `STRATEGY_DECK.md` for that.
>
> Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **"Already built (verified, not claimed)" —
> every row · survivorship control · point-in-time data · kill switch · live accuracy.**
>
> Known defects: three rows in the "Already built (verified, not claimed)" table are
> contradicted by measurement elsewhere in this repository — (1) *Kill switch +
> pre-trade risk limits* cited `stock-trader/trader/risk_gate.py`, a file in a
> **different repository** that cannot halt this daemon — **defect (1) closed
> 2026-08-04 by P4C**: the citation is gone and an in-repo, fail-closed,
> halt-simulated switch (`daemon/internal/killswitch`) replaces it, governing the
> simulated book; (2) *Survivorship-bias
> control* was measured open in `ALPHA_WORKFLOW.md` §B2 (21 delistings / 1,077 names
> / 7.5 years) — **narrowed 2026-08-04 by P3A**: `delisted_at` stamps rose 16 → 716,
> closing 2019–2022, with a 2023–2025 residual that
> `proofs/P3A_SURVIVORSHIP_BACKFILL.md` quantifies rather than hides; (3) *Point-in-time
> data, no look-ahead* was contradicted by `ALPHA_WORKFLOW.md` §B3
> (`universe_membership` holding zero rows) — **partially closed 2026-08-04**: the
> table now holds 1,854,228 rows, all derived from `bars-1d`, so it is only as
> point-in-time as that bar history is complete, which nothing has audited.
>
> **The whole table below was replaced in P6.** The three-status classification and
> its evidence are in `proofs/P6_GOVERNANCE_CLEANUP.md` §1, which is the authority
> on what this document used to attest.
>
> Authority: `proofs/P0_FREEZE.md`, **lifted 2026-08-04** by `proofs/P10_FREEZE_LIFT.md`.

Audited 2026-07-25 against a full institutional quant-platform specification.
Split three ways, because the honest answer is not "build all of it": a spec
written for a bank running billions contains controls that are load-bearing at
that scale and pure ceremony at this one. Adopting those anyway is how a
project acquires the appearance of rigor without the substance.

## Spec coverage, by status

This section replaces a table previously headed *"Already built (verified, not
claimed)"*. That heading asserted verification as a property of the whole table,
which is how three false rows travelled undetected. Nothing here is described as
verified. Each row carries one of three statuses, and a status is only as good as
the artifact named beside it.

A row reads **BUILT AND IN FORCE** only when the artifact is in *this* repository,
is exercised by a test or a scheduled job, and is currently imported, scheduled, or
gated on. Failing the last condition gives **BUILT BUT NOT IN FORCE**. Failing the
first gives **NOT BUILT**, whatever exists in a sibling repository.

The classification, its evidence, and the eleven cited paths that did not resolve
are recorded in full in **`proofs/P6_GOVERNANCE_CLEANUP.md` §1**, which is the
authority. The summary:

### BUILT AND IN FORCE

No look-ahead in feature and backtest code · execution simulation (next-bar-open
fills, single round-trip cost proxy) · model registry and governance · automatic
model retirement · drift monitoring · calibration monitoring · explainability ·
data-quality and freshness checks · corporate actions · pre-trade risk limits
**(paper book only)** · kill switch **(paper book only — `daemon/internal/killswitch`,
fail-closed, read before every order, halt-simulated end to end; added by P4C
2026-08-04)** · data-client rate limits and backoff · CI (`go test -race`,
clone-only rebuild, web lint and build) · secrets management · data licensing and
the HTTP 451 export guard · local database backup · multi-source price validation ·
canary rollout · dataset versioning · nightly bias regression.

The last four were listed below, in an earlier version of this file, as *missing*.
They have since been built. This document was stale in both directions.

### BUILT BUT NOT IN FORCE

| Control | What is actually true |
|---|---|
| Point-in-time universe | **Amended 2026-08-04.** `universe_membership` held 0 rows; it now holds 1,854,228 across 2,146 days and 1,777 symbols. Every row derives from `bars-1d`, so it is only as point-in-time as that bar history is complete — not assessed here |
| Survivorship-bias control | **PARTIALLY IN FORCE.** `ALPHA_WORKFLOW.md` §B2 measured it open. `proofs/P3A_SURVIVORSHIP_BACKFILL.md` records `delisted_at` stamps rising 16 → 716 and the verdict *substantially closed for 2019–2022*, with a 2023–2025 residual it quantifies. FC3 narrows to that window rather than lifting |
| Walk-forward validation | `tools/revalidate_structural.py` runs on demand. Nothing schedules it; no gate consumes it |
| ~~Kill switch~~ | ~~`daemon/internal/killswitch/killswitch.go` has **zero importers and no test file**. Nothing calls it~~ — **moved to BUILT AND IN FORCE 2026-08-04 by P4C.** See `proofs/P4C_KILL_SWITCH_CORRECTION.md` |
| Offsite backup | `offsite SKIPPED: no destination configured` — one machine, one volume |
| Forensic audit trail, externally | The chain and its anchors live in the operator's own database. Tamper-evident against an outsider, not against the operator |

### NOT BUILT

| Claimed | What is actually true |
|---|---|
| Kill switch via `stock-trader/trader/risk_gate.py` | A different repository. No claim here may rest on it |
| Slippage measurement / executed-qty ledger | A `stock-trader` artifact. This repository has no broker connection and executes nothing |
| `test_pit_fundamentals`, `test_no_lookahead`, `test_fill_timing` | None of the three exist anywhere in this repository. Look-ahead coverage does exist, under other names |

### Figures withdrawn

The retirement row used to print a hand-typed accuracy and baseline, and three
other documents printed three different pairs for the same record — **FC1**.
**Closed in P2 (2026-08-04):** no document types the record any more. It is
generated into `partials/live_accuracy.md` from `data/accuracy_registry.json`
and included identically wherever it appears, and CI fails on a superseded
literal. The current record is the block below; see
`proofs/P2_LIVE_RECORD_RECONCILIATION.md`. The ledger row read 235k; the
measured count on 2026-08-04 is 322,515.

<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-27T19:00:54) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 13 collapsed cross-section(s) of 48 day(s): 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 326 symbols), 1w 2026-07-29 (25 distinct across 326 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-08-27T19:00:54. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,767 | 44.4% | 55.3% | -11.0pp | 24 | [34.8%, 54.4%] |
| prequential-majority (1d) | all | 2,438 | 56.2% | 54.2% | +2.0pp | 22 | [37.9%, 73.0%] |
| directional-ensemble (1w) | all | 4,942 | 45.0% | 58.4% | -13.4pp | 24 | withheld |
| prequential-majority (1w) | all | 4,550 | 59.1% | 58.5% | +0.6pp | 21 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 614 | 40.7% | 56.1% | -15.4pp | 21 | withheld |
| filingsdrift21 | all | 15 | — | — | — | 2 | withheld |
| liquidity21 | all | 276 | withheld — no null | — | — | 2 | withheld |
| liquidity21#persist | all | 1 | withheld — no null | — | — | 1 | withheld |
| liquidity21-crypto | all | 21 | — | — | — | 3 | withheld |
| trend21 | all | 276 | withheld — no null | — | — | 2 | withheld |
| trend21#persist | all | 1 | withheld — no null | — | — | 1 | withheld |
| trend21-crypto | all | 21 | — | — | — | 3 | withheld |
| vol21 | all | 278 | withheld — no null | — | — | 1 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4438 [0.3477, 0.5443] and its null 0.5535 [0.3878, 0.7081] OVERLAP across [0.3878, 0.5443]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1097 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (4/10 credible days of 24, 20 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (4/10 credible days of 21, 17 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (4/10 credible days of 21, 17 degenerate) — no interval, so no verdict
- `filingsdrift21` — PENDING (first grade 2026-08-14, 15/30 resolved)
- `liquidity21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21#persist` — BENCHMARK (INSUFFICIENT 1/30)
- `liquidity21-crypto` — PENDING (first grade 2026-08-14, 21/30 resolved)
- `trend21` — NO BASELINE — naive-persistence null not frozen for these calls
- `trend21#persist` — BENCHMARK (INSUFFICIENT 1/30)
- `trend21-crypto` — PENDING (first grade 2026-08-14, 21/30 resolved)
- `vol21` — NO BASELINE — naive-persistence null not frozen for these calls

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 9,214 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=15, looks=43, divisor=645, corrected_alpha=7.751937984496124e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.42pp (active-only 83.56% minus survivorship-clean 83.14%, n=75,179 clean vs 18,025 active, revalidation of 2026-08-23T10:20:15+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->


## Deliberately NOT building (cargo cult at this scale)

Each of these is genuinely important at a bank and actively harmful here,
because it adds operational surface without reducing any risk this system has.

- **Kafka / Kubernetes / multi-AZ failover.** One user, one machine, daily
  bars. This would add failure modes, not remove them.
- **Order-book replay simulation.** The strategies trade daily closes. There is
  no order book in the decision, so simulating one models nothing.
- **FIX protocol.** Alpaca's REST API is the execution path.
- **SOC2 / formal compliance program.** No customers, no custody, no
  regulated activity. Becomes real the day any of those change.
- **PagerDuty / 24-7 on-call rota.** macOS notification plus a Discord webhook
  is the proportionate version of the same control.
- **Separate risk-officer approval workflow.** One engineer. The control that
  actually substitutes for it is the automated gate that cannot be argued with.

## The honest summary

Most of the spec's *principles* — measure independently, never trust raw data,
retire what stops working, log enough to reconstruct any decision — are
implemented and binding. Two are not, and the earlier version of this file
claimed both:

- ~~**Fail closed.** The halt control exists as a package and is wired to nothing.
  A kill switch nothing calls is a design document.~~ **Amended 2026-08-04 (P4C).**
  The halt control is now imported by the paper worker, read fresh before every
  order, and proven by an end-to-end halt simulation. It halts the only executor
  this repository has. What it still cannot do is halt live trading, because
  there is no live order path here to halt — the control is real, its blast
  radius is a simulated book.
- **Reconstruct any decision, provably.** The ledger reconstructs decisions. It
  does not prove the operator did not rewrite them, because every anchor sits in
  the operator's own database — see `proofs/P6_GOVERNANCE_CLEANUP.md` §5.

The spec's *infrastructure* is sized for a different problem, and that judgement
stands. But the gap is not only the absence of live forward evidence. It is also
that three controls this file listed as built are unwired, empty, or in another
repository. The first live structural evidence arrives 2026-08-07; that date does
not close any of the three.
