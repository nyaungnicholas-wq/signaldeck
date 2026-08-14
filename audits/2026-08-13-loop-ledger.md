# /signaldeckfix loop — ledger (2026-08-13)

Repo `C:\Users\Nicholas_N\Desktop\claude code\signaldeck` @ `613bdf7`.
Deployed binary stamp `1cfa982` (substantively current — delta is docs/tooling only).

## Baseline (cycle 1)

| Check | Command | Result |
|---|---|---|
| Go build/vet/test | `go build ./... && go vet ./... && go test ./...` | clean |
| Web typecheck | `npx tsc --noEmit` | clean |
| Web lint | `npx eslint .` | clean |
| Python tests | `pytest tools/ ops/` | 345 passed, 4 skipped |
| Standing gates | docs_gate/schema_contract/deployment_drift/structural+research+anchor liveness/audit_register/verify_dod/live_accuracy/check_revalidation/bars_completeness | all rc=0 |
| ops self-tests | test-no-bare-pkill / lib-portable / ledger-provenance / offsite-is-offsite / backup-budget | all rc=0 |
| **manifest-check** | `bash ops/manifest-check.sh` | **rc=1 — FAIL (pre-existing)** |

Two harness traps hit and corrected while establishing this baseline, both worth
keeping because each makes a red gate look green:
- `cmd | tail` then `rc=$?` reads **tail's** exit code. Every gate "passed"
  until exit codes were captured from the gate itself.
- bare `pytest` from the repo root walks `quarantine/` and reports 140 failures.
  The sanctioned scope is `tools/ ops/`. The 140 are quarantined by design.

## Ledger

| ID | Component | Symptom | Status |
|---|---|---|---|
| L-1 | `internal/pipeline/adaptiveweights.go`, `internal/store` | **A30 carried forward.** A ROW cap feeds a DAY gate, so the adaptive layer can never un-gate and the whole fleet blends on the static prior. | **FIXED (not wired) — needs sign-off** |
| L-2 | ops/manifest-check | Working tree holds files absent from a clone of HEAD → "the published system is not the audited one". | **BLOCKED — foreign untracked files** |
| L-3 | worker_runs | 104 `orphaned` + 49 `degraded` runs in 24h, none named in `data/health.json`. | **NOT APPLICABLE — working as designed** |
| L-4 | forecast-monitor | Only worker in `status=error`; A29. | **NOT APPLICABLE — the gate is correct** |
| L-5 | `internal/api/fleethealth.go` | **Staleness watchdog is structurally blind to every long-cadence worker.** 7 workers can never be reported stale, however long they are dead. | **FIXED + MUTATION-VERIFIED** |
| L-6 | `internal/pipeline/signal8.go` | `ThirteenFPoller.NextFire` discards `last`, so a missed daily window is never caught up. Live instance was 32h overdue on 08-13 morning; **that instance has since CLEARED on its own** (ran 08-13 17:00 PT = 20:00 ET, on schedule) once the restart churn stopped — the defect is latent again, not current. | **FIXED + MUTATION-VERIFIED** |
| L-7 | `internal/api/api.go` `failingWorkers` | Same row-cap defect at a **second** call site, 5x smaller window: `/api/ready` was blind to 67 of 101 workers. | **FIXED + VERIFIED** |
| L-8 | `internal/api/fleethealth.go` `medianGap` | Cadence is inferred from observed gaps, which daemon restarts corrupt; a healthy 6-hourly worker reads as 0.4h and trips the 3x factor. | **FIXED — declared interval now wins** |
| L-9 | `internal/api/api.go` `/api/honesty` | **Third instance of the row-cap pattern.** A 5000-row read feeds a 10-distinct-day gate that can therefore never pass; the interval is permanently withheld and `distinctDays` permanently reads 1 against 56 days of real history. | **FIXED + VERIFIED** |
| L-10 | `internal/ensemble/ensemble.go` | A "KNOWN GAP" comment described a defect fixed weeks ago and argued AGAINST enforcing the day floor — advice now wrong on every factual claim. | **FIXED (comment-only)** |
| L-11 | `tools/verify_backup.py` | **Backup staleness check failed OPEN.** `except Exception: pass` around the live-DB comparison meant any read failure skipped it silently and the backup reported VERIFIED without ever being compared. Untested, and `--live` is passed on every production run. | **FIXED + VERIFIED** |
| L-12 | `.github/workflows/ci.yml` | Script-style test files (`main()`, no TestCase) are collected by NEITHER `unittest discover` NOR pytest. Measured: **3** such files ran nowhere in CI. | **FIXED + MUTATION-VERIFIED** |
| L-13 | `ops/lib-portable.sh` | **The sqlite3-CLI branch had no busy timeout** while the Python fallback used `timeout=120`, so a write racing the daemon died instantly. Cost a real backup's `backup_last_ts`, which made the failsafe take a redundant 4.7GB VACUUM against a live daemon. | **FIXED + VERIFIED** |
| L-14 | `ops/lib-portable.sh` `sd_is_running` | **Latent fail-open.** The dormant `pgrep` branch matches `signaldeckd`, never `signaldeckd.exe`, so installing procps would make it report a LIVE daemon as down and let the backup VACUUM against it. | **FIXED + VERIFIED** |
| L-15 | `web/.../desk/RecommendationCard.tsx` | **A fabricated measurement.** `o?.prob ?? 0` rendered an absent Bull/Base/Bear probability as "0%", which reads as the model RULING OUT that scenario rather than not having measured it. | **FIXED — tsc/eslint only, NOT visually verified** |
| L-16 | `daemon/internal/prereg/prereg_test.go` | **The preregistration integrity gate could not read its own subject.** `^NAME = (\d+)$` never matched a CRLF-committed grader, so every gate reported ABSENT and the test could not detect ANY divergence. | **FIXED + MUTATION-VERIFIED** |
| L-17 | this session's own verification | **`go test ./...` without `-count=1` replayed CACHED passes.** Every "full suite pass" reported before cycle 17 was partly cached; the uncached run showed `internal/prereg` failing. | **CORRECTED — all runs now `-count=1`** |
| L-18 | `internal/workers/workers.go` `Runner.Intervals` | **A data race I INTRODUCED in cycle 20.** `Intervals` read `r.workers` unlocked on a false invariant; `api.Serve` (run.go:655) starts before the final `runner.Add` (run.go:662), so an API request overlaps the append. | **FIXED + MUTATION-VERIFIED** |
| L-19 | `internal/api/fleethealth.go` `cadenceFor` | **Gap in my own L-8 fix.** It inferred a cadence for LONG-RUNNING workers (declared `Interval == 0`), which the Worker interface defines as "Run is called once and blocks". `internal/health.StaleWorkers` skips those; the two surfaces would still have disagreed. | **FIXED + MUTATION-VERIFIED** |
| L-20 | `internal/health/health.go` `FailingWorkers` | **A verdict that could never clear.** A reconnected stream ingestor stays in ONE in-flight run forever, so no completed row could break its old error streak — crypto-live was reported failing for 3h+ of healthy streaming, holding `health.json` `ok:false` the whole time. | **FIXED + MUTATION-VERIFIED** |

