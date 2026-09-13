# SignalDeck — Strategy Deck

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.2 · **Last reviewed:** 2026-08-05 · **Revalidate by:** 2026-09-04
> **Revalidation trigger:** any re-grade, or any phase artefact filed in `proofs/`. A status document with no expiry drifts silently; §2's live record is generated so it cannot, but the prose around it can.
> **Status:** ACTIVE — supersedes prior summary decks
> **Scope:** The single current status document for the platform. Where it disagrees with an older document, this one wins: the others are publishable as a record of what was measured when, not as current status.
> **Frozen claim classes:** FC3 (survivorship, 2023–2025 — see §8.2, the residual has since been re-measured), FC4 (point-in-time membership, narrow claim), FC6 (`portopt` not bound to allocation). Set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4. **v1.1 omitted FC4 from this line and stated no status at all for FC2 or FC7; §13.4 now carries all eight.**
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE — current statement of record

## 0. Disclaimers

- **Past performance is not indicative of future results.**
- **Trading involves risk of loss, including total loss of capital.**
- **Nothing here is financial advice, an offer, or a solicitation.**
- **Every performance figure in this document is HISTORICAL (backtested/simulated) or PENDING (awaiting sufficient live data). None represents realised returns on capital.** The platform holds no capital and has no broker connection.
- Simulated results carry the limitations inherent in hypothetical performance: they do not reflect the impact of a real order on the market and are not subject to the financial risk of actual trading.
- **This platform has not demonstrated predictive skill on live data.** See §3 and §12.

## 1. What the system is, and what it is not
SignalDeck is a market-measurement and research-record platform. It records live market data into SQLite, computes per-symbol scores, and grades its own scores against realised returns.
It is not an auto-trader. It has no broker connection. It executes nothing. It holds no capital. It is not financial advice.
The Go daemon lives in `daemon/`. The web UI lives in `web/`.

## 2. Current grader status
The directional ensemble is RETIRED. The `modelhealth` component set `verdict: retired, emitting: false`. It is off and does not publish.
The headline structural predictors are trend21, vol21 and liquidity21, but the registry carries **seven** registered backtested claims — those three plus `trend63`, `filingsdrift21`, `trend21-crypto` and `liquidity21-crypto`. All seven are PENDING with zero graded forecasts. *(v1.1 named only three here while its own generated block listed seven.)*
First resolvable structural evidence: 2026-08-07. **First grading dates differ per predictor and are carried per-row in the registry, not summarised here:** the structural trio grades 2026-08-14, `filingsdrift21` a day earlier, and `trend63` materially later. A 21-day horizon cannot be graded sooner than its horizon allows.
Outstanding structural forecast counts are not typed in this corpus any more. They were previously stated two ways in one document; they now come from the generated block below, per predictor. That closed FC8.

The live record is not typed into this deck. It is generated from `data/accuracy_registry.json` by `tools/live_accuracy.py` and injected into every document listed in `partials/INCLUDES.txt`, this one included; CI fails on a superseded literal. Intervals appear only where the sample clears `min_distinct_blocks = 10`; every other row reads `withheld`, and a withheld interval carries no verdict.

**FC1 was closed for the documents and left open in the product until 2026-08-05.** v1.1 claimed FC1 "resolved by mechanism". The mechanism scanned markdown only (`--scan $(git ls-files '*.md')`), and the API, the MCP surface and the web UI each carried their own hand-typed copy of this record — disagreeing with each other on the sample size, one of them by a factor of ten — while the gate reported clean. The gate now also runs over shipped source (`--scan-code`), and it found **36 hand-typed live-record literals across 15 files**, including one *current* figure typed into Go. See §8.1.

**Three qualifications a reviewer must apply to the block below:**

1. **Overlapping observations are corrected for, and the correction is load-bearing.** `n` counts symbol-days, which overlap heavily; `Distinct days` is the effective sample. Intervals are day-clustered effective-N Wilson, Bonferroni-corrected across BOTH multiplicities this surface pays — family (rows in one cycle) and looks (cycles over the same accruing rows), `divisor = family_size × looks` — with both counters ratcheted by `max()` so publishing fewer rows or rotating a log cannot refund multiplicity already spent. **Do not read `n` as an independent sample size.**
2. **The retire rule and the published interval use different widths.** The pre-registered auto-retire criterion is written against a *95%* day-clustered Wilson interval; the published interval is Bonferroni-corrected and therefore wider. The retired row's corrected interval still excludes the null, so retirement holds *a fortiori* — but the wording must be reconciled. **Needs verification.**
3. **A strategy boundary runs through this record** at 2026-08-04 (§10). Whether accuracy rows are affected, or only P&L, needs an explicit ruling.

