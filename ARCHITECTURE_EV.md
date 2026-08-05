# EV-Centric Architecture — gap map and roadmap (2026-07-26)

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** AUTHORITATIVE — freeze lifted 2026-08-04
> **Scope:** EV-centric architecture gap map and roadmap.
> **Frozen claim classes:** FC6 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, statuses in `proofs/P10_FREEZE_LIFT.md` §4
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE — caveats are the frozen classes above

> ## ⚠ P0 freeze (2026-08-04) — LIFTED 2026-08-04 by `proofs/P10_FREEZE_LIFT.md`
> Remediation complete; freeze lifted. **Do not attach capital.** Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **layer status
> ("EXISTS" / "STRONG" / "BUILT") · kill switch · position sizing · correlation control.**
> Every `EXISTS` in the scorecard was frozen pending re-verification in P6, on the
> principle that a function which compiles is not a control that binds. P6 did that
> re-verification; read the scorecard against `proofs/P6_GOVERNANCE_CLEANUP.md` §1 and
> the note below it. Confirmed in this file and not in dispute:
> the live entry trigger is a bare probability threshold, `riskgate.Evaluate` sizes only
> *after* go/no-go, and correlation is display-only.
> Authority: `proofs/P0_FREEZE.md`, **lifted 2026-08-04** by `proofs/P10_FREEZE_LIFT.md`.

Target pipeline: Data → Feature Store → Prediction → Uncertainty → Risk →
**Expected Value** → Portfolio Optimization → Execution → Post-Trade Attribution.
Prediction is one module, not the spine. This doc maps the ten missing layers
onto what already exists and orders the work. The file:line references were
checked on 2026-07-26 and have not been re-checked since; treat them as a
snapshot of that date, not a current attestation.

## Scorecard

**P6 re-verification.** `EXISTS` in this table means *the code is present*. It
does **not** mean the control binds anything, and P0 froze every one of these
labels for exactly that reason. Read each `EXISTS` against the three statuses
in `proofs/P6_GOVERNANCE_CLEANUP.md` §1: several are **BUILT BUT NOT IN FORCE**,
and the Verdict column already says which — "riskgate is a pure function",
"portopt not wired to allocation", "shadow-only". Where the Verdict column and
the Status column disagree, the Verdict column is the honest one.

| # | Layer | Status | Verdict |
|---|-------|--------|---------|
| 1 | Decision Engine (EV gate) | PARTIAL | **Biggest gap.** All inputs computed, never joined at the decision |
| 2 | Research Graph | PARTIAL | Graph exists but only hypothesis↔evidence edges |
| 3 | Causal / interventions | EXISTS | Feature ablations, corrected; no placebo/data interventions |
| 4 | Market simulator | WEAK | Linear single-factor sensitivity only |
| 5 | Automatic research scientist | EXISTS | Grid-based, corrected, shadow-only; retirement in engine loop |
| 6 | Prediction attribution | STRONG | Full comp decomposition on pressure score; GBM leg unattributed |
| 7 | Capital allocator | EXISTS | riskgate is a pure function; portopt not wired to allocation |
| 8 | Knowledge base | PARTIAL | Bayesian ledger is permanent; three fragmented hypothesis stores |
| 9 | Continuous self-critique | EXISTS | canary/model-health/drift/honesty-gaps; registry scheduled (plist now versioned) |
| 10 | Evidence Engine | PARTIAL→BUILT | `internal/evidence` added this session; wiring below |

## Layer 1 — Decision Engine (build first after evidence)

Everything the EV gate needs already exists, computed and persisted — and none
of it enters the entry decision:

- Expected return: `expectancy.Lookup` (daemon/internal/expectancy/expectancy.go:191)
- Cost-adjusted EV + no-trade zone: `distribution.ExpectedValue`, `p_inside`
  (daemon/internal/distribution/distribution.go:91-153) → `return_forecasts`
- Tail: `confidence.AdverseExcursion` (confidence.go:88), `papertrade/downside.go`
- Liquidity/cost/capacity: `papertrade/execution.go` (sqrt impact, 5% ADV cap)
- Correlation: `portopt` + `risklens` — currently display-only via /api/quant

The live trigger is a bare probability threshold: `papertrade.DecideTarget(pred.CalProb)`
at pipeline/paper.go:194. `riskgate.Evaluate` sizes only *after* go/no-go.

**Plan:** new `internal/ev` package. `Assess(candidate) EVAssessment` joins the
ten inputs; `Decide(EVAssessment) BUY | SELL | DO_NOTHING` with an explicit EV
threshold net of `tau`, refusal reasons enumerated (like riskgate's `Sizing`
labels). Insert at paper.go:194 before riskgate. Candidates ranked by EV across
the whole pass (kills the arbitrary symbol-order loop at paper.go:185) — that
ranking IS the opportunity-cost input. Every DO_NOTHING is ledgered so Layer 9
can later audit "what did refusals cost" (currently unauditable).

