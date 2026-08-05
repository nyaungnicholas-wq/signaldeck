# Institutional spec — what's covered, what's missing, what's cargo cult

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** FROZEN — NOT AUTHORITATIVE
> **Scope:** Coverage assessment against an institutional quant-platform specification. Its status table is superseded by `proofs/P6_GOVERNANCE_CLEANUP.md` §1.
> **Frozen claim classes:** FC1, FC5, FC6, FC7 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4
> **Authority:** `proofs/P0_FREEZE.md` (freeze) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** BLOCKED — internal use only

> ## ⛔ NOT AUTHORITATIVE — FROZEN — DO NOT DISTRIBUTE
> **P0 freeze, 2026-08-04.** This document is under remediation and **must not be
> published, presented, or quoted externally.**
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
> Authority and scope: `proofs/P0_FREEZE.md`. Lifts only after P1–P3 complete.

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

Generated from `data/accuracy_registry.json` (grade of 2026-08-04T18:23:15) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,911 | 43.1% | 56.8% | -13.7pp | 10 | [31.1%, 55.9%] |
| prequential-majority (1d) | all | 2,298 | 59.8% | 56.6% | +3.2pp | 7 | withheld |
| directional-ensemble (1w) | all | 938 | 45.6% | 52.2% | -6.6pp | 5 | withheld |
| prequential-majority (1w) | all | 332 | 57.2% | 49.8% | +7.4pp | 2 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 300 | 55.3% | 60.0% | -4.7pp | 6 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 62 | 45.2% | 44.4% | +0.8pp | 4 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1d)` — INSUFFICIENT DAYS (7/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (5/10 distinct days) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (2/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (6/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (4/10 distinct days) — no interval, so no verdict

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 77 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 3,624 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 66 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 3,649 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 66 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 3,649 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 3,665 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=9, divisor=117, corrected_alpha=0.00042735042735042735.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 326/335 graded symbols (97.3%): 9 inactive symbol(s) with no delisted_at.

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
