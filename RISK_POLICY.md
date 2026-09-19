# Risk policy

> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Effective:** 2026-08-04
> **Authority for status:** `proofs/P4A_RISK_POLICY.md`
> **Scope:** the simulated paper book only.

## ⛔ Read this before anything else

**This policy governs a simulated book. No order has ever been placed from this
repository, and there is no code path that could place one.** The only executor
here is `daemon/internal/pipeline/paper.go`, which fills against stored daily
bars by arithmetic. Alpaca appears in this codebase solely as a market-**data**
ingest client.

**A rule marked SPEC ONLY is not enforced by anything.** It is a decision
recorded in advance so that the day it is implemented, it is not designed under
pressure. Writing a limit down does not make it bind. Roughly half of what
follows is SPEC ONLY, and the summary table in §5 says exactly which half.

### Status legend

| Marker | Meaning |
|---|---|
| **[IN FORCE — paper]** | Implemented, imported, exercised by a test, and consulted on every relevant decision in the paper pipeline |
| **[SPEC ONLY — NOT IN FORCE]** | Written down. Not implemented. Nothing checks it |

The two components carrying the IN FORCE rules are `daemon/internal/riskgate`
(pre-trade admission and sizing) and `daemon/internal/killswitch` (the halt).
Every numeric limit below is env-overridable; the variable is named with the
rule.

---

## 1. Position-level rules

### 1.0 The triple barrier, and how it fills — **[IN FORCE — paper]**

§1.2–§1.4 are one mechanism: three exits, and the first one **reached** wins.
`daemon/internal/papertrade/barriers.go` decides; `paperbarrier.go` fills.
Toggle: `SIGNALDECK_PAPER_BARRIERS` (default **true**).

**A barrier is confirmed by a CLOSE and filled at the OPEN of a strictly later
bar. Highs and lows are never read for the trigger.** That single rule is what
makes a daily-bar stop honest, and it has two consequences that are accepted
deliberately rather than modelled away:

- **Wicks do not stop the book out.** A bar that trades through the stop and
  closes back above it is not an exit. This is a real behavioural difference
  from an intrabar stop, not an approximation of one.
- **Gaps are paid in full.** A close far through the stop fills at the next
  open, wherever that is. A daily-bar stop does not fill at its level, and
  pretending it does is the most common way a stop makes a backtest better than
  the strategy.

The ambiguity that forces most daily-bar barrier schemes into a path assumption
never arises here: the favorable level is above entry and the adverse level
below it, and a close is one price, so both can never fire on the same bar.

**Volatility is measured at entry and then fixed.** The ATR comes from bars that
had already closed when the entry filled (`store.BarsBefore` enforces `ts < t`
in SQL), and it is never re-measured. These are barriers, not a trailing stop:
re-measuring each pass would let a quiet market ratchet the stop toward a
position that never agreed to be held that tightly.

**An unmeasurable ATR is not a zero ATR.** With no usable volatility the *price*
barriers are not placed at all — a zero would collapse both onto the entry and
stop the position out on its first close. The time stop still applies, because
a horizon has nothing to do with volatility.

### 1.1 Risk per trade — **[IN FORCE — paper]**

**Rule: a position's risk is bounded by its notional × the distance to its hard
stop — at most 10% of equity × 2.0 ATR/entry.**

Now measurable, because §1.2 gives it a denominator. It is bounded rather than
targeted: sizing is driven by quarter Kelly (§1.5), not solved backwards from a
1% risk budget.

**The reconciliation (decided 2026-08-04).** This policy previously said the two
sizing philosophies "need reconciling before either is trusted," which left the
book with two answers to *how much* and no rule for which wins. They are now
assigned different jobs, and the ordering is the whole reconciliation:

> **Kelly sizes. Risk-per-trade caps. Kelly never sizes past the cap.**

- **Quarter Kelly (§1.5) is the sizer.** It answers "how much does the measured
  edge justify," from the realized fill log. It is the only thing that may
  *propose* a size.
- **Risk-per-trade is a ceiling, never a target.** It answers "what is the most
  this position may lose if the stop fills as modelled." It may only ever
  *reduce* Kelly's proposal, never raise it.

That ordering resolves the conflict without either philosophy overriding the
other, because they are no longer both sizers. Solving backwards from a fixed
1% risk budget would let a tight stop *inflate* a position beyond what the edge
supports — which is the failure the original objection was pointing at, and it
is avoided by making the risk rule one-directional.

