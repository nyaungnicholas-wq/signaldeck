# SignalDeck — Strategy Deck

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.1 · **Last reviewed:** 2026-08-04 · **Revalidate by:** 2026-09-04
> **Revalidation trigger:** any re-grade, or any phase artefact filed in `proofs/`. A status document with no expiry drifts silently; §2's live record is generated so it cannot, but the prose around it can.
> **Status:** ACTIVE — supersedes prior summary decks
> **Scope:** The single current status document for the platform. Where it disagrees with an older document, this one wins: the others are publishable as a record of what was measured when, not as current status.
> **Frozen claim classes:** FC3, FC6 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, statuses in `proofs/P10_FREEZE_LIFT.md` §4
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE — current statement of record

## 1. What the system is, and what it is not
SignalDeck is a market-measurement and research-record platform. It records live market data into SQLite, computes per-symbol scores, and grades its own scores against realised returns.
It is not an auto-trader. It has no broker connection. It executes nothing. It holds no capital. It is not financial advice.
The Go daemon lives in `daemon/`. The web UI lives in `web/`.

## 2. Current grader status
The directional ensemble is RETIRED. The `modelhealth` component set `verdict: retired, emitting: false`. It is off and does not publish.
The structural predictors are trend21, vol21 and liquidity21. All are PENDING.
First resolvable structural evidence: 2026-08-07. First grading under the pre-registered protocol: 2026-08-14. A 21-day horizon cannot be graded sooner.
Outstanding structural forecast counts are not typed in this corpus any more. They were previously stated two ways in one document; they now come from the generated block below, per predictor. That closed FC8.

The live record is not typed into this deck. It is generated from `data/accuracy_registry.json` by `tools/live_accuracy.py` and injected into every document listed in `partials/INCLUDES.txt`, this one included; CI fails on a superseded literal. That mechanism closed FC1 — the corpus previously carried four hand-typed versions of the same record. Intervals appear only where the sample clears `min_distinct_blocks = 10`; every other row reads `withheld`, and a withheld interval carries no verdict.

<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-04T20:34:19) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

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

**Tests** is the package's own test count. **Wired** is the number of
**non-test** callers of the package — a control nothing imports in production is
not a control, which is exactly how `TradableAt` returned empty for its entire
life and `MarkDelisted` sat with no callers. All figures measured 2026-08-04.

| Control | Package | Tests | Wired | Status |
|---|---|---:|---:|---|
| Model registry, health grading, retirement | `internal/modelhealth` | 20 | 2 | **IN FORCE** |
| Drift monitoring (two-sample KS, n-adjusted) | `internal/modelhealth/drift.go` | *(above)* | 2 | **IN FORCE** |
| Canary / staged model rollout | `internal/canary` | 26 | 3 | **IN FORCE** |
| Multi-source price validation | `internal/pricecheck` | 9 | 1 | **IN FORCE** |
| Dataset versioning and checksums | `internal/datasetver` | 10 | 1 | **IN FORCE** |
| Corporate actions / split repair | `internal/splitfix` | 11 | 1 | **IN FORCE** |
| Data licensing + HTTP 451 export guard | `internal/datalicense` | 6 | 2 | **IN FORCE** |
| No look-ahead, purge/embargo (cross-sectional) | `internal/alphax` | 11 | — | **IN FORCE** |
| No look-ahead, fill timing (backtest) | `internal/backtest` | 31 | — | **IN FORCE** |
| Data quality, freshness, `dq-auditor` | `internal/maintain` | 29 | — | **IN FORCE** |
| Calibration monitoring, `GET /api/calibration` | `internal/api` | 255 | — | **IN FORCE** |
| Pre-trade risk admission | `internal/riskgate` | — | 5 | **IN FORCE — paper** |
| Kill switch | `internal/killswitch` | — | 2 | **IN FORCE — paper** |

Three named guards are worth citing individually, because each exists to catch a
defect that actually happened here:

