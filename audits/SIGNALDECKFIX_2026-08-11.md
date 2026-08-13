# SignalDeck audit/repair ledger — 2026-08-11

Repo: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`
Branch: `hmm-regime-and-pbo` @ `1b8b876`
Pre-existing uncommitted work at start (PRESERVED, not authored by this pass):
10 modified files — the daily regeneration of `README.md`, `CASE_STUDY.md`,
`HOW_PREDICTORS_WORK.md`, `INSTITUTIONAL_GAP.md`, `PREDICTION_PROCESS.md`,
`SHIP_READINESS.md`, `STRATEGY_DECK.md`, `partials/deck_facts.md`,
`partials/live_accuracy.md`, `ops/revalidation-status.json`.

## Baseline (measured, not assumed)

| Check | Command | Result |
|---|---|---|
| Go build | `cd daemon && go build ./...` | PASS |
| Go vet | `cd daemon && go vet ./...` | PASS |
| Go tests | `cd daemon && go test ./...` | PASS |
| Python tests | `.venv/Scripts/python.exe -m pytest tools/ -q` | 334 passed, 4 skipped |
| Web typecheck | `cd web && npx tsc --noEmit` | PASS |
| Web lint | `cd web && npx eslint` | PASS |
| docs_gate / schema_contract / deployment_drift / research_liveness / structural_liveness / check_revalidation / check_spa_ledger / verify_dod | each `python tools/<t>.py` | exit 0 |
| prereg_readiness | `python tools/prereg_readiness.py` | **exit 1** |

**Baseline trap recorded:** `go build ./... \| tail -30 && echo $?` reports the
exit code of `tail`, not `go`. Both the Go and Python baselines initially read
GREEN while actually failing (wrong module dir; `pytest` absent from system
python). The Go module root is `daemon/`, not the repo root; Python must be run
with `.venv/Scripts/python.exe`. Use `${PIPESTATUS[0]}`.

## Findings

### L1 — forecast-monitor reports RAW MODEL COLLAPSE, and it is a false positive
Component: `daemon/internal/store/forecastmon.go:160` (`ForecastDayStatsRaw`)
Symptom: `worker_runs` holds 15/15 `status='error'` for `forecast-monitor`,
each saying "RAW MODEL COLLAPSE ... 21 distinct raw scores across 329 symbols
(ratio 0.064, floor 0.15) ... the ensemble itself has stopped discriminating".
Evidence (measured against live DB, 1d horizon, dedup latest row per symbol/day):

```
day          all  d_all ratio_all | abstain  real  d_real ratio_real
2026-08-05   329   179  0.544     |      4   325    179  0.551
2026-08-06   329   152  0.462     |     24   305    152  0.498
2026-08-07   329    32  0.097     |    298    31     31  1.000
2026-08-09   329    19  0.058     |    310    19     18  0.947
2026-08-10   329    21  0.064     |    308    21     20  0.952
```

Root cause: the query counts ABSTENTION rows in a discrimination statistic.
A withheld prediction is persisted with `n_used=0`, `raw_prob=0.5` exactly
(verified: 4442 rows `n_used=0 AND raw_prob=0.5`, **0** rows `n_used=0 AND
raw_prob<>0.5`). Since 2026-08-06 (`906310c` "Require measured legs, and keep
recording inputs on withheld rows") abstentions rose 7/day -> 308/day, so ~300
identical 0.5s dominate `COUNT(DISTINCT ROUND(raw_prob,3))`. **The more honestly
the ensemble abstains, the more "collapsed" it is measured to be.**
Among rows that actually carry a forecast, discrimination is 0.95-1.00 — the
opposite of collapse. The stated diagnosis is false.
Downstream: the monitor's real signal is destroyed, and the genuine event
(coverage 322/329 -> 20/329 on 2026-08-07) is described as the wrong failure.
Severity: HIGH. Status: **FIXED — see R1**

### L2 — the genuine event (coverage starvation) is monitored by nothing
Component: `daemon/internal/forecastmon/forecastmon.go`
What actually happened on 2026-08-07: symbols receiving a real forecast fell
from ~322/329 to ~20/329; admitted-leg counts (`n_used`) collapsed to 0 or 1.
No check measures forecast COVERAGE, so once L1 is fixed the distinct-ratio test
goes quiet (real symbol count 19-21 falls under `MinSymbolsForCollapse=30`) and
nothing at all would report the starvation.
Severity: HIGH (fixing L1 alone would have silenced the only thing noticing).
Status: **FIXED — see R2**

### L3 — `EarliestVerdictAt` serves a date 191 days in the past
Component: `daemon/internal/store/gradeable.go:122`, served over MCP at
`daemon/internal/mcp/storesource.go:139`
Root cause: `SELECT horizon_days, MIN(day) ... GROUP BY kind`. `regime_outcomes.day`
is **not** the call day — it is `md.SettleDay`, the UTC day of the last 1d bar at
or before the call (`store/regimeoutcomes.go:400-412`). For a stale/delisted
symbol it trails the call by up to 393 days, so `MIN(day)` reliably selects the
single stalest symbol in the table.
Evidence (measured): every structural call in `regime_outcomes` was made on or
after **2026-07-18**, yet `MIN(day)` = **2025-07-15** (lag 373-393d; e.g. row
id=12256 symbol 42, call 2026-07-23, `day` 2025-07-15).
Replicating the function against the live DB yields **2026-01-31 — 191 days in
the past**; the truthful figure is **2027-02-13**.
This is a recurrence of the exact class the function's own header says it exists
to fix ("Serving this date as gradeable is what made 2026-08-07 look reachable").
Severity: HIGH. Status: **FIXED — see R3**

### L4 — `prereg_readiness.py` prints a fabricated "First call date"
Component: `tools/prereg_readiness.py:81`, rendered at `:151`
Same `MIN(day)` root cause as L3. Reports "trend21 First call date 2025-07-15"
for a kind whose earliest call is 2026-07-18, and derives EARLIEST RESOLUTION /
FIRST POSSIBLE VERDICT / ON TRACK from it.
Severity: MEDIUM (advisory tool; exit 1 verdict happens to remain correct).
Status: **FIXED — see R4**

### L5 — `structural_liveness.py` will pin to STALLED on a healthy resolver
Component: `tools/structural_liveness.py:95`
The overdue query omits `AND ro.superseded_by IS NULL`, which the Go grader DOES
apply (`store/regimeoutcomes.go:503`). Measured: superseded-and-unresolved is
**32.4%** of rows fleet-wide (trend21 2933/9055, vol21 2953/9118, trend63
2933/9055, liquidity21 2920/8989). The grader will never touch them, so from
the first due date they accumulate as phantom overdue at ratio ~0.32 against
`STALL_RATIO = 0.20` -> permanent STALLED verdict on a correctly working
resolver. Currently latent (0 rows due until 2026-08-17).
Severity: MEDIUM (latent, fires 2026-08-19). Status: **FIXED — see R5**

### L6 — neither fleet-health surface reports a worker that fails every run
Components: `daemon/internal/health/health.go:51` (`StaleWorkers` ->
`data/health.json`) and `daemon/internal/api/fleethealth.go:229` (`systemHealth`
-> `/api/health`)
`forecast-monitor` has **never once succeeded** (15/15 `error`) yet appears in
neither surface; `data/health.json` reads
`staleWorkers:["expectancy-trainer","gbm-trainer"]`.
Two independent reasons, both measured:
- `api/fleethealth.go:229` judges on `ts[0]` = newest run START **regardless of
  status**. A worker that runs punctually and fails every time is healthy by
  construction.
- `health.StaleWorkers` judges last SUCCESS but substitutes daemon boot time
  when a worker has never succeeded, and thresholds at `3 x Interval`.
  forecast-monitor's interval is 24h -> 72h threshold. Measured daemon uptime
  between the 20 recorded boots: min 0.07h, mean 9.67h, and **18 of 19 uptimes
  are shorter than 72h**. The grace clock resets before the threshold can ever
  be reached.
Severity: HIGH — this is the mechanism that hid L1/L2 for four days.
Status: **FIXED — see R6.** (An earlier draft of this ledger said the change was
blocked because the files were contended by other sessions. That was an
unsupported claim and it was wrong: `git status` shows
`internal/api/fleethealth.go`, `internal/health/health.go` and
`internal/fleetmon/fleetmon.go` all clean. Recorded because a fabricated reason
not to fix something is the same class of defect as everything else in this
file.)

### L7 — `ops/market-open-guard.sh` rejected every scheduled fire, and exited 0
Component: `ops/market-open-guard.sh:11`
`export TZ=America/Los_Angeles` in a shell with **no tz database**. The Git Bash
the scheduled tasks run under (`C:\Program Files\Git\bin\bash.exe`) has no
`/usr/share/zoneinfo`, and an unresolvable `TZ` does not error — it silently
falls back to UTC. The 06:20-13:10 PT window was therefore evaluated in UTC.
Measured at the real trigger instant:

```
TZ=America/Los_Angeles  -> hm=1320  -> `hm >= 1310` -> exit 0, NO COLLECTION
TZ=PST8PDT,M3.2.0/2,... -> hm=0620  -> collect
```

The guard whose entire job is the window check was rejecting every weekday fire
while Task Scheduler recorded `0x00000000`. The window it *did* accept was
23:20-06:10 PT. Severity: HIGH. Status: **FIXED — see R7**

### L8 — `/api/accuracy` could silently un-retire a refuted model
Component: `daemon/internal/api/accuracy.go:175`
`claims, _ := d.St.EvidenceClaims(ctx, "", "")` dropped the error. `claims` is
the ONLY input that can set `retired=true` from `SourceEvidence`
(`publication/verdict.go:155-164`), so a transient DB failure made a model a
historical claim had already refuted publish as live, HTTP 200, with nothing in
the payload saying the read failed. Twelve lines below, the sibling
`RetirementHistory` refuses on exactly this ("An unreadable history must not
read as 'never retired'"). Severity: HIGH. Status: **FIXED — see R8**

### L9 — `/lab/backtest` hero tiles rendered every percentage 100x too small
Component: `web/src/app/lab/backtest/page.tsx:28-57`
The daemon emits fractions (`backtest.go:92-98`); the tiles appended `%` to the
raw value. A +37% backtest read **`0.37%`**, a 62% win rate read **`0.62%`**,
and drawdown lost its sign. `<BacktestResults>` renders the identical fields
correctly **sixty lines below on the same page** — the exact shape the
2026-08-09 audit found on `/lab/portfolio`. The Win Rate tile also ignored
`WinRateMeaningful`, printing a rate from a single closed trade.
Severity: CRITICAL. Status: **FIXED — see R9**

### L10 — a failed fetch published "we have never been wrong"
Component: `web/src/app/lab/track-record/page.tsx:62-66, 861-865`
`regimePostmortems().catch(() => undefined)` left `pms` null, `rows` empty, and
the panel titled **WHEN WE WERE WRONG** rendered "no high-conviction regime
misses resolved yet" — an affirmative claim about our record, on the honesty
page, produced by a dropped request. The source comment called this "its honest
empty state". Severity: CRITICAL. Status: **FIXED — see R10**

### L11 — `AVG ACCURACY 0.0%` on a page promising measured accuracy
Component: `web/src/app/market/regimes/page.tsx:357-361`
`avgAccuracy` returned `0` for an empty forecast set, rendering a model measured
to be never right where nothing had been measured. `StatTile` already accepts
`null` and renders an em-dash (the 2026-08-09 fix); this call site predated it.
Severity: HIGH. Status: **FIXED — see R11**

## Repairs

| ID | File(s) | Verification |
|---|---|---|
| R1 | `daemon/internal/store/forecastmon.go` (+`ForecastDayStat.Withheld`) | `go test ./internal/store/...`; live `go run ./cmd/forecastmon --days 14` |
| R2 | `daemon/internal/forecastmon/forecastmon.go`, `source.go`, `cmd/forecastmon/main.go` | new `TestWithheldRowsDoNotReadAsCollapse`, `TestRealRawCollapseStillTrips`; live CLI |
| R3 | `daemon/internal/store/gradeable.go` | replicated against live DB: 2026-01-31 -> 2027-02-13 |
| R4 | `tools/prereg_readiness.py` | live run: First call date 2025-07-15 -> 2026-07-18 |
| R5 | `tools/structural_liveness.py` | live run: still WAITING, exit 0 |
| R7 | `ops/market-open-guard.sh` | simulated trigger under Git Bash: exit-0 -> collect |
| R8 | `daemon/internal/api/accuracy.go` | `go build`/`go vet`/`go test ./...` |
| R9 | `web/src/app/lab/backtest/page.tsx` | `tsc --noEmit`, `eslint`, `npm run build` |
| R10 | `web/src/app/lab/track-record/page.tsx` | same |
| R11 | `web/src/app/market/regimes/page.tsx` | same |
| R5b | `tools/test_structural_liveness.py` | fixture gained `superseded_by` (the live schema always had it); new `test_superseded_rows_are_never_overdue` |
| R6 | `internal/health/health.go`, `internal/api/fleethealth.go`, `internal/fleetmon/fleetmon.go` | new `health.FailingWorkers`; 3 new tests; **applied to the live DB it reports exactly `forecast-monitor` out of 100 workers** |
| R12 | `internal/api/slowcache.go`, `internal/api/dashboard.go` | new `detachedCtx()` ceiling; new `TestSWRCache_WedgedBackgroundRebuildCannotHoldItsSlotForever`; `go test -race` clean |
| R14 | `internal/modelhealth/modelhealth.go`, `drift.go`, `internal/pipeline/modelhealth.go` | `Inputs.FeatureDriftPct` → `*float64`; new `DriftFractionJudged`; 2 new tests, load-bearing |
| R13 | `internal/pipeline/modelhealth.go` | counters moved after the persist; `failed` counted and reported as `ErrDegraded` |
| R11 | `internal/pipeline/shorts.go` | catch-up `NextFire` + bounded gap-fill sweep; new `TestShortVolPoller_MissedDayIsBackfilledOnALaterRun`, load-bearing |

### L14 — an unmeasurable drift number scored a PERFECT stability
Component: `daemon/internal/pipeline/modelhealth.go` (`featureDrift`),
`internal/modelhealth/modelhealth.go` (`Grade`), `internal/modelhealth/drift.go`
Every failure path in `featureDrift` returned `0` — and `Grade` scored
`stability = clamp01(1 - 0) = 1.0`. That is verbatim the defect
`internal/modelhealth/drift.go:5-10` says the package exists to close ("nothing
ever computed it, so it was always zero and stability always scored a perfect
1.0"), reinstated through the error path of its own fix. The 0 is persisted into
the JSON the UI renders as a graded axis. `DriftFraction` had the same
conflation one layer down: it returns 0 when `judged == 0`, so "no feature
drifted" and "no feature had enough data to judge" were the same number.
Severity: HIGH. Status: **FIXED — see R14**

**R14.** `Inputs.FeatureDriftPct` is now `*float64`: nil means unmeasured and
`Grade` WITHHOLDS the component rather than scoring it, naming the reason. The
overall figure renormalises over the components actually present — summing an
absent one as zero would penalise a model for a measurement the platform failed
to take, the mirror of the same dishonesty. With every component present the
divisor is 1 and the arithmetic is identical to what it replaced. Added
`DriftFractionJudged` so the caller can tell an empty denominator from a clean
one. `modelhealth.Ptr` mirrors `fleetmon.Ptr`.
An existing test, `TestDriftNowAffectsTheHealthScore`, was **relying on the
conflation** — its "clean" baseline never set drift at all — and now says
`Ptr(0)` explicitly.

### L13 — `model-health` reported `graded N` when zero verdicts were persisted
Component: `daemon/internal/pipeline/modelhealth.go`
`graded++` / `retired++` ran BEFORE the marshal + `SetMeta` pair, and both
failure paths `continue`. The structural path additionally discarded its
`SetMeta` error outright (`_ =`), so a failed write left no trace at all. A
contended meta write (a hazard documented at `internal/workers/workers.go:404-407`)
had the worker report `graded 5, retired 2` with `status=ok` while
`/api/modelhealth` kept serving the PREVIOUS pass — and a model retired this
pass would have gone on publishing.
Severity: HIGH. Status: **FIXED — see R13**

**R13.** Counters moved after a successful persist; a new `failed` counter is
reported separately and the run now returns `workers.ErrDegraded` when any
verdict was computed but not written, so the pass can no longer read as `ok`.
Both persist failures are logged.

### L11 — `finra-shorts` lost a day of data forever while reporting "will retry"
Component: `daemon/internal/pipeline/shorts.go`
Both failure paths returned a nil error (so `status=ok`) with the message
"dq recorded; will retry". That claim WAS true under the old 6h tick; it stopped
being true when the worker became a `ScheduledWorker` firing once per trading
day at 18:30 ET **with no catch-up branch** (cf. `cot.go` and
`briefing/weekly.go`, which both have one). The next fire targets a NEW day, the
one-time backfill is gated shut by `finraShortsBackfillKey`, and no gap-filling
code existed — so the missed day was never requested again. `internal/srchealth`
tracks this source by `MAX(day)`, so the hole became invisible the moment the
next day landed.
Severity: HIGH. Status: **FIXED — see R11**

**R11.** Three parts: a catch-up branch in `NextFire` (26h stale threshold,
matching the repo's existing pattern); a bounded gap-fill sweep in `Run` that
walks back at most `maxCatchUpDays = 5` trading days from the target, stops early
at the first day already stored, ingests oldest-first, continues past failures
with a DQ per failed day, and advances the cursor **only** when the target itself
succeeded; and honest messages that bound the retry claim by the window instead
of promising one unconditionally. Steady-state cost is unchanged — with
yesterday stored the sweep is one day, exactly as before.

### L12 — "detached" rebuild meant "unbounded" rebuild
Components: `daemon/internal/api/slowcache.go` (both caches), `dashboard.go`
Every background refresh in the api package ran on a bare `context.Background()`
— **no deadline at all**. A cold build inherits the request context and is
bounded by it; a background one had nothing to stop it. So a build that never
returned never ran its `defer releaseColdSlot()` and never cleared `rebuilding`.

Two consequences, both silent:
- **`swrCache` / `swrBodyCache`:** the wedged rebuild holds one of only
  `maxConcurrentColdBuilds = 2` slots forever. Two of them exhaust the ceiling
  process-wide — every subsequent cold build fails admission, and every warm
  entry serves its stale copy indefinitely returning `(payload, nil)`: no error,
  no age. Affects `/api/track-record`, `/api/regimes`,
  `/api/predictions/latest`, `/api/honesty`, `/api/calibration`, `/api/datastats`,
  `/api/macro` and the other body-cached routes.
- **`dashCache` is worse and was under-reported.** It takes no cold-build slot,
  so it cannot starve the others — it freezes *itself*. `rebuilding` is cleared
  on exactly one line, so a wedged build leaves it `true` for the life of the
  process: no further rebuild is ever kicked and `builtAt` never advances, while
  the payload keeps advertising `cacheTtlS: 60`.

Throughout, `cache-warmer` reports `"warmed dashboard + movers caches"` every
60 seconds (`pipeline/cachewarm.go:30-33`), because it checks for an error the
warm path structurally cannot return.
Severity: HIGH. Status: **FIXED — see R12**

**R12.** One helper, `detachedCtx()`, used at all three sites — a
3-minute ceiling (~4x the slowest build measured when the package was written:
~45s for `/api/predictions/latest`, 22-44s for `/api/track-record`), so it
cannot fire on a merely slow rebuild, only on one that is never coming back.
Plus `noteRebuildFailure`, because the silence was half the defect: a breach of
the ceiling is logged distinctly from an ordinary build error, since "still
running at the ceiling" and "returned an error" are different problems.

Swept the package for the same shape: the only other `context.Background()` uses
(`api.go:216,663`, `tvstatus.go:189`, and the two `acquireColdSlot` calls, which
carry their own 5s timer) are all already bounded.

The regression test was **proved load-bearing by running it against the
pre-fix line**: with `context.Background()` restored it fails in 2.01s with
`wedged background rebuild was never cancelled — it would hold its cold-build
slot for the life of the process`. It asserts three things — the wedge is
abandoned at the ceiling, the slot returns (verified by refilling the ceiling),
and the entry retries rather than freezing on its stale copy. `go test -race`
on the cache tests is clean.

**R6 — the fix for L6.** A second pure rule beside `StaleWorkers`:
`FailingWorkers` flags any worker whose last `MinConsecutiveFailures = 3`
non-in-flight runs were all `status='error'`. It is wired into BOTH surfaces
(`data/health.json` gains `failingWorkers` and `ok` goes false; `/api/fleet-health`
gains the same field and a distinct fleetmon breach), plus a `worker_failing`
DQ event.

Only `error` counts. `degraded` is a worker honestly reporting it had nothing to
deliver — expectancy-trainer and gbm-trainer do this by design and staleness
already reports them, so counting them here would be double noise. `orphaned` is
the boot sweep: the daemon died, not the worker. `running` carries no verdict and
is SKIPPED rather than treated as a success, so an in-flight run cannot reset a
real streak — and `statusWindow = MinConsecutiveFailures + 2` so an in-flight row
cannot shrink the evidence below the threshold either (stream ingestors always
have one).

Verified against the live DB by replicating the exact query and rule over all
**100** workers: it returns `['forecast-monitor']` — statuses
`[error, error, error, error, error]` — and **nothing else**. That is the worker
`data/health.json` currently omits entirely while reporting
`staleWorkers:["expectancy-trainer","gbm-trainer"]`. No false positives on the
other 99.

Why staleness could never have caught it, measured: forecast-monitor's interval
is 24h, so its threshold is 72h; the boot grace hands a never-successful worker
the daemon's boot time; and mean daemon uptime across the 20 recorded boots is
**9.67h, with 18 of 19 uptimes shorter than 72h**. The grace clock resets before
the alarm can fire — the check was unreachable by construction.

**R5b note — the fixture change was NOT "make red go green".** Applying R5 broke
19 tests with `no such column: ro.superseded_by`: the fixture had never mirrored
a column the real table carries, so `overdue_rows` could not be tested against
the grader's actual admission filter. The new test was then proved
load-bearing by re-running it against a copy of the module with the filter
stripped out — the superseded row is reported overdue (`[1]`), so the test fails
without the fix.

## Final verification (after the last change)

| Check | Result |
|---|---|
| `cd daemon && go build ./...` | RC=0 |
| `cd daemon && go vet ./...` | RC=0 |
| `cd daemon && go test ./...` | RC=0 |
| `.venv/Scripts/python.exe -m pytest tools/ -q` | **335 passed**, 4 skipped (was 334 — one new regression test) |
| `gofmt` deviation on every file touched, LF-normalized | **unchanged from HEAD** (health_test.go 8→8, fleethealth.go 3→3; both pre-existing and left alone per "don't reformat what you didn't write"). Note `gofmt -l` flags nearly every file in this repo because they are CRLF — that signal is noise here, and `gofmt -l ... \| head` silently truncating it is how I first misread it. |
| `cd web && npx tsc --noEmit` | RC=0 |
| `cd web && npx eslint` | RC=0 |
| `cd web && npm run build` | RC=0, 59 routes |
| 8 repo gates (docs/schema/drift/research/structural/revalidation/spa/dod) | all RC=0 |
| `ops/test-no-bare-pkill.sh` under Git Bash | RC=0 |
| `go run ./cmd/forecastmon --days 14` | RC=1 — correctly, and now for the right reasons |
| `gofmt -l` on files touched this pass | clean |

`prereg_readiness.py` still exits 1. That is correct and unchanged in meaning:
the registered 2026-08-07 gradable date is genuinely unreachable. Only the dates
it printed were wrong; the worst-case first verdict now reads 2028-05-04.

## Not verified
- **The three changed web pages were never opened in a browser.** `/lab/backtest`,
  `/lab/track-record` and `/market/regimes` are all behind the sign-in gate, and
  entering credentials is not something I will do. `/accuracy` was loaded
  anonymously and correctly rendered its honest REFUSED state. The three fixes
  rest on `tsc`, `eslint`, a full `next build`, and reading both the component
  and the Go type it consumes — not on seeing them render.
- No scheduled task was run end-to-end; `market-open-guard.sh` was verified by
  simulating its decision under the real Git Bash at real trigger timestamps,
  not by waiting for 06:20 PT.
- `ops/eighty-loop.ps1` (801 lines) and `ops/selfimprove-loop.ps1` (590) got a
  grep pass only, not a line-by-line read.
- Everything in the O1-O20 backlog is evidenced but unrepaired.

**R2 note — the fix deliberately does NOT just silence the alarm.** Excluding
abstentions drops the forecast count to 19-31, below `MinSymbolsForCollapse=30`,
so the distinct-ratio test would have gone quiet and the real event would have
become invisible. `MinCoverageRatio` + `Starved()` were added in the same change
so the starvation is reported as itself. Live output now reads:
`FORECAST COVERAGE STARVED on 5/14 day(s), most recently 2026-08-11: only 2 of
46 symbols received a forecast (coverage 0.043, floor 0.50)`.
The genuine published collapse (8/11 days) and 3 calibration inversions still
trip — the change weakened nothing.

## R-OPS — the ops layer (O2, O5, O6, O7, O8)

All five verified against the real Git Bash / real Task Scheduler before and
after, not from reading source.

**O5 — `signaldeck-ctl.sh` could not report the web as down.** `sd_is_running node`
asks whether *any* process named node exists; on this box that is always true
(3 unrelated node processes: Claude Code, the OmniRoute gateway). Demonstrated
live: `ctl status` printed `com.signaldeck.web: running` while
`curl http://localhost:8323/` was refused outright (HTTP 000). Added
`sd_port_listening` to `lib-portable.sh` (PowerShell `Get-NetTCPConnection`,
then `lsof`, then `netstat`) and pointed the web arm at port 8323. It fails
CLOSED — unlike `sd_is_running`, whose callers guard a destructive VACUUM and
must assume the daemon is up; here the dangerous answer is a confident
"running" for something that is not. After: `com.signaldeck.web:
registered-idle (stopped)`, matching curl, while the daemon still correctly
reads running (HTTP 200 on 8322). Two new checks in `ops/test-lib-portable.sh`
assert against real kernel state.