<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (registry `REFUSED` since 2026-09-13T14:43:41) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **GRADING REFUSED — no accuracy figures are published.** Reason: publication gate: the graded window contains 18 collapsed cross-section(s) of 76 day(s): 1d 2026-07-27 (6 distinct across 330 symbols), 1d 2026-07-28 (8 distinct across 330 symbols), 1d 2026-07-29 (13 distinct across 328 symbols), 1d 2026-07-31 (6 distinct across 328 symbols), 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld. The window starts at the survivorship epoch and does not roll forward, so a collapsed day stays in it: this clears when the window is re-registered, not by waiting for more grades.. The grade computed at 2026-09-13T14:42:10 (0.0h old) is withheld, not lost: it is retained inside the registry under `stale_last_registry` for the historical record and is deliberately not reprinted here, because a number the publication gate refused to stand behind is not a live number. The in-app `/accuracy` page and `/api/accuracy` apply the same gate from the same registry.

<!-- END GENERATED live_accuracy -->

Read the block above rather than any figure elsewhere in this deck. Where a row shows an interval, the registry's own notice beside it is the verdict; where it shows `withheld`, there is no verdict to quote.

## 3. The one thesis
The platform's only defensible claim is procedural, not predictive: it grades its own predictions against a protocol fixed before the outcomes were known, and it retires what fails. It has not yet demonstrated predictive skill on live data.
Everything else in this deck is either infrastructure supporting that, or a defect preventing it.

## 4. Directional family — retired
Status: RETIRED, by an auto-retire rule pre-registered before the outcomes came in.
The retirement rule fired and stopped it publishing, before the sample was large enough to carry an interval. That sequencing is the load-bearing fact: the rule did not wait for a result it could no longer avoid.
The sample has since grown past the distinct-day floor, and the interval the registry now publishes for the 1d row is worse than the point estimate that triggered retirement, not better. The figures are in the generated block in section 2; the registry's own notice beside each row is the only verdict quotable from them.
Inversion is not a rescue: inverting a below-baseline directional predictor produces another below-baseline predictor.

## 5. Structural family — pending and weakened
Status: PENDING and weakened. Weakened for two reasons. The backtests behind the structural band tables (HISTORICAL) are computed over a bar history that `ALPHA_WORKFLOW.md` §B2 measured survivor-seeded; the P3A backfill closes that for 2019–2022 and leaves a stated 2023–2025 residual — see section 8. And the live record (LIVE) does not begin until 2026-08-07, with first grading 2026-08-14.
trend21 was refuted as a predictor in a separate study: it reproduces a driftless random walk. Treat trend21's HISTORICAL band tables as explained by geometry, not skill.

## 6. Pre-registration rules
`PREREGISTRATION.md` holds the frozen 2026-08-14 structural grading protocol.
It is digested into a hash-chained pre-registration table as the `prereg-document` record. `daemon/internal/prereg/document_claims_test.go` parses its prose against the chain.
Its SHA-256 is `01d6bcdfe1451b6f2c84185478f9c8f3c405ef3ae845a11a937d865eacf2a724`.
It cannot be edited outside the amendment path. It deliberately carries no governance header, because editing it would break its digest.
Amendments go through `daemon/cmd/prereg-amend`.

## 7. Bias controls actually in force

This section used to be a bullet list asserting "BUILT AND IN FORCE" with a file
path each and no evidence column. That is the one construct that has burned this
repository repeatedly — `INSTITUTIONAL_GAP.md` claimed a kill switch that lived
in a *different repository*, and claimed survivorship and point-in-time control
that measurement later contradicted. So it now carries the same evidence
treatment `RISK_POLICY.md` §5 gives its rules.

**Tests** is the package's own test-function count. **Wired** is the number of
**non-test** files outside the package that import it — a control nothing imports
in production is not a control, which is exactly how `TradableAt` returned empty
for its entire life and `MarkDelisted` sat with no callers.

**This table is now GENERATED** by `tools/controls_evidence.py` from the Go
source, and `--check` fails if any control measures zero on either column. v1.1
typed it by hand and left `—` in six cells while marking every row **IN FORCE**
anyway — which contradicts the sentence above it, and was unnecessary: all six
had real, measurable values. An unmeasured cell can no longer be published as
evidence, because it can no longer be typed.

<!-- BEGIN GENERATED controls_evidence -->

Generated by `tools/controls_evidence.py` from the Go source. Do not edit by hand.
**Tests** is the package's own test-function count. **Wired** is the number of non-test files outside the package that import it — a control nothing imports in production is not a control.

