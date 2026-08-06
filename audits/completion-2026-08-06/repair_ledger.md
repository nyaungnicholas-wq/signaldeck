# Repair Ledger — SignalDeck Completion 2026-08-06

All repairs are source-only and reversible with `git checkout -- <file>`. No database write,
no service restart, no git state change, no deletion was performed.

Baseline before any repair: `go build ./...` exit 0; `go test -short ./...` exit 0,
119 packages ok, 0 FAIL, 10 no-test-files.

---

## R2 — `TradingDayAtET` could never match an evening hour · **FIXED · VERIFIED**

**Severity:** Critical · **Root cause:** a missing predicate, not a wrong constant.

`marketcal.OpenForBars` answers "should the feed be producing bars *right now*" — true only for
`09:45 ≤ mins ≤ 16:00` ET. `workers.TradingDayAtET` needed a different question: "did this *date*
trade". No such predicate existed, so it reused `OpenForBars`. Both real callers pass evening times
(`shorts.go:79` → 18:30 ET = 1110 min; `shortinterest.go:69` → 18:45), which exceed 960 on every
date. The 10-iteration loop retried the *same clock time* each day, matched nothing, and fell
through ~10 days out on every reschedule — permanently.

**Observed consequence (live DB, read-only):** `finra-shorts` and `finra-shortint` last ran
2026-08-02 23:56 (20 runs each, then silence). `MAX(short_volume.day)` = 2026-07-31.
`MAX(short_interest.settlement_date)` = 2026-07-15 — 22 days stale.

**Change**
- `daemon/internal/marketcal/marketcal.go` — added `IsTradingDay(t)`: weekend + full-holiday check,
  deliberately independent of time of day.
- `daemon/internal/workers/schedule.go` — `TradingDayAtET` now tests `IsTradingDay`, with the trap
  documented inline.

**Verification**
```
go build ./...                                                    exit 0
go test ./internal/marketcal/... ./internal/workers/...            ok (0.280s / 3.220s)
go test ./internal/workers/ -run TradingDayAtET -v                 PASS (2 new subtests)
```
New test `TestTradingDayAtETLandsOnTheNextTradingDay` pins the exact fire instant
(midweek-before-slot → same day 18:30; Friday-past-slot → Monday 18:30) and asserts
`OpenForBars(got) == false`, so the two predicates cannot be re-conflated.

**What it proves:** the scheduler now returns the next trading day for evening slots, and the
regression is pinned by date rather than by "not a weekend".
**What it does NOT prove:** that the FINRA feeds ingest successfully once scheduled — that requires
the daemon to be running this code (see BLOCKED-1) and a live FINRA fetch.

**Pre-existing test that did not catch this:** `TestTradingDayAtETSkipsWeekend` asserted only that
the result was not Sat/Sun. A 10-day fall-through from Friday lands on a Monday, so it passed for
four days while the function was broken. Left in place; the new test carries the real assertion.

---

## R3 — the watchdog took its alarm threshold from the schedule it polices · **FIXED · VERIFIED**

**Severity:** Critical (self-concealing) · **Root cause:** an unbounded derived value.

`stalenessInterval` (`run.go`) derives a worker's cadence by measuring the gap between two
`NextFire` calls; `health.StaleWorkers` then flags at `3 × interval`. `health.go:58` floors that
with `minThreshold` but nothing caps it. The broken FINRA schedule reported a ~10-day cadence, so
the two dead workers were granted a ~30-day silence budget. The worse a schedule breaks, the longer
the watchdog waits before saying so.

R2 fixes this instance; R3 fixes the class.

**Change** — `daemon/cmd/signaldeckd/run.go`: added `maxDerivedCadence = 8 * 24h` (clears the
longest real schedule, weekly, per `briefing/weekly.go` and `pipeline/cot.go`) and clamped the
derived gap to it. The clamp also `slog.Warn`s with the worker name and the implausible value,
because the number is itself the bug signal and silence is how this hid.

**Verification**
```
go build ./... && go vet ./cmd/signaldeckd/                        exit 0
go test ./cmd/signaldeckd/ -run TestStalenessInterval -v           PASS (7/7)
  WARN implausible derived worker cadence — clamping  derived=240h0m0s clampedTo=192h0m0s
```
Two new cases: a 10-day pathological schedule clamps to 192h; a legitimate 7-day weekly cadence
passes through unmodified (guarding against re-creating the weekly false positive this function was
originally written to fix). All 5 pre-existing cases still pass.

**What it proves:** no schedule, however broken, can now buy more than a 24-day threshold, and the
anomaly is logged.
**What it does NOT prove:** that 8 days is the right ceiling for schedules added later — it is
derived from the fleet's current longest cadence and will need revisiting if a monthly job appears.

---

## R5 — positions in de-activated symbols could be entered but never exited · **FIXED · VERIFIED**

**Severity:** Critical · **Root cause:** the exit path was scoped to the entry path's population.

`PaperTrader.Run` fetched `ListSymbols(ctx, true)` (active only) and `buildStep` evaluated **every**
exit inside `for _, s := range syms` — stop, take-profit, horizon expiry, probability flip,
kill-switch flatten and the −25% liquidation rung alike. A symbol pruned from the universe while
the book still held it was never visited, so no risk control could reach the position. A position
the system can open but cannot close defeats every control at once.

**Change** — `daemon/internal/pipeline/paper.go`
- `Run`: after the as-of clock is computed **from the active universe only**, union in any symbol
  carrying an open position across all strategies, re-admitted as EXIT-ONLY (`Active` stays false).
  Reuses the existing `store.PaperPositions`; no new query was written.
- `buildStep`: the entry path now `continue`s on `!s.Active`, so re-admission can never turn into
  an entry-path bug (buying a name the universe already dropped).

**Verification**
```
go test ./internal/pipeline/ -run "ExitsPositionInDeactivatedSymbol|DeactivatedSymbolNeverEntered" -v
  --- PASS: TestPaperTrader_ExitsPositionInDeactivatedSymbol (0.11s)
  --- PASS: TestPaperTrader_DeactivatedSymbolNeverEntered   (0.09s)
go test ./internal/pipeline/                                       ok 21.494s
```

**Negative control (the important one).** The repair was temporarily disabled
(`if false && len(held) > 0`) and the test re-run:
```
--- FAIL: TestPaperTrader_ExitsPositionInDeactivatedSymbol (0.09s)
    paper_test.go:158: position in a DEACTIVATED symbol was never exited...
FAIL
```
The fix was then restored and the suite re-run green (`grep "if false"` → no matches). This is the
evidence that distinguishes a real repair from a vacuous test — the same check that
`TestTradingDayAtETSkipsWeekend` lacked.

**What it proves:** a held position survives its symbol's removal from the universe and is closed by
the normal exit path, with a ledgered SELL; and an inactive symbol is never newly entered.
**What it does NOT prove:** that no position is *currently* stranded in the live book. That is a
data question, not a code question — see BLOCKED-2.

---

## Deferred — require explicit approval

| ID | Action | Why deferred |
|---|---|---|
| R1 | Rebuild `bin/signaldeckd.exe` and restart the daemon on HEAD | Live-state change; deploys 22 commits at once. **This is the single highest-value action available** — see BLOCKED-1. |
| R6 | Quarantine the contaminated 1d label population and the paper equity curve | Database write; invalidates existing grades. Not exactly reconstructable → quarantine, never backfill. |
| R-WEB | Give the web app a Windows Scheduled Task, or retire the surface | System/external change. |
