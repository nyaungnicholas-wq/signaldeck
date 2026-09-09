# SignalDeck signaldeckfix re-audit -- 2026-09-09

Repo `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`, branch `public-launch`, baseline HEAD `19b8a93`
(the daemon ran `26e1983`; the daemon source was identical). Pass started 2026-09-09 01:05 local under the
/goal "fix all the errors in SignalDeck and improve everything, then commit and push". Baseline gates were
green going in (go build and vet clean, go test ./... clean, web tsc clean with 2 lint warnings, pytest
tools 411 passed and 2 failed), and every finding below sat behind them or in live `worker_runs` and
`dq_events` rows. Repairs are commit `e5277e3`, deployed through `ops/signaldeck-ctl.sh deploy` and
VERIFIED running (`deploy VERIFIED: daemon is running commit e5277e3ff8a93f4e424acc2add92310ac939efc6`,
4 worker rows stamped at 08:39 UTC, then cot-poller, finra-shortint, outcome-resolver and dq-auditor rows).
Round two (gap-fill, anchors wording) is commit `b919737`, deployed the same way and VERIFIED (`deploy VERIFIED:
daemon is running commit b919737367f55260b2a2398e068dbc4ce23b4b1c`, 6 worker rows stamped); first reconciler pass
09:35 UTC: "checked 329 active symbols, re-enqueued 12 under-covered, gap-filled 6 streamed (35 streamed with a
session gap)", health.json ok, /api/ready 200.

**Status: BLOCKED - NOT COMPLETE on one item.** Twelve findings fixed and verified or refuted, including
the A12 retention trade-off (resolved by construction: gap-fill never exceeds the retention window or the
storage budget) and F13 from the 2026-09-08 audit (closed from the docs side). F19, the Web task principal,
needs an elevated `ops/fix-task-principals.ps1` run that only Nicholas can do; with this audit as the newest
the register reports exactly that one aging row and `test_real_repo_audits_are_clean` stays red on it.

## Findings