A stop-derived cap is also the direction that stays safe when it is wrong: an
underestimated edge produces a position too small, an underestimated stop
distance produces one too large, and only the second can hurt.

**Status.** The cap in force today is the bound at the head of this section
(notional ≤ 10% of equity, so risk ≤ 10% × 2.0 ATR/entry), which `riskgate`
enforces through the position-weight limit. **An explicit 1.0%-of-equity
per-trade ceiling remains [SPEC ONLY — NOT IN FORCE]**: it needs
`riskgate.Evaluate` to read the candidate's ATR, which it does not currently
receive. What has changed is that this is now a missing *input*, not an
unresolved *philosophy* — the rule it would feed is decided, one-directional,
and cannot fight the Kelly fraction when it lands.

### 1.2 Hard stop — **[IN FORCE — paper]**

**Rule: an adverse barrier at 2.0 × the name's 20-day ATR below entry, closed on
the first daily close beyond it, filled at the next open.**

`AdverseATRMult` 2.0 (`SIGNALDECK_PAPER_BARRIER_ADVERSE_ATR`), ATR period 20
(`SIGNALDECK_PAPER_BARRIER_ATR_PERIOD`).

2.0 × ATR because a stop inside ordinary daily noise is not a risk control, it
is a fee: it converts variance into realized losses without changing the
distribution of the underlying idea. 20 bars is one trading month — long enough
that a single wild session does not set the stop, short enough to track a name
whose volatility regime has changed.

A stop that would land at or below zero is **refused**, not clamped. An
unstopped position wearing the label of a stopped one is worse than an
acknowledged unstopped position.

### 1.3 Take-profit — **[IN FORCE — paper]**

**Rule: a favorable barrier at 3.0 × the same ATR, giving 1.5 : 1 reward to
risk.** `FavorableATRMult` 3.0 (`SIGNALDECK_PAPER_BARRIER_FAVORABLE_ATR`).

1.5 : 1 rather than something wider because the underlying edge is a
**directional probability over a fixed horizon**, not a trend-following claim.
A target the horizon cannot plausibly reach is a decoration — the time stop
would collect the position long before price got there.

### 1.4 Time stop — **[IN FORCE — paper]**

**Rule: close at horizon expiry — 1 trading day for `flagship-1d`, 5 for
`flagship-1w` — if neither price barrier has been reached.**

The forecast that justified the entry is explicitly a 1-day (or 1-week) claim. A
position still open on day 30 is not held on the strength of any measured edge;
it is held because nothing told it to leave. The horizon is the honest holding
period, and the time stop is what makes the trade match the claim.

> **⚠ Consequence you must know before reading any `flagship-1d` result.**
> A 1-day forecast implies a **1-bar hold**, so on the 1d book the time stop is
> reached on the entry bar's own close, every time. The price barriers can only
> fire on that same bar, and the fill is the next open either way — so on
> `flagship-1d` they change the *recorded reason*, not the exit timing.
> They genuinely bind only on `flagship-1w`, where there are five bars to
> traverse.
>
> This also means the probability-flip exit is now **unreachable** on the 1d
> book. Every `flagship-1d` position opens at one bar's open and closes at the
> next — which is exactly what trading a 1-day forecast means, and is a
> materially different strategy from the open-ended hold this book ran before
> 2026-08-04. Proven by
> `pipeline.TestBarrier_HorizonExpiryDominatesTheOneDayBook`.

### 1.5 Max position size — **[IN FORCE — paper]**

**Rule: no position may exceed 10% of book equity** (`MaxPositionWeight` 0.10,
`SIGNALDECK_RISK_MAX_POSITION_WEIGHT`), sized by **quarter Kelly** on the
strategy's realized round-trip record (`KellyFraction` 0.25) when at least 20
closed round trips support it (`MinEdgeTrips` 20), and by the equal-slice
budget when they do not. At most 10 names (`MaxPositions` 10).

Positions below 0.5% of equity are **refused, not filled**
(`MinTicketFrac` 0.005). Every cap above trims rather than refuses, and trims
compose: a name arriving last against a nearly-full sector gets whatever dust
is left, and filling that pays two spreads to move nothing.

