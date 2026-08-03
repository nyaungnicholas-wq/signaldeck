# Remediation — 2026-08-03

Two passes: fix everything found in the 2026-08-02 deep check, then re-audit and
fix what that turned up. Every claim below was reproduced before the fix and
measured after it.

## Gate status

| Gate | Before | After |
|---|---|---|
| `go build ./...` | pass | pass |
| `go vet ./...` | pass | pass |
| `go test ./...` | pass | pass |
| `tools` python tests | **1 failure** | pass |
| `tsc --noEmit` | pass | pass |
| `eslint --max-warnings 0` | **16 errors, 121 warnings** | pass (0/0) |
| `next build` | pass | pass |
| `ops/test-run-captures-stderr.ps1` | — | pass (new) |
| `ops/test-appendline-survives-lock.ps1` | — | pass (new) |
| `ops/test-lib-portable.sh` | — | pass (new) |
| `npx playwright test` (E2E) | **never ran — no browser** | **19/19 pass** |

## Pass 1

### 1. Worker-run journal — the multiplicity divisor was being loosened

`worker_runs` start/finish writes went inline through the single SQLite write
connection (`SetMaxOpenConns(1)`) shared by the whole fleet. Under contention
the write was dropped: 302 error rows in 24h and **77 runs stuck at `running`
forever**. `ResearchLoop` derives its Bonferroni multiplicity divisor from a max
over `worker_runs`, so every lost row shrank the divisor and *loosened* the
multiple-comparison correction — false discoveries pass a weaker test.

The fix existed and was quarantined (`quarantine/incomplete-2026-07-27/`,
whose README says "the fix is quarantined, the bug is not"). Restored
`journal.go` + its tests into `internal/workers`, added the `jrnl` field to
`Runner`, and routed both writes through the never-drop queue. Added
`store.ReconcileOrphanRuns`, which closes runs left behind by a dead process as
`orphaned` — never `ok` (the outcome is unknown) and never deleted (deleting a
look refunds multiplicity the fleet actually spent).

Measured after restart: **81 orphans swept, 0 rows stuck from before the
restart, 0 journal write retries.**

### 2. Loop harness discarded every failure diagnostic

`Run()` in `ops/selfimprove-loop.ps1` used `Receive-Job 2>&1`, which redirects
Receive-Job's own error stream, not the native stderr its child process wrote.
Thirty consecutive eighty-loop cycles were killed and journalled with an **empty**
code block while Python printed the exact SyntaxError and caret. Reproduced:
`h0055`–`h0057` fail with `NameError` / two distinct `SyntaxError`s, none of
which reached the journal. stderr is now merged inside the job.

### 3. Prediction resolver — head-of-line blocking

Corrects the first-pass report. The 43,853 unresolved rows were **not** all
stuck: 43,542 belong to symbols with current bars and are legitimately pending.
The defect is **992 rows on symbols whose bars stopped entirely** (WBA, PARA,
MRO, JNPR and other delisted tickers). Ordered oldest-first, they occupied two
thirds of every 1500-row batch on every 10-minute pass, permanently.
`UnresolvedPredictions` now requires a forward bar to exist. Rows are skipped,
never resolved and never deleted — the outcome for a symbol that stopped
printing is genuinely unknown. Measured: of the 998 rows the old query offered
first, **0** passed the full grading predicate.

### 4. `regime-outcome-runner` red on every pass

`RegimeForecastCalls` selected every row of `regime_forecasts`, including stale
forecasts for inactive S&P names whose bars had stopped — a naive-persistence
baseline is not computable from a series that no longer advances. The refusal
was correct; scoring symbols the product no longer covers was the defect. Now
joined to `symbols` on `active = 1 AND delisted_at IS NULL`.

Measured: the unmatched-null gap went from **8/3498 to 3/1151**. It still fires,
and that is now the gate working — 3 in-universe symbols genuinely lack a
baseline. See "Still open".

### 5. `hud-sync` drowning the audit log

trader-hud is a separate project that is usually not running. At a 1-minute
interval it produced 258 error rows/day, pinned `health.json` to `ok:false`, and
filled **1,418 of the last 4,000 log lines** with one message. It now reports
`ErrDegraded` (an optional external process being off is not a SignalDeck
failure) and backs off 1m → 2m → … → 30m. Verified live: `degraded`, escalating
1m → 2m → 4m.

### 6. Web lint gate