**O6 — `market-close.sh` always exited 0.** `set -u` only, the backup script's
status discarded at `:55`, and `|| true` on the final line. Every
`signaldeck-backup-offline.sh` failure path (VACUUM failure, content-verification
failure, missing python, budget breach) was swallowed; Task Scheduler showed
`0x00000000` while `logs/backup-offline.log` had no entries for 08-08 or 08-09.
Now captures `backup_rc` and `daemon_rc`, still always restarts the daemon (a
failed backup must never leave the platform down), logs both, and exits with the
backup's own code. Deliberately no `set -e` — it would abort before the restart.

**O7 — `restore-rehearsal.sh` always took the FAIL branch on Windows.**
`stat -f %m` is BSD; GNU reads `-f` as `--file-system`. Measured: it prints a
multi-line filesystem report **and exits 0**, so the `|| echo 0` fallback never
fired and that text was assigned to `BACKUP_MTIME`. The integer compare then
errored and evaluated false, making the transitional branch unreachable — a
legitimately pre-anchor backup pages an operator instead of warning. Replaced
with a `file_mtime` helper that tries GNU then BSD and **validates the value is
an integer**, because the failing command's exit status cannot be trusted.
Verified under Git Bash: `1786492068` for a real backup, `0` for a missing file.
Also gave `OFFSITE_DIR` the same `SIGNALDECK_OFFSITE_DIR` / `$OneDrive`
resolution its sibling `signaldeck-backup-offline.sh` already had, so the
rehearsal cannot read a different location than the backup wrote.