Quarter Kelly rather than full: full Kelly is growth-optimal only when the edge
is known exactly, and with an estimated win rate and payoff it overbets badly.
The quarter haircut is Thorp's own recommendation for real books — a modest
loss of growth for a large reduction in the variance of growth.

The sizing edge comes only from the **realized fill log**, never from the
calibrated probability. A probability is a forecast; a Kelly fraction has to be
paid for out of P&L.

### 1.6 Liquidity cap — **[IN FORCE — paper]**

**Rule: a fill may not exceed the participation cap applied to the name's
trailing average daily dollar volume** (`papertrade.MaxParticipation() × ADV`).
A symbol with no usable ADV estimate is **skipped, not filled**.

ADV is measured on bars at or **before** the fill, never after — a fill may not
know how much traded on days it has not seen. Oversized demand produces a
**partial** fill, not a pretend complete one, and impact is charged as
`coef × sigma × sqrt(notional / ADV)`.

---

## 2. Portfolio-level rules

### 2.1 Max gross exposure — **[IN FORCE — paper]**

**Rule: gross deployed notional may not exceed 100% of equity**
(`MaxGrossExposure` 1.00, `SIGNALDECK_RISK_MAX_GROSS_EXPOSURE`). No leverage.

A fully deployed book is refused at **admission** (`riskgate.Admit`) — the
answer is "no entries", not "not this one". A partially deployed book has its
next entry trimmed to the remaining headroom.

The cap arms only when gross is **measurable**: if any open position could not
be marked at the as-of clock the total is an understatement, and an understated
gross hands out headroom the book may not have. In that case the cap goes
**unarmed** and says so, rather than arming on a number wrong in the permissive
direction.

This is a floor on honesty as much as on risk. A simulated book quietly running
at 1.3× gross has been reporting the returns of a different, riskier strategy
than the one described.

### 2.2 Max net beta-adjusted exposure — **[SPEC ONLY — NOT IN FORCE]**

**Rule: net beta-adjusted exposure may not exceed 0.60 of equity.**

**Betas are not estimated anywhere in this repository.** No code could evaluate
this rule, and stating a number for it is a plan, not a control. What this means
in practice is worth saying: the book is long-only, so its net beta is
approximately gross exposure times the average beta of what it holds — near
1.0. The gross cap (§2.1) is the only thing bounding it.

0.60 as the target because a directional daily-horizon edge that requires more
than that is a market bet wearing a signal's clothes.

### 2.3 Sector cap — **[IN FORCE — paper]**

**Rule: one sector may not exceed 30% of the book** (`MaxSectorWeight` 0.30,
`SIGNALDECK_RISK_MAX_SECTOR_WEIGHT`), with a hard count cap of 10 names
(`MaxPositions` 10, `SIGNALDECK_RISK_MAX_POSITIONS`).

Only the headroom under the cap may be deployed; an exhausted sector is
refused. Ten equally-weighted names from one sector are one bet with extra
commission, and a third of the book is the point past which "diversified" stops
being true.

**Known limitation, stated rather than hidden:** `riskSector` returns `""` for
any symbol outside the static sector map, which is most of the broad universe.
An unclassified name is bounded by the position cap alone, not the sector cap.
That is deliberate — mapping every unclassified name into one bucket would
throttle the whole book at the sector cap — but it means sector concentration
is enforced only over the classified subset.

### 2.4 Crypto cap — **[SPEC ONLY — NOT IN FORCE]**

**Rule: crypto may not exceed 10% of gross exposure.**

Crypto symbols exist in the universe (`md.Crypto`), but the sector classifier
does not bucket them separately, so today they fall into the unclassified
bucket of §2.3 and are capped by position only. Implementing this means adding
a crypto bucket to `riskSector` and a distinct cap — small work, not yet done.

10% because crypto's daily volatility is several times the equity book's, so an
equal *notional* weight is a much larger *risk* weight, and the position cap
alone does not see the difference.

### 2.5 Correlation cap — **[IN FORCE — paper]**

**Rule: a candidate whose measured return correlation to the existing book is
0.80 or above is refused** (`MaxCorrToBook` 0.80,
`SIGNALDECK_RISK_MAX_CORR_TO_BOOK`).

Correlation is measured over a 64-day daily-return window and reported only with
at least 20 overlapping days; below that it is an artifact of a handful of
points and the cap goes **unarmed** rather than firing on noise.

