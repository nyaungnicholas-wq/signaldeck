# SignalDeck forecast repair ledger -- 2026-09-10

Repo `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`, branch `public-launch`, baseline HEAD `e29eb6e`
(daemon running `d08d3f1`, deployed earlier that day). Pass started 2026-09-09 17:50 PT under the mission
"repair forecast quality and the evidence pipeline; determine honestly whether any candidate improves
out of sample". Three outcomes are kept apart throughout: engineering defects repaired (this table),
historical research (research/forecastplan/exp, manifest `forecastplan-exp-v1`), and prospective evidence
(the pinned grader's refusal and the registered forward tests, none of which this pass changes).

Repairs are commit `87e7f57` (daemon) with tests; audits and research modules are `1b76fac`, `63449b1`,
`9dd47de`, `d8cca71` and later commits. **Deployed 2026-09-10 03:2x UTC on Nicholas's authorisation** through the
sanctioned path `bash ops/signaldeck-ctl.sh deploy` (the session's permission classifier had blocked the first
attempt): `deploy VERIFIED: daemon is running commit b84670c9781f902253829e08f7d2cb0aea78ed59 (resolvable); 6 worker
run(s) already stamped with it`. Rollback: `git revert 87e7f57` then the same deploy command.

**Status: repairs deployed and verified live; research campaign complete and negative (see
research/forecastplan/exp/EVIDENCE_STATUS_2026-09-10.md); publication status unchanged (REFUSED).**

## Findings

| id | area | severity | impact | evidence (measured) | status |
|---|---|---|---|---|---|
| **F1** | daemon/internal/pipeline/rvforecast.go RVForecastRunner.Run | high | The HAR forward test (prereg seq 105) froze forecasts from FORMING bars and from STALE bars: `t = len(rv)-1` took whatever the last bar was. A 09:36 ET pass froze 281 rows from the 2026-09-08 session six minutes after the open; passes inside the nightly universe sweep (symbols.active widened) froze 305 rows on delisted names whose last bar dated 2024-09..2026-07, later closed as "the outcome window never became fully estimable". The registry read those as 230 call days over 223 calendar days when the study is 4 sessions old | audit_rv_coverage.py (out/rv_coverage_by_day.csv): 230 call days, 226 with 1-7 rows, 4 with >= 30 symbols (2026-09-03 301, 09-04 850, 09-08 850, 09-09 850), 3 resolved; 306 ungradable rows; rv rows for ts 2026-09-08 created 13:36:26Z (6) and 13:40:12Z (275). Fix: refuse a call bar unless `md.DailyBarSettled` and `marketcal.SessionsClosedSince(ts, now) == 0`; detail string counts "skipped: last bar forming or stale". Tests TestRVForecastRunnerRefusesFormingAndStaleCallBars (forming, stale, settled), commit 87e7f57 | fixed |
| **F2** | daemon/internal/store/rvforecasts.go UpsertRVForecast | high | A "frozen" forecast was rewritten by every later pass until it resolved (`ON CONFLICT DO UPDATE ... WHERE actual IS NULL`): the forming-bar rows of F1 were silently replaced after the close, so the record survived by luck and could be changed by any bar revision before resolution | TestRVForecastUpsertNeverRewritesEvidence previously asserted "an unresolved row did not refresh" (the refresh was tested behaviour); rows for ts 2026-09-04 show created_ts 18:06:49Z (mid-session) yet resolved with later values. Fix: `FreezeRVForecast` inserts once (`ON CONFLICT DO NOTHING`) and reports whether it wrote; the runner counts `already frozen`; the test now asserts the unresolved row is NOT rewritten; TestFreezeRVForecastReportsInsertion; commit 87e7f57 | fixed |
| **F3** | daemon/internal/api/accuracy.go collapsedGradingWindow (also cmd/collapsecheck via CollapsedGradingWindow) | high | The publication gate reproduced the graded window as "the newest distinct_days + 10 sessions". Once the ensemble abstained (from 2026-08-06) distinct_days stopped growing while sessions kept passing, so the slice was sliding forward past the 2026-07-27..08-06 collapsed days still inside the graded record; the code comment already named this as fail-open | Registry 2026-09-09: 1d distinct_days 28 -> a 38-session slice; collapsed days 07-27..08-06 would have left it within about two weeks and the REFUSED envelope would have opened on a record built on them. Fix: window starts at `store.SurvivorshipEpoch` (2026-07-24), the grader's own population boundary; over-refusal is the only possible direction. Existing collapse-gate tests pass (`go test ./internal/api/`); commit 87e7f57 | fixed |
| **F4** | daemon/internal/pipeline/predict.go PredictionRunner, maintain.go dailyBarStale, new marketcal/sessions.go | medium | Forecasts were minted for stocks whose settled daily series had missed two or more sessions; the pinned grader then EXCLUDED them after the fact (stale-feed quarantine: 2,464 graded observations over 465 symbols and 34 days on 2026-09-09; 16,648 rows on 1,894 delisted names were voided by the 09-09 fix pass). Minting them was the defect | Fix: `marketcal.DailyBarStale` (the dq-auditor's rule, moved so one definition serves both) gates the prediction loop; detail counts "stock(s) skipped: daily series stale"; maintain.dailyBarStale delegates to it (TestDailyBarStaleCalendar still passes); marketcal session tests (SessionsClosedSince, SessionClose, DailyBarStale, SessionDate); commit 87e7f57 | fixed |
| **F5** | tools/accuracy_registry.py structural verdicts vs the "internal diagnostic" (Trend21 79.3 / Liquidity21 69.3 / Vol21 56.2 / FilingsDrift21 43.6) | medium | The diagnostic and the pinned grade were being read as the same statistic; they are different cohorts. The diagnostic is every resolved regime_outcomes row; the grade is the frozen-baseline cohort (calls from 2026-07-28) after the settlement and stale-feed clauses. The unbaselined July 18-26 calls score 66.3% on vol21 and lift the pooled number | audit_structural_cohorts.py (out/structural_cohorts.csv): vol21 all-resolved 0.5623 (n 5,659) vs frozen-baseline 0.4813 (n 3,133) vs pinned 0.4861 (n 3,166); trend21 0.7927 vs 0.7948 vs 0.7956; liquidity21 0.6934 vs 0.6781 vs 0.6938; the per-day model and persistence accuracies track within 2 pp on every day. No code change: the grade is right and the diagnostic must not be quoted as a grade | refuted |
| **F6** | paper book "13 open positions, zero graded sessions" | low | The 13 positions are 10 in `flagship-1w-replay` and 1 in `flagship-1d-replay` (a finished reconstruction whose cursor stopped 2026-08-19 04:00Z and whose positions are marked at the cutoff by design), 1 live GLD in `flagship-1w` (opened 2026-09-03, 5-bar horizon), 1 manual BTC lot in `manual:u5`. Cash reconciles to the cent for all five books from the trade ledger (100,000 minus buys plus sells). The forward test (seq 87) has 3 of 60 eligible sessions (08-31, 09-01, 09-03; mean excess -0.244%), which is the registered floor doing its job | Read-only SQL 2026-09-09: cursor cash 98,232.71 (1d), 98,551.51 (1w), 90,476.94 (1d-replay), 908.14 (1w-replay), 99,210.13 (manual) each equal to the ledger-implied figure to 0.00; `tools/forward_test.py --verdict` dry run: 3/60 sessions, INSUFFICIENT EVIDENCE. No bookkeeping defect; the headline count conflates a reconstruction with the live book | refuted |
| **F7** | HAR forward test coverage expectation (60 days x 30 symbols) | low | Elapsed calendar time was being read as qualifying evidence. Filed 2026-09-04 04:19Z; sessions since: 09-04, 09-08, 09-09 (09-05/06 weekend, 09-07 Labor Day). Qualifying call days so far: 09-03 (frozen 09-04 05:37Z, before the open), 09-04, 09-08 resolved, 09-09 pending. Nothing is missing; the 60th qualifying day cannot arrive before 2026-11-27 even with no gaps | audit_rv_coverage.py summary: days_qualifying 4, days_qualifying_resolved 3, max_symbols_per_day 850 | refuted |

## Evidence

- **F1/F2.** The rule now enforced is the registration's own: a forecast is made once, from the last completed
  session, and never rewritten. Existing rows are untouched (nothing is deleted or relabelled); the 306 stale-bar
  rows already carry a stated ungradable reason and any grader of seq 105 must additionally require the call bar
  to post-date the filing, which `audit_rv_coverage.py` makes visible per day.
- **F3.** `ForecastDayStats` is read from the epoch for every horizon the registry publishes; the refusal text
  changes only in that pre-epoch 1w days (07-17, 07-18) no longer appear, and the 07-27..08-06 days keep the
  envelope REFUSED. That is the honest state of the incumbent's record.
- **F4.** For the streamed hot set the last settled daily bar during a session is yesterday's, which is zero
  elapsed sessions, so nothing streamed is skipped; the guard fires only for a series that truly missed sessions.
- **Direction diagnosis (research, not a defect).** `audit_direction_information.py` on the graded live rows
  since the survivorship epoch (dedup to one row per symbol, horizon, trading day; settlement and stale-feed
  clauses not applied): 1d n 4,723 over 37 days, accuracy 44.1%, always-up 55.9%, up-calls 28%, within-day
  AUC 0.519 (se 0.017, 24 days), pooled AUC 0.447; 1w n 9,118 over 40 days, accuracy 46.1%, always-up 54.5%,
  up-calls 33%, within-day AUC 0.498 (se 0.009, 38 days). The per-symbol ranking carries no information and the
  book leaned bearish through a rising window; there is no sign inversion to exploit (an inverted signal would
  read well below 0.5 within day) and nothing in the timestamps is misaligned.

## Verification commands

- `cd daemon && go build ./... && go vet ./... && go test ./... -count=1` (clean on 87e7f57)
- `cd daemon && go test ./internal/pipeline/ -run "RVForecastRunner|Freeze" -count=1 -v`
- `.venv/Scripts/python.exe research/forecastplan/audit_rv_coverage.py` -> `RV COVERAGE OK days=230 qualifying=4 max_symbols=850`
- `.venv/Scripts/python.exe research/forecastplan/audit_structural_cohorts.py` -> `STRUCTURAL COHORTS OK kinds=7 rows_resolved=17274`
- `.venv/Scripts/python.exe research/forecastplan/audit_direction_information.py` -> `DIRECTION INFO OK horizons=4 rows=26293`
- `.venv/Scripts/python.exe tools/audit_register.py --audits-dir audits`

## Blocked

- None.

## Verified live after the deploy (2026-09-10 03:23 UTC, revision b84670c9)

- rv-forecast-runner first pass: `froze 0 forecast(s) over 2 horizon(s) (564 already frozen by an earlier pass);
  3 symbol(s) skipped: last bar forming or stale, 36 below the 530-session floor, 2 could not fit, 0 had no usable
  regressor row, 0 lost to a locked database` (F1, F2 behaving as designed).
- prediction-runner first pass: `wrote 42 predictions (1 stock(s) skipped: daily series stale by two or more
  sessions) (7 symbol(s) scored on settled bars only ...) (42 row(s) withheld by the cross-section gate ...)` (F4).
- Gate (F3): `go run ./cmd/collapsecheck --registry data/accuracy_registry.prev.json` (a registry that carries rows)
  refuses on 18 collapsed cross-sections of 74 days, all 2026-07-26..08-06; the pre-epoch 1w days 07-17..07-23 no
  longer appear. Against the current REFUSED envelope (rows empty) the gate is vacuous by construction; the stored
  envelope is regenerated by the daily accuracy job, which is when `/api/accuracy` will carry the new wording.
- `data/health.json`: `{"ok":true,"staleWorkers":[]}`; watchdog `healthy: 99 of 102 workers checked`.

## Not verified

- The daily accuracy job's regenerated envelope (next scheduled run).
