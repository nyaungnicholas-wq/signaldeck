# Execution spec

> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Effective:** 2026-08-04
> **Authority for status:** `proofs/P5_EV_EXECUTION_SPEC.md`
> **Companion:** `RISK_POLICY.md` (the limits this spec trades inside)

## ⛔ Read this before anything else

**This spec governs a simulated book.** There is no broker order path in this
repository and no code that could place a real order. Alpaca is a market-**data**
client here. The only executor is `daemon/internal/pipeline/paper.go`, filling
against stored daily bars by arithmetic.

Everything about **order types** is therefore SPEC ONLY by construction: there
is nothing to route an order to. What is IN FORCE is the *decision* layer —
admission, ranking, sizing, cost, and the refusal ledger.

### Status legend

| Marker | Meaning |
|---|---|
| **[IN FORCE — paper]** | Implemented, imported, exercised by a test, consulted on every relevant decision |
| **[SPEC ONLY — NOT IN FORCE]** | Written down. Not implemented. Nothing checks it |

---

## 1. Admission

### 1.1 No live trigger in this system is a bare probability threshold

**This is the central claim of the spec, and it is IN FORCE.**

The calibrated probability `cal_prob` supplies **intent** only, via
`papertrade.DecideTarget`: GoLong above the long threshold, GoFlat below the
flat threshold, HOLD in the deadband between them. **Intent is not admission.**
`cal_prob` answers "which direction, if any, does the forecast point?" and has
no authority to open a position.

Admission is `ev.Decide(ev.Assess(candidate))`, which answers a different and
harder question: **is this direction worth its cost?** A candidate at `cal_prob`
0.95 whose expected move is smaller than the round-trip cost of capturing it is
refused, and there is a test that proves exactly that
(`TestEVGate_RefusesHighProbNegativeNetEV`).

### 1.2 The admission conjunction — **[IN FORCE — paper]**

A candidate becomes tradeable only when **all** of the following hold. Any one
failing produces a refusal, and every refusal is ledgered (§5).

1. **The kill switch is clear.** `killswitch.Check()` reports not halted, read
   fresh before every order rather than cached for the pass. See
   `RISK_POLICY.md` §4.
2. **The book is admitted for new risk.** `riskgate.Admit` passes: drawdown,
   daily loss, position count, gross exposure, and usable equity.
3. **The candidate is admitted by the risk gate.** `riskgate.Evaluate` passes:
   correlation to book, sector headroom, position cap, minimum ticket, and a
   non-negative measured expectancy.
4. **`ev.Decide(ev.Assess(candidate))` returns BUY** (or SELL for an exit).
5. **NetEV > 0 after costs.** `MinNetEV` 0.0 (`SIGNALDECK_EV_MIN_NET_EV`) —
   NetEV must strictly *exceed* the floor. Zero is the honest conservative
   default: a trade whose cost-adjusted expected value is not strictly positive
   is at best paying spread to flip a fair coin.
6. **The candidate is outside the no-trade zone.** `MaxPInside` 0.75
   (`SIGNALDECK_EV_MAX_P_INSIDE`) — refuse when the forecast distribution puts
   at least three quarters of its mass *inside* the round-trip cost band. 0.75
   rather than 0.5 because `p_inside` is the honest majority outcome on a 1-day
   horizon; gating at 0.5 would freeze the book on its own honesty.
7. **An adverse-excursion bound exists and is within tolerance.**
   `MaxTailP90` 0.10 (`SIGNALDECK_EV_MAX_TAIL_P90`) — refuse when the
   90th-percentile **adverse excursion** of comparable holds is 10% of entry or
   worse. A setup that typically trades 10% against the entry before resolving
   would meet the 20% drawdown breaker half-way on two names, regardless of
   where it ends. The tail is checked only when *measured*; a thin episode
   sample withholds it, and withholding an advisory input is not the same as it
   being fat.
8. **The EV rank threshold is satisfied.** `MaxRank` 10
   (`SIGNALDECK_EV_MAX_RANK`) — a candidate ranked below tenth by net EV in its
   pass is refused as outranked. This matches `riskgate` `MaxPositions` 10: the
   book cannot hold more names than that, so a candidate outside the top ten is
   competing for capital better EV has already claimed.
9. **The liquidity / ADV participation cap is satisfied.** `CapacityUSD` =
   participation cap × trailing average daily dollar volume. A name with no
   usable ADV estimate is skipped, never filled at zero impact.

### 1.3 Required inputs fail closed — **[IN FORCE — paper]**

Three inputs are **required**: the calibrated probability, the forecast
distribution (the EV itself), and the round-trip execution cost of this specific
fill. Each carries an explicit has-flag. If any one is absent the verdict is
`DO_NOTHING` with reason `missing-required-input` — the engine refuses rather
than deciding over a silent default. A missing cost is not a free trade.

