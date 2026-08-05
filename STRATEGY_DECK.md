# SignalDeck — Strategy Deck

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** ACTIVE — supersedes prior summary decks
> **Scope:** The single current status document for the platform. Where it disagrees with an older document, this one is wrong until reconciled — older documents are frozen, not corrected.
> **Frozen claim classes:** FC1, FC2, FC3, FC4, FC5, FC6, FC7, FC8 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4
> **Authority:** `proofs/P0_FREEZE.md` (freeze) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** BLOCKED — internal use only

## 1. What the system is, and what it is not
SignalDeck is a market-measurement and research-record platform. It records live market data into SQLite, computes per-symbol scores, and grades its own scores against realised returns.
It is not an auto-trader. It has no broker connection. It executes nothing. It holds no capital. It is not financial advice.
The Go daemon lives in `daemon/`. The web UI lives in `web/`.

## 2. Current grader status
The directional ensemble is RETIRED. The `modelhealth` component set `verdict: retired, emitting: false`. It is off and does not publish.
The structural predictors are trend21, vol21 and liquidity21. All are PENDING.
First resolvable structural evidence: 2026-08-07. First grading under the pre-registered protocol: 2026-08-14. A 21-day horizon cannot be graded sooner.
The outstanding structural forecast count is FROZEN under FC8: one document states ~12,529 where the per-kind table sums to 11,853. No count is stated here.
Live accuracy figures are FROZEN under FC1: four documents print four different versions of the same record. No accuracy percentage is stated in this deck.
Confidence intervals: the platform withholds every interval for the directional record. Distinct-day block counts are 9, 5, 4 and 4, against a floor of `min_distinct_blocks = 10`. Therefore NO interval verdict, NO significance claim, and NO statement about the sign of skill may appear. This is FC2.

## 3. The one thesis
The platform's only defensible claim is procedural, not predictive: it grades its own predictions against a protocol fixed before the outcomes were known, and it retires what fails. It has not yet demonstrated predictive skill on live data.
Everything else in this deck is either infrastructure supporting that, or a defect preventing it.

## 4. Directional family — retired
Status: RETIRED, by an auto-retire rule pre-registered before the outcomes came in.
The retirement rule fired and stopped it publishing. That is the load-bearing fact, not the figures, which are FROZEN under FC1.
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
Bias controls are BUILT AND IN FORCE unless noted otherwise.
- No look-ahead in feature and backtest code: Go tests in `daemon/internal/backtest/backtest_test.go`, `daemon/internal/alphax/alphax_test.go`, `daemon/internal/api/trackrecord_test.go` and 5 more, all run in CI.
- Model registry and governance: `daemon/internal/modelhealth`, gate in `daemon/internal/pipeline/modelhealth.go`.
- Automatic model retirement: `daemon/internal/pipeline/modelhealth_gate_test.go`.
- Drift monitoring: `daemon/internal/modelhealth/drift.go`, two-sample KS with an n-adjusted critical value.
- Calibration monitoring: `GET /api/calibration`.
- Multi-source price validation: `daemon/internal/pricecheck`.
- Canary / staged model rollout: `daemon/internal/canary`.
- Dataset versioning and checksums: `daemon/internal/datasetver`.
- Nightly bias regression: scheduled task `SignalDeck Bias`; last log line reads `nightly bias regression: PASS`.
- Data-quality and freshness checks: `dq-auditor`, wired in `daemon/internal/maintain/maintain.go`.
- Corporate actions: `daemon/internal/splitfix`.
- Data licensing: `daemon/internal/datalicense` plus an HTTP 451 guard on raw bar export, asserted by `daemon/internal/api/export_test.go`.
- CI: `.github/workflows/ci.yml` runs `go build`, `go vet`, `go test -race ./...`, a clone-only rebuild from `git archive HEAD`, and the web lint and build.