16 errors / 121 warnings → 0/0. The one real bug was
`watchlist/page.tsx`: `setMatches` called synchronously inside `useEffect`
(cascading render per keystroke) — `matches` is now derived with `useMemo`. Also
fixed a genuine leak the linter flagged separately: the unmount cleanup closed
over the initial `undo`, so unmounting mid-undo left a 5s timer that called
`setState` on a dead component. 11 `react-hooks/static-components` violations
hoisted, 4 unescaped entities, 89 unused imports and 12 dead default imports
removed.

### 7. `test_accuracy_registry` failing on Windows only

`shutil.which("bash")` resolves to `C:\Windows\System32\bash.exe` — **WSL** —
before Git Bash, and `C:/Users/...` does not exist there, so the sourced prelude
died and `sha256_of` returned `""`. The shell function was correct all along.
Candidates are now probed for whether they can actually stat a repo file.

### 8. Ledger writes silently lost

`Add-Content` raises a *non-terminating* error under a file lock, so
`try { Add-Content } catch { }` never caught anything and 188 lines went to the
error stream. Replaced with a retrying `FileStream` append (`FileShare.ReadWrite`).

## Pass 2 — the re-audit

The second sweep looked for the bug *class* behind several pass-1 findings: this
repo moved from macOS to Windows on 2026-07-31, and four commands it depends on
do not exist under Git Bash.

| Missing | Consequence |
|---|---|
| `caffeinate` | wrapped `VACUUM INTO` **and both compressors** — so the offline backup produced **nothing** |
| `sqlite3` | CLI absent, so the VACUUM and every meta write would have failed anyway |
| `pgrep` | "is the daemon live?" always answered NO — the guard against VACUUMing a live multi-GB database **failed open** |
| `osascript` | every alert (budget breach, restore-rehearsal failure, grading refusal) was a silent no-op |

This is the actual reason `logs/backup-offline.log` last recorded a run on
2026-07-29 *on the Mac* — not "compression regressed".

`ops/lib-portable.sh` adds `sd_nosleep`, `sd_sqlite` (Python's bundled sqlite3
as the fallback), `sd_is_running` (fails **closed**), and `sd_notify` (always
writes the alert to the log, so it can never vanish again). It also converts
MSYS paths for the Python fallback — bash reads `/tmp` as
`C:/Users/…/AppData/Local/Temp` and Windows Python reads it as `C:\tmp`, so an
unconverted path writes the backup somewhere nobody looks.

Verified by running it: the script executed on Windows for the first time since
the move, **compressed the two uncompressed snapshots (2.2 GB → 526 MB,
2.1 GB → 485 MB)** and pruned to 5.52 GB, inside the 6 GB budget.

### Off-machine backup