### 1.4 Risk runs before EV — **[IN FORCE — paper]**

Ordering is load-bearing, and it changed in P4B.

`riskgate.Admit` runs once per pass before any candidate is considered, and each
surviving candidate must clear `riskgate.Evaluate` **before** it is ranked or
decided. Sizing a trade that a go/no-go has already approved is not risk control
— it is a decorator on a decision already made, and it leaves the gate arguing
about *how much* of something the book should never have been doing.

A risk-rejected name must not even consume an EV rank slot, because **rank is
the opportunity cost**: capital denied to rank 1 by an untradeable rank 8 is
capital misallocated by the accounting, not by the market.

A second `riskgate.Evaluate` runs at fill time. That is not a re-litigation of
admission but a measurement against the book *as it now stands* — cash, slots
and sector headroom all move within a pass as earlier entries consume them. Its
refusal is a different fact from the admission one: not "this name is
untradeable" but "there is no longer room for it today". Both are ledgered.

### 1.5 No lookahead — **[IN FORCE — paper]**

A fill happens at the **open of the first daily bar strictly after** the
prediction's timestamp, and never at a bar later than the run's as-of clock. A
prediction made from bar P cannot fill on bar P. ADV is measured on bars at or
before the fill. This is the guarantee every performance number rests on.

---

## 2. Order types

Both rules below are **[SPEC ONLY — NOT IN FORCE]**. Nothing routes an order
anywhere; the paper model fills at an open with modelled slippage and impact.
These are specifications for a future execution layer, not descriptions of code.

### 2.1 Limit-at-touch for horizons of 1d and longer

**Rule: entries and exits on a `1d` or longer horizon are placed as limit orders
at the touch — bid for buys, ask for sells — with a same-session cancel.**

A daily-bar strategy has no urgency that pays a spread. The forecast is about
where the price will be in a day; it makes no claim about the next ten seconds,
so paying the spread for immediacy buys nothing the signal asked for. The
accepted cost is non-execution, which is the correct trade: a missed entry costs
the expected value of one candidate, while a crossed spread on every entry is a
guaranteed levy on all of them.

### 2.2 Market orders reserved for 1h

**Rule: a market order is permitted only on a `1h` horizon, and only when the
measured signal decay over the expected limit-fill delay exceeds the spread
cost.**

**There is no 1h execution path in this system.** The traded horizon is `1d`
(`flagship-1d`) with `flagship-1w` alongside it. This rule exists so that if an
hourly strategy is ever added, the burden of proof is on the market order:
crossing the spread must be *demonstrated* cheaper than waiting, not assumed
because the horizon is short.

---

## 3. Exits

### 3.1 What is in force

**The triple barrier — [IN FORCE — paper].** See §3.2.

**Probability-flip exit — [IN FORCE — paper].** A position is closed when
`cal_prob` falls back through the flat threshold.

**Which one wins.** Both are confirmed by a CLOSE and filled at a strictly later
OPEN, so both carry a trigger timestamp and **the earlier one wins** — "first
touched" means first in *time*, not first in whatever order the code checks. A
tie goes to the barrier: a risk control that loses coin flips to a signal is not
a risk control.

**Barriers are evaluated for every open position**, whether or not a fresh
prediction exists and whatever it says. A stop that only fires when the model
happens to have an opinion is not a stop; it is a second opinion. Proven by
`TestBarrier_FiresWithoutAFreshPrediction`.

**Exits are never blocked — [IN FORCE — paper].** Not by the EV engine, not by
the risk gate, not by the kill switch. Both `ev.Decide` and `riskgate.Evaluate`
state this first in their own function bodies so it cannot be reordered behind a
check that might refuse. Routing a de-risking trade through a component that can
say no is how a book ends up trapped in the position a breaker was tripped by.
The SELL is still **ledgered**, with reason `exit-never-blocked`, so the decision
log remains the complete record of every transition.

### 3.2 The triple barrier — **[IN FORCE — paper]**

**Rule: every position carries three exits, and the first one reached wins.**
`daemon/internal/papertrade/barriers.go`. Toggle `SIGNALDECK_PAPER_BARRIERS`
(default true).

| Barrier | Definition | Env | Status |
|---|---|---|---|
| **Favorable** | close ≥ entry + 3.0 × ATR(20) | `SIGNALDECK_PAPER_BARRIER_FAVORABLE_ATR` | **IN FORCE — paper** |
| **Adverse** | close ≤ entry − 2.0 × ATR(20) | `SIGNALDECK_PAPER_BARRIER_ADVERSE_ATR` | **IN FORCE — paper** |
| **Horizon expiry** | after 1 bar (`1d`) or 5 (`1w`) | — | **IN FORCE — paper** |