## 8. Open data defects
These are the reasons nothing here is finished.
- `universe_membership` held 0 rows and now holds 1,854,228, across 2,146 days and 1,777 symbols (measured 2026-08-04). Every row carries `source = 'bars-1d'`, so the membership is only as point-in-time as the retained daily-bar history is complete.
- Survivorship control is **partially closed, with a quantified residual**. Symbols carrying a `delisted_at` stamp went from 16 to 716. `proofs/P3A_SURVIVORSHIP_BACKFILL.md` records the verdict as substantially closed for 2019–2022 and states the residual plainly: 2023–2025 is under-covered because the upstream delisted-companies endpoint under-reports recent years, and the live detector only began watching in 2026. Treat HISTORICAL results over 2023–2025 as still survivor-seeded. FC3 is narrowed to that window rather than lifted.
- Remediation phases were landing while this deck was written. Every phase status here is a snapshot taken 2026-08-04; read `proofs/` directly before relying on one.
- Walk-forward validation (`tools/revalidate_structural.py`) runs on demand only. Nothing schedules it and no gate consumes its output.
- The corpus contained four mutually inconsistent versions of the live directional record. The documents now include one generated block (`partials/live_accuracy.md`) instead of typing it. FC1 stays frozen until the P2 artefact is filed.

## 9. Risk policy specification
`daemon/internal/riskgate` is the pre-trade check: it can refuse or shrink a trade. It holds no state, performs no I/O, and reads no clock.
It is BUILT AND IN FORCE for the PAPER book only: importers are `daemon/internal/pipeline/paper.go`, `paperrisk.go`, `daemon/internal/stresslab/replay.go`, `daemon/internal/api/stress.go`.
No real capital is attached to any of them.
The kill switch, `daemon/internal/killswitch/killswitch.go`, is BUILT AND IN FORCE for the PAPER book (P4C, 2026-08-04): file-based (`ops/HALT`), fail-closed, read fresh before every order by `daemon/internal/pipeline/paper.go`, and exercised by an end-to-end halt simulation (`pipeline.TestKillSwitch_HaltsEntriesAndLedgersTheRefusal`) plus `killswitch_test.go`. It refuses ENTRIES; risk-reducing exits still execute, so a halt cannot trap the book. It stops new risk, it does not liquidate. Its blast radius is a simulated book — there is no live order path here to halt.
A kill switch previously claimed for this platform lived at `stock-trader/trader/risk_gate.py`, in a DIFFERENT repository. It cannot halt this daemon and no claim here rests on it. **FC5 resolved** — see `proofs/P4C_KILL_SWITCH_CORRECTION.md`.
Risk is an ADMISSION GATE, not a post-hoc sizer (P4B): `riskgate.Admit` gates the book once per pass and `riskgate.Evaluate` gates each candidate BEFORE `ev.Decide` renders a verdict, so a risk-rejected name never consumes an EV rank slot. Every refusal — risk gate and kill switch alike — is ledgered to `ev_decisions`.
Position sizing is FROZEN under FC6: marked as existing, not bound to any allocation decision. `portopt` is not wired to sizing.

## 10. Execution specification
There is no live execution path. No broker connection exists in this repository.
Simulated execution, HISTORICAL only: `daemon/internal/backtest/backtest.go` fills at the next bar's OPEN and applies a single `CostBps` round-trip proxy for commission, spread and slippage together. There is no separate slippage model.
Slippage measurement, an executed-quantity ledger, and broker reconciliation are NOT BUILT here. Documents that claimed them were citing a `stock-trader` artifact in a different repository.
Alpaca appears here as a MARKET DATA client only (`daemon/internal/ingest/alpaca/client.go`, with 429 backoff), not as an execution client.
Consequence: any statement about achievable fills is a modelling assumption, not a measurement.
Position-level EXITS are BUILT AND IN FORCE for the PAPER book (P4D, 2026-08-04): a triple barrier — hard stop at 2.0×ATR(20), take-profit at 3.0×ATR(20), horizon expiry at 1 bar (`1d`) / 5 bars (`1w`). Every barrier is confirmed by a CLOSE and filled at the OPEN of a STRICTLY LATER bar; highs and lows are never read, so no intrabar path assumption is made. Wicks do not stop the book out and gaps are paid in full — both asserted by tests. See `proofs/P4D_BARRIER_EXITS.md`.
MATERIAL: a 1-day forecast implies a 1-BAR HOLD, so every `flagship-1d` position now opens at one bar's open and closes at the next, and the probability-flip exit is unreachable on that book. This is a DIFFERENT STRATEGY from the open-ended hold the paper book ran before 2026-08-04. Any accuracy or P&L figure spanning that date covers two strategies and must be split at it.