| Guard | What it prevents |
|---|---|
| `TestDocumentedControlsHaveNonTestCallers` (`internal/pipeline/delisting_test.go`) | A control named in documentation losing its production callers and becoming decoration — the `MarkDelisted` failure, turned into an invariant |
| `TestKillSwitch_HaltsEntriesAndLedgersTheRefusal` (`internal/pipeline/riskgate_admission_test.go`) | The halt being asserted rather than exercised |
| `TestFlatten_ClosesAPositionNothingElseWouldClose` (`internal/pipeline/paperflatten_test.go`) | The terminal drawdown rung being decided but not wired |

**Bar-history completeness — MEASURED, with a stated shortfall.**
`tools/bars_completeness.py` (added 2026-08-04): stocks **96.49%** coverage over
1,916,310 symbol-days against an SPY-derived 1,907-session calendar; crypto
100%. A third of the shortfall is rights, warrants and units, which trade
sporadically by construction — **common stock alone is 97.36%**. Ten symbols of
1,770 stop printing early with no `delisted_at`, nine of them warrants. See
`proofs/P11_BARS_COMPLETENESS.md`. This bounds the foundation under every
point-in-time claim; it does not repair the 43,857 missing common-stock
symbol-days.

**CI:** `.github/workflows/ci.yml` runs `go build`, `go vet`, `go test -race ./...`
with a total and per-package coverage floor, a clone-only rebuild from
`git archive HEAD`, the manifest check, the secret scan with its own self-test,
ledger-provenance reachability, the docs gate with its own self-test, and the web
lint and build.

## 8. Open data defects
These are the reasons nothing here is finished.
- `universe_membership` held 0 rows and now holds 1,854,228, across 2,146 days and 1,777 symbols (measured 2026-08-04). Every row carries `source = 'bars-1d'`, so the membership is only as point-in-time as the retained daily-bar history is complete.
- Survivorship control is **partially closed, with a quantified residual**. Symbols carrying a `delisted_at` stamp went from 16 to 716. `proofs/P3A_SURVIVORSHIP_BACKFILL.md` records the verdict as substantially closed for 2019–2022 and states the residual plainly: 2023–2025 is under-covered because the upstream delisted-companies endpoint under-reports recent years, and the live detector only began watching in 2026. Treat HISTORICAL results over 2023–2025 as still survivor-seeded. FC3 is narrowed to that window rather than lifted. **The residual is now sized, not just named** (measured 2026-08-04): 600 delistings are recorded for 2020–2022 against 94 for 2023–2025 — the recent window holds 15.7% of the de-SPAC era's count. Real US delisting rates do not fall six-fold after 2022, so the shortfall is coverage, not the market. Closure work exists and is not yet ingested: `tools/alpha/fetch_form25.py` reads SEC Form 25 / 25-NSE — the exchange's official Notification of Removal from Listing, and the free complete record — resolving CIK to ticker through issuers' own filings because SEC's current-listings map cannot by construction contain a company being delisted. It passes 12/12 of its resolver tests and writes to a STAGING database for review before any of it reaches `symbols.delisted_at`. Until that ingest lands, this bullet stands.
- Remediation phases were landing while this deck was written. Every phase status here is a snapshot taken 2026-08-04; read `proofs/` directly before relying on one.
- ~~Walk-forward validation runs on demand only.~~ **CLOSED 2026-08-04.** `ops/com.signaldeck.revalidation.plist` schedules it monthly via `ops/revalidate-structural.sh`, which writes `ops/revalidation-status.json`; `tools/check_revalidation.py` is the gate that reads it and runs in CI, failing on a missing or stale snapshot rather than skipping. The run also publishes the **survivorship effect** — active-only accuracy minus survivorship-clean accuracy — every time, so a drift in it appears in CI output instead of inside a JSON file nobody opens. The current reading is positive, meaning the published claim is INFLATED by excluding dead names; the magnitude is in the snapshot, not typed here.
- The corpus contained four mutually inconsistent versions of the live directional record. **Closed by P2:** the documents listed in `partials/INCLUDES.txt` now include one generated block instead of typing it, and CI fails on a superseded literal. FC1 is resolved by mechanism rather than by correcting four documents by hand.