A price barrier reached on the expiry bar is recorded as the price barrier. The
fill is identical either way, so this only decides which fact the log records —
and "the stop was hit on the last day" is more useful than "it expired".

See `RISK_POLICY.md` §1.0–§1.4 for the per-rule justification and for the
consequence that on `flagship-1d` the 1-bar horizon dominates.

### 3.3 How the hazard was resolved

**On daily bars a barrier cannot be filled honestly *intrabar*.** A daily bar
records open, high, low and close. It does **not** record whether the low
preceded the high. A position with a stop below and a target above may have
reached either first, and the bar cannot say which. Choosing the favourable
ordering manufactures profit that never existed.

Of the three honest options — next-open fills, intraday bars, or a disclosed
path assumption — **this implementation takes the first**:

> **A barrier is confirmed by a CLOSE and filled at the OPEN of a strictly later
> bar. Highs and lows are never read for the trigger.**

No path assumption is made, so none needs disclosing. The ambiguity does not
arise at all: the favorable level is above entry and the adverse level below it,
and a close is one price, so both can never fire on the same bar.

Two costs are accepted rather than modelled away, and both are asserted by
tests:

- **Wicks do not stop the book out.** A bar trading through the stop that closes
  back above it is not an exit — a real behavioural difference from an intrabar
  stop, not an approximation of one.
  (`TestBarrier_WickThroughTheStopDoesNotExitTheBook`)
