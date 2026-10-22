# /signaldeckfix round 2 - 2026-10-11 evening
HEAD 335a56dc on branch public-launch; live daemon c8e18a2c (deployed 10:29 PT). Changes are in the working tree, NOT committed, NOT deployed. Pre-existing uncommitted doc work (7 .md files, partials/, ops/data-integrity.json, scheduled doc regeneration) was left untouched.

## Ledger
| ID | Component | Finding | Status | Evidence |
|---|---|---|---|---|
| SD2-01 | api/slowcache.go | Every swrBodyCache background rebuild that failed logged err=<nil>: swrRender discarded the handler status and refresh passed the still-live refresh context's Err(). Live: 12:47:38 swrbody:st2 screener after the screener's 60s build budget expired. All 13 swr routes affected. | FIXED (not deployed) | swrRender returns (body, status); renderFailure() names the status, an empty 200, or the ctx error. Tests TestSWRRenderReportsHandlerStatus, TestRenderFailureNeverNil, TestSWRBodyRefreshLogsHandlerFailure; the call-site test FAILS with the old line restored. |
| SD2-02 | ensemble/ensemble.go | The hourly symbol learner persisted FLAT calibration maps (PAV pooled to one block; ValidateKnots passes equal values) that serving refuses every 10-min pass: the WARN "refused stale per-symbol maps count=1" fired 523 times and could never clear. DB has 8 flat fitted 1w rows (AMGN INTU MPC CPNG EDBL LQD PLSM ZCMD); LQD is the hot-set one. | FIXED (not deployed) | CalibrateKnots refuses !KnotsDiscriminate(ky), the same rule serving uses. TestCalibrateKnotsRefusesFlatMap FAILS without the guard, passes with it; symbolagent/monotone/floors tests still pass. Rows clear on the next hourly pass after deploy. |
| SD2-03 | api/api.go health, pipeline/apiprobe.go | api-probe failed twice (11:03:46, 13:13:46): /api/health reads stalled ~10s on the read pool; probe filed "listener unreachable" while the listener answered; the anonymous health path never logged the read error. | FIXED (not deployed) | Health reads bounded by healthReadBudget=4s (overrun counts as degraded); fleet-read error logged on the anonymous path; probe reports a timeout as "gave no response". Test TestAPIProbe_SlowAnswerIsAnErrorButNotUnreachable. |
| SD2-04 | store write path | "long write-lock hold" on InsertSnap1s 5.4-21.8s clustered at :04-:06 and :50; the hold includes the auto-checkpoint inside the commit, so InsertSnap1s is the victim of a WAL backlog, not the cause. Overlapping runs: downsampler at 2 of 3 holds, model-health at 1. The Cleanup task is daily 14:05 and does not touch the DB (ruled out). | NOT COMPLETE | Source not proven. Next: log WAL size around each long hold, then confirm the downsampler. WAL currently ok (37.7 MB). |
| SD2-05 | web runtime | node_modules still has sharp 0.35.4 vs lockfile 0.35.5 (carried from SF-05). | BLOCKED: owner authorization for production web release | npm ci + ops/web-release.ps1 |
| SD2-06 | web lint | 2 exhaustive-deps warnings | NOT APPLICABLE (stable callbacks, SF-09) | eslint: 0 errors, 2 warnings |
| SD2-07 | LQD in hot set | LQD (bond ETF) still has a personal hot-set model although funds were excluded from the research universe. | NOT COMPLETE: owner's scope call | symbol_models row LQD 1w |

## Verification (after the last change)
- go build ./... && go vet ./... OK
- go test ./... -count=1 exit 0, 133 ok, 0 FAIL
- go test -race on changed ensemble/api/pipeline tests ok
- web tsc --noEmit exit 0, npm test 146/146, eslint 0 errors
- task health OK (both ports listening)
- grader health RESULT ok, WAL 37.7 MB
- fresh-context diff review found no bugs, its gap (no call-site test) closed, minor edges (empty 200, health budget overrun read as clean) handled

## Not verified
- no deploy, so SD2-01..03 are not live (worker_runs.revision != HEAD)
- flat-map clearing not observed in the DB
- source of the hourly WAL backlog (SD2-04)