`offsiteDir` defaulted to a hardcoded macOS iCloud path on *every* platform. On
Windows that is just a name: the daemon created ordinary folders under
`C:\Users\…\Library\Mobile Documents\` and copied 2.7 GB onto the **same
physical disk as the database**, while `/api/quality` reported
`offsiteConfigured: true`. The default is now macOS-only and opt-in elsewhere,
and `offsiteConfigured` requires demonstrated volume separation. Verified live:
it now reports `false`.

## Pass 3 — the three items carried forward

### Scheduling (was "needs a design call")

The finding was bigger than one script: **1 of 14 launchd jobs existed on
Windows**. `ops/install-windows-tasks.ps1` now generates Scheduled Tasks from
the plists themselves, so the two schedules cannot drift. 8 registered;
`awake` (macOS `caffeinate`), the on-demand daemon and the four direct-binary
jobs are skipped with a stated reason.

`sd_svc_start/stop/restart`, `sd_kill_hard`, `sd_sqlite_read` and
`sd_days_ago` complete the shim set, and `signaldeck-ctl.sh` /
`signaldeck-refresh.sh` / `market-close.sh` now route through them.

Smoke-testing the first task found three further latent bugs:

1. Under Task Scheduler's PATH, bare `python3` hits the Microsoft Store alias
   stub. Six scripts called it directly; all now use `sd_py`.
2. `ops/research-liveness.sh` passed `--emit-dq-event`, a flag
   `research_liveness.py` never implemented — argparse exited 2, so this check
   **had never produced a verdict, on macOS either**. Removed; it now exits 0
   and immediately reported that `worker_runs` 145573 and 146527 narrated
   48-rule grid searches on 07-28/29 whose judgments were never persisted.
3. `signaldeck-refresh.sh` used the `sqlite3` CLI and BSD `date -v`; both are
   absent, so the daily sweep could not have run. `meta.last_full_sweep` is
   still `2026-07-29`, which confirms it.

**Consequence to be aware of:** Market-Open (06:20) and Market-Close (13:10,
weekdays) now start and stop the daemon, matching the Mac. The dashboard will
be down outside that window, and Market-Close is what triggers the nightly
backup.

### The 3 unmatched nulls — diagnosed

The refusal now names the rows, which immediately identified them:
`GOOGL/liquidity21`, `BMNR/vol21`, `ABCL/vol21`. None is a data gap — all
three have complete bars and no zero volumes.

They are **exact ties**. `NaiveLiquidityAt` returns "no label" when the current
21-bar mean log dollar volume equals the trailing median, and with
`window = 200` the inclusive window is 201 elements, so the median IS an
element of the window and `cur == med` holds *to the bit*. Verified for GOOGL:
both sides `23.012578864499954`, difference exactly `0.000e+00`.

So this is not a coverage gap — it is an honest abstention being counted as one.
A null with no direction cannot grade anything, so the correct fix is to treat a
tie as an abstention: do not freeze that (symbol, kind, day) at all, and do not
count its absence against null coverage.

**Not applied.** That changes the frozen population, which is pre-registered
protocol, and amending it silently is precisely what this repo's discipline
forbids.

### The sub-null ensemble — diagnosed

Measured on 132,170 resolved 1d predictions. It is **not** a sign error and
**not** an off-by-one; the grading is correctly aligned.

Correlation between the model's probability and the realised return, by bar
offset from the call:

| offset | meaning | corr |
|---|---|---|
| −1 | the bar BEFORE the call | **+0.208** |
| 0 | the call bar itself | **+0.115** |
| +1 | the next bar — the graded target | **−0.078** |
| +2 | two bars after | −0.018 |

The predictor is dominated by the immediately preceding return, and 1-day
equity returns short-term mean-revert, so that loading scores negatively on the
horizon it is graded at. Calibration is inverted through the middle: its
highest-confidence "up" bucket (0.7–0.8) resolves up only 37.3% of the time.

Against the trivial constant baseline it is worse than "no edge":

- base rate P(up) = 0.4720, so **always saying DOWN scores 0.5280**
- the model scores **0.4832** — **4.5 points worse than a constant**
- it says UP on 42.0% of calls while up happens 47.2% of the time

The `+0.115` at offset 0 is the likely source of the contradictory ICs: a
backtest that lines features up with the call bar can absorb contemporaneous
information that is unavailable live, which is exactly the shape of `+0.0497`
backtested against `−0.020` measured.

The honest fixes, none of which is flipping a sign: withhold the 1d directional
claim until it beats the constant baseline; re-measure the `+0.0497` IC with
strict as-of alignment to find the leak; and treat "trailing return mean-reverts
at 1d" as a new pre-registered hypothesis rather than a patch.

## Pass 4 — the third deep check

### The E2E suite had never run at all

`npx playwright test` failed 15 of 19 specs with "Executable doesn't exist" —
the Chromium binary was never downloaded, which is the whole reason handoff item
2b read "not yet run". After `npx playwright install chromium`, one genuine
failure remained: the offline-banner spec clicks a Retry button while the
first-run welcome-tour modal is still up, and the dialog intercepts the pointer
event. Three other click-driven specs already suppress the tour with
`localStorage.setItem("sd-tour-done", "1")`; this one was missing the guard.

**19/19 now pass.**

### A data-quality message that contradicted itself

The live feed carried:

    source=crypto_perp newest row NO ROWS old (budget 2h) — … newest row is in
    the future; check clock skew

One sentence saying the table is both empty and holding a future row. `ageString`
returned "no rows" for *any* negative age, and a future-stamped row also yields a
negative age. A reader anchors on "no rows" and goes hunting for a dead ingest
when the actual defect is a timestamp. I verified against the live DB that **no
table holds rows ahead of the clock**, so the "no rows" reading was the wrong one.

Split the sentinel (`noRowsAge`) from a genuinely negative age, which now renders
as "ahead of our clock by …". Three tests pin it, including that the sentinel is
unreachable by any realistic age.

### The python gate was hand-maintained and had drifted

`ops/selfimprove-loop.ps1` named five test modules explicitly.
`tools/test_verify_backup.py` — the checks pinning the 2026-08-01 content-loss
defect, where a backup passed `quick_check` holding 14 of 261,164 ledger rows —
was never added, so it ran nowhere. It also uses a `main()` rather than a
`TestCase`, so `python -m unittest` reports "NO TESTS RAN" and exit 5.

The gate now discovers `test_*.py` and picks the right runner per file. **6/6 pass.**

### A tied null is an abstention, not a coverage gap — applied

Following the diagnosis in pass 3, `naiveLabel` now returns a second value
separating a DATA GAP (no series, no bar at or before the call) from a
DEGENERATE statistic (the value sitting exactly on its own trailing median).
Both leave the row unfrozen — **the frozen population is byte-for-byte
unchanged** — so this is a change to the refusal's accounting, not to what is
measured. Only a real gap now counts against null coverage.

## Pass 5 — committing, and what committing exposed

Committed on branch `windows-port-remediation-2026-08-03`, then deployed through
the sanctioned path for the first time.

### The README refusal was self-inflicted

Running `accuracy-registry.sh` during the portability work tripped the grading
refusal at 22:33:41 and replaced the published tables with a REFUSED banner. The
stated cause — "research-loop liveness check failed (exit 2)" — was the
`--emit-dq-event` flag defect fixed twenty minutes later. Re-running the grader
restored the real tables.

### Every verdict read WITHHELD because of the dirty build

`ops/accuracy-registry.sh:313` falls back to `WITHHELD (provenance
unresolvable)` when the grader drops the verdict field, which it does for rows it
cannot attribute to a commit. 1,436 `worker_runs` in 24h carried a `+dirty`
stamp — **1,436 of which my own three development deploys wrote** — on top of
259,745 `prediction_ledger` rows predating revision stamping entirely.

### Deploy could not have worked on Windows

`build_from_head` wrote `bin/signaldeckd` with no extension while the Scheduled
Task launches `bin/signaldeckd.exe`, so a "successful" deploy would have left the
task running the OLD binary. It also failed at `install(1)` with "File exists",
because a running daemon holds its own image open on Windows where Unix would
silently replace the inode.

### The provenance field was answering at random

With a clean tree and an attributable build, `/api/version` still reported
`resolvable: false` — intermittently. Six consecutive calls against one binary
and one repository returned **true, false, false, false, true, false**.

`revisionResolvable` spawns `git cat-file` per call under a 2s budget and fails
closed. Spawning `git.exe` here measures 370–930ms idle, and the daemon runs 97
workers against the same disk. Failing closed then downgraded a *verified* build
to unattributable, and the registry withholds verdicts on exactly that field — so
a published accuracy record turned on a coin flip.

Timeout raised to 10s; a positive answer is cached for 5 minutes, keyed by
`(revision, dirty, dir)` so one positive cannot answer for a different stamp. A
negative is never cached.

**Verified after deploy: 10/10 calls `resolvable: true`, `revision` equal to
HEAD, `modified: false`, and new `worker_runs` rows carrying a clean 40-hex
commit stamp.** Every row written from here is attributable.

## Still open

0. **Environment gaps, not code defects.** `go test -race` cannot run: there is
   no C compiler on this machine (`CGO_ENABLED=0`, no gcc/clang), which is why
   the ledger already recorded the race detector as blocked. Installing a
   mingw-w64 or MSVC toolchain would unblock it. Separately, `data/` holds
   **6.77 GB of stale database copies** (`bak-preimport`, `bak-prereg20`,
   `premigration`, plus two orphaned `loopsnapshot` WAL sidecars with no parent
   `.db`). Left in place: they are yours to delete, not mine.

1. **No off-machine backup exists.** Unchanged and unfixable from here — it
   needs physical hardware. With an external drive attached:
   `SIGNALDECK_OFFSITE_DIR=/d/SignalDeckBackups bash ops/signaldeck-backup-offline.sh`,
   then confirm `/api/quality` `ops.offsiteConfigured` is `true`. **This remains
   the single largest risk in the system.**
2. **`ops/signaldeck-refresh.sh` is macOS-bound.** It drives `launchctl`, which
   has no Git Bash equivalent; restarting the daemon on Windows needs Task
   Scheduler or a service wrapper. A design decision, not a mechanical port.
3. **3 active symbols still lack a naive baseline** (down from 8). The refusal is
   now doing its job; the remaining gap is real and in-universe.
4. **The directional ensemble still grades worse than its null** (1d 48.0%
   [0.472, 0.489]). Not a bug and deliberately not "fixed" — it is a measured
   result.
5. **The two contradictory ICs** (+0.0497 vs −0.020) are untouched.
6. **`/api/accuracy`** is referenced only in audit prose; no route ever existed
   and nothing calls it. Recorded here so it stops being re-reported.
7. **The daemon is running a `+dirty` build** under
   `SIGNALDECK_ALLOW_DIRTY_BUILD=1` — the state it was found in. A clean deploy
   needs a commit first; the attributability guard is correct to refuse.