This is an **admission** decision, never a trim. A name that moves with the book
is not a smaller version of a good idea, it is more of the idea already owned —
trimming it just buys the same exposure in a less honest package.

0.80 rather than something tighter because equities are correlated by
construction; a market factor runs through all of them, and a cap that fired on
ordinary market beta would refuse the entire universe. It exists to catch the
pathological case — a second share class, a sector twin, an ETF and its largest
holding — not to enforce a statistical independence this asset class does not
offer at any price.

The position, sector and count caps all measure concentration by **label**. This
one measures it by **behaviour**, which is the only version that survives a
regime where the labels stop meaning anything.

---

## 3. Drawdown ladder

Two rungs exist; three do not.

| Trigger | Action | Status |
|---|---|---|
| −5% peak-to-trough | New position size **halved** | **[SPEC ONLY — NOT IN FORCE]** — `riskgate` has no size-attenuation rung; sizing is quarter-Kelly or equal-slice, and drawdown does not scale it |
| −8% peak-to-trough | **Suspend new entries** | **[SPEC ONLY — NOT IN FORCE]** — the only suspend rung in force is at −20% |
| −12% peak-to-trough | Flatten and halt | **[SPEC ONLY — NOT IN FORCE at this level]** — see the level note below; the rung now EXISTS, at 25% |
| −20% peak-to-trough | Suspend new entries | **[IN FORCE — paper]** — `MaxDrawdown` 0.20, `SIGNALDECK_RISK_MAX_DRAWDOWN` |
| −25% peak-to-trough | **Flatten: close every open position** | **[IN FORCE — paper]** — `riskgate.ShouldFlatten`, `DefaultFlattenDrawdown` 0.25, `SIGNALDECK_RISK_FLATTEN_DRAWDOWN` |
| −5% in one marking period | Suspend new entries for the session | **[IN FORCE — paper]** — `MaxDailyLoss` 0.05, `SIGNALDECK_RISK_MAX_DAILY_LOSS` |

**The terminal rung now exists (2026-08-04).** This document previously said
"nothing in this repository can flatten a book", which made the ladder's last
and most important rung a sentence rather than a control. `riskgate.ShouldFlatten`
decides it and `pipeline.planExit` executes it, closing every open position at
the next available open. Wired and proved end to end by
`TestFlatten_ClosesAPositionNothingElseWouldClose`, which disables barriers and
holds the signal bullish so that nothing *except* the rung could have closed the
position.

**On the level, because it is not the −12% the ladder above asks for.** The
enforced suspend rung is −20%. A flatten at −12% would liquidate the book while
it was still opening new positions — not a ladder but a contradiction. A
liquidation rung must sit at or beyond the suspend rung, so it is set at 25%:
suspend at 20, flatten at 25. `FlattenLimit` floors the configured value at
`MaxDrawdown` so a misconfiguration cannot invert the order. Moving to the
tighter specified pair (−8 suspend / −12 flatten) means moving **both** rungs
together; that pair stays SPEC ONLY.

**It fails the opposite way to the halt, deliberately.** An unknown drawdown
does NOT flatten. Halting entries on bad data costs opportunity and reverses
instantly; liquidating on bad data realises losses, pays spread and impact, and
cannot be undone by discovering the data was wrong. The safe default for a brake
is ON and for a liquidation is OFF. `Book.DrawdownKnown` is false only when
there is no equity curve yet — no peak to be below — so this is a book that
*cannot* be in drawdown, not one whose drawdown was lost.

Read the rest of the table honestly: the graduated de-risking this policy wants
from −5% is still not there. Two of five rungs are enforced, plus the session
brake. **The remaining gap is real and this document does not minimise it.**

Four properties of the enforced rungs are worth stating:

- **Drawdown is current, not historical.** A book that fell 30% and recovered is
  not in a 30% drawdown, and halting it for a healed wound would be a bug.
- **Unmeasurable is not zero.** A book with no equity history leaves the breaker
  **unarmed**, and the decision says so rather than reading as a clean pass.
  "The breaker could not measure this" and "the breaker measured zero" must
  never collapse into the same allow.
- **Exits are never gated.** A tripped rung stops entries and nothing else.
- **The numbers.** 20% is the conventional institutional soft stop: far enough
  out that ordinary variance does not trip it, close enough that the book can
  still recover arithmetically. 5% in a session is evidence that something is
  wrong with the day, not with one name.