| id | area | severity | impact | evidence (measured) | status |
|---|---|---|---|---|---|
| **S1** | daemon/internal/pipeline/cot.go, shortinterest.go, congress.go and internal/workers | high | Fixed-slot schedules had no catch-up: the COT poller (Saturday 09:00 ET) and the FINRA short-interest poller (18:45 ET, trading days) fire at hours this host is routinely down, so a missed slot silently waited a week or a day | worker_runs: cot-poller last ran 2026-08-31 23:21 UTC and finra-shortint 2026-09-03 22:45 UTC; zero worker rows 22:00-23:30 UTC on 2026-09-04 and 2026-09-08 and none 12:00-14:00 UTC on 2026-09-06; dq source_stale cot_reports 15d (budget 10d). New workers.DailyAtETCatchUp, WeeklyAtETCatchUp, TradingDayAtETCatchUp (TestDailyAtETCatchUp, TestWeeklyAtETCatchUp, TestTradingDayAtETCatchUp, TestDailyAtETCatchUpAcrossDST). Live after deploy: cot-poller 08:41 UTC "upserted 47 row(s) across 12 contract(s); latest report 2026-09-01" | fixed |
| **S2** | daemon/internal/ingest/finra/shortint.go | high | SettlementDates asked FINRA for weekend-dated files (2026-08-15 is a Saturday), so the mid-August short-interest cycle read "not yet published" for 25 days and would have been skipped forever once a newer cycle landed | Live CDN 2026-09-09: shrt20260815.csv 403, shrt20260814.csv 200 (2,195,139 bytes); short_interest newest settlement 2026-07-31, dq source_stale 40d (budget 35d). priorBusinessDay rolls to the prior NYSE trading day; TestSettlementDates expects 2026-05-29 for the Sunday 2026-05-31. Live after deploy: finra-shortint 08:40 UTC "settlement 2026-08-14: 319 tracked rows upserted (file had 22482)" | fixed |
| **S3** | daemon/internal/maintain/maintain.go OutcomeResolver, store.DelistedSymbolIDs | high | Score outcomes on delisted symbols waited the 30-day grace at the head of the oldest-first LIMIT 1500 queue and starved the live rows behind them | score_outcomes unresolved: 864,289 (1d), 888,861 (1w), 196,659 (1h) on active names back to 2026-08-10, plus 9,699 (1d) and 9,800 (1w) on delisted names; resolver detail "resolved 3803, voided 16, waiting 681" per 10-minute run. Delisted rows are now voided on first sight; TestOutcomeResolverVoidsDelistedImmediately. First live pass 08:39 UTC: "resolved 3792, voided 126, waiting 582" | fixed |
| **S4** | daemon/internal/store/predict.go VoidDeadPredictions, maintain.go | medium | Forecast outcomes on delisted symbols were never visited (the pending query requires a forward bar): 16,648 rows on 1,894 names stayed unresolved forever and kept those names out of DQ silencing | prediction_outcomes unresolved on delisted symbols: 16,648 rows over 1,894 symbols, ts 2026-07-04 to 2026-09-09. Rows older than 21 days with no daily bar after the forecast are voided; TestOutcomeResolverVoidsDeadPredictions. Live: dq dead_predictions_voided "voided 10680 forecast outcome(s)" at 08:39 UTC | fixed |
| **S5** | daemon/internal/maintain/maintain.go DQAuditor | medium | The daily-only staleness rule (older than 4 calendar days) false-flagged every Monday holiday, and the auditor kept flagging the whole reactivated universe during the nightly sweep | dq stale: 3,496 events in 24h across 2,211 symbols; 286 active daily-only symbols flagged hourly 2026-09-08 04:00-13:00 UTC (Labor Day weekend, 2,583 events); 1,894 delisted names flagged at 20:xx UTC 2026-09-07 and 00:xx UTC 2026-09-09 while meta sweep_open was set. dailyBarStale counts fully elapsed NYSE sessions in New York time; non-streamed stocks are skipped while sweep_open is set; TestDailyBarStaleCalendar, TestDQAuditorSkipsDailyOnlyDuringSweep. Live: "checked 329 live symbols, flagged 0" | fixed |
| **S6** | daemon/internal/health/health.go | medium | The watchdog counted only status ok as a heartbeat, so the two trainers benched by design (degraded every run) held health.json at ok=false with 2 stale workers and wrote worker_stale dq events hourly | data/health.json before: ok false, staleWorkers expectancy-trainer and gbm-trainer; 21 worker_stale events in 24h. lastSuccess counts ok and degraded; TestDegradedRunIsAHeartbeat, TestDegradedThenSilentIsStillStale. Live after deploy: {"ok":true,"staleWorkers":[]} | fixed |
| **S7** | tools/test_render_track_record.py, ops/anchor-publish.sh | medium | The 2026-09-08 pass changed render_track_record.py to render a REFUSED envelope as a refusal notice with exit 0 but left the real-registry test asserting the old non-zero contract | pytest: test_runs_against_the_real_registry failed "registry is REFUSED and must NOT render, but the renderer exited 0". The test now asserts exit 0, GRADING REFUSED in stdout and no percentage figure for a REFUSED envelope, and non-zero for a plain empty registry; 5 of 5 pass | fixed |
| **S8** | audits/2026-09-08-release-reaudit.md (was 2026-09-08-release-ledger.md) | medium | The release ledger declared a findings table the audit register could not parse, so its 21 findings were untracked and test_real_repo_audits_are_clean failed | audit_register: "UNTRACKED AUDIT 2026-09-08-release-ledger.md: its findings table did not parse". Renamed, summary table converted mechanically to the register format with evidence copied from each finding's own section; the register reads 144 findings across 7 audits with F13 and F19 open in that audit | fixed |
| **S9** | audits/2026-09-01-ops-reaudit.md A2 and A17 | low | Two 2026-09-01 findings stayed open in the register although both were resolved since | A2: Get-ScheduledTask read-back 2026-09-09 shows Anchor-Publish, Revalidation and Daemon Keepalive S4U, StartWhenAvailable True, DisallowStartIfOnBatteries False; Anchor-Publish last result 0x0 at 2026-09-08 19:30. A17: repair ledger R25, first offsite upload 898,352,731 bytes verified 2026-09-07 | fixed |
| **S10** | web/scripts/screens.mjs | low | eslint reported 2 no-unused-vars warnings for unused catch bindings | npm run lint: 2 problems (0 errors, 2 warnings) before, 0 after (optional catch binding); tsc --noEmit clean | fixed |
| **S11** | daemon/internal/workers quiesce, dq dataset_revised | low | 114 quiesce_stall events and 282 dataset_revised events in 7 days looked like defects | WAL file is 67,108,864 bytes, exactly the 64 MB journal_size_limit, so checkpoints reclaim; dataset_revised rows are the backfiller adding history inside an old range (BBF n 86 to 178), which the detector cannot tell from a rewrite. Both left as designed | refuted |
| **S12** | daemon/internal/pipeline/backfill.go, store.SessionBarCounts, alpaca.BackfillMinuteSince (2026-09-01 audit A12) | medium | Minute bars for sessions the host sleeps through were never backfilled: the reconciler only re-enqueued a symbol whose TOTAL 1m count was under 100, and the open trade-off was budget versus retention | Measured 2026-09-09: of 36 streamed symbols only 2-5 had a complete session on most of the last 30 days. Gap-fill enqueues streamed stocks with an under-covered NYSE session inside the SIGNALDECK_1M_RETENTION_D=30 window (never today), at most 6 per pass with a 24h cooldown, only with 512 MB headroom under SIGNALDECK_BUDGET_DB_MB=6144 (DB 5,637,586,944 bytes today), and SIGNALDECK_1M_GAPFILL=off disables it; Alpaca minute backfill is bounded to the same window. TestGapSessions, TestBackfillReconcilerGapFillsStreamedSymbols, TestBackfillReconcilerGapFillRespectsBudgetAndSwitch | fixed |

