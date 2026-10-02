# /signaldeckfix ledger — 2026-09-23/24

Repo `signaldeck`, branch `public-launch`, inspected at 0cf4f7e. Daemon runs 7a963da
(0cf4f7e..7a963da touches no daemon/web code). Pre-existing uncommitted: 10 generated docs
from the 14:06 PT scheduled accuracy-registry run — left untouched, not in this change.

Status vocabulary: FIXED (code changed + verified locally, awaiting deploy/CI where noted),
VERIFIED (checked, healthy or correct as-is), BLOCKED (needs Nicholas), NOT APPLICABLE (not a defect).

## Defects found and repaired

| ID | Component | Symptom | Root cause | Repair | Verification | Status |
|---|---|---|---|---|---|---|
| S01 | ops + daemon | Daemon restarts in clusters of 3 (09-22 01:29/01:46/01:59Z, 09-23 01:52/02:07/02:20Z, 21:02/21:17/21:31Z); 3-35 in-flight runs orphaned per kill; congress-poller orphaned twice (53h without a good run) | Market-Close and Daily-Refresh stop the daemon with `schtasks /End` = TerminateProcess; the SIGTERM handler (`main.go` NotifyContext) never runs. Clusters come from StartWhenAvailable catch-up after logon. Matched to the second in TaskScheduler/Operational + refresh.log + backup-offline.log | `cmd/signaldeckd/stopfile.go`: `stopOnFile` cancels the same ctx when `data/.stop-request` appears (leftover removed at boot). `ops/lib-portable.sh sd_svc_stop`: writes the stop file, waits up to `SD_STOP_GRACE` (150s, see R2) for exit, then `/End` as fallback | `TestStopFile` (leftover ignored + removed; request cancels + consumed); `test-lib-portable.sh` new check passes, FAILS with the stop-file write mutated out; `go build/vet` clean | FIXED — end-to-end (a real maintenance stop logging "stop requested via stop file", zero orphans at next boot) needs the deploy |
| S03 | pipeline calibration | 1d withheld on every pass: gate reads p95-p05 0.018-0.020 while the stored raw 1d spread is 0.18-0.25 | `ResolvedRawPredictionPairs` (the fleet calibration fit) had no epoch filter: 1d map fit on 12,439 pairs / 53 days, only 687 / 19 days inside the graded window (GradingEpochTS, prereg seq 117). The July survivor-seeded universe and the 07-27..08-06 collapse fit a near-flat map: every 1d pass squeezed into 1.8pp, agreement 1.000 | `AND o.ts >= GradingEpochTS` in the fit query — the same population rule every grading consumer was repointed to on 09-20 (the function's own doc says fit and grade must agree) | Probe with the real `ensemble.CalibrateRanking` on live rows: all-history -> spread 0.0176 agree 1.000 usable=false (reproduces live); graded window -> isotonic wins, ranking-collapse refusal publishes raw, spread 0.2316 agree 0.621 usable=true; 1w unchanged either way. 5 fixtures with pre-epoch dates moved inside the window (whole weeks, weekday preserved); store + pipeline suites green | FIXED — changes what the 1d shadow record writes; needs the deploy |
| S04 | backfill-reconciler | 35 of 36 streamed stocks have no 1m bars since 09-18; health all green | Gap-fill paused every pass ("db 6108 MB of 6144 MB budget") and the run was filed `ok`; the paused branch never even counted gaps | Always count streamed session gaps; gate only the enqueue on budget; paused + gaps -> `workers.ErrDegraded`. Per-pass cap and cooldown kept | `TestBackfillReconcilerGapFillRespectsBudgetAndSwitch` now asserts degraded + gap counted; FAILS against HEAD's code; both gap-fill tests pass | FIXED (reporting). The budget itself: BLOCKED (B4) |
| S05 | dataset-version-runner | 78 `dataset_revised` "provider rewrote history" events in 3 days (72 on 09-20); BTC/USD 27 revisions | Versioned slice included the still-forming newest daily bar; every sampled stored slice ended on a bar that was forming when hashed | Version settled bars only (older than 3d); a pre-lag stored slice (ends inside the lag) is re-baselined, not compared, so the deploy does not emit one false event per symbol; a settled-history rewrite still fires | New `datasetver_settle_test.go` (forming-bar edit silent; settled rewrite fires; pre-lag snapshot re-baselined then still catches a rewrite) | FIXED — no consumer reads the event (alert only), so no data was quarantined on the false positives |
| C01 | CI daemon job | `test-docker-build.sh` 12 FAIL since 8b65747 | 8b65747 pinned 5 more files in `build_manifest.SOURCE_ARTIFACTS`; fixture's hand-kept stub list went stale -> "pinned=6 missing=5" refusal | Fixture reads the list from `SOURCE_ARTIFACTS` | `bash ops/test-docker-build.sh` 30 passed, 0 failed | FIXED — CI confirmation needs a push |
| C02 | CI tools job | 3 `DevBoxStagingLockTests` FAIL: `set: Illegal option -o pipefail` | Tests ran the bash dev-box script with `POSIX_SH` (dash on Ubuntu) | Run it with bash | Reproduced with Git's `/usr/bin/dash`: HEAD 3 fail in the class, fixed 5/5 pass; whole file under MSYS dash differs from HEAD by exactly those 3 | FIXED — CI confirmation needs a push |
| C03 | CI docs-gate | `DOCS_INDEX.md is stale` | 09-20 hand-edited the generated index (DATA_SOURCES row, with free text in the closed-vocabulary backtest_data column); the registry still said 0.2.0 FROZEN | Registry now matches the document (1.0, 2026-08-04, ACTIVE, backtest_data none); index regenerated | all 7 docs-gate CI steps exit 0 locally | FIXED |
| C04 | ops test | `test-selfimprove-loop.ps1` "no gate body calls exit" FAIL | Regex matched the string literal `"GATE-FAIL: $m exit $LASTEXITCODE"` | Strip string literals before matching | 21/21 pass; mutation: `$r = 1; exit 1` still detected | FIXED |
| W01 | web Shell | Failed logout navigated to /login anyway (daemon's honest 500 swallowed) | `.catch(() => {})` then `.finally(navigate)` | Navigate only on success; alert on failure. `/api/auth/*` is anonymous-allowed, so an expired session still logs out cleanly (no false alarm) | tsc, eslint, 42/42 web tests | FIXED — needs web rebuild |
| W02 | web sitemap | Sitemap listed `/health`, which robots.txt disallows | Sitemap filtered only `/login` | Also filter `/health` | tsc, eslint | FIXED — needs web rebuild |

## Adversarial review (fresh context) — gaps found in the repairs above, and their outcome

| ID | Gap | Outcome | Evidence |
|---|---|---|---|
| R1 | Stop written while the daemon was still opening its store was deleted as a "leftover"; `-sic-bulk` one-shot could consume a stop meant for the daemon | FIXED: only a file older than process start is removed; watcher armed after the one-shot returns | `TestStopFileWrittenDuringStartupIsHonoured`; fails with the mtime check mutated out |
| R2 | 90s grace < ShutdownGrace 75s + journal close 30s + checkpoint | FIXED: `SD_STOP_GRACE` default 150 | lib-portable suite green |
| R3 | `sd_is_running` answers "running" when it cannot tell, so a stop can wait the full grace | NOT APPLICABLE (by design): fail-safe direction (a false "not running" lets a heavy job onto a live DB); costs time, never data | pre-existing comment in lib-portable.sh |
| R4 | Nothing failed if the S03 epoch filter was removed | FIXED: `TestResolvedRawPairs_ExcludePreEpochRows` | fails with the filter mutated out |
| R5 | Publish gate asked 5% distinct (1/20) while the pinned collapse detector (forecastmon.MinDistinctRatio 0.15) refuses the graded window below 15% — a 1d (or 1w) pass at 5-15% would publish and then block grading | FIXED: `ensemble.MinDistinctRatio = 0.15`, need = ceil(0.15 N); pin test `TestPublishGateMatchesCollapseDetector` (fails against the old gate). Strictly more conservative; every real-day fixture keeps its verdict (08-06 now refused on distinct as well as spread, matching the detector that named it collapsed). Probe pass under S03: 227/282 = 0.80 distinct | ensemble + forecastmon suites |
| R6 | `cmd/caldiag` still mirrored the unfiltered fit | FIXED: same epoch filter | build |
| R7 | `degraded` reaches `/api/health` workers map but no alert channel | NOT APPLICABLE (by design): same visibility as every other degraded worker; alerting policy unchanged | health.go:408 |
| R8 | S05 re-baseline judged by today's clock, so a pre-fix snapshot checked >3d later still fired falsely | FIXED: `stored.LastTs > stored.CheckedAt - lag` | `TestDatasetVersionRebaselinesAnOldPreLagSnapshotCheckedLate`; fails with the old condition |
| R9 | Re-baseline reset the revision count | FIXED: `rec.Revisions = stored.Revisions` | same test asserts 2 survives |
| R10 | `shutil.which("bash")` could pick the WSL launcher and ignored SIGNALDECK_TEST_SH | FIXED: bash beside the resolved POSIX shell | DevBox tests 5/5 default and under dash |
| R11 | On the daemon's 500 the cookie is already cleared; "still signed in" was wrong for this browser and hid the daemon's message | FIXED: ApiError -> relay the daemon's message, go to /login; network failure -> say still signed in, stay | tsc, eslint, 42/42 |

## Blocked on Nicholas

| ID | What | Why blocked | Unblock + verify |
|---|---|---|---|
| B1 | Deploy daemon (S01, S03, S04, S05) and rebuild web (W01, W02) | Skill rule: production deploys need explicit authorization | `bash ops/signaldeck-ctl.sh deploy`; then `cd web && npm run build` + restart `SignalDeck Web`. Verify: `worker_runs.revision` = new HEAD; next maintenance stop logs "stop requested via stop file" and the following boot sweeps 0 orphans; `logs/signaldeckd.log` stops showing "flat cross-section" for 1d once a broad pass runs on the new map; backfill-reconciler shows `degraded` while paused |
| B2 | Push to origin (public repo) to turn CI green | Outward-facing; CI red since 09-20 20:36 (5 pushes) | `git push`; then `gh run list --branch public-launch --limit 1` shows success |
| B3 | Host is off/asleep during US market hours (no prediction pass 13:30-20:00Z on 09-22 or 09-23; boots at 18:29/18:51/14:02 PT) | Hardware/ops decision (Oracle VM plan, or keep this PC on). This is the real day-supply limit behind "Blocker 4" | Market-hours predictions: `SELECT date(ts,'unixepoch'),COUNT(*) FROM predictions WHERE ts BETWEEN <13:30Z> AND <20:00Z>` > 0 |
| B4 | 1m storage budget (6144 MB, DB at 6108 MB) pauses streamed gap-fill | Policy: budget likely sized for the offsite release asset (~1.05 GB compressed vs a 2 GB GitHub asset limit); 638 GB disk free | Raise `SIGNALDECK_BUDGET_DB_MB` or shorten `SIGNALDECK_1M_RETENTION_D`; reconciler detail then shows `gap-filled N streamed` |
| B5 | Cloudflare tunnel task not registered (check-task-health exit 1 on "CREATE SignalDeck Cloudflare Tunnel") | Needs the browser `cloudflared tunnel login` (carried from 09-21) | `ops/CLOUDFLARE_TUNNEL.md`; `powershell -File ops/check-task-health.ps1` exit 0 |
| B6 | Live web build has `localhost` in og:image / robots sitemap / sitemap locs | `NEXT_PUBLIC_SITE_URL` is build-time and there is no public hostname yet (B5) | Build with `NEXT_PUBLIC_SITE_URL=https://<host>`; `curl <host>/robots.txt` |
| B7 | Stale backup debris (655 MB .gz without sha256 from 09-21 18:35, `.db-journal` files 09-12/09-21) | Deletion — Nicholas's call | Inspect `data/backups/`, delete by hand |

## Checked and not a defect / accepted as designed

| ID | Item | Evidence | Status |
|---|---|---|---|
| V01 | expectancy/gbm/forecast-monitor/ai-analyst `degraded` | Retired models withheld by design (memory 09-20); ai-analyst is router saturation | VERIFIED |
| V02 | forecast-monitor "0 of 1033" | 1033 = the daily broad-universe sweep (325 active + 708 swept), the population the 09-19 gate fix was written for | NOT APPLICABLE |
| V03 | Go: build, vet, 3,590 tests, race on 11 packages | clean; 8 skips all legitimate | VERIFIED |
| V04 | Python: 483 pass / 1 legit skip; TestFrozenSnapshotVerdicts not skipping | agent run 09-23 | VERIFIED |
| V05 | Web: tsc, eslint (315 files), 42 tests, 64 routes 200, public pages no console errors | agent run | VERIFIED (57 login-walled routes not exercised — needs a session) |
| V06 | Data freshness: daily bars, crypto 1m/perp/quotes, grader (graded 14:06 PT, grader_fresh), offline + offsite backup 09-23, restore rehearsal 09-20 | agent run | VERIFIED |
| V07 | Tests default to the live DB read-only (gbm selfref, prereg claims) | read-only census tests by design; CI has no DB and skips | NOT APPLICABLE |
| V08 | `alphax` leakage test fails when SIGNALDECK_DB points at a missing file | an explicit bad path is an error, unset skips | NOT APPLICABLE |
| V09 | 51 committed .go files not gofmt-clean | pre-existing formatting; skill rule: never reformat code not written this session | NOT APPLICABLE (noted) |
| V10 | CSV export ignores `cw.Error()` | once streaming, headers are committed; a mid-stream write error is a client disconnect with nobody to report to | NOT APPLICABLE |
| V11 | backup `_ = SetMeta` | failure direction is a false "stale" alarm, never a missed one | NOT APPLICABLE |
| V12 | `research/spyx` A7 tolerance 6e-6 vs 1e-6 | closed research, recorded failing since 09-01, not in CI | NOT APPLICABLE |
| V13 | `drafts/research_tests` 235 fails | 08-06 draft suite of findings, not in CI | NOT APPLICABLE |
| V14 | market/activity fetches notifyStatus unrendered | deliberate placeholder, one GET per visit | NOT APPLICABLE |