| Control | Package | Tests | Wired |
|---|---|---:|---:|
| Model registry, health grading, retirement | `internal/modelhealth` | 22 | 2 |
| Canary / staged model rollout | `internal/canary` | 26 | 3 |
| Multi-source price validation | `internal/pricecheck` | 9 | 1 |
| Dataset versioning and checksums | `internal/datasetver` | 10 | 1 |
| Corporate actions / split repair | `internal/splitfix` | 11 | 1 |
| Data licensing + HTTP 451 export guard | `internal/datalicense` | 9 | 3 |
| No look-ahead, purge/embargo (cross-sectional) | `internal/alphax` | 11 | 2 |
| No look-ahead, fill timing (backtest) | `internal/backtest` | 31 | 2 |
| Data quality, freshness, dq-auditor | `internal/maintain` | 44 | 4 |
| Pre-trade risk admission | `internal/riskgate` | 41 | 5 |
| Kill switch | `internal/killswitch` | 6 | 2 |
| Portfolio optimiser (NOT bound to allocation) | `internal/portopt` | 13 | 1 |

<!-- END GENERATED controls_evidence -->

**Status is the one column that stays human-authored**, because it encodes a
judgement the source cannot make: `internal/riskgate` and `internal/killswitch`
are **IN FORCE — paper** (their blast radius is a simulated book), `portopt` is
**FROZEN, not bound to allocation** (FC6), and every other row above is **IN
FORCE**. `internal/api` is a process entrypoint rather than an imported library,
so a caller count is the wrong evidence form for it and it is deliberately not
in the generated table; its calibration monitoring is covered by the package's
own tests and by `GET /api/calibration` responding.

Three named guards are worth citing individually, because each exists to catch a
defect that actually happened here:

| Guard | What it prevents |
|---|---|
| `TestDocumentedControlsHaveNonTestCallers` (`internal/pipeline/delisting_test.go`) | A control named in documentation losing its production callers and becoming decoration — the `MarkDelisted` failure, turned into an invariant |
| `TestKillSwitch_HaltsEntriesAndLedgersTheRefusal` (`internal/pipeline/riskgate_admission_test.go`) | The halt being asserted rather than exercised |
| `TestFlatten_ClosesAPositionNothingElseWouldClose` (`internal/pipeline/paperflatten_test.go`) | The terminal drawdown rung being decided but not wired |

**Bar-history completeness — MEASURED, with a stated shortfall.**
`tools/bars_completeness.py` (added 2026-08-04) measures stock and crypto
coverage against a calendar derived from the most-printed stock symbol; the
current figures are in §8's generated block, which reads them from that same
tool rather than restating them here. `proofs/P11_BARS_COMPLETENESS.md` analyses
the composition of the shortfall as of 2026-08-04: about a third of it is
rights, warrants and units, which trade sporadically by construction, so common
stock alone measured roughly a point higher, and the handful of symbols that
stop printing early with no `delisted_at` were almost all warrants. This bounds
the foundation under every point-in-time claim; it does not repair the missing
symbol-days.

**CI:** `.github/workflows/ci.yml` runs `go build`, `go vet`, `go test -race ./...`
with a total and per-package coverage floor, a clone-only rebuild from
`git archive HEAD`, the manifest check, the secret scan with its own self-test,
ledger-provenance reachability, the docs gate with its own self-test, and the web
lint and build.

## 8. Open data defects
These are the reasons nothing here is finished.

> **READ THIS BEFORE ANY NUMBER BELOW.** These measurements used to be
> hand-typed with a date stamp. On 2026-08-05, one day after v1.1 was written,
> **every one of them was already stale** — `universe_membership` by 0.8M rows,
> the `delisted_at` count by a factor of 2.6, and the survivorship window ratio
> by enough to invert its conclusion. FC1 taught this exact lesson about the
> accuracy record and the fix was applied only to the accuracy record. It is
> applied here now: `tools/deck_facts.py` measures them from
> `data/signaldeck.db` into the generated block below, and CI fails when the
> block no longer matches the database. **A figure restated in the prose of this
> section is still hand-typed — the block is the measurement, the prose is the
> argument about it.**

<!-- BEGIN GENERATED deck_facts -->

Measured from `data/signaldeck.db` by `tools/deck_facts.py`. Do not edit by hand — CI fails when this block no longer matches the database. The universe reaches **2026-08-22**, the last observation day it holds.

| Measurement | Value |
|---|---|
| `universe_membership` rows | 2,697,299 |
| — observation days | 2,163 |
| — distinct symbols | 2,947 |
| — `source` values present | `bars-1d` |
| `symbols.delisted_at` stamps | 1,894 |
| — delisted 2020-2022 | 621 |
| — delisted 2023-2025 | 1,117 |
| — recent window against earlier | **179.9%** of the 2020-2022 count |
| Daily-bar calendar (from `SPY`) | 1,934 sessions |
| Stock bar coverage | 91.92% — 2,708,947 of 2,947,224 symbol-days over 2,940 symbols |
| — still-listed names only | 98.92% over 1,046 symbols |
| — names carrying `delisted_at` only | 83.20% over 1,894 symbols |
| — symbols that stop printing early with no `delisted_at` | 15 |
| Crypto bar coverage | 100.00% over 7 symbols |