## 9. Risk policy specification
`daemon/internal/riskgate` is the pre-trade check: it can refuse or shrink a trade. It holds no state, performs no I/O, and reads no clock.
It is BUILT AND IN FORCE for the PAPER book only: importers are `daemon/internal/pipeline/paper.go`, `paperrisk.go`, `daemon/internal/stresslab/replay.go`, `daemon/internal/api/stress.go`.
No real capital is attached to any of them.
The kill switch, `daemon/internal/killswitch/killswitch.go`, is BUILT AND IN FORCE for the PAPER book (P4C, 2026-08-04): file-based (`ops/HALT`), fail-closed, read fresh before every order by `daemon/internal/pipeline/paper.go`, and exercised by an end-to-end halt simulation (`pipeline.TestKillSwitch_HaltsEntriesAndLedgersTheRefusal`) plus `killswitch_test.go`. It refuses ENTRIES; risk-reducing exits still execute, so a halt cannot trap the book. It stops new risk, it does not liquidate. Its blast radius is a simulated book — there is no live order path here to halt.
A kill switch previously claimed for this platform lived at `stock-trader/trader/risk_gate.py`, in a DIFFERENT repository. It cannot halt this daemon and no claim here rests on it. **FC5 resolved** — see `proofs/P4C_KILL_SWITCH_CORRECTION.md`.
Risk is an ADMISSION GATE, not a post-hoc sizer (P4B): `riskgate.Admit` gates the book once per pass and `riskgate.Evaluate` gates each candidate BEFORE `ev.Decide` renders a verdict, so a risk-rejected name never consumes an EV rank slot. Every refusal — risk gate and kill switch alike — is ledgered to `ev_decisions`.
Position sizing IS bound: `riskgate.Evaluate` sizes every candidate by **quarter Kelly** on the realized round-trip record, capped at 10% of equity, with a 0.5% minimum ticket below which it refuses rather than filling a remnant. `RISK_POLICY.md` §1.1 resolves the two sizing philosophies that once competed — **Kelly sizes, risk-per-trade caps, Kelly never sizes past the cap.**
**FC6 is narrower than "position sizing" and should not be read as covering it.** What stays frozen is the PORTFOLIO OPTIMISER: `portopt` is imported only by `daemon/internal/api/capstones.go`, an API surface, and is not bound to any allocation decision.
The DRAWDOWN LADDER's terminal rung is BUILT AND IN FORCE for the PAPER book (2026-08-04): `riskgate.ShouldFlatten` closes every open position once the book is 25% below its peak, executed by `pipeline.planExit`. It sits beyond the 20% suspend rung deliberately — a liquidation that fires while the book is still entering is a contradiction, not a ladder. It fails the OPPOSITE way to the halt: an unknown drawdown does not flatten, because `DrawdownKnown` is false only when there is no equity curve, so there is no peak to be below. Proved by `pipeline.TestFlatten_ClosesAPositionNothingElseWouldClose`, which disables barriers and holds the signal bullish so nothing except the rung could have closed the position. The graduated −5%/−8% rungs remain SPEC ONLY.

## 10. Execution specification
There is no live execution path. No broker connection exists in this repository.
Simulated execution, HISTORICAL only: `daemon/internal/backtest/backtest.go` fills at the next bar's OPEN and applies a single `CostBps` round-trip proxy for commission, spread and slippage together. There is no separate slippage model.
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
The frozen claim set `C` (members FC1-FC8) is defined once, in `proofs/P6_GOVERNANCE_CLEANUP.md` §4.
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
6. ~~Audit the completeness of the `bars-1d` history itself.~~ **DONE 2026-08-04** — `tools/bars_completeness.py`, `proofs/P11_BARS_COMPLETENESS.md`. Stocks 96.49% (common stock alone 97.36%); crypto 100%; 10 of 1,770 symbols stop early with no `delisted_at`, nine of them warrants. The foundation is now bounded rather than assumed. The residual 43,857 missing common-stock symbol-days are sized, not repaired.
7. ~~File the remaining phase artefacts.~~ **DONE 2026-08-04** — P1 through P11 are filed in `proofs/`. P2 and P3A, the two this line was waiting on, are among them.