---

## 4. Kill switch — **[IN FORCE — paper]**

`daemon/internal/killswitch`. File-based, fail-closed, read before every order,
living in **this** repository and halting **this** repository's only executor.

### 4.1 How to trip it

```bash
echo "reason for the halt" > ops/HALT
```

Any file at the path halts. Its contents become the audited reason and are
snapshotted into the decision ledger. Clearing it (`rm ops/HALT`) resumes on the
next pass — no restart. A halt that cannot be lifted without a restart is an
outage, not a control.

The path is `ops/HALT` by default, overridable with `SIGNALDECK_KILL_SWITCH`.
`SIGNALDECK_HALT=1` halts without a file, for environments with no writable path
of their own.

### 4.2 Truth table

| Condition | Result | Why |
|---|---|---|
| File definitively absent | **RUNNING** | The only outcome that permits trading, and only because the OS answered definitively |
| File present (any contents, including empty) | **HALTED** | Contents become the reason; the operator's explanation is optional, the halt is not |
| A directory at the halt path | **HALTED** | Someone put something there; second-guessing what they meant is not the switch's job |
| `stat` fails for any other reason (permissions, unmounted volume, malformed path) | **HALTED** | Absence of evidence is not evidence of absence |
| `SIGNALDECK_HALT` = `1`/`true`/`yes`/`on` | **HALTED** | — |
| `SIGNALDECK_HALT` set to an unrecognised value | **HALTED** | An unreadable halt instruction is still a halt instruction |
| `SIGNALDECK_HALT=0` **with a halt file present** | **HALTED** | The env override may only ever halt. It must not talk the system past a file on disk asking it to stop |

The fail-closed row is the design. A permission error is not evidence of safety,
and treating an unreadable switch as "no halt requested" makes the control
vanish at exactly the moment the machine is sick enough to be worth halting.

### 4.3 Why a file

The halt must be trippable by a human with no running process to talk to, by a
cron job, by a monitor, and by an operator over SSH with the daemon wedged.
`touch ops/HALT` works in all four. An HTTP endpoint or an in-memory flag works
in none, because the situations that justify a halt are the situations where the
process is the thing you do not trust.

### 4.4 Exits are never gated

The switch is checked before every order, and it refuses **entries**. A
risk-reducing **exit** still executes while halted, and the halt is recorded
alongside it.

This is deliberate. A halt that traps the book inside the position it was
tripped by is a larger risk than the one it controls. `riskgate` and `ev` each
state the same doctrine first in their own decision functions so it cannot be
reordered behind a check that might refuse.

Note the consequence honestly: this switch **stops new risk, it does not
liquidate**. That remains true and is deliberate — flattening is a different
and more dangerous operation than halting, and routing it through the same
control would make one operator action mean two things.

Liquidation is a SEPARATE rung and it now exists: §3's flatten fires from
`riskgate.ShouldFlatten` on drawdown, not from this file. The two are
independent on purpose — tripping the halt never liquidates, and a flatten
does not require the halt to have been tripped.

### 4.5 How it is tested

- `killswitch_test.go` — the full truth table, the fail-closed branch via an
  unreadable path, and a mid-run halt simulation showing the switch takes effect
  on the *next* order rather than the next restart.
- `pipeline.TestKillSwitch_HaltsEntriesAndLedgersTheRefusal` — end-to-end halt
  simulation against the real worker and a real store, using a seeded candidate
  that provably fills when unhalted. Asserts no position, no trade, a ledgered
  refusal with reason `kill-switch-halted`, and resumption after the file is
  removed.
- `pipeline.TestKillSwitch_ExitsStillExecuteWhileHalted` — an open position is
  exited while the platform is halted.
- `pipeline.TestKillSwitch_UnparseableHaltInstructionHaltsTheWorker` — fail
  closed at the pipeline boundary, not only inside the package.

---

## 5. Status summary