## Layer 10 — Evidence Engine (built this session)

Prior state: two half-systems that never meet. `tools/accuracy_registry.py`
(cluster-robust verdicts: VALIDATED/DECAYED/FAILED/…) writes a JSON **nothing
reads back** — a DECAYED predictor keeps shipping its number. The Go
`researchledger` has Bayes-factor evidence chains and decay sweeps but no
expiry dates and no link to the registry. No `last_validated`/`revalidate_by`
exists anywhere in the schema; no claims table; no auto-downgrade path to the UI.

`internal/evidence` adds: first-class claims with scoped evidence items
(n_effective, method, correction, CI, source), rule-earned confidence tiers,
`revalidate_by` staleness sweep with one-tier auto-downgrade, retirement on
refuting evidence, `/api/evidence`. Follow-ups: subsume the Python registry's
grading into the Go worker (clusterstat already exists in Go — canary.go:270),
(scheduling correction: the registry IS scheduled — `com.signaldeck.accuracy`
is loaded in launchd; its plist was unversioned and is now mirrored into ops/),
and make the web
UI cite claim IDs so a downgrade visibly changes what displays.

## Layers 2+8 — one lineage spine, not two features

The graph (`researchx/graph.go`) and the knowledge base share a root cause:
three hypothesis registries (`research_hypotheses`, `research_loop_hypotheses`,
`research_ledger_hypotheses`) with three ID schemes and no edges to
`dataset_versions`, `model_forecasts`, `prediction_ledger`, or `paper_trades`.

**Plan:** a `lineage_edges` table (src_kind, src_id, dst_kind, dst_id, edge_kind)
plus `TraceTo(predictionSeq)`. Populate at write time in the four producers.
Add a git-SHA column to experiment records (no code version is captured today).
Exempt permanent tables from `store/retention.go`'s indiscriminate pruning.
Then "which feature produced the most PnL" is one recursive query.

## Layer 6 — attribute the leg that actually trades

The pressure-score decomposition is complete (signals/score.go:247, comp_*
persisted). But the **calibrated probability that drives trading comes from the
GBM/ensemble legs, which have zero per-feature attribution**. Plan: per-feature
contribution for GBM (path-dependent split gain or SHAP-style), merged with the
comp decomposition into one "+12% trend / −6% …" answer per ledgered prediction.

## Layer 4 — simulator, scoped honestly

`scenario.Estimate` is a single-factor linear beta×shock (scenario.go:148), one
call site. A full agent-based simulator is out of scope for this codebase; the
honest middle: regime-conditional block bootstrap over stored bars + joint
multi-factor shocks (vol spike + spread widening + gap) replayed through the
REAL pipeline (signalbt's replay-committed-features discipline) and the paper
book, reporting how gates/sizing/drawdown breakers behave. Delayed-data and
missing-candle scenarios reuse the e2e harness.

## Layers 3, 5, 7, 9 — extend, don't rebuild

- **3 Causal:** add placebo controls (shuffled-label / permuted-feature runs must
  find nothing) and time-shift interventions to researchlab; keep its
  Bonferroni discipline. Trace ablation wins to live-prediction deltas.
- **5 Auto-scientist:** the loop is honest (grid + correction + shadow-only,
  researchloop.go:32-35 forbids self-promotion — keep that). Gaps: no semantic
  dedup across the three registries; no promotion path loop→ledger (a human
  gate is fine, an *impossible* path is not); retirement exists only in
  researchengine's decaySweep — unify.
- **7 Allocator:** wire `portopt` into sizing (it currently only feeds
  /api/quant); Kelly edge is book-wide realized payoff (paperrisk.go:128) —
  move to per-signal expectancy with shrinkage toward the book prior.
- **9 Self-critique:** registry scheduling is in force on this host as the
  `SignalDeck Accuracy` scheduled task (state Ready, 2026-08-04). The versioned
  `ops/com.signaldeck.accuracy.plist` is the macOS source that
  `ops/install-windows-tasks.ps1` translates from — it does not itself run
  anything here. Add the decision-layer audit (cost of refusals) once Layer 1
  ledgers DO_NOTHINGs.

## Order of work

1. **Evidence Engine** (done this session) → wire UI + scheduler next.
2. **Decision Engine** (`internal/ev`) — the EV gate + DO_NOTHING ledger.
3. **Lineage spine** (Layers 2+8) — table + git SHA + retention exemption.
4. **GBM attribution** (Layer 6 completion).
5. **Stress replay** (Layer 4 middle path).
6. Extensions to 3/5/7/9 as above.

Every step passes ops/REVIEW_LIFECYCLE.md Levels 1–3 before merge; claims any
step ships must be registered in the Evidence Engine with a revalidate_by date.