## 11. Negative studies
These are results that came back against the platform.
- The directional ensemble graded below its baseline and was retired.
- trend21 was refuted as a predictor: it reproduces a driftless random walk.
- The daily research loop searches a 48-rule grid and reports, most days, that nothing survived Bonferroni correction, regime-survival and the counterfactual.
- A pairs-trading study (H018 / CORR63) returned a DO NOT SHIP verdict, which stands.
- The "move down the liquidity spectrum" hypothesis was tested and refuted.
These are recorded because a research record that only keeps its wins is not a research record.

## 12. Honest capability assessment
The platform has not demonstrated predictive skill on live data. Its directional family failed and is retired. Its structural family is unresolved and pending.
What the platform has demonstrated is a working measurement and retirement process, supported by an array of bias controls that are built and in force.
The platform holds no live capital and executes no trades. All results cited are either HISTORICAL (backtested) or PENDING (awaiting sufficient live data).
The open data defects in section 8 prevent any HISTORICAL table from being read as survivorship-clean.
The publication freeze remains in force. Its stated lift conditions are now all filed, but lifting requires an explicit successor artefact that does not exist, and external publication is blocked independently by the unanchored chain.

## 13. Governance and limitations
A publication freeze is in force: `proofs/P0_FREEZE.md`. No document may be published, presented, distributed or quoted externally, and no capital may be attached to any signal described in the repository.
The freeze's stated lift conditions are filed proof artefacts for P1, P2, P3A and P3B. As at 2026-08-04 all four exist in `proofs/`, alongside P4A, P4C, P5, P6 and P7. **The freeze has nonetheless not lifted**, for two independent reasons. First, `proofs/P0_FREEZE.md` §1 requires the freeze to be lifted by an explicit successor artefact, and no such artefact has been filed; the conditions being met is not the same act as lifting. Second, external publication is blocked separately by the anchoring status below, and that gate does not move with the phase count. Read `proofs/` directly rather than trusting this list — phases were landing while this document was written.
The frozen claim set `C` (members FC1-FC8) is defined once, in `proofs/P6_GOVERNANCE_CLEANUP.md` §4.
The prediction ledger is hash-chained and signed. Its anchors live in the same SQLite file the operator controls.
Anchoring status is INTERNAL — UNANCHORED. `ops/anchor-publish.sh` exists, is invoked by nothing, is not scheduled, and has never succeeded; its log's last entry, dated 2026-07-27, reads `FAIL: no public clone at /nonexistent — nothing was externally timestamped`.
Therefore the chain is tamper-evident against an actor without the signing key, and is NOT evidence against the operator, who holds both the key and the database.
Offsite backup is BUILT BUT NOT IN FORCE: the backup log records `offsite SKIPPED: no destination configured`. Backups exist on one machine, on one volume.
Single operator, single machine. There is no independent reviewer.

## 14. Next actions
1. Close the 2023–2025 survivorship residual that `proofs/P3A_SURVIVORSHIP_BACKFILL.md` quantifies. Until then, HISTORICAL results spanning those years stay survivor-seeded and FC3 stays frozen for that window.
2. Decide the freeze explicitly. Its conditions are met and its lift is a deliberate act, not an automatic one: file a successor artefact that states which documents become publishable, or record why it stays in force. Leaving it ambiguous is the worse option.
3. Create the public anchors repository, schedule `ops/anchor-publish.sh` daily, and treat a push failure as an alert rather than a log line.
4. Add third-party timestamping over the chain head, weekly. Steps 3 and 4 differ: step 3 moves evidence to a host whose credentials the operator still holds.
5. Configure `SIGNALDECK_OFFSITE_DIR` to a volume that is not the machine's own.
6. Audit the completeness of the `bars-1d` history itself. Every point-in-time and survivorship claim now rests on it, and nothing in the repository currently measures it.
7. File the remaining phase artefacts. Until P2 and P3A are filed, the freeze stands.