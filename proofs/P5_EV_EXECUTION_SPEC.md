# P5 — EV / execution spec, and P4B risk-before-admission: proof artifact

**Date:** 2026-08-04 · **Phases:** P4B, P5 · **Artifact:** `EXECUTION_SPEC.md`

---

## 1. What was asked

**P4B:** move risk evaluation before admission; make `riskgate.Evaluate` a gate,
not a decorator; require a risk pass before any candidate can become tradeable;
log rejections; ensure no candidate bypasses it.

**P5:** turn a "bare probability threshold" into a real decision framework —
`EXECUTION_SPEC.md`, an admission conjunction, order types, exits, and a ledger
for every refusal.

---

## 2. P4B — risk before admission

### 2.1 What was already true

The brief's premise was that "sizing after an unconditional go/no-go is not risk
control." **The go/no-go was not unconditional, and the risk gate could already
reject.** Before this phase, `paper.go` ran:

```
assess → rank by net EV → ev.Decide → riskgate.Evaluate → fill
                                       └── if !gate.Allow { refused++; continue }
```

So `riskgate` was already a hard refusal point, not a pure sizer. Saying that
plainly matters: a change justified by a false premise is a change nobody can
audit.

### 2.2 What was nevertheless wrong with it

Two real defects, both structural:

1. **Risk ran after EV had already ranked and decided.** Rank is the
   opportunity-cost input — `MaxRank` 10 refuses anything below tenth. A
   candidate the risk gate would never fund still occupied a rank slot, so it
   could push a fundable candidate below the threshold. Capital denied to rank 1
   by an untradeable rank 8 is capital misallocated by the accounting, not by
   the market.
2. **Risk refusals were counted, not logged.** `refused++` and nothing else. EV
   refusals went to `ev_decisions` with an enumerated reason; risk refusals
   vanished. "Every DO_NOTHING is auditable" was true of one gate and false of
   the other.

### 2.3 What changed

**New: `riskgate.Admit(book, limits) Decision`** — the book-wide breakers as a
standalone function, answering the question that is logically *prior* to any
candidate: is this book open for new risk at all? The breakers do not depend on
which candidate is asking, so evaluating them per-candidate *after* something
else decided the candidate was worth trading makes risk look like a property of
the trade rather than a precondition for trading. `Evaluate` calls `Admit`
first, so the two can never disagree about what a halted book is.

`Admit` covers: usable equity, `MaxDrawdown`, `MaxDailyLoss`, `MaxPositions`,
and gross-exposure exhaustion. A permitting `Decision` carries `Notional 0` and
`SizingNone` — admission is not a size, and nothing may mistake it for one.

**New pipeline order in `paper.go`:**

```
Phase 1  exits execute (never gated) · entry candidates collected
         └── kill switch checked; halted ⇒ ledger + skip, no assessment work
Phase 2  RISK ADMISSION
         ├── riskgate.Admit once per pass — refusal admits NOTHING
         └── riskgate.Evaluate per candidate — survivors only
Phase 3  rank SURVIVORS by net EV · ev.Decide
Phase 4  kill switch re-read · riskgate.Evaluate for final size · fill
```

Phase 4 is not a re-litigation of admission. Cash, slots and sector headroom all
move within a pass as earlier entries consume them, so the final call measures
against the book *as it now stands*. Its refusal is a different fact: not "this
name is untradeable" but "there is no longer room for it today". Both are
ledgered.

**Rejection logging.** `ledgerGateRefusal` writes risk-gate and kill-switch
refusals to the **same** `ev_decisions` ledger, with two new enumerated reasons:
`risk-gate-refused` and `kill-switch-halted`. The full `riskgate.Decision` — its
`Reasons` **and** its `Breaches` — or the `killswitch.State` is snapshotted into
`inputs_json` under a `risk` / `halt` key, so *which limit fired* survives with
no schema change.

One ledger, deliberately: a refusal filed in a table nobody joins against is a
refusal nobody reads. The property worth having — every candidate the book did
not trade, with its reason, in one query — is only true if all three gates write
to one place. The assessment is embedded anonymously in the snapshot, so its
fields stay at the top level exactly as before and every existing reader keeps
working.

### 2.4 P4B done-when

| Criterion | Status | Evidence |
|---|---|---|
| Risk evaluation moved before admission | ✅ | Phase 2 precedes Phase 3 in `paper.go` |
| `riskgate.Evaluate` is a gate, not a decorator | ✅ | `Admit` is a standalone gate; `Evaluate` refuses before ranking |
| Risk pass required before tradeable | ✅ | only `admitted` candidates are ranked and decided |
| Risk gate can reject | ✅ | `TestRiskGate_RejectsBeforeEVDecides` |
| Rejection is logged | ✅ | `ev_decisions`, reason `risk-gate-refused`, with `breaches` |
| No candidate bypasses it | ✅ | `TestAdmitAndEvaluateAgreeOnEveryBookWideBreaker` — every book-wide breaker refuses in both functions, for every candidate |

---

## 3. P5 — the execution spec

`EXECUTION_SPEC.md`, ~2,700 words. **14 rules in force, 5 spec only.**

### 3.1 "No live trigger is a bare probability threshold" — already true, now documented