The membership derives entirely from the daily-bar history, so it is point-in-time only to the extent that history is complete: the stock coverage row is the bound under every point-in-time claim in this deck. **Read the two cohort rows before the blended one.** They answer different questions — the still-listed row is whether the live universe has holes, the delisted row is how densely the imported dead names were ever sampled — and while dead names are being imported the blended figure moves with the import rather than with data quality. The symbols that stop printing with no `delisted_at` are the survivorship-relevant ones: they leave the universe without being recorded as dead, which is indistinguishable from having stopped looking.

<!-- END GENERATED deck_facts -->

### 8.1 FC1 — closed in documents 2026-08-04, closed in code 2026-08-05

The corpus carried four mutually inconsistent versions of the live directional record. P2 closed that **for markdown**: the documents in `partials/INCLUDES.txt` include one generated block instead of typing it, and CI fails on a superseded literal.

The gate scanned `*.md` only. The shipped surfaces were never covered, and each had grown its own copy:

| Surface | What it stated | Now |
|---|---|---|
| `api/explain.go` (`whyNotDirection`, served JSON) | a superseded pair over a sample ~4× the registry's | states the qualitative fact, points at `GET /api/accuracy` |
| `mcp/content.go` (served evidence) | the same superseded accuracy over a *different*, larger sample | same |
| `web/.../TodaysRead.tsx` (home page) | a **fifth** number set, presented as the *current* live record | qualitative, sourced from the registry |
| `web/app/accuracy/page.tsx` | the pre-epoch full record — correctly labelled and dated in the UI | kept, marked `SUPERSEDED-SNAPSHOT` |
| `api/modelhealth.go` (served) | an inversion argument pinned to a stale pair, **whose conclusion no longer follows on the current record** | restated as the general argument, which does hold |
| 10 further files | dated methodology figures in comments | marked `SUPERSEDED-SNAPSHOT` |

`tools/live_accuracy.py --scan-code` now runs the same gate over `*.go`/`*.ts`/`*.tsx` in CI, with `SUPERSEDED-SNAPSHOT` working at block scope so a genuinely dated record can stay. It found **36 literals across 15 files**, one of them a *current* figure typed into Go — the same defect in the opposite direction.

### 8.2 Survivorship (FC3) — the residual has closed; the deck's statement of it had not

v1.1 stated: `delisted_at` on 716 symbols; 600 delistings for 2020–2022 against 94 for 2023–2025 (15.7%); Form 25 closure work "not yet ingested".

**The current counts are in the generated block above, and they invert that conclusion:** the recent window no longer holds a small fraction of the earlier one, it holds substantially more. The registry's survivorship bound cites `edgar:form-25` as its source, so the ingest has landed. The under-coverage that FC3 was narrowed to no longer appears in the data. *(The three figures that used to sit in this paragraph are the reason this section is generated: they were re-measured by hand on 2026-08-05 and would have gone stale on the next ingest exactly as their predecessors did.)*

**FC3 should not be lifted on this measurement alone.** Two things must be confirmed first: that the STAGING review step `proofs/P3A_SURVIVORSHIP_BACKFILL.md` requires actually happened before these rows reached `symbols.delisted_at`, and that `P3A` is amended to record the new counts. Until both, FC3 stays frozen and this paragraph is the disclosure.

**The effect is now sized and generated, not named.** The monthly revalidation publishes active-only minus survivorship-clean accuracy, and `tools/live_accuracy.py` renders it into the **Survivorship:** line of the generated block in §2 — so it travels with every document instead of sitting in a JSON file. The reading is **positive**, meaning the active-only figure is **inflated** by excluding dead names. *(v1.1 named the direction and withheld the size.)*

### 8.3 Point-in-time universe (FC4)

`universe_membership` held 0 rows when FC4 was raised and is now populated; its row, day and symbol counts, and the `source` values every row carries, are in the generated block above (v1.1 typed 1,854,228 rows over 1,777 symbols, which was already 0.8M rows behind the database a day later). The membership derives entirely from the daily-bar history, so it is only as point-in-time as that history is complete — the block bounds that below 100% in the same measurement. FC4 stays frozen for that narrower claim; the derivation has not been audited.

