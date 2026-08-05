# P4D — Barrier exits with next-open fills: proof artifact

**Date:** 2026-08-04 · **Phase:** P4D
**Closes:** `RISK_POLICY.md` §1.2–§1.4 and `EXECUTION_SPEC.md` §3.2, previously
SPEC ONLY.

## What was asked

Implement the triple-barrier exits — hard stop, take-profit, time stop — using
next-open fills, i.e. the honest option of the three named in
`EXECUTION_SPEC.md` §3.3.

## The rule the implementation is built on

> **A barrier is confirmed by a CLOSE and filled at the OPEN of a strictly later
> bar. Highs and lows are never read for the trigger.**

A daily bar does not record whether the low preceded the high. A position with a
stop below and a target above may have reached either first, and the bar cannot
say which. Reading highs and lows therefore requires a path assumption, and the
flattering assumption manufactures profit that never existed.

Consulting only the close removes the ambiguity rather than assuming it away:
the favorable level is above entry, the adverse level below, and a close is one
price — so both can never fire on the same bar. **No path assumption is made, so
none needs disclosing.**

## What was built

| File | What |
|---|---|
| `daemon/internal/papertrade/barriers.go` | the arithmetic — `ATR`, `Levels`, `FindBarrierExit`. Pure: no I/O, no clock, no store |
| `daemon/internal/pipeline/paperbarrier.go` | `planExit` / `findBarrier` — measures entry volatility, scans the holding window, resolves barrier vs. probability flip |
| `daemon/internal/store/store.go` | `BarsBefore` — last *n* bars **strictly before** t, ascending |
| `daemon/internal/pipeline/paper.go` | exit path restructured: every open position is evaluated, not only those with a fresh signal |
| `daemon/internal/ev/ev.go` | `BarrierReason` — `barrier-adverse` / `-favorable` / `-expiry` |
| `daemon/internal/pipeline/paperev.go` | `ledgerBarrierExit` — snapshots the barrier **and** the fill timestamp |

Envelope, all env-overridable: adverse 2.0 × ATR, favorable 3.0 × ATR, ATR
period 20, horizon 1 bar (`1d`) / 5 bars (`1w`). Master toggle
`SIGNALDECK_PAPER_BARRIERS`, default **true**.

## Four properties, each with a test that fails if it breaks

**1. The fill is strictly later than the trigger.**
`TestBarrier_FillsAtAStrictlyLaterOpen` — day 3's close confirms; the fill lands
on day 4 at its **open price exactly** (88.88, a value nothing else in the
fixture uses), and the ledger records `triggerTs` 259200 < `fillTs` 345600.

**2. Wicks do not exit.** `TestBarrier_WickThroughTheStopDoesNotExitTheBook` — a
bar spiking to 80 through a ~96 stop and closing at 100 leaves the position
**open** and ledgers nothing. `TestBarrier_CloseThroughTheStopExitsTheBook` is
the same fixture with one bar changed to *close* at 91, and it exits — so the
difference is unambiguously the close and not the wick.

**3. Gaps are paid in full.** The same test asserts the exit price is day 5's
open (90.5), **not** the stop level (~96). A daily-bar stop does not fill at its
level, and this book does not pretend it does.

**4. Barriers do not need the model's permission.**
`TestBarrier_FiresWithoutAFreshPrediction` — a weekly position with **no new
prediction at all** still closes at its horizon. A stop that only fires when the
model has an opinion is not a stop.

Plus, in `papertrade`: ATR gap handling, ATR period windowing, unmeasurable-ATR
refusal, non-positive-stop refusal, entry-bar-close exits, price-outranks-expiry
on a shared bar, first-barrier-wins, and the toggle.

## No-lookahead, enforced in three places

1. **ATR is measured from bars that had already closed at entry.**
   `store.BarsBefore` enforces `ts < t` **in SQL**, so the rule cannot be lost by
   a caller. The entry fills at the open of the bar at `OpenedTs`, whose high,
   low and close are unknown at that instant.
