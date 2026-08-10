# Verification Ledger — SignalDeck Completion 2026-08-06

Every row is a command that was actually executed, with its real result. Nothing here is inferred.

## Build / static analysis

| # | Command | Result | Duration | Proves | Does NOT prove |
|---|---|---|---|---|---|
| V1 | `go build ./...` (daemon) | **exit 0** | 2.6 s | module compiles at HEAD + repairs | nothing about the running binary |
| V2 | `go vet ./...` (daemon) | **exit 0**, no output | 1.34 s | no vet-class defects module-wide; all packages incl. tests compile | not a linter; no logic guarantee |
| V3 | `gofmt -l .` | 387 files listed | 0.21 s | byte-level formatting state | **335 of 387 differ only by CR (CRLF checkout)**; 52 genuine drift. CI never runs gofmt — nothing gates on it |

## Tests

| # | Command | Result | Proves | Does NOT prove |
|---|---|---|---|---|
| V4 | `go test -short ./...` (baseline, pre-repair) | exit 0 · **119 ok · 0 FAIL · 10 no-test-files** | pre-repair unit health | `-short` **skips e2e by definition** |
| V5 | `go test -short ./...` (post-repair) | 118 ok · **1 FAIL** (`internal/mcp`) | surfaced NEW-1, a real test defect | — |
| V6 | `go test ./internal/mcp/ -count=1` ×3 | ok, ok, ok | failure not deterministic | — |
| V7 | `go test ./internal/mcp/ -race -count=2` | ok, 12.9 s | no race on executed paths | TSan sees only executed interleavings |
| V8 | `go test ./internal/mcp/ -count=5 -cpu=1,2` | ok, 8.3 s | not contention-dependent | — |
| V9 | `go test ./internal/workers/ -run TradingDayAtET -v` | **PASS** (2 new subtests) | R2 correct; fire instant pinned by date | not that FINRA ingest succeeds |
| V10 | `go test ./cmd/signaldeckd/ -run TestStalenessInterval -v` | **PASS 7/7**; observed `derived=240h clampedTo=192h` | R3 clamps pathological cadence, spares weekly | not that 8 d suits future schedules |
| V11 | `go test ./internal/pipeline/ -run "ExitsPositionInDeactivatedSymbol\|DeactivatedSymbolNeverEntered" -v` | **PASS 2/2** | R5 exits held-inactive symbols; never enters them | not that no position is *currently* stranded |
| V12 | **Negative control** — R5 disabled (`if false && …`), same test | **FAIL** with the intended diagnostic | **the test genuinely detects the bug** | — |
| V13 | R5 restored, `grep "if false"` → no matches; `go test ./internal/pipeline/` | ok, 21.5 s | clean restore | — |
| V14 | `go test -short -race` on workers/store/api/ensemble/pipeline | clean | no races on executed paths | as V7 |
| V15 | **FINAL** `go build && go vet && go test -short -count=1 ./...` | **0 / 0 / 0 — 119 ok · 0 FAIL · 10 no-test** | all repairs green; baseline package count restored; `-count=1` defeats cache | still `-short`: **e2e never ran** |

## Database

| # | Command | Result | Proves | Does NOT prove |
|---|---|---|---|---|
| V16 | `sha256sum data/backups/signaldeck-20260806-131007.db` | **matches manifest** `4f06f4c8…a8ca` | backup artifact is intact | — |
| V17 | `PRAGMA integrity_check` on that backup (`?mode=ro`) | **`ok`**, 32.1 s | **no page-level or B-tree corruption** in the 5.11 GB image as of 13:11 today | it is a *backup*; the live DB has advanced since, and logical defects are a separate question |
| V18 | 7 bounded read-only integrity queries (agent) | 4 clean, 3 defects | logical constraints: `bars` has **zero** duplicate `(symbol_id, tf, ts)` | — |
| V19 | Accuracy recomputation over `prediction_outcomes` | 1d 48.15% vs 49.64% base (n=164,332); 1w 47.92% vs 43.31% (n=130,960); Brier 0.2998 / 0.2801 vs 0.250 / 0.2455 constant | **no directional edge** | pooled across overlapping windows → pseudoreplicated; no valid CI from n |
| V20 | Accuracy by confidence bucket (1d) | 48.4 / 47.1 / 46.7 / 48.7 / **50.0 %** | **confidence carries no information** | — |

## Live system

| # | Command | Result | Proves | Does NOT prove |
|---|---|---|---|---|
| V21 | `curl localhost:8322/api/version` | `revision 0499416`, unchanged after a mid-session restart (pid 22220 → 33844) | **restarting does not close the deploy gap — only a rebuild does** | — |
| V22 | `git rev-list --count 0499416..HEAD` | **22** | scale of drift | — |
| V23 | `git show 0499416:…/predict.go \| grep -c "SETTLEMENT GUARD"` → **0**; HEAD → **1** | deploy-gated | the running process still freezes 1d labels against unfinished bars | — |
| V24 | `Get-ScheduledTaskInfo 'SignalDeck Accuracy'` | Last 2026-08-06 14:05, **Result 0** | the publication runner recovered on its next cycle | root cause not proven fixed — it simply did not fail today |
| V25 | `grader_heartbeats` ids 7–10 | id 10 success 2026-08-06T21:05Z | grading is current | — |
| V26 | `python tools/docs_gate.py check` | **exit 1, 2 violations** | the gate now actually executes | CI is red until `STRATEGY_DECK.md`'s two regions are resolved |
| V27 | `python tools/test_docs_gate_anchor.py` | **OK (3 tests)** | forged-marker exploit still rejected | — |

## Not run — explicitly

| Check | Status | Why |
|---|---|---|
| e2e suite | **BLOCKED** | binds a real port / touches a real DB; the live daemon is running and must not be disturbed |
| `npx tsc --noEmit` (web) | **NOT RUN** | web app is down and unschedulable; deferred with BLOCKED-3 |
| Full `-race` across all 119 packages | **NOT RUN** | prohibitively slow; restricted to the 5 highest-risk packages (V14) |
| Live FINRA ingest after R2 | **BLOCKED** | requires the daemon to run the repaired code — gated on BLOCKED-1 |
| Production-like dry run | **BLOCKED** | requires a deploy — gated on BLOCKED-1 |