**The blended coverage figure is not a data-loss measurement, and reading it as one is a mistake this deck has already made once.** The block splits it by listing status because the two cohorts answer different questions. Still-listed names are close to complete: the live universe has no meaningful holes. The shortfall is almost entirely in names carrying `delisted_at`, and it is not damage — it is how sparsely those names were ever sampled. `tools/bars_completeness.py` counts every session between a symbol's own first and last bar as expected, so a dead name whose retained history runs at a weekly cadence scores near 20% while having lost nothing it once had.

**That is a point-in-time defect in its own right, and it is newly worse.** Membership is derived from bars, so a name sampled weekly is recorded as a universe member on roughly one day in five of the days it was actually listed. The survivorship import reduced the bias FC3 names — dead companies are in the historical universe now — and in the same motion introduced flicker in exactly the names it added: they appear and disappear on the sampling cadence rather than on the market's. **A point-in-time universe reconstructed over the imported era therefore under-counts dead names on most days, in a pattern that correlates with how thinly each was covered.** No study on this platform has been re-run against that.

**Coverage is a moving number while the import runs, and was one when it was last published.** The daily-bar symbol count went 1,070 → 1,770 → 2,940 over three days as the import landed in batches. §7's previously published bound was measured in the middle of that, at 1,770 symbols, and was already describing a population that no longer existed when it was filed. This is why the figure is generated: any hand-typed statement of it is a snapshot of an import in progress, presented as a property of the data.

### 8.4 Cost, capacity and liquidity — NOT BUILT

See §10. No capacity analysis, no ADV or participation constraint, no borrow-cost model, no market-impact model, no tax treatment. **No claim about the scale at which any result here would survive is supported.**

### 8.5 Risk-limit provenance — CLOSED 2026-08-05

Every limit resolves through a `SIGNALDECK_RISK_*` environment variable, and nothing recorded which values a pass ran under, so a past paper result could not be tied to its envelope; an out-of-range override was discarded in silence. `riskgate.DescribeLimits()` now records the resolved envelope with per-limit provenance, `pipeline/paper.go` logs it every pass, and a rejected override is logged as a WARNING instead of swallowed. A test pins the provenance table to `Defaults()` so the two copies cannot drift.

### 8.6 Snapshot caveat

Remediation phases were landing while this deck was written. Read `proofs/` directly before relying on a phase status.

### 8.7 Closed

- ~~Walk-forward validation runs on demand only.~~ **CLOSED 2026-08-04.** `ops/com.signaldeck.revalidation.plist` schedules it monthly via `ops/revalidate-structural.sh`, which writes `ops/revalidation-status.json`; `tools/check_revalidation.py` gates it in CI, failing on a missing or stale snapshot rather than skipping.

## 9. Risk policy specification
`daemon/internal/riskgate` is the pre-trade check: it can refuse or shrink a trade. It holds no state, performs no I/O, and reads no clock.
It is BUILT AND IN FORCE for the PAPER book only. Its five non-test importers are `pipeline/paper.go`, `pipeline/paperev.go`, `pipeline/paperrisk.go`, `stresslab/replay.go` and `api/stress.go` — the count is measured in §7's generated table, and v1.1's prose listed four of the five.
No real capital is attached to any of them.

### 9.1 The envelope actually in force

*(v1.1 disclosed three of these eleven. A reader of v1.1 alone would have concluded that concentration, correlation and leverage controls were absent. They are not.)*

| Limit | Default | Why it is where it is |
|---|---|---|
| Max position weight | 10% of equity | Per-name concentration |
| Max positions | 10 | Slot discipline; matches the `equity/MaxPositions` convention the engine already implied |
| **Max sector weight** | **30%** | Ten names from one sector are one bet with extra commission |
| **Max correlation to book** | **0.80** | Catches the pathological case — second share class, sector twin, ETF and its top holding — not ordinary market beta |
| **Max gross exposure** | **1.00×** | **No leverage.** A simulated book quietly running 1.3× gross is reporting a different, riskier strategy than the one described |
| Max drawdown (suspend) | 20% | Conventional institutional soft stop |
| Terminal drawdown (flatten) | 25% | See below |
| **Max daily loss** | **5%** | A session losing a twentieth of the book is evidence about the day, not one name |
| Kelly fraction | 0.25 | Full Kelly is growth-optimal only when the edge is known exactly; with estimated `p` and `b` it overbets badly |
| **Min edge trips** | **20 closed round trips** | A sample too thin to describe a payoff shape is too thin to size on |
| Min ticket | 0.5% of equity | Caps TRIM rather than refuse and trims compose; filling the remnant pays two spreads and cannot move the book |

**Provenance is now recorded (2026-08-05).** Every limit resolves through a `SIGNALDECK_RISK_*` environment variable. `riskgate.DescribeLimits()` captures the resolved envelope with per-limit attribution, `pipeline/paper.go` logs it on every pass, and an out-of-range override is logged as a WARNING instead of being silently replaced by the default. Fractional limits are hard-clamped to (0,1], so leverage above 1.0× cannot be set by this path.