2. **Volatility is fixed for the life of the position.** Barriers, not a
   trailing stop. Re-measuring each pass would let a quiet market ratchet the
   stop toward a position that never agreed to be held that tightly.
3. **The ledger stores both timestamps.** `barrier.triggerTs` and `fillTs` are
   separate fields, so an auditor can assert `fillTs > triggerTs` over every row
   — the guarantee as a query, not a promise.

## ⚠ The material consequence: `flagship-1d` is now a one-bar strategy

A 1-day forecast implies a **1-bar hold**, so on `flagship-1d` the time stop is
reached on the entry bar's own close, every time. Therefore:

- Every `flagship-1d` position opens at one bar's open and closes at the next.
- The **price barriers change the recorded reason, not the exit timing**, on
  that book. They genuinely bind only on `flagship-1w`, which has five bars to
  traverse.
- **The probability-flip exit is now unreachable on the 1d book.**

This is what trading a 1-day forecast means, and it is a materially different
strategy from the open-ended hold this book ran before 2026-08-04. It is stated
in `RISK_POLICY.md` §1.4 behind a warning marker and proven by
`TestBarrier_HorizonExpiryDominatesTheOneDayBook`.

**Track-record implication, flagged not buried:** the paper book's first live
structural evidence is dated 2026-08-07. A record spanning this change is a
record of **two different strategies**, and any accuracy or P&L figure that
crosses 2026-08-04 must say so or be split at it. `SIGNALDECK_PAPER_BARRIERS=false`
restores the previous behaviour exactly
(`TestBarrier_DisabledRestoresTheFlipOnlyBook`) if the cleaner option is to keep
the pre-existing record intact and start a second one.

## Two existing tests changed, and why

Neither was wrong; both asserted behaviour this phase deliberately replaced.

- `TestEVGate_LedgersBuyAndExit` now sets `SIGNALDECK_PAPER_BARRIERS=false`. It
  is a Decision Engine test covering the probability-**flip** exit, and with
  barriers on that path is unreachable on a 1d book — the test would have
  silently stopped testing what it names.
- `TestKillSwitch_ExitsStillExecuteWhileHalted` now accepts a `barrier-*` exit
  reason. This makes it a **stronger** case than before: a risk control fired
  while the platform was halted, and the halt did not stop it.

## Verification

```bash
cd daemon && go build ./... && go vet ./... && go test ./... -count=1
```

Full suite: **exit code 0.**

```bash
cd daemon && go test ./internal/pipeline/ -run Barrier -count=1 -v
```

```
--- PASS: TestBarrier_FillsAtAStrictlyLaterOpen
--- PASS: TestBarrier_HorizonExpiryDominatesTheOneDayBook
--- PASS: TestBarrier_WickThroughTheStopDoesNotExitTheBook
--- PASS: TestBarrier_CloseThroughTheStopExitsTheBook
--- PASS: TestBarrier_CloseThroughTheTargetExitsTheBook
--- PASS: TestBarrier_FiresWithoutAFreshPrediction
--- PASS: TestBarrier_DisabledRestoresTheFlipOnlyBook
ok  github.com/nyaungnicholas-wq/signaldeck/internal/pipeline
```

```
OK RISK_POLICY.md (3705 words)
OK EXECUTION_SPEC.md (3086 words)
```

## What this phase did NOT do

- **The 1.0%-of-equity risk *target* is still SPEC ONLY.** Risk per trade is now
  *bounded* (notional × stop distance), but sizing is still quarter Kelly.
  Solving position size backwards from a risk budget would override the Kelly
  fraction, and those two sizing philosophies need reconciling before either is
  trusted.
- **No trailing stop.** Barriers are fixed at entry, on purpose.
- **No intraday bars.** The next-open rule makes them unnecessary for honesty,
  not unnecessary for precision — an intraday feed would let the stop fill
  nearer its level instead of at the following open.
- **Still a simulation.** There is no broker order path in this repository.