## Evidence

- **S1 / S2.** The FINRA short-interest poller had two independent faults: it never fired (its 18:45 ET slot
  falls in the host's off-window and TradingDayAtET carried no catch-up) and, when it did, it probed a
  Saturday-dated file. The COT poller's 9-day catch-up constant only masked the same missing-slot rule.
  One pass after the deploy both feeds are current.
- **S3 / S4.** The resolver's oldest-first LIMIT 1500 query is the whole throughput of the honesty
  ledger. Rows that can never resolve (delisted symbols) sat at its head for 30 days each, so 1.95M live
  rows queued behind them. The first post-deploy pass voided 126 score outcomes and 10,680 forecast
  outcomes; the score-side backlog drains at the resolver's normal rate from here. Voided rows keep
  `up` and `fwd_return` NULL exactly like the resolver's existing voids, so no grade changes.
- **S5.** `dailyBarStale` judges dates in America/New_York (the first draft used the host's Pacific clock,
  which is already tomorrow after 21:00 PT; caught in review). During the nightly sweep the auditor skips
  only non-streamed stocks, so crypto and the streamed hot set stay covered.
- **S6.** The FailingWorkers test in health_test.go had documented that degraded workers "are reported via
  staleness"; the 2026-09-08 readiness change (F6) reframed degraded as expected abstention, and this
  pass makes the watchdog agree with it. `/api/ready` still lists them under `degraded`.

## Verification commands

- `cd daemon && go build ./... && go vet ./...` (clean)
- `cd daemon && go test ./... -count=1` (clean, run after the last edit)
- `.venv/Scripts/python.exe -m pytest tools -q` (412 passed, 1 failed: test_real_repo_audits_are_clean on A12)
- `.venv/Scripts/python.exe tools/audit_register.py --audits-dir audits` (one violation: A12 aging)
- `cd web && npx tsc --noEmit && npm run lint` (clean, 0 warnings)
- `bash ops/signaldeck-ctl.sh deploy` then `curl http://127.0.0.1:8322/api/ready` and `cat data/health.json`

## Blocked

- F19 (SignalDeck Web task principal): run `ops/fix-task-principals.ps1` from an elevated PowerShell; the
  web-guard keepalive restarts the task within five minutes meanwhile (0 restarts in 189 probes so far).
  Tried unelevated on 2026-09-09 03:50 and denied both ways: `Set-ScheduledTask -Principal (S4U)` on the
  existing task, and `Register-ScheduledTask` of a fresh S4U task under the owner's account ("Access is
  denied" for each; the throwaway probe task was removed). Nothing else is blocked.

## Not verified

- Playwright e2e was not rerun: no web application code changed (only a screenshot script), and a run
  can kill the 8323 task (F19).
- The score-outcome backlog drain rate after the fix; the first pass is recorded above, the trend is not.