**Stated limitation of the sizing model.** Kelly is estimated from a short realized record produced by a signal family that has not demonstrated live skill. A 20-trip floor is a weak basis for a Kelly estimate; the quarter-Kelly haircut and the 10% cap are what make this survivable, and the cap should be expected to bind almost always. **Kelly sizing here is not evidence that an edge was measured.**
The kill switch, `daemon/internal/killswitch/killswitch.go`, is BUILT AND IN FORCE for the PAPER book (P4C, 2026-08-04): file-based (`ops/HALT`), fail-closed, read fresh before every order by `daemon/internal/pipeline/paper.go`, and exercised by an end-to-end halt simulation (`pipeline.TestKillSwitch_HaltsEntriesAndLedgersTheRefusal`) plus `killswitch_test.go`. It refuses ENTRIES; risk-reducing exits still execute, so a halt cannot trap the book. It stops new risk, it does not liquidate. Its blast radius is a simulated book — there is no live order path here to halt.
A kill switch previously claimed for this platform lived at `stock-trader/trader/risk_gate.py`, in a DIFFERENT repository. It cannot halt this daemon and no claim here rests on it. **FC5 resolved** — see `proofs/P4C_KILL_SWITCH_CORRECTION.md`.
Risk is an ADMISSION GATE, not a post-hoc sizer (P4B): `riskgate.Admit` gates the book once per pass and `riskgate.Evaluate` gates each candidate BEFORE `ev.Decide` renders a verdict, so a risk-rejected name never consumes an EV rank slot. Every refusal — risk gate and kill switch alike — is ledgered to `ev_decisions`.
Position sizing IS bound: `riskgate.Evaluate` sizes every candidate by **quarter Kelly** on the realized round-trip record, capped at 10% of equity, with a 0.5% minimum ticket below which it refuses rather than filling a remnant. `RISK_POLICY.md` §1.1 resolves the two sizing philosophies that once competed — **Kelly sizes, risk-per-trade caps, Kelly never sizes past the cap.**
**FC6 is narrower than "position sizing" and should not be read as covering it.** What stays frozen is the PORTFOLIO OPTIMISER: `portopt` is imported only by `daemon/internal/api/capstones.go`, an API surface, and is not bound to any allocation decision.
The DRAWDOWN LADDER's terminal rung is BUILT AND IN FORCE for the PAPER book (2026-08-04): `riskgate.ShouldFlatten` closes every open position once the book is 25% below its peak, executed by `pipeline.planExit`. It sits beyond the 20% suspend rung deliberately — a liquidation that fires while the book is still entering is a contradiction, not a ladder. It fails the OPPOSITE way to the halt: an unknown drawdown does not flatten, because `DrawdownKnown` is false only when there is no equity curve, so there is no peak to be below. Proved by `pipeline.TestFlatten_ClosesAPositionNothingElseWouldClose`, which disables barriers and holds the signal bullish so nothing except the rung could have closed the position. The graduated −5%/−8% rungs remain SPEC ONLY.

## 10. Execution specification
There is no live execution path. No broker connection exists in this repository.

### 10.0 Entry rule (PAPER book)

*(v1.1 titled this section "Execution specification" and stated no entry rule at all. The logic below is what `pipeline/paper.go` and `paperev.go` actually do.)*

One simulated portfolio per horizon — `flagship-1d` and `flagship-1w` — each starting flat with a notional book. Per pass, in order:

1. **Exits execute first**, then entry candidates are collected and sized, so an exit's freed cash and slot are available in the same pass.
2. **Intent** comes from the calibrated probability: `cal_prob >= LONG` → long, `<= FLAT` → flat, otherwise **HOLD (deadband)**. `cal_prob` alone is *not* the go/no-go.
3. **The kill switch is checked before a candidate is even assessed**, and a halted pass ledgers the refusal rather than silently not entering.
4. **Decision engine** (`internal/ev`): `ev.Decide(…, ev.EnterLong, thresholds)` renders the verdict on **net EV and rank**, after the risk gate has already admitted the name.
5. **Sizing** targets `equity/MaxPositions` dollars, then every §9.1 cap trims it — slot, cash, position weight, sector headroom, correlation.
6. Every entry ledgers its reason, including net EV, its rank, and `cal_prob` against the long threshold.

**Still unspecified, and required before this is executable with real capital:** order type (fills are modelled at the open, implying market-on-open, but no order type is declared), time-in-force, partial fills, halt/gap/limit-up-down handling, and the universe definition and rebalance calendar.
Simulated execution, HISTORICAL only: `daemon/internal/backtest/backtest.go` fills at the next bar's OPEN and applies a single `CostBps` round-trip proxy for commission, spread and slippage together. There is no separate slippage model.