**O8 — `check-task-health.ps1` was green-by-construction.** `0xC000013A` was the
only condition that could set exit 1, so it printed `OK` over a table containing
a terminated task and a refused start. Now also fails on a non-benign
`LastTaskResult` and on **no `NextRunTime`**.
The second rule matters more than it looks: my first attempt used a 48h
staleness threshold and it produced two FALSE POSITIVES against live data —
`SignalDeck Restore` is weekly and legitimately idles ~7 days, and
`SignalDeck Daemon`/`Eighty Loop` sit at `0x800710E0` because
`MultipleInstances=IgnoreNew` is declining a trigger while the previous instance
runs, which is exactly how they are meant to behave. A check that cries wolf at
a healthy weekly task is one nobody reads — the same lesson as the 120-minute
grader-health ceiling. Final rules: `0x800710E0` is benign only while `State=Running`,
and "will never run again" is tested by asking the scheduler (`NextRunTime`
empty) rather than by elapsed time. It now flags exactly `SignalDeck Web`
(terminated, no trigger, 95h stale) and `SignalDeck Daemon Keepalive`
(`0x00000001`), exit 1, and nothing else.

**O2 — the stale-binary preflight had never executed.** `daemon-guard.ps1:147`
wraps it in `Test-Path ops\run-daemon-with-provenance.ps1`, and that file does
not exist — the only copy is `round2-drafts\devops\`. A `Test-Path` guard around
a check fails OPEN, and nothing logged the skip, so it looked correct in source
while the 22-commits-behind incident it was written to prevent stayed
unguarded. It now reports the absence on every run and still starts the daemon
(matching the block's documented warn-and-start default).

**A trap this surfaced, now recorded in the file itself:** Task Scheduler runs
Windows PowerShell 5.1, which reads `.ps1` as ANSI. A UTF-8 em-dash inside a
double-quoted string made `check-task-health.ps1` a hard parse error
(`TerminatorExpectedAtEndOfString`). I swept every `ops/*.ps1` with the real
parser afterwards: six other files carry non-ASCII but only in COMMENTS, where
it survives, so they were left alone rather than churned.

## Withdrawn findings — reported by an investigation agent, NOT true

**O4 — "the `SignalDeck Web` task has no triggers" is NOT a defect.** It is the
documented on-demand model, and the evidence is in the repo:
- `ops/signaldeck-web-task.ps1` is the canonical Windows registration ("TARGET:
  replaces ops/com.signaldeck.web.plist on Windows", approved 2026-08-06). It
  registers **no trigger on purpose** and prints
  `Start it with:  Start-ScheduledTask -TaskName "SignalDeck Web"`. A weekly
  trigger sits in the file as an explicitly-NOT-wired optional block, labelled
  "only if a human decides the UI should follow market hours ... a behaviour
  change from the plist, not part of the port".
- `ops/com.signaldeck.web.plist` sets `RunAtLoad=false` with no `KeepAlive`, and
  `install-windows-tasks.ps1:160-162` only adds an at-logon trigger when one of
  those is set. No trigger is the faithful translation.
- The web is started by `signaldeck-ctl.sh up` (`kick "$WEB"`), and stopped by
  market-close. `LastResult 0x00041306` (SCHED_S_TASK_TERMINATED) is the NORMAL
  result of that deliberate stop.

Why it looked dead on 2026-08-11: `signaldeck-ctl.sh collect` — the path
market-open takes — starts **daemon + tunnel only, no web UI** by design. The
95h staleness was a downstream symptom of **L7**: `market-open-guard.sh` was
exiting 0 every weekday, so `collect` never ran at all. L7 is fixed.

**This caused a real false positive in my own repair.** My first
`check-task-health.ps1` rule flagged the Web task on both "result not success"
and "no NextRunTime". Both were wrong for exactly this reason. Fixed by an
explicit `$onDemandStoppable` list rather than by inferring intent from a
missing trigger — because guessing "it must be on-demand" from an absent trigger
is precisely how a trigger that got LOST would be excused. The check now reports
`OK` on this fleet, and both of its failure branches were demonstrated firing on
live data before the exemptions were added (`SignalDeck Daemon Keepalive`
`0x00000001`, and the Web no-NextRunTime case).

**Task state cannot judge whether the web is up**, and should not try: an
on-demand task stopped normally is indistinguishable from one that died. The
liveness signal for the web is the PORT — which is what `signaldeck-ctl.sh` now
asks (R-OPS / O5).

## R-OPS2 — O3 resolved: tunnel stays OFF, and the swallowed status is fixed

**Decision (Nicholas, 2026-08-11): leave the tunnel off.** It exists only to
carry TradingView webhooks; it is not registered on this machine, ngrok is not
installed, and nothing else depends on it. `ops/TUNNEL_RESTORE_RUNBOOK.md` holds
the fully-prepared elevated commands for both ngrok and cloudflared should that
ever change. No task was registered and nothing was published to the internet.

**The code defect underneath it is fixed.** `sd_svc_start` ended in
`schtasks //Run ... >/dev/null 2>&1` — and the macOS branch in an unconditional
`return 0` — with every call site discarding the status, so `up`/`collect`
printed a fixed success string regardless. It now returns a real verdict:

| code | meaning |
|---|---|
| 0 | started (or already running) |
| 1 | the service exists but could not be started |
| 2 | the service is NOT REGISTERED |

Existence is probed with `//Query` rather than inferred from `//Run`, because
`//Run` returns 1 for **both** "no such task" and "task refused" (measured), and
those need different answers.

`signaldeck-ctl.sh` gained `kick SERVICE [optional]` + `report_starts`: failures
are counted, named, and make `up`/`collect` exit non-zero so the scheduled
caller sees them. The tunnel is marked `optional`, which encodes the decision —
**absent is a note, not an error** — while a service that IS registered and then
fails to start is still a hard failure. That distinction is the whole point: had
I made absence fatal, every market-open run would have gone red for a state
Nicholas deliberately chose, which is the fastest way to make this output
unreadable.

Verified live:
```
$ ./ops/signaldeck-ctl.sh collect
  note: com.signaldeck.tunnel is not registered on this machine — skipping (optional)
SignalDeck collecting — daemon up (no web UI)                       RC=0
```
and, with `optional` temporarily removed to prove the failure path is real:
```
  ERROR: com.signaldeck.tunnel is not registered on this machine — cannot start it
SignalDeck start INCOMPLETE — 1 service(s) did not start            RC=1
```
Exit codes confirmed against the real fleet: tunnel → 2, daemon → 0, bogus
name → 2. Two permanent checks added to `ops/test-lib-portable.sh`.

Also corrected two now-false messages: `status` told the operator to run
`install-windows-tasks.ps1`, which **skips this task by design** (it requires a
`.sh` in `ProgramArguments`), so following that advice changed nothing and made
a deliberate state look broken; and the script header claimed `up`/`collect`
bring up a tunnel unconditionally.

## Resolved by decision — was BLOCKED

**O3 — the `SignalDeck Tunnel` task.** I have NOT prepared a command to register
it, because every version of that command would register a task that cannot
work. Measured 2026-08-11:

| Fact | Evidence |
|---|---|
| The task does not exist | `Get-ScheduledTask -TaskName 'SignalDeck Tunnel'` → not registered |
| The installer skips it BY DESIGN | `install-windows-tasks.ps1:82` requires a `.sh` in `ProgramArguments`; `com.signaldeck.tunnel.plist` names the ngrok binary directly → `SKIP  ... no shell script in ProgramArguments` |
| **ngrok is not installed on this machine** | `command -v ngrok` → MISSING. The plist points at `/Users/natalienyaung/.local/bin/ngrok`, a macOS path |
| cloudflared IS installed, but unconfigured | `C:\Program Files (x86)\cloudflared\cloudflared.exe`; `~/.cloudflared/` does not exist — no tunnel credentials |
| The daemon would reject the webhook anyway | `AllowedHosts` defaults to `127.0.0.1:8322,localhost:8322` (`config.go`). The public domain must be added via `SIGNALDECK_ALLOWED_HOSTS` or the Host allowlist drops it |

So this is three decisions, not a command: **(a)** ngrok or cloudflared,
**(b)** which public hostname, **(c)** confirmation that exposing the daemon
publicly again is wanted. Registering a tunnel publishes an endpoint to the
internet, which is not something to do on an inferred instruction.

The part of O3 that IS a code defect and remains open: `lib-portable.sh:188`
runs `schtasks //Run` with stdout, stderr AND exit status all discarded, and
neither `kick()` nor the `up`/`collect` arms inspect the result — so every start
path reports success whether or not the tunnel came up. That is fixable without
any decision and is left in the backlog.

## R16 — a refused override is no longer silent

Ten helpers across eight packages, plus five one-off inline parsers, all had the
same shape: read the variable, fail to parse it, return the default, say
nothing. ~50 variables. The 2026-08-09 audit fixed this for ONE key family
(`SIGNALDECK_RISK_*`, via `DescribeLimits`); the rest were untouched.

**The stakes are highest on retention.** The dangerous direction is not a bad
value that keeps too much data — it is an operator LENGTHENING a window to
protect data, mistyping it, silently getting the shorter default, and having the
sweep delete rows they meant to keep. That deletion is irreversible.

New `internal/envcfg` owns one concern: recording an override the daemon
refused. `Reject` for ordinary keys, `RejectCritical` for the retention windows
that drive deletion. Deduped by key (several helpers run once per sweep, so one
typo must not produce an unbounded log), thread-safe, and values are REDACTED
for any key naming a token/secret/password/credential — a rejected token is
still a token.

**Runtime behaviour is deliberately unchanged.** The default still applies and
the daemon still starts. A config layer that refused to boot on a typo would be
a far riskier change than the defect calls for. Only the silence is fixed.

Surfacing: `data/health.json` gains `rejectedEnv`, and `ok` goes false ONLY for
a critical (deletion-governing) rejection. Making every refused knob page an
operator would be the red-by-construction mistake this pass keeps almost
making — an alert-threshold typo is worth reporting, not worth waking someone.

`config.atoiOr` took only the VALUE, so it could not name the key it had
refused even in principle; it now takes the key, and its four call sites pass it.

Verified end-to-end against the real helper (`internal/maintain`):
`SIGNALDECK_SCORES_RETENTION_D=3650d` → still returns the 90-day default
(runtime unchanged), records ONE critical rejection naming raw `3650d` and
`using 90`, and `HasCritical()` is true. `=3650` is honoured and silent; unset
is silent. 10 envcfg tests pass under `-race`.

## R10 — the "offsite" backup was on the same disk, and said OK every night

**Measured 2026-08-11/12, this machine:**

| Check | Result |
|---|---|
| `OFFSITE` resolves to | `$OneDrive/SignalDeckBackups` |
| OneDrive signed in? | **NO** — every account (Personal, Business1, FileCoAuth) has an EMPTY `UserFolder`, `cid` and `UserEmail` |
| Sync placeholder attributes on the backups | **none** — `0x20` (Archive) only; a OneDrive-managed file carries pinned/unpinned/recall bits |
| Files anywhere under the OneDrive tree | **3** — two of them are these backups. Nothing has ever synced |
| Volume | DB on `/c`, "offsite" on `/c` — **one physical disk** (`PHYSICALDRIVE0`) |
| Contents | 5.42 GB (a 4.61 GB RAW `.db` + a 0.81 GB `.gz`) |
| `logs/backup-offline.log` | `offsite OK` |
| `meta.backup_last_offsite` | `1786492388` = 2026-08-11 23:53 UTC |

Nothing was uploading anywhere. The daily copy was C: → C:, and the script
recorded a fresh "last offsite copy" timestamp for a copy that dies with the
disk. That is verbatim the defect this file's own header says the OneDrive
default was ADDED to fix — *"the old default copied 2.7GB onto C: and looked
like it worked"* — reintroduced by its replacement.

**The Go side already refused to go along with it.** `api.go` (2026-08-02) makes
`offsiteConfigured` require volume separation, so the surface was reporting
`offsiteConfigured:false` beside a `lastOffsiteTs` from minutes earlier. Of that
contradictory pair the fresh timestamp is the more persuasive half, which is why
the shell is where this had to be fixed.

**R10.** `signaldeck-backup-offline.sh` now decides what to CALL the copy:
- `volume_of` / `same_volume` (portable `df -P` mount point). `stat -c %d` is
  explicitly NOT used — measured under Git Bash it returns the identical device
  id `2585421839` for every path on the machine, so it cannot distinguish
  volumes and would answer "same" forever.
- Same volume → the copy is KEPT (a second copy still survives an accidental
  delete) but logged as a LOCAL second copy, a `backup_offsite_same_volume` DQ
  event is recorded, and **`backup_last_offsite` is NOT written**.
- Different volume → unchanged: meta written, `offsite OK` with the verified
  byte count.
- New size verification: `cp` exiting 0 is not the same as the bytes arriving,
  so a short copy is deleted and never recorded.
- `same_volume` treats UNKNOWN as DIFFERENT — the copy is kept either way, so
  mislabelling a genuinely external disk is the costlier error. (`api.go` takes
  the opposite default for the opposite reason: there it gates a *claim*.)

New `ops/test-offsite-is-offsite.sh` extracts the functions from the real script
with `sed` rather than copying them, so the test cannot drift from what it
checks. Its guard assertion was proved load-bearing against a deliberately
regressed copy with the meta write hoisted above the branch (`UNGUARDED`).

**Not done, deliberately:** the stale `backup_last_offsite` value is left in the
DB. Deleting it is a data mutation and the value is not reconstructable, so it
is reported rather than rewritten. It will now simply stop being refreshed and
age visibly beside `offsiteConfigured:false`.

**Still true and NOT fixed by this:** there is no off-machine copy of this
database. Point `SIGNALDECK_OFFSITE_DIR` at an external volume — that is a
hardware decision, not a code one.

## R15 — a failed API bind left a fully green, headless daemon

`cmd/signaldeckd/run.go` launched the API in a goroutine that only LOGGED a
failure:

```go
go func() {
    if err := api.Serve(ctx, deps); err != nil {
        slog.Error("api server exited", "err", err)
    }
}()
```

`api.Serve` returns nil on a graceful shutdown (`http.ErrServerClosed` is
filtered inside it), so a non-nil error there is ALWAYS a real fault — most
often the bind failing because a crashed predecessor still holds `:8322`. When
that happened the goroutine exited, `run()` carried on to `runner.Start`, and
the daemon sat there with every worker green, `worker_runs` uniformly `ok`,
`health.json` written, backups succeeding — and the entire web surface down.
Nothing self-probes the listener, so the only way to notice was to try the site.

**R15.** `run()` now derives a cancellable child context, and an API failure
cancels it. The child matters: cancelling the PARENT would look like an operator
stop, while cancelling the child leaves `ctx.Err()` nil in `main()` — the
condition main already treats as an internal fault and exits 1 for (a fix from a
previous pass, which this reuses rather than duplicates). The fleet drains and
checkpoints the WAL on the way out, the task's `RestartCount` policy fires, and
`ops/check-task-health.ps1` now reports the non-zero result.

A permanently-held port will restart-loop rather than run headless. That is the
intended trade: a visibly failing service gets fixed, a silently headless one
does not.

**Verified end-to-end against a real held port**, with the live daemon holding
`:8322` and the test binary pointed at a throwaway DB:

```
ERROR api server exited — the daemon cannot serve; shutting down the fleet
      err="listen tcp 127.0.0.1:8322: bind: Only one usage of each socket address ..."
WARN  worker: last run lookup worker=13f-poller err="context canceled"
ERROR signaldeckd stopped WITHOUT a shutdown signal — internal fault; exiting non-zero
EXIT=1
```

**Proved load-bearing** by rebuilding with the log-only line restored and
running the same scenario: `EXIT=124` (still running after 45s) with 7 worker
log lines and no API — the headless daemon, reproduced. The live daemon was
untouched throughout (`:8322` still held by pid 9660, `/api/health` 200).

## R9 — external timestamping was dead, and nothing measured it

**Measured 2026-08-12.** The two halves had come apart:

| | |
|---|---|
| Anchors SIGNED locally | working — `ledger_anchors` holds 10 rows, newest 2026-08-10 |
| Anchors PUBLISHED externally | **never** — last log line 2026-07-27, `PUSH FAILED` |
| What invokes `ops/anchor-publish.sh` | **nothing.** `ops/accuracy-registry.sh` does not call it; no scheduled task references it; the only mentions repo-wide are prose |
| The clone it publishes to | `$HOME/.signaldeck/anchor-publish` — **does not exist**, and `SIGNALDECK_ANCHOR_REPO` is unset |
| What measured the gap | **nothing.** `verify_dod.py` checks only that documents MENTION anchoring — text presence, not whether it happened |

The whole security argument for anchoring is that *"a digest sitting in a third
party's git history is the only evidence an operator who holds the signing key
cannot fabricate after the fact."* Sixteen days of anchors carried none of that
guarantee, and the system said nothing.

**R9, the part that is code.**
1. `ops/anchor-publish.sh` now records `meta.anchor_last_published` — **after
   `git push` succeeds, not after `git commit`**. A local commit provides none
   of the third-party guarantee, so recording it there would have made the same
   claim the outage disproved. Nothing recorded a successful publish before,
   which is precisely why nothing could measure the outage.
2. New `tools/anchor_liveness.py` measures newest-local-anchor vs
   last-published: `NO ANCHORS` (0), `NEVER PUBLISHED` (1), `STALE` past
   `--max-age-hours` (1), `OK` (0), unreadable DB (**2 — inconclusive, never a
   pass**). Read-only apart from one optional `anchor_publish_stale` dq_event,
   written on failure only.
3. Wired into `ops/accuracy-registry.sh`, the daily task that ALREADY runs and
   already runs the sibling `research_liveness.py`. That gets the finding into
   the data-quality stream today, with no new task to register.
   **Deliberately non-blocking:** unpublished anchors make the record less
   externally verifiable, but they do not make the graded numbers wrong, and
   suppressing the registry over a missing `git push` would withhold an honest
   track record as punishment.
4. `tools/test_anchor_liveness.py` — 6 tests. The PASSING cases are pinned as
   hard as the failing ones: a check that can only go red is one nobody reads
   (the `check-grader-health.ps1` lesson), and one that can only go green
   measures nothing. All four verdicts were exercised against synthetic DBs.

Live output today:
```
anchor_count      : 10
newest anchor     : 2026-08-10T05:55:03Z
last published    : never
verdict           : NEVER PUBLISHED
These anchors exist ONLY on this machine and carry no third-party timestamp.
```

**BLOCKED, and NOT fixed by any of the above: nothing is published yet.**
Publishing requires a clone of the public anchors repo at
`SIGNALDECK_ANCHOR_REPO`, and pushing to a public repository is an
outward-facing action I will not take on an inferred instruction. The gap is now
*visible and measured* rather than silent — that is the whole of what code can
do here. To close it: clone the public anchors repo, point
`SIGNALDECK_ANCHOR_REPO` at it, and run `bash ops/anchor-publish.sh`;
`python tools/anchor_liveness.py` flips to `OK` when the push lands.

## R17-R20 — the web surface

**O17 and O18 were already fixed by a CONCURRENT session, not by me.** The web
tree is shared, and between the audit and this pass another session modified
`s/[market]/[symbol]`, `signals/report/...`, `watchlist/compare`, `ProofStrip`
and `lib/api.ts`. Verified rather than assumed:
- **O17**: `evidenceCaveat` is now in the TS type AND rendered verbatim
  (`signals/report/.../page.tsx:191-194`) behind a `HelpTip`, with
  `firstGradableOn` appended and the tile's own comment explaining that
  `historicalAccuracy` is a backtest lookup, not a measurement.
- **O18**: the hard-coded `48.08 / 54.50 / 12,931` figures are GONE; only a
  comment remains recording that they "matched no source".

I touched neither file — the two sessions' edits do not overlap.

**O19 — a partial sum labelled "Total Value".** `intel/institutions` fetches a
manager's book capped at 200 rows and a symbol's holders capped at 100, and
`daemon/internal/api/signal8.go` returns only `count` = rows SENT, never a
book-wide sum. So the tile added up the rows on screen and called the result the
total, understating by an unknown amount for any book larger than the cap.
Fixed by naming the caps as constants (so the truncation test cannot drift from
the request that causes it) and switching the label when the cap is hit:
`Value of Top 200` with the sub-line "book is larger than the fetch cap". Same
for the symbol view's Total Shares and Total Value. No API change: a book-wide
`SUM` is more surface than a display honesty fix needs.

**O20 — the two highest-severity silent catches.**
- `useScreenerData` swallowed a failed `api.ranking()`, leaving `ranking` null;
  `screenerModel` read `ranking ?? []`, every row came back `rank === null`, and
  `ScreenerTable` rendered "—" under the tooltip **"not in the latest ranking
  pass"** — a factual assertion that the pass RAN and excluded that symbol, on
  every row, produced by a dropped request. Now tracked as `rankingFailed` and
  threaded to the table, which says "ranking unavailable — this request failed,
  so the rank is UNKNOWN, not absent". (The rows path in that same hook already
  split loading/error/empty correctly, which is exactly why only the enricher
  columns leaked.)
- `/market/macro` wired `err` to only ONE of its four fetches; `regime`,
  `sectors` and `ranking` were swallowed, so REGIME MAP rendered "—" under
  "symbols classified" and two whole sections vanished with nothing saying why.
  Each now records which feed failed (deduped, since the page polls every 2
  minutes) and a banner names them: "... could not be loaded — those sections
  are MISSING, not empty."

**Still open in O20:** roughly fifteen lower-blast-radius silent catches remain
(`usePortfolio`, `CommandPalette`, `MicroPanel`, `BigCandle` markers,
`intel/news` watchlist, several `s/[market]/[symbol]` fetches, `login`'s
`openSignup` default, `Shell`'s logout). Same class, much smaller consequence
each. Listed in the working notes as F18.

**NOT verified in a browser.** `/market/macro`, `/market/overview` and
`/intel/institutions` all sit behind the sign-in gate and I will not enter
credentials. These rest on `tsc --noEmit`, `eslint`, a full `next build` (59
routes), and reading each component against the Go type it consumes.

## CONFIRMED but NOT repaired — open backlog

Each verified by direct evidence this session; none fixed, all still live.

| ID | Component | Finding |
|---|---|---|
| ~~O1~~ | — | **FIXED after this table was first written — see R6.** Both surfaces now carry `FailingWorkers`. |
| ~~O2~~ | — | **FIXED — see R-OPS.** |
| ~~O3~~ | — | **RESOLVED — tunnel stays OFF (Nicholas, 2026-08-11); the swallowed-exit-status code defect is FIXED. See R-OPS2.** |
| ~~O4~~ | — | **NOT A DEFECT — withdrawn.** See "Withdrawn findings" below. |
| ~~O5~~ | — | **FIXED — see R-OPS.** |
| ~~O6~~ | — | **FIXED — see R-OPS.** |
| ~~O7~~ | — | **FIXED — see R-OPS.** |
| ~~O8~~ | — | **FIXED — see R-OPS.** |
| ~~O9~~ | — | **PARTIALLY FIXED — see R9. The publish itself is BLOCKED on an operator action.** |
| ~~O10~~ | — | **FIXED — see R10.** |
| ~~O11~~ | — | **FIXED — see R11.** |
| ~~O12~~ | — | **FIXED after this table was first written — see R12.** |
| ~~O13~~ | — | **FIXED — see R13.** |
| ~~O14~~ | — | **FIXED — see R14.** |
| ~~O15~~ | — | **FIXED — see R15.** |
| ~~O16~~ | — | **FIXED — see R16.** |
| ~~O17~~ | — | **FIXED by a CONCURRENT session, verified 2026-08-12 — see R17-20.** |
| ~~O18~~ | — | **FIXED by a CONCURRENT session, verified 2026-08-12 — see R17-20.** |
| ~~O19~~ | — | **FIXED — see R17-20.** |
| ~~O20~~ | — | **PARTIALLY FIXED — the two highest-severity sites; see R17-20.** |

**O1 was fixed** (R6) and **O12 was fixed** (R12). **O13, O14 and O11 are the
ones to fix next** — all three are the same class as everything already repaired
here, a fix whose own error path reinstates the defect it closed:

- **O14** is the sharpest: `featureDrift` returning `0` on a read error restores
  the exact "stability always scores a perfect 1.0" behaviour that
  `internal/modelhealth/drift.go:5-10` was written to eliminate, and the 0 is
  persisted into the JSON the UI renders as a graded axis.
- **O13** reports `graded N` when zero verdicts were written.
- **O11** silently drops a day of `short_volume` forever while reporting "ok …
  will retry", a promise that stopped being true when the worker migrated to
  `ScheduledWorker` without a catch-up branch.

## Working-as-designed — deliberately NOT changed
- `data/health.json` `ok:false` with `staleWorkers:[expectancy-trainer,
  gbm-trainer]`: both run hourly and return `degraded` ("anti-predictive and
  benched fleet-wide", "no model leg cleared its OOS edge bar"). Honest.
- `/api/ready` 503 and `/api/accuracy` 503: detailed honest refusals.
- `backfiller` / `crypto-live` / `stock-streamer` rows with status `running` and
  historical `orphaned`: long-running stream ingestors swept at boot. Their
  `Interval() <= 0` correctly exempts them from the staleness rule.
- `congress-poller` permanently `degraded` ("congress mirrors unavailable"):
  honest reporting of a dead upstream.
- `prereg_readiness.py` exiting 1: the claimed 2026-08-07 gradable date is
  genuinely unreachable. The verdict is right (the dates it prints were not).