`cal_prob` supplies **intent** (`papertrade.DecideTarget`: GoLong / GoFlat /
HOLD deadband). **Admission** is `ev.Decide(ev.Assess(candidate))`. The two are
separate and the spec says so in its first section, because the distinction is
the entire claim.

The proof this is not cosmetic already existed and still passes: a candidate at
`cal_prob` 0.95 whose expected move is smaller than its round-trip cost is
refused (`TestEVGate_RefusesHighProbNegativeNetEV`). A bare threshold buys it on
sight.

### 3.2 The admission conjunction — all nine conditions IN FORCE

Kill switch clear · `riskgate.Admit` passes · `riskgate.Evaluate` passes ·
`ev.Decide` returns BUY/SELL · `NetEV > MinNetEV` (0.0, strictly exceeded) ·
`PInside < MaxPInside` (0.75) · adverse-excursion bound within `MaxTailP90`
(0.10) · `Rank ≤ MaxRank` (10) · ADV participation cap satisfied.

Required inputs — probability, distribution, round-trip cost — fail closed via
explicit has-flags. A missing cost is not a free trade.

### 3.3 Order types and exits — SPEC ONLY, and why

There is **no broker order path in this repository.** Order types are therefore
specifications by construction, and the spec says so rather than describing
`papertrade`'s arithmetic as if it were routing:

- **Limit-at-touch for ≥ 1d.** A daily-bar forecast makes no claim about the
  next ten seconds, so paying a spread for immediacy buys nothing it asked for.
- **Market for 1h only**, and only where measured signal decay exceeds the
  spread. There is no 1h path today; the rule exists so the burden of proof
  falls on the market order if one is ever added.

Exits: the **triple barrier** (favorable +3.0×ATR, adverse −2.0×ATR, horizon
expiry) is specified and **not built**. The reason is recorded rather than
skipped: on daily bars a bar does not say whether the low preceded the high, so
an intrabar barrier cannot be filled honestly. Any implementation must fill at
the next open, ingest intraday bars, or disclose an explicit path assumption and
publish the result measured both ways. **A stop backtested with an undisclosed
path assumption is worse than no stop** — it reports a risk control that was
never available at the price claimed.

What is in force is the probability-flip exit, and the spec states its defect
plainly: between entry and the flip the position's downside is bounded by
nothing.

### 3.4 Every refusal is auditable

Nine enumerated reasons now: `positive-net-ev` · `exit-never-blocked` ·
`missing-required-input` · `inside-no-trade-zone` · `tail-too-fat` ·
`net-ev-below-floor` · `outranked-by-better-ev` · **`risk-gate-refused`** ·
**`kill-switch-halted`**.

Append-only. `net_ev` is NULL when unmeasurable, never a silent zero.
Corrections are new rows, not edits.

### 3.5 P5 done-when

| Criterion | Status |
|---|---|
| No live trigger is a bare probability threshold | ✅ `cal_prob` = intent; `ev.Decide` = admission; proven by `TestEVGate_RefusesHighProbNegativeNetEV` |
| Execution logic is explicit | ✅ `EXECUTION_SPEC.md` §1–§4, every rule status-marked |
| Every candidate has entry/exit/cost logic | ✅ entry §1, cost §4 both IN FORCE; exit is IN FORCE as the probability flip and SPEC ONLY as the triple barrier, marked as such |
| Every DO_NOTHING is auditable | ✅ `ev_decisions`, 9 reasons, all three gates writing to one table |

---

## 4. Verification

```bash
cd daemon && go build ./... && go vet ./... && go test ./... -count=1
```

Full suite: **exit code 0.**

Gate-specific:

```bash
cd daemon && go test ./internal/pipeline/ -run "KillSwitch|RiskGate|EVGate" -count=1 -v
```

```
--- PASS: TestEVGate_RefusesHighProbNegativeNetEV
--- PASS: TestEVGate_RefusesWhenDistributionMissing
--- PASS: TestEVGate_LedgersBuyAndExit
--- PASS: TestEVGate_RanksCandidatesByNetEV
--- PASS: TestRiskGateEnforcesSectorCapAcrossOneStep
--- PASS: TestRiskGateRecordsSizingRationaleOnTheFill
--- PASS: TestRiskGateNeverBlocksAnExit
--- PASS: TestKillSwitch_HaltsEntriesAndLedgersTheRefusal
--- PASS: TestKillSwitch_UnparseableHaltInstructionHaltsTheWorker
--- PASS: TestKillSwitch_ExitsStillExecuteWhileHalted
--- PASS: TestRiskGate_RejectsBeforeEVDecides
ok  github.com/nyaungnicholas-wq/signaldeck/internal/pipeline
```

Documents are machine-gated on 30 required phrases each and a ban list
containing `stock-trader/trader/risk_gate.py`:

```
OK RISK_POLICY.md (3252 words)
OK EXECUTION_SPEC.md (2690 words)
```

---

## 5. What these phases did NOT do

- **No barrier exits were implemented.** §3.3 explains why, and the honest
  version of that decision is that it is a *deferral*, not a completion. A
  position's downside is still bounded by nothing but the next probability flip.
- **No live execution layer.** Everything above governs a simulation.
- **The cost model is still unvalidated against a real fill.** It is a
  defensible estimate that has never met a filled order.
- **FC6 (position sizing not bound to any allocation decision) is untouched.**
  It concerns surfaces outside the paper book and was out of scope.