**Not modelled anywhere:** market impact, short borrow cost and availability, financing, taxes, and any capacity or ADV constraint. **No statement about the scale at which any result here would survive is supported.** A blended round-trip proxy is defensible for a liquid large-cap universe; it is not defensible for the rights, warrants and units this universe contains, which §7 shows trade sporadically by construction.
Slippage measurement, an executed-quantity ledger, and broker reconciliation are NOT BUILT here. Documents that claimed them were citing a `stock-trader` artifact in a different repository.
Alpaca appears here as a MARKET DATA client only (`daemon/internal/ingest/alpaca/client.go`, with 429 backoff), not as an execution client.
Consequence: any statement about achievable fills is a modelling assumption, not a measurement.
Position-level EXITS are BUILT AND IN FORCE for the PAPER book (P4D, 2026-08-04): a triple barrier — hard stop at 2.0×ATR(20), take-profit at 3.0×ATR(20), horizon expiry at 1 bar (`1d`) / 5 bars (`1w`). Every barrier is confirmed by a CLOSE and filled at the OPEN of a STRICTLY LATER bar; highs and lows are never read, so no intrabar path assumption is made. Wicks do not stop the book out and gaps are paid in full — both asserted by tests. See `proofs/P4D_BARRIER_EXITS.md`.
Book-level EXIT: separately from the per-position barriers, the drawdown flatten (§9) closes every open position at the next available open. It is the WEAKEST exit candidate by design — a barrier or probability flip that already fired is earlier and more specific and still wins, so a liquidation never overwrites the reason a position was already leaving for (`TestFlatten_DoesNotOverwriteAnEarlierExitReason`).
MATERIAL: a 1-day forecast implies a 1-BAR HOLD, so every `flagship-1d` position now opens at one bar's open and closes at the next, and the probability-flip exit is unreachable on that book. This is a DIFFERENT STRATEGY from the open-ended hold the paper book ran before 2026-08-04. Any accuracy or P&L figure spanning that date covers two strategies and must be split at it.

## 11. Negative studies
These are results that came back against the platform.
- The directional ensemble graded below its baseline and was retired.
- trend21 was refuted as a predictor: it reproduces a driftless random walk.
- The daily research loop searches a 48-rule grid and reports, most days, that nothing survived Bonferroni correction, regime-survival and the counterfactual.
- A pairs-trading study (H018 / CORR63) returned a DO NOT SHIP verdict, which stands.
- The "move down the liquidity spectrum" hypothesis was tested and refuted.
- Two DESIGN defects in the retired directional family are recorded here so that reviving it cannot inherit them silently. The mean-reversion leg was `pMR = 1 − pRaw` **exactly** — the algebraic complement of the momentum blend, carrying no information not already in it, while the architecture described the legs as independent; blending a probability with its own complement also inflates the multiplicity denominator by counting one bet as two. And two of seven legs (`pressure`, fail-safe on an unmeasured grade; `sentiment`, gated on headline count and freshness) entered without measured out-of-sample lift, against the stated rule that an unproven leg is dropped rather than down-weighted. Neither is live — the family is retired — and neither is repaired.
These are recorded because a research record that only keeps its wins is not a research record.

## 12. Honest capability assessment
The platform has not demonstrated predictive skill on live data. Its directional family failed and is retired. Its structural family is unresolved and pending.
What the platform has demonstrated is a working measurement and retirement process, supported by an array of bias controls that are built and in force.
The platform holds no live capital and executes no trades. All results cited are either HISTORICAL (backtested) or PENDING (awaiting sufficient live data).
The open data defects in section 8 prevent any HISTORICAL table from being read as survivorship-clean.
The publication freeze was **LIFTED on 2026-08-04** by `proofs/P10_FREEZE_LIFT.md` — the explicit successor artefact `proofs/P0_FREEZE.md` §1 required, authorised by the repository owner. Two provisions of P0 §1 were never conditional on the remediation and are **not** lifted: no capital may be attached to any signal here, and no execution or risk control may be described as existing unless it lives in this repository and has a passing test.