- **Gaps are paid in full.** The fill is the next open, wherever that is, not
  the stop level. (`TestBarrier_CloseThroughTheStopExitsTheBook` asserts the
  fill price is the next bar's open and not the barrier.)

Volatility is measured at entry from bars that had already closed when the entry
filled (`store.BarsBefore` enforces `ts < t` in SQL) and is then **fixed** for
the life of the position. These are barriers, not a trailing stop. An
unmeasurable ATR places no price barriers at all rather than a zero-width pair;
the time stop still applies, because a horizon has nothing to do with
volatility.

### 3.4 The auditable no-lookahead record

Every barrier exit ledgers both timestamps separately — `barrier.triggerTs` (the
close that confirmed it) and `fillTs` (the open it filled at). Anyone auditing
this book can assert `fillTs > barrier.triggerTs` over every row, which is the
no-lookahead guarantee expressed as a query rather than a promise.

---

## 4. Cost model

**[IN FORCE — paper]**, reproducing `papertrade`'s execution model on the
intended size, because the gate must judge the trade it would actually take.

| Component | Formula | Note |
|---|---|---|
| Spread | `CostBpsFor(market) / 1e4` per side | market-dependent half-spread |
| Impact | `ImpactCoef × sigma × sqrt(notional / ADV)` per side | square-root law; `sigma` from the Parkinson high/low estimator, `2·sqrt(ln 2)` |
| **Round trip** | `2 × (spread + impact)` | both sides, always |

**NetEV is measured net of the full round-trip cost, not the one-way cost.** A
position that must be closed has already committed to paying twice, and charging
once is the most common way a backtest flatters itself.

One subtlety worth recording: `DistEV` from the forecast distribution is
*already* net of the cost band `tau`. Only the part of the modelled round-trip
cost **exceeding** `tau` is charged again — charging all of it would
double-count the band and refuse trades that are genuinely positive.

The cost is unknowable without a liquidity estimate or a usable bar range, and
in that case the has-flag stays false and admission refuses (§1.3). Fail closed;
never fill free.

**Limitation:** this model has never been checked against a fill that actually
happened. It is a defensible estimate, not a measurement.

---

## 5. The refusal ledger

**[IN FORCE — paper]** — table `ev_decisions`.

**Every verdict is ledgered: every BUY, every SELL, and every `DO_NOTHING`** —
including risk-gate and kill-switch refusals. A refusal that is not recorded is
indistinguishable from an entry that silently never happened, and the ability to
ask "what did the system decline to do, and why" is the whole point.

### 5.1 Schema

| Column | Meaning |
|---|---|
| `seq` | autoincrement; the append order is the audit order |
| `ts` | decision time (the pass's as-of clock) |
| `strategy`, `symbol_id`, `symbol`, `horizon` | what was decided about |
| `decision` | `BUY` / `SELL` / `DO_NOTHING` |
| `reason` | the enumerated reason label |
| `net_ev` | **NULL when unmeasurable** — never a silent zero |
| `rank`, `rank_of` | net-EV rank within the pass (0 = unranked) |
| `inputs_json` | the full assessment snapshot, with its has-flags |

### 5.2 Enumerated reasons

`positive-net-ev` · `exit-never-blocked` · `missing-required-input` ·
`inside-no-trade-zone` · `tail-too-fat` · `net-ev-below-floor` ·
`outranked-by-better-ev` · `risk-gate-refused` · `kill-switch-halted` ·
`barrier-adverse` · `barrier-favorable` · `barrier-expiry`

The three `barrier-*` reasons label a SELL that a position-level barrier caused
rather than the signal. They are still exit-never-blocked in substance — no
barrier can *refuse* a trade, only cause one — but "the stop fired" and "the
model changed its mind" are different facts about a book, and filing them under
one reason would leave the ledger unable to say which control closed a position.
Their rows also carry the full `barrier` snapshot and the fill timestamp (§3.4).

`risk-gate-refused` and `kill-switch-halted` were added in P4B/P4C. They are not verdicts of the EV engine — the
engine never reaches an opinion on a candidate risk has already declined — but
they are written to the **same** ledger deliberately. A refusal filed in a table
nobody joins against is a refusal nobody reads, and the property worth having
("every candidate the book did not trade, with its reason, in one query") is
only true if all three gates write to one place.

For those two reasons the full `riskgate.Decision` — its `Reasons` **and** its
`Breaches` — or the `killswitch.State` is snapshotted into `inputs_json` under a
`risk` / `halt` key, so *which limit fired* survives without a schema change.
The assessment is embedded anonymously, so its fields stay at the top level
exactly as before and every existing reader keeps working.

### 5.3 Append-only

Every `DO_NOTHING` is therefore **auditable**: the reason, the rank it held, the
inputs the gate had, and the specific limit that fired are all recoverable from
one row, long after the tables those inputs came from have moved on.

A decision, once rendered, is history. **Corrections are new rows, not edits.**
The write goes directly rather than through the atomic `PaperApply`: decisions
are diagnostics, not book state, and the cursor check preceding each pass makes a
same-bar re-run a no-op.

---

## 6. Status summary

| Rule | Value | Enforced by | Status |
|---|---|---|---|
| Intent from `cal_prob` | long / flat / HOLD deadband | `papertrade.DecideTarget` | **IN FORCE — paper** |
| Admission from `ev.Decide(ev.Assess(candidate))` | BUY / SELL / DO_NOTHING | `internal/ev` | **IN FORCE — paper** |
| NetEV floor | `MinNetEV` 0.0, strictly exceeded | `ev.Decide` | **IN FORCE — paper** |
| No-trade zone | `MaxPInside` 0.75 | `ev.Decide` | **IN FORCE — paper** |
| Adverse excursion bound | `MaxTailP90` 0.10 | `ev.Decide` | **IN FORCE — paper** |
| EV rank threshold | `MaxRank` 10 | `ev.Decide` | **IN FORCE — paper** |
| Required inputs fail closed | prob · distribution · cost | `ev.Decide` | **IN FORCE — paper** |
| Liquidity / ADV cap | participation × trailing ADV | `papertrade` | **IN FORCE — paper** |
| Risk admission before EV | `Admit` then `Evaluate` | `internal/riskgate` | **IN FORCE — paper** |
| Halt before every order | `ops/HALT`, fail-closed | `internal/killswitch` | **IN FORCE — paper** |
| No lookahead | fill at next bar's open, ≤ as-of | `paper.go` | **IN FORCE — paper** |
| Round-trip cost charged | `2 × (spread + impact)` | `paperev.go` | **IN FORCE — paper** |
| Exits never blocked | all three gates | `ev` · `riskgate` · `killswitch` | **IN FORCE — paper** |
| Every refusal ledgered | `ev_decisions`, append-only | `store` | **IN FORCE — paper** |
| Favorable barrier | close ≥ entry + 3.0 × ATR(20) | `papertrade.FindBarrierExit` | **IN FORCE — paper** |
| Adverse barrier | close ≤ entry − 2.0 × ATR(20) | `papertrade.FindBarrierExit` | **IN FORCE — paper** |
| Horizon expiry exit | 1 bar (`1d`) / 5 bars (`1w`) | `papertrade.FindBarrierExit` | **IN FORCE — paper** |
| Barrier fills at a strictly later open | `fillTs > triggerTs` | `pipeline.planExit` | **IN FORCE — paper** |
| Limit-at-touch for ≥ 1d | same-session cancel | — | SPEC ONLY |
| Market order for 1h | only when decay > spread | — (no 1h path) | SPEC ONLY |

Eighteen rules in force, two specified only.

**The honest one-line summary: the decision layer is real and tested; the
execution layer is a specification, because there is nothing here that
executes.**