### L-1 — A30, reproduced live

Direct evidence, read-only against `data/signaldeck.db`, simulating
`LabeledFeatures`' own query:

```
1d: total rows=197625 days=39 | capped at 20000 rows -> days=7
1w: total rows=163864 days=33 | capped at 20000 rows -> days=3
```

`adaptive.MinCellDays = 20`. History is nearly twice the floor; the capped read
shows 7 and 3. The floor is unreachable **by construction**, so waiting for more
history can never clear it — which is why the learner's honest message ("only N
distinct trading day(s), below the 20-day floor") implies the wrong remedy.

Repair: `Store.LabeledFeaturesRecentDays(ctx, h, days, maxRows)` in
`internal/store/features_days.go` — reads the most recent N distinct **days**,
always whole days, with `maxRows` demoted to a safety ceiling that drops whole
days from the oldest end and reports `ceilingBound` when it bites. Measuring the
input to a day gate in days is the point; raising the constant only postpones
the recurrence.

**Not wired into `adaptiveweights.go`.** Wiring it changes live blend weights and
therefore published predictions, which needs Nicholas's sign-off — the same
reason the 2026-08-12 audit left A30 open.

### L-2 — manifest-check

Failing dirs: `ops/`, `research/`, `research/eighty/` at baseline (another
session's untracked `ops/schema_inventory.py`, `research/eighty/h0628-0631.py`),
plus `daemon/`, `daemon/internal/` added by this session's two new files.
Not staged: never `git add` a shared tree another session is editing.

### L-3 — orphaned runs are deploy churn, not a crash loop

21 distinct revisions ran between 19:27 and 21:10 on 08-12 (a prior session's
rebuild cycles). Orphans cluster in exactly those hours — 79 of 104 in hour 20 —
and land on the long-running workers a restart necessarily interrupts
(`crypto-live`, `stock-streamer`, `backfiller`). **Zero orphans after 08-12
23:36**; stable on `1cfa982a` for 867 runs since. Asking "is this still
happening, or did it already stop?" is what kept this from being a false finding.

### L-5 — the staleness watchdog cannot see a long-cadence worker

`fleetHealth` judges staleness from `RecentWorkerRuns(ctx, 2000)` — a fixed ROW
cap feeding a TIME-based verdict. Measured against the live DB:

```
2000-run window spans: 08-12 21:42 -> 08-13 01:21   (3.6 hours)
distinct workers inside window: 94
workers ABSENT from the window entirely: 13f-poller, congress-poller,
    cot-poller, finra-shortint, finra-shorts, signalbt-weekly, weekly-report
```

At ~3,000 runs/day the window is 3.6 hours wide, so a worker on a daily or
weekly cadence has NO rows in it. `starts[name]` is never populated, the
staleness loop never iterates over it, and `medianGap` would refuse it anyway
below `minRunsForCadence = 3`. The seven workers exempted are exactly the
long-cadence data pollers — the ones whose death is least self-evident. The
every-minute workers, which are obviously alive, are the only ones judged.

**Same shape as L-1/A30**: a row cap upstream of a gate that counts time. That
is now twice in one codebase; worth grepping for other row-capped reads feeding
time-based or day-based judgments.

Note the comment directly above this code documents fixing a *different* blind
spot in the same function on 2026-08-11 (staleness judged without regard to
status). This one survived that repair.

**The retained data is already sufficient — it is only the read that is wrong.**
`PruneWorkerRuns` (store.go:1387) deliberately preserves the newest **20 runs
PER WORKER** using the very `ROW_NUMBER() OVER (PARTITION BY worker …)` window
function this fix needs. Verified live: all seven "invisible" workers hold
exactly 20 retained runs each, spanning weeks —

```
13f-poller       20 runs  07-31 19:37 -> 08-11 17:00
cot-poller       20 runs  07-28 11:42 -> 08-08 06:00
finra-shorts     20 runs  07-30 21:15 -> 08-12 18:06
signalbt-weekly  20 runs  08-02 14:18 -> 08-09 15:00
```

against a `minRunsForCadence` of 3. So the retention layer is per-worker aware
and the reading layer is not; the history is kept precisely for this judgment
and then never looked at.

Proposed fix: read the newest K runs PER WORKER with the same window function,
and **no time floor**. A `since` bound was in the first draft of this repair and
was wrong — a worker whose newest run predates the floor drops out of the result
and stays unjudged, which reproduces the exact defect being fixed on the
longest-dead worker. Pruning already bounds the table, so the floor buys nothing
and costs the only case that matters.

### L-6 — 13f-poller cannot catch up a missed window

`ThirteenFPoller.NextFire(last, now)` returns `workers.DailyAtET(now, 20, 0)`
and **discards `last`**. `DailyAtET` pushes a fire time that is not strictly in
the future to tomorrow, so a daemon down or restarting across the 20:00 ET
window silently skips that day and never retries. Last run 08-11 17:00, i.e.
32h against a 24h schedule — and 21 daemon revisions were deployed between
19:27 and 21:10 on 08-12, straddling exactly that window.

The sibling `ShortVolPoller.NextFire` in the same repo implements the catch-up
branch and its comment names the convention ("mirrors `internal/pipeline/cot.go`
and `internal/briefing/weekly.go`"), so catch-up is the established pattern here
and 13f-poller is the outlier. `finra-shorts` was checked and is NOT affected —
7.2h old against an 18:30 ET daily fire, on schedule.

### L-7 — `/api/ready` was blind to 67 of 101 workers  (FIXED)

`Deps.failingWorkers` read `RecentWorkerRuns(ctx, 400)`, justified by a comment
claiming "a few hundred rows reliably covers one run of every worker including
the rare-cadence ones". **Measurement refutes it.** The claim confuses per-worker
RETENTION (what pruning keeps) with per-worker COVERAGE (what a global
newest-first read returns) — a global read is dominated by whichever workers
cycle fastest, regardless of how retention chose the surviving rows.

```
newest  400 rows: span 0.85 h, covers 34 of 101 workers   <- what /api/ready saw
newest 2000 rows: span 3.70 h, covers 94 of 101 workers
```

Fixed by reading per worker. Before/after on live data, applying the same
`failingFromRuns` rule:

```
OLD RecentWorkerRuns(400)   workers_covered= 55  failing_detected=2
NEW PerWorker(20)           workers_covered=101  failing_detected=4
                            newly visible: forecast-monitor (error),
                                           congress-poller (degraded)
```

`forecast-monitor` is the worker `data/health.json` already named, and
`/api/ready` previously could not see it at all — it sat outside the 400-row
window. That part is verified.

**CORRECTION (cycle 23).** An earlier version of this paragraph said the two
surfaces "now agree". Measured on identical data, they do not, and they are not
meant to:

```
agree on   : forecast-monitor
api ONLY   : congress-poller, expectancy-trainer, gbm-trainer   (all 'degraded')
health ONLY: crypto-live                                        (3 consecutive 'error')
```

`health.FailingWorkers` requires MinConsecutiveFailures=3 consecutive `error`
runs and counts ONLY `error`, routing `degraded` through staleness instead
(`lastSuccess` counts only `status='ok'`). `api.failingFromRuns` takes the newest
COMPLETED run per worker and counts `degraded` and `orphaned` as well, with an
in-flight-successor recovery rule that makes its crypto-live verdict
sampling-dependent by design. Two rules, two purposes, both documented — the fix
widened WHICH WORKERS api can see, it did not merge the two definitions of
"failing".

Regression test: `internal/store/workerruns_perworker_test.go`, 4 tests, all
passing. **Mutation-verified** — changing `PARTITION BY worker` to
`PARTITION BY 1` (making ROW_NUMBER global again, the exact regression) fails
`TestRecentWorkerRunsPerWorker_FastWorkerCannotCrowdOutSlowOne` with
"slow: expected all 3 rows, got 0 — a rare-cadence worker was crowded out",
then passes again on restore. A first mutation attempt produced uncompilable Go
and returned "setup failed", which proves nothing; it was redone as a pure SQL
string change so the failure is a real assertion failure, not a build error.

### L-8 — why the L-5 fix is NOT shipped

Wiring the same per-worker read into `fleethealth`'s staleness path was tried
and **reverted**, because it fixes the read while inheriting an unsound cadence
estimator. `medianGap` infers a worker's period from OBSERVED gaps, and a
restart re-runs workers at boot; the 21 revisions deployed 2026-08-12 between
19:27 and 21:10 left tight restart clusters, so a healthy 6-hourly worker reads
as ~0.4h cadence and then trips the 3x factor.

Measured on live data, same rule, both reads:

```
OLD global newest-2000   workers_seen= 94  judged= 94  flagged STALE=41
NEW per-worker newest-20 workers_seen=101  judged=101  flagged STALE=70
```

So the blindness fix alone trades 7 silent blind spots for ~70 false alarms —
strictly worse on a surface whose job is to be believed. Note `data/health.json`
reports 0 stale from the SAME fleet, because its watchdog uses each worker's
DECLARED `Interval()` instead of inferring one; the disagreement between the two
surfaces IS this defect.

The real repair is to judge against the declared interval in `fleethealth` too,
which needs the worker registry threaded into `Deps` (it currently has no access
to it). Left as a documented known defect in the code rather than half-changed.

NOT verified: the live `/api/fleet-health` payload — it returns 401 without a
token, so the 41-stale figure above is a simulation of that code path against
the same DB, not a reading of the endpoint.

### L-6 — 13f-poller catch-up  (FIXED)

`ThirteenFPoller.NextFire` now carries the catch-up branch its two siblings
already had, with `thirteenFStaleAfter = 26 * time.Hour` — the daily interval
plus a 2h margin, matching `finraShortsStaleAfter` exactly (`cotStaleAfter` is
9d for a 7d worker, the same interval-plus-margin rule).

Regression test `internal/pipeline/thirteenf_nextfire_test.go`, 4 tests, all
passing and **mutation-verified**: deleting the catch-up branch fails
`_MissedWindowCatchesUp` and `_BoundaryIsExclusive` and nothing else, then all
four pass on restore. The boundary test pins the comparison as strictly
greater-than, so a run exactly at 26h is still on schedule.

### L-9 — `/api/honesty` cannot ever publish its interval  (THIRD INSTANCE)

Found by sweeping for the A30/L-7 pattern — a ROW-capped read feeding a
TIME-or-DAY-based judgment. This is the most extreme case found:

```
handler: Deps.honesty  (GET /api/honesty, 60s cached)
read:    ResolvedOutcomes(ctx, 0, h, 5000)     <- 5000 ROW cap
gate:    enoughDays := len(days) >= clusterstat.MinDistinctDays   (floor 10)

1d: total 1,050,993 rows over 56 distinct days | capped@5000 spans  1 day
1w: total   702,629 rows over 51 distinct days | capped@5000 spans  1 day
```

At ~19k resolved outcomes per day a 5000-row read cannot span even two days, so
`enoughDays` is **always false**: the bootstrap confidence interval on this
endpoint is permanently withheld and `distinctDays` permanently reports 1.

The withholding itself is honest and must not be relaxed — "a withheld interval
beats a narrow one" is correct. The defect is that the stated reason implies the
wrong remedy: it reads as "not enough market days yet" when 56 days exist, 5.6x
the floor. Identical in shape to A30, where the learner truthfully reported 8
days while 41 existed.

**Fix design, CORRECTED after measuring.** The obvious day-aware read is the
wrong instrument here — measured cost of widening this request-path handler:

```
1d: 10-day read = 484,535 rows   20-day = 584,442   (today's cap: 5,000)
1w: 10-day read = 206,810 rows   20-day = 361,269
```

~97x more rows on a 60s-cached request path. Do NOT ship that.

The right fix is already proven in this repo. `ResolvedRawPredictionPairs` had
the identical defect (3,000 rows spanning 5 days for 1d and 1 for 1w) and was
repaired by deduping IN SQL — `ROW_NUMBER() OVER (PARTITION BY symbol_id,
settle_day(o.settle_ts, o.ts))`, keeping `rn=1` — so one row per symbol-day.
Measured today it spans 38 days (1d) and 34 (1w) in ~11.8k / 11.0k rows, and the
40,000 cap is not even binding.

`/api/honesty` already calls `dedupeIndependent(raw)` in Go immediately after
reading, so pushing that dedupe into SQL buys the day span AND removes work,
rather than trading one for the other.

**APPLIED.** `Store.ResolvedOutcomesIndependent` in
`internal/store/outcomes_independent.go`, wired at `api.go` in `Deps.honesty`.
Measured effect at the SAME 5,000-row limit — no extra rows read:

```
before: 5,000 raw rows  ->  1 distinct day   (floor 10: never passes)
after:  5,000 indep obs ->  22 days (1d), 20 days (1w)
```

**A test caught a regression in this fix and it was right to.**
`TestHonestyGate_BelowMinIndependentN` asserts `rawN >= 100` while
`independentN == 3` — the gap between those two numbers IS the endpoint's
honesty message ("we read 120 raw rows; only 3 are independent evidence").
Deduping in SQL erased it, because the collapsed rows never reach Go and
`rawN := len(raw)` silently became equal to `indepN`. That would have traded one
honesty defect for another.

Fixed properly rather than by adjusting the test: the query now also carries
`COUNT(*) OVER (PARTITION BY symbol_id, settle_day(...)) AS grp_n`, and the store
returns `rawRows` as the sum over returned rows. That figure is stricter than the
old one — it is the raw evidence actually behind the returned observations,
rather than "however many rows this reader happened to fetch". All 8 honesty
tests pass unchanged.

### L-10 — a stale comment arguing against a fix that already landed

`ensemble.Calibrate`'s "KNOWN GAP" block claimed the fleet-wide calibration map
"is still fitted on ~5 clustered days", because its caller read "the newest
calibrationPairLimit=3000 ROWS with no timestamp", and that closing the gap
"would require deduping to one pair per (symbol, trading-day) … a change to the
store query".

Every factual claim is now false. That store change was made:
`ResolvedRawPredictionPairs` dedupes with `ROW_NUMBER() OVER (PARTITION BY
symbol_id, settle_day(...))`, **returns days** alongside the pairs, and the cap
is 40,000. `pipeline.globalCalibration` already enforces a day floor
(`calibrationMinDays = 10`) on those days. Measured 2026-08-13: 1d = 11,833
pairs over **38** days, 1w = 11,021 over **34** — the cap is not even binding.

Why this is a defect and not just untidy prose: the comment actively argued
against enforcing the floor, so a future reader would either distrust live
calibration or re-do work already done. This session was itself misled once by
exactly this failure mode — the `RecentWorkerRuns(400)` justification in L-7,
which asserted coverage that measurement refuted. Corrected in place rather than
deleted, so the reasoning that was once right is visible as superseded.

Comment-only: no behavior change, `go build`/`vet` clean, ensemble and pipeline
suites pass.

### Row-cap sweep — scope of the pattern (cycle 7)

Swept every remaining row cap. The discriminator is whether the read is **pooled
across entities** or **scoped to one**:

| cap | read | verdict |
|---|---|---|
| `adaptiveMaxRows` 20000 | pooled across symbols | **DEFECT (L-1/A30)** |
| `RecentWorkerRuns(400/2000)` | pooled across workers | **DEFECT (L-7/L-5)** |
| `ResolvedOutcomes(…5000)` | pooled across symbols | **DEFECT (L-9)** |
| `calibrationPairLimit` 40000 | pooled, but deduped per symbol-day in SQL | already fixed |
| `expectancyMaxRows` 5000 | `LegValuesBySymbol` — one symbol | safe |
| `gbmMaxRows` 5000 | `LabeledFeaturesBySymbolVersion` — one symbol | safe |
| `pressureMaxRows` 20000 | `LabeledFeaturesBySymbol` — one symbol | safe |
| `symbolAgentMaxRows` 5000 | `LabeledFeaturesBySymbol` — one symbol | safe |

A per-symbol cap is harmless because one symbol emits roughly one row per day, so
N rows buys ~N days. The defect needs many entities competing for the same row
budget, where the fastest crowd out the days. Worth stating explicitly, because
"row cap near a day gate" alone would flag four safe sites as false positives.

## Adversarial pass over THIS session's own repairs (cycle 9)

Every change was re-audited as if reviewing someone else's work; three claims held, one measurement was wrong and was redone, and one real regression was found in the author's own fix.

1. **settle_day drift — RULED OUT.** Risk was that the SQL dedupe keeps a different row than the Go dedupeIndependent. It cannot: internal/store/settledayfn.go registers settle_day as a direct call into md.SettleDay, deliberately so one implementation exists rather than two spellings drifting.

2. **The day-count measurement used the WRONG day function — redone.** Earlier figures emulated the day fold as ts/86400, but SettleDay delegates to TradingDay, which is floor((ts - TradingDayOffsetSecs)/86400) with a 5-hour offset. Re-measured with exact semantics the claim HOLDS: 22 distinct settle-days for 1d and 20 for 1w at limit 5000, against a floor of 10. The offset shifts group boundaries, not the count.

3. **L-9 is a 6x LATENCY regression on a request path — accepted, with a follow-up.**
```
OLD raw + LIMIT      5,000 rows in 0.25 s
NEW dedupe window    5,000 rows in ~1.4 s   (upper bound)
```
score_outcomes carries exactly ONE index, idx_outcomes_unresolved ON (resolved_at) WHERE resolved_at IS NULL, which is the exact complement of this query's "resolved_at IS NOT NULL" filter, so every variant full-scans. A time-bounded inner scan was tried and barely helped (1.26s at 90 days) for the same reason. The 1.4s is an UPPER BOUND because it was measured with a Python settle_day callback invoked ~2 million times, where the daemon uses a registered deterministic native function. Accepted because /api/honesty sits behind a 60s response cache, so the cost is at most once per minute per horizon, and it buys a statistic that was previously impossible to publish at all. Follow-up needing sign-off: an index on score_outcomes (horizon, ts) would remove it, but that is a migration on a 5GB live database and must not be done unasked.

4. **L-9 changed a PUBLISHED field's meaning — the copy still reads true.** rawN was previously "rows this reader fetched" (at most 5,000); it is now the raw rows the returned observations stand for — 658,709 for 1d. Every consumer was checked: web/src/app/lab/honesty/page.tsx renders it as "N raw minute-cadence rows collapse to M independent (symbol, UTC-day) resolutions", which is exactly what the new number means, and a far stronger statement of pseudo-replication than the old "5,000 to 219" that described only the single day it happened to read.

5. **L-6 cannot fire-storm — RULED OUT.** The worry was that a persistently failing 13f-poller would spin, since NextFire returns now while stale. scheduledLoop sets last = time.Now() BEFORE runOnce and unconditionally of outcome, so the next evaluation sees a fresh last and returns to the 20:00 ET slot. The zero-time contract that lastRunAt documents ("a miss must be treated as run at the next scheduled slot, not run now") is honored by the !last.IsZero() guard and pinned by the _ZeroLastUsesScheduleNotCatchUp test.

6. **L-7 costs nothing.** worker_runs holds 3,409 rows because pruning keeps it small; old and new reads both run in 0.005s.

### L-11 — the backup staleness check failed OPEN  (FIXED)

`verify_backup.verify()` check 6 compares the backup's `prediction_ledger` count
against the live DB and reports a backup holding under half the live rows. It
ended in `except Exception: pass`. Any failure to read the live DB skipped the
comparison, left `problems` empty, and the backup reported **VERIFIED without
ever being compared** — the exact class this repo keeps producing, on the one
artifact whose silent absence is discovered only when it is needed.

Three things make it more than theoretical:
- `ops/signaldeck-backup-offline.sh:253` passes `--live "$DB"` on **every** run,
  so the check is always requested.
- The live DB is a ~5GB SQLite under continuous write load, so "could not read
  it" is an ordinary outcome, not a rare edge.
- It had **zero test coverage** — grepping `test_verify_backup.py` for
  live/stale returns nothing.

Checks 3 and 4 in the same function already do the right thing
(`except sqlite3.Error: problems.append(...)`), so the file's own convention was
correct and this one branch deviated. Now fails closed, and distinguishes
"not requested" from "requested but could not run".

Regression test: `tools/test_verify_backup_staleness.py`, 5 tests,
**mutation-verified** — restoring the original swallow fails exactly
`_missing_live_db_is_a_problem_not_a_skip` and
`_unreadable_live_db_is_a_problem_not_a_skip`, and nothing else.

### L-12 — a test file no CI runner can see

`tools/test_verify_backup.py` exposes a `main()` rather than a `TestCase`, so
CI's `python3 -m unittest discover -s tools -p 'test_*.py'` and pytest both
collect **zero** tests from it. Only `ops/selfimprove-loop.ps1` runs it, via an
explicit branch that shells out to `python <file>` when the source has no
`TestCase`. That remediation was applied to the selfimprove loop and never to
CI — a one-site fix for a two-site problem.

Left open rather than "fixed" by adding a TestCase to that file: **that would
make it worse.** selfimprove-loop branches on `class \w+\(.*TestCase`, so adding
one flips the file to the unittest branch and silently stops running its
`main()` checks. The new L-11 tests were therefore put in a SEPARATE file, which
all three runners see. Verified: pytest 350 passed, `unittest discover` Ran 335
OK including the new class, and the standalone check still passes.

### L-13 — a backend asymmetry cost a real backup's bookkeeping  (FIXED)

Found by pulling on a question rather than a grep: the in-daemon `db-backup`
failsafe is documented to fire "only when the offline backup has actually missed
a day" — so why did it take a full 4.6GB backup at 08-12 23:10, five hours after
the offline script had written a perfectly good one at 17:53?

Traced through the evidence:

```
logs/backup-offline.log
  2026-08-12T17:57:00  OK: signaldeck-20260812-175306.db (4749 MB)
  2026-08-12T17:57:32  sha256: 94c6e2c6...
  Error in 2nd command line argument: database is locked
  2026-08-12T17:57:32  WARN: meta update failed
```

The offline script starts only when the daemon is DOWN, but writes
`backup_last_ts` about four minutes later — by which time the daemon was back
up. The write hit a lock, the failure was downgraded to a WARN, and the script
exited 0. `backup_last_ts` stayed at 08-11 16:48, so the failsafe's 30h gate saw
30h+ elapsed and ran a **redundant full VACUUM INTO against a live daemon** —
precisely the contention that gate exists to prevent — plus ~4.6GB of duplicate
disk on a machine memory records as having ONE physical volume.

**Root cause: the two backends did not agree.** `sd_sqlite`'s Python fallback
opens with `timeout=120`; its sqlite3-CLI branch passed no busy timeout at all,
so it failed the instant the DB was locked. The CLI is preferred and has been
installed since 2026-08-04, so the timeout-less path is the one that runs.
`sd_sqlite_read` had the identical gap — and that one backs the pre-rebase and
reference-transaction git hooks, where a lock-time failure is a silent WRONG
ANSWER ("nothing referenced"), the same shape as the CRLF defect its own comment
documents.

This is the third time this file has been bitten by one backend carrying a
property the other lacks; its path-conversion comment already says "the two
backends must not each carry their own copy of this". Fixed in the shared helper
with `-cmd ".timeout 120000"` on both CLI branches — a dot-command rather than a
prepended `PRAGMA busy_timeout`, because the pragma prints its value and would
corrupt every caller that reads a result.

Regression test in `ops/test-lib-portable.sh`: hold a real `BEGIN IMMEDIATE`
lock for 3s in a background process, race a write against it, assert the write
WAITS and lands. **Mutation-verified** — removing `-cmd ".timeout 120000"` fails
exactly those two checks and nothing else.

NOT changed: the meta-update failure is still a WARN rather than a non-zero
exit. With a 120s timeout a failure now means something genuinely wrong and
arguably should fail the run, but flipping a successful 4.7GB backup to
"failed" is an operational call, not an audit one.

### L-14 — a dormant branch one `apt install` away from breaking backups  (FIXED)

`sd_is_running` prefers `pgrep`, which is absent under Git Bash — so on this
machine the branch never executes and the powershell.exe branch answers. But it
matches `pgrep -x "$name"`, and on Windows the process is `signaldeckd.exe`. If
procps were ever installed, that branch would activate, miss the daemon, and
`return 1` immediately — reporting a LIVE daemon as down and letting
`signaldeck-backup-offline.sh` run a multi-gigabyte VACUUM INTO against it. That
is the exact fail-open direction the function's own header says must never
happen.

This is not hypothetical for this file: its path-conversion comment records that
installing a sqlite3 CLI on 2026-08-04 "silently switched sd_sqlite onto the
unconverted path and every backup began failing". Same shape, different
prerequisite.

`sd_kill_hard` already falls THROUGH when `pkill` misses; `sd_is_running`
returns. Fixed by probing `"$name.exe"` as well rather than by falling through —
falling through would break macOS, where the later branches are all absent and
the fail-safe `return 0` would then report every dead process as running.

Regression test: a fake `pgrep` on PATH that only knows `*.exe` names, i.e. the
machine this defect needs. **Mutation-verified** — removing the `.exe` probe
fails exactly that check.

### Audit of the remaining lib-portable.sh backends — CLEAN

The dual-backend seam produced L-13 and L-14, so every other function in the
file was checked for a property one branch has and the other lacks. Verified
behaviourally under the REAL Git Bash (`C:\Program Files\Git\bin\bash.exe`), not
Claude's shell, because the two differ on exactly these tools:

| function | finding |
|---|---|
| `sd_is_running` / `sd_port_listening` | fail-safe DIRECTIONS are correct and opposite by design (UP vs NOT-listening), and documented. Live check: daemon TRUE, bogus FALSE, port 8322 TRUE. |
| `sd_days_ago` / `sd_dow_days_ago` | BSD `date -v` vs GNU `date -d`; both agree with an independent computation at 0/1/7/30 days, dates and weekdays. |
| `sd_kill_hard` | falls through on a `pkill` miss and returns 1 when no backend works — fails closed. |
| `sd_notify` | always writes the log line regardless of which notifier exists, which is the documented point. |
| `sd_svc_start` | already covered: reports 2 for an unregistered service rather than a generic failure. |

Environment fact worth keeping: `pgrep` is ABSENT and `powershell.exe` /
`tasklist` PRESENT in both Git Bash and Claude's shell, so the powershell branch
is the live one today.

### Cycle 13 — git hooks, scheduled tasks, health checkers: NO DEFECTS FOUND

Recorded as a clean result rather than omitted, because a cycle that finds
nothing looks identical to a cycle that did nothing. Four hypotheses were raised
and all four were refuted by evidence:

1. **Relative `core.hooksPath` might not resolve from a subdirectory.**
   REFUTED. `core.hooksPath = ops/githooks`; git 2.55.0 reports `ops/githooks`
   from the root and `../ops/githooks` from `daemon/`, and the directory
   resolves in both. All three hook self-tests pass (pre-push, pre-rebase,
   reference-transaction).

2. **`check-task-health.ps1` reports OK while printing non-zero exit codes.**
   REFUTED — it is already correct, and more carefully than expected. It
   maintains a benign-code list, flags genuinely failing tasks, and deliberately
   SKIPS judging its own exit code because its `LastTaskResult` IS its own
   previous verdict; without that skip one failure latches red forever. Its own
   `0x1` came from a real `SignalDeck Web` fault on 8/12 19:59 that has since
   cleared. NOTE: that self-latch fix is dated 2026-08-13 and arrived in the
   working tree from a CONCURRENT session — not this session's work, and left
   alone.

3. **`SignalDeck Check-Grader-Health` has never run** (`LastRun 11/30/1999`,
   `0x41303`). Benign: it is registered with `NextRun 09:20` and was installed
   after yesterday's 09:20 slot. Run manually to prove it is not the
   red-by-construction check memory records: `RESULT: ok`, heartbeat fresh at
   600 min against the corrected 26h threshold.

4. **L-7 might block deploys** by making `/api/ready` list more failing workers.
   REFUTED, and this one mattered because it is a consequence of THIS session's
   own change. `/api/ready` has exactly one consumer, `ops/docker-entrypoint.sh`,
   which logs and continues — "Report it, but do not block the demo on it" —
   and `signaldeck-ctl.sh deploy` does not read it at all. L-7 will surface more
   failing workers there and gate nothing.

Fleet state at 07:35: 16 scheduled tasks, all `S4U` (Interactive=0, so the
console-kill exposure memory records is not present), both service ports
listening, `0x800710E0`/`0x41301` only on tasks that are currently Running.

## INCIDENT — I BROKE THE LIVE WEB UI (cycle 10, found cycle 14)
**Self-inflicted, currently live, needs an elevated restart to clear.**

CAUSE: running `npx next build` in cycle 10 to exercise the production build rewrote web/.next/ underneath an ALREADY-RUNNING `next start` server.
```
SignalDeck Web task started   2026-08-13 00:00:04
web/.next/BUILD_ID rewritten  2026-08-13 05:29:19   <- my build
```

MECHANISM: the running server still holds the midnight build manifest, so it serves HTML referencing the NEW chunk names and then 404s them. The files DO exist on disk; the static handler is keyed to the build it loaded at boot.

MEASURED IN THE BROWSER:
```
bodyFont          "Times New Roman"
bodyBackground    rgba(0, 0, 0, 0)
totalCssRules     0
```

SCOPE: app-wide, not one page. /login and /welcome css return 404, / css returns 500. The UI renders completely unstyled for anyone using it right now.

NOT REPAIRED, DELIBERATELY: ops/restart-web.ps1 is the sanctioned remedy. Its own docstring says "MUST RUN ELEVATED", says it kills the node process holding port 8323, and records this as audit finding A24 — the same stale-bundle situation, which previously could not be cleared from an unelevated session because Stop-ScheduledTask does not cascade to the session-0 orphan and `schtasks /End` returns SUCCESS while the socket stays held. Killing a process is not something to do unasked, and the gentle path is documented not to work.

REMEDY for Nicholas, from an ELEVATED PowerShell:
```powershell
powershell -File "C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\restart-web.ps1"
```
verify by re-requesting the stylesheet the login page references and expecting 200 rather than 404.

CONFIRMED UNFIXABLE FROM THIS SESSION (cycle 15, measured, not assumed):
`[Security.Principal.WindowsPrincipal]::IsInRole(Administrator)` = **False**, and
port 8323 is held by `node pid=33176 session=0` — exactly the session-0 orphan
`restart-web.ps1` was written for. Still broken at cycle 15: the login page
references `3dvabgaibcole.css`, that file EXISTS on disk, and the server still
404s it, which fits the running process holding a handle to the `.next`
directory the build replaced. Only a restart clears it.

LESSON: **`next build` is not a read-only gate when the server serving that directory is live. Build to a scratch directory, or restart the server as part of running the build.** Note this is the same class as the repo's own deploy rule, where build_from_head builds from `git archive HEAD` into a temp dir precisely so it is "not the working tree".

### Cycle 16 — observation, NOT a defect: hard-coded paths in `tools/alpha/`

Five research scripts pin absolute machine-specific paths:

```
tools/alpha/fetch_delisted.py:29       ENV = C:\...\stock-trader\.env
tools/alpha/fetch_form25.py:64         ENV = C:\...\stock-trader\.env
tools/alpha/fetch_sp500_removals.py:31 ENV = C:\...\stock-trader\.env
tools/alpha/smoke_real.py:10           DB  = C:\...\signaldeck\data\signaldeck.db
tools/alpha/xsection.py:40             DB  = C:\...\signaldeck\data\signaldeck.db
```

Every other tool in `tools/` derives the DB from its own location
(`dirname(__file__)/../data/signaldeck.db`), so this deviates from a clear
in-repo convention, and three of them reach into a DIFFERENT project
(`stock-trader`) for credentials.

**Downgraded from "live defect" after checking.** A first pass looked like
`ops/overnight.ps1` and `ops/accuracy_gates.py` invoked `tools/alpha/xsection.py`
— they do not. Both match the bare string `xsection` because
`accuracy_gates.py` has a GATE of that name (`python ops/accuracy_gates.py
xsection` → `gate_xsection`). Nothing in `ops/`, `.github/` or `daemon/` runs
`tools/alpha/*.py`; the only references are comments and the JSON artifacts they
produce by hand. `xsection.py` also accepts `--db`, so the constant is a default,
not a hard wire.

What remains real is narrower: the prereg data-integrity spec names
`tools/alpha/xsection_ic.json` and `xscore_result.json` as artifacts, so
REGENERATING those on another machine would fail at these paths. That is a
cold-clone reproducibility gap in hand-run research tooling, not a break in
anything scheduled. Recorded rather than fixed — unprompted edits to research
one-offs are out of scope for a repair pass.

### L-17 — my own verification was replaying a CACHED pass

Every "full Go suite pass" reported before cycle 17 ran `go test ./...` WITHOUT `-count=1`, so Go replayed cached results.

Demonstrated directly:
```
go test ./internal/prereg/            -> ok  (cached)
go test ./internal/prereg/ -count=1   -> FAIL
```

It surfaced only because `go test -race` bypasses the cache. The race run itself found ZERO data races. The uncached full suite then showed exactly ONE failing package, internal/prereg, and everything else genuinely passing — so the other fixes in this session hold.

Correction applied: every Go verification from cycle 17 on uses `-count=1`.

Lesson: a cached PASS is indistinguishable from a real one, and this repo's recurring failure shape is a check that reports success while not doing the work. The verification harness had the same shape as the defects it was hunting.

### L-16 — the preregistration gate could not read its own subject (FIXED)

`TestGradingProtocolMatchesTheActualGrader` compares the registered protocol against constants greped out of `tools/accuracy_registry.py`, so a loosened refusal threshold cannot diverge silently from the registered one.

It failed with "grader source does not define MIN_INDEPENDENT_N at all", and the same for MIN_DISTINCT_DAYS, MIN_DISTINCT_BLOCKS and MAX_ALPHA. The grader DOES define all four: MIN_INDEPENDENT_N = 30 (line 80), MIN_DISTINCT_DAYS = 10 (86), MIN_DISTINCT_BLOCKS = 10 (99), MAX_ALPHA = 0.05 (228).

Root cause: the file is committed entirely CRLF (3106 CRLF lines, 0 bare LF) and `.gitattributes` pins it `-text` deliberately, because its SHA-256 is in the pre-registration chain and its bytes must be identical on every platform. The parser used `(?m)^NAME = (\d+)$`; with `(?m)` the `$` matches before the `\n` but AFTER the `\r`, so it never matched.

Consequence: the gate could not detect ANY divergence between the registered protocol and the executed grader. It was not merely red, it was blind, and reported the wrong reason.

Fix: `\r?$` in both `graderConst` and `graderFloatConst`. This does NOT weaken the check; before the fix it compared nothing. Explicitly NOT fixed by normalising the grader to LF: that would rewrite bytes the prereg chain has already hashed.

Attribution, checked: the committed blob is also CRLF and byte-identical to the worktree (sha cb3c01e91a04b8d8), so nothing changed today and this is not new.

MUTATION-VERIFIED: temporarily changing the grader's MIN_INDEPENDENT_N from 30 to 31 now fails with "grader says 31, registered protocol says 30 — the registered refusal rule and the executed one have diverged". The file was restored byte-identically, same sha256.

Full uncached suite green afterwards: `go build`, `go vet`, `go test -count=1 ./...` all clean.

### Cycle 19 — a WAL "incident" that was NOT one, and what it revealed

Observed `WAL: CRITICAL (3,794.0 MB)` with `RESULT: unhealthy` (real exit code
1) against a 5,005 MB database. Measured the growth, then re-measured:

```
18:20   WAL 3,843 MB   (growing ~5 MB/min)
18:22   WAL    64 MB   (truncated to journal_size_limit)
18:23+  WAL    64 MB   stable, check reports "WAL: ok"
```

**Not escalated, because it had already stopped.** `vacuumed=false` on both
governor runs, so the spike was not the governor's own VACUUM; something wrote
heavily for a few minutes and the WAL then returned to its limit. Reporting the
first reading would have been a false incident — the A29 lesson ("is this still
happening, or did it already stop?") applied to my own observation.

Two things worth keeping:

1. **`check-grader-health` point-samples a metric that swings 60x in minutes.**
   It is a DAILY task reading an instantaneous WAL size with no persistence or
   hysteresis requirement, so its verdict depends on when it happens to fire: the
   same fleet reads CRITICAL at 18:20 and ok at 18:23. That is not a defect I
   fixed — a 3.8 GB WAL is worth knowing about even transiently — but a single
   daily sample of a spiky value can both false-alarm and miss a sustained
   problem, and the check makes no distinction between the two.

2. **The checkpoint ladder is permanently BUSY and that is working as designed.**
   Every hourly pass reports `TRUNCATE ... BUSY — WAL NOT truncated`, yet the WAL
   still returns to 64 MB because `journal_size_limit` caps the file
   independently. The `quiesce_stall` dq events (~1/hour, `timedOut=true`) are
   the honest report of the same condition: the drain deliberately excludes
   never-finishing stream ingestors, so a busy fleet simply does not grant a
   reader-free moment. Both are the documented BUSY rung, not a fault.

Also confirmed this cycle: `crypto-live` fails every minute with
`tickstream unreachable ... 127.0.0.1:8321` — an EXTERNAL project (TickStream)
that is not running on this machine, reported honestly, same class as hud-sync's
trader-hud dependency. Not a SignalDeck defect.

**A third repeat of my own harness trap:** I read `rc=0` from
`powershell … | tail -6` and briefly concluded the health check exits 0 while
reporting CRITICAL. `$?` after a pipe is `tail`'s status. Measured properly it
exits 1, correctly. This is the same mistake recorded in the Baseline section of
this ledger, made twice more since.

## Not verified this cycle

`go test -race`; web e2e/playwright; live external-provider behavior; whether
the day-aware read clears the floor **in production** (only simulated read-only
against the live DB: a 20-day read costs ~78k rows for 1d and ~80k for 1w,
roughly 4x today's pooled 40k).