## 13. Governance and limitations
The publication freeze recorded in `proofs/P0_FREEZE.md` is **LIFTED** (`proofs/P10_FREEZE_LIFT.md`, 2026-08-04). Read P0 as the record of what was frozen and why, not as a restriction in force. **No capital may be attached to any signal described in this repository** — that provision survives the lift and is not conditional on anything.
The lift conditions were P1, P2 and P3, each with its own filed artefact; `proofs/P10_FREEZE_LIFT.md` §2 records them checked rather than assumed — `live_accuracy.py --check` exiting 0 across `partials/INCLUDES.txt`, and `universe_membership` measured at 1,854,228 rows. Meeting the conditions and lifting are separate acts, and the freeze correctly stood between the last condition landing and the successor artefact being filed.
Two classes remain frozen and travel as named caveats rather than a blanket block: **FC3** (survivorship, 2023–2025 only) and **FC6** (`portopt` not bound to allocation).
The anchoring restriction is **narrowed, not removed**. Publishing a track record while stating plainly that its anchors are internal is honest; what anchoring protects is one specific claim. **No document may claim or imply that this record cannot have been altered by its operator.** "Tamper-evident" must always carry that qualification, and *unfalsifiable*, *cannot be retouched* and *provably unmodified* must not appear at all.
### 13.4 Frozen claim set — every member's status

`C` is defined once, in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, members FC1–FC8. **Every member's status must appear here: a member whose status is stated nowhere is indistinguishable from one silently dropped, which is what v1.1 did to FC2, FC4 and FC7.**

| Class | Subject | Status |
|---|---|---|
| FC1 | Live accuracy figures | **CLOSED 2026-08-05.** Closed for markdown 2026-08-04; the shipped surfaces were outside the gate until the code scan landed (§8.1) |
| FC2 | CI verdicts published while intervals were withheld | **SUPERSEDED** — the registry publishes a corrected interval where the block floor is met and withholds the verdict wherever the interval is withheld |
| FC3 | Survivorship control | **FROZEN**, narrowed to 2023–2025 — but the residual has been re-measured and no longer appears in the data; two confirmations are required before lifting (§8.2) |
| FC4 | Point-in-time universe | **FROZEN for the narrow claim** — table populated, `bars-1d` derivation unaudited (§8.3) |
| FC5 | Kill switch existence and enforcement | **RESOLVED** for the paper book (P4C, §9) |
| FC6 | Portfolio optimiser not bound to allocation | **FROZEN** (§9) |
| FC7 | Unevidenced "already built" attestations | **SUPERSEDED by §7's generated evidence table** (`tools/controls_evidence.py`, gated in CI) — and §8.1 is a live instance of exactly this failure mode, so FC7 is a standing rule, not a closed item |
| FC8 | Structural forecast counts | **RESOLVED** — generated per predictor, not typed |
The prediction ledger is hash-chained and signed. Its anchors live in the same SQLite file the operator controls.
Anchoring status is INTERNAL — UNANCHORED. `ops/anchor-publish.sh` exists, is invoked by nothing, is not scheduled, and has never succeeded; its log's last entry, dated 2026-07-27, reads `FAIL: no public clone at /nonexistent — nothing was externally timestamped`.
Therefore the chain is tamper-evident against an actor without the signing key, and is NOT evidence against the operator, who holds both the key and the database.
Offsite backup is BUILT BUT NOT IN FORCE: the backup log records `offsite SKIPPED: no destination configured`. Backups exist on one machine, on one volume.
Single operator, single machine. There is no independent reviewer.

## 14. Next actions
1. Close the 2023–2025 survivorship residual that `proofs/P3A_SURVIVORSHIP_BACKFILL.md` quantifies. Until then, HISTORICAL results spanning those years stay survivor-seeded and FC3 stays frozen for that window.
2. ~~Decide the freeze explicitly.~~ **DONE 2026-08-04** — `proofs/P10_FREEZE_LIFT.md` lifts it, names the two provisions that survive the lift, records the conditions as checked rather than assumed, and narrows the anchoring restriction to the one claim anchoring actually protects.
3. Create the public anchors repository, schedule `ops/anchor-publish.sh` daily, and treat a push failure as an alert rather than a log line.
4. Add third-party timestamping over the chain head, weekly. Steps 3 and 4 differ: step 3 moves evidence to a host whose credentials the operator still holds.
5. Configure `SIGNALDECK_OFFSITE_DIR` to a volume that is not the machine's own.
6. ~~Audit the completeness of the `bars-1d` history itself.~~ **DONE 2026-08-04** — `tools/bars_completeness.py`, `proofs/P11_BARS_COMPLETENESS.md`. The foundation is now bounded rather than assumed, and the bound is **generated into §8** rather than recorded here: the figures P11 filed on 2026-08-04 (stocks 96.49%, common stock alone 97.36%, over 1,770 symbols) are already well behind the database, because the symbol count has since grown by two-thirds and the new names carry shorter histories. That is the same staleness this line would have kept reproducing. The shortfall is sized, not repaired.
7. ~~File the remaining phase artefacts.~~ **DONE 2026-08-04** — P1 through P11 are filed in `proofs/`. P2 and P3A, the two this line was waiting on, are among them.