| Rule | Limit | Enforced by | Status |
|---|---|---|---|
| Risk per trade (bounded) | ≤ 10% notional × 2.0 ATR | `papertrade` barriers + `riskgate` | **IN FORCE — paper** |
| Risk per trade (1.0% target sizing) | 1.0% of equity | — | SPEC ONLY |
| Hard stop | 2.0 × ATR(20), on close, next-open fill | `papertrade.FindBarrierExit` | **IN FORCE — paper** |
| Take-profit | 3.0 × ATR(20), on close, next-open fill | `papertrade.FindBarrierExit` | **IN FORCE — paper** |
| Time stop | horizon expiry (1 bar / 5 bars) | `papertrade.FindBarrierExit` | **IN FORCE — paper** |
| Max position size | 10% of equity, quarter-Kelly sized | `riskgate.Evaluate` | **IN FORCE — paper** |
| Minimum ticket | 0.5% of equity, else refuse | `riskgate.Evaluate` | **IN FORCE — paper** |
| Liquidity cap | participation × trailing ADV | `papertrade` execution model | **IN FORCE — paper** |
| Max gross exposure | 100% of equity | `riskgate.Admit` + `Evaluate` | **IN FORCE — paper** |
| Max net beta exposure | 0.60 of equity | — (no beta estimated) | SPEC ONLY |
| Sector cap | 30% of equity, 10 names | `riskgate.Evaluate` | **IN FORCE — paper** (classified names only) |
| Crypto cap | 10% of gross | — | SPEC ONLY |
| Correlation cap | 0.80 to book | `riskgate.Evaluate` | **IN FORCE — paper** |
| Drawdown −5% → size halved | — | — | SPEC ONLY |
| Drawdown −8% → suspend | — | — | SPEC ONLY |
| Drawdown −12% → flatten (at that level) | — | — | SPEC ONLY — see §3 level note |
| Drawdown −25% → **flatten, close every position** | 0.25 | `riskgate.ShouldFlatten` + `pipeline.planExit` | **IN FORCE — paper** |
| Drawdown −20% → suspend entries | 0.20 | `riskgate.Admit` | **IN FORCE — paper** |
| Daily loss → suspend session | 0.05 | `riskgate.Admit` | **IN FORCE — paper** |
| Kill switch | `ops/HALT`, fail-closed | `killswitch.Check` | **IN FORCE — paper** |
| Every refusal ledgered | — | `ev_decisions` | **IN FORCE — paper** |

Fifteen rules in force, six specified only.

---

## 6. What would have to be true before this governs real money

Nothing in this document is a live-trading control. The following are
preconditions, not aspirations:

1. **The six remaining SPEC ONLY rules are implemented and tested** — the
   1.0%-risk sizing target, net beta, the crypto cap, and the −5% / −8% / −12%
   drawdown rungs.
2. ~~The barrier-fill hazard is resolved~~ **— closed 2026-08-04.** Barriers
   confirm on a close and fill at the next open; highs and lows are never read
   for the trigger, so no path assumption is made and none needs disclosing.
   What remains is the honest cost of that choice: wicks do not stop the book
   out, and gaps are paid in full. Both are asserted by tests
   (`TestBarrier_WickThroughTheStopDoesNotExitTheBook`,
   `TestBarrier_CloseThroughTheStopExitsTheBook`).
3. **An execution layer exists at all** — order placement, acknowledgement,
   reconciliation, and a real broker-side position to compare the book against.
   None of it exists. The kill switch currently halts a simulation.
4. ~~**The kill switch gains a flatten path**, or §3's −12% rung is formally
   withdrawn.~~ **DONE 2026-08-04** — `riskgate.ShouldFlatten` decides the
   terminal rung and `pipeline.planExit` executes it, closing every open
   position at the next available open. It sits at 25%, beyond the 20%
   suspend rung, because a liquidation that fires while the book is still
   entering is a contradiction rather than a ladder; the tighter −8/−12
   pair stays SPEC ONLY and would have to move both rungs together.
   The remaining live-money work is that this flattens a SIMULATION.
5. **Slippage is measured against real fills**, not modelled. The current cost
   model is a defensible estimate that has never been checked against a fill
   that actually happened.
6. **Live forward evidence accumulates** and the survivorship and point-in-time
   defects recorded in `INSTITUTIONAL_GAP.md` and
   `proofs/P6_GOVERNANCE_CLEANUP.md` are closed. Position sizing derived from a
   backtest with an open survivorship gap is sized on a number that is too good.

Until all six hold, the honest description of this policy is: **a
well-instrumented set of controls on a simulation, plus a written plan for the
controls a real book would additionally need.**
