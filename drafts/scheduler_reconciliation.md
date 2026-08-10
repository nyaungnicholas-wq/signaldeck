<!--
TARGET: this is a standalone report, not a patch. It documents the state of
ops/com.signaldeck.*.plist vs the host's Windows Scheduled Tasks as of
2026-08-06 ~15:20 PT, on branch hmm-regime-and-pbo @ 0b1f73b.
HOW TO APPLY: nothing to apply. Read it. Remediation items are listed at the
bottom as proposals only; no scheduled task, ACL or DB row was modified to
produce this document.
-->

# SWARM 1c — scheduler reconciliation + .env ACL audit

Evidence collected read-only on 2026-08-06. Every row cites either a plist
file, the task's own `Export-ScheduledTask` XML, or a command transcript.

---

## PART A — plist vs Windows Scheduled Task

`ops/` holds 16 `com.*.plist` files: 14 `com.signaldeck.*`, plus
`com.stocktrader.hud.plist` and `com.tickstream.daemon.plist`.
The host has 13 `SignalDeck *` Scheduled Tasks, all in `TaskPath = \`.

### Reconciliation table

| # | plist Label | StartCalendarInterval cadence | matching Windows task | that task's trigger | verdict |
|---|---|---|---|---|---|
| 1 | `com.signaldeck.accuracy` | daily 14:05 | SignalDeck Accuracy | `CalendarTrigger start=2026-08-05T14:05:00-07:00 [ScheduleByDay everyNdays=1]` | **MATCH** |
| 2 | `com.signaldeck.awake` | *(none — `RunAtLoad`+`KeepAlive`, runs `/usr/bin/caffeinate -i`)* | — | — | **MISSING** (installer: `SKIP SignalDeck Awake  no shell script in ProgramArguments`) |
| 3 | `com.signaldeck.bias` | daily 02:40 | SignalDeck Bias | `CalendarTrigger start=2026-08-05T02:40:00-07:00 [ScheduleByDay everyNdays=1]` | **MATCH** |
| 4 | `com.signaldeck.cleanup` | daily 14:05 (`StartCalendarInterval` is a bare `<dict>`, not an array) | SignalDeck Cleanup | `CalendarTrigger start=2026-08-05T14:05:00-07:00 [ScheduleByDay everyNdays=1]` | **MATCH** (cadence); log sink lost — see D-4 |
| 5 | `com.signaldeck.daemon` | *(none — on-demand; `RunAtLoad=false`, no `KeepAlive`; runs `ops/signaldeck-ctl.sh launch`)* | SignalDeck Daemon | `LogonTrigger` (no delay) | **DRIFT — severe.** Task is hand-made, unmanaged by the installer, and executes `bin\signaldeckd.exe` **directly**, bypassing the provenance gate. See D-1 |
| 6 | `com.signaldeck.daily-refresh` | Mon–Fri 13:15 | SignalDeck Daily-Refresh | 5× `CalendarTrigger start=…13:15:00-07:00 [ScheduleByWeek days=Monday…Friday every=1]` | **MATCH** (cadence). Last run **failed** — see F-1 |
| 7 | `com.signaldeck.market-close` | Mon–Fri 13:10 | SignalDeck Market-Close | 5× `CalendarTrigger start=…13:10:00-07:00 [ScheduleByWeek days=Monday…Friday every=1]` | **MATCH** |
| 8 | `com.signaldeck.market-open` | Mon–Fri 06:20 **plus `RunAtLoad=true`** | SignalDeck Market-Open | 5× `CalendarTrigger start=…06:20:00-07:00 [ScheduleByWeek days=Monday…Friday every=1]` — **no LogonTrigger** | **DRIFT.** The at-login half of the schedule is silently dropped. See D-2 |
| 9 | `com.signaldeck.research-liveness` | daily 08:15 | SignalDeck Research-Liveness | `CalendarTrigger start=2026-08-05T08:15:00-07:00 [ScheduleByDay everyNdays=1]` | **MATCH** |
| 10 | `com.signaldeck.restore` | `Weekday 0` = **Sunday 07:00, weekly** | SignalDeck Restore | `CalendarTrigger start=2026-08-05T07:00:00-07:00 [ScheduleByWeek days=Sunday every=1]` | **MATCH** |
| 11 | `com.signaldeck.revalidation` | `Day 1, Hour 3, Minute 20` = **day-of-month 1, MONTHLY 03:20** | SignalDeck Revalidation | `CalendarTrigger start=2026-08-05T03:20:00-07:00 [ScheduleByDay everyNdays=1]` = **DAILY 03:20** | **DRIFT — cadence.** Monthly job runs ~30× too often. See D-3 |
| 12 | `com.signaldeck.structural-liveness` | daily 08:30 | SignalDeck Structural-Liveness | `CalendarTrigger start=2026-08-05T08:30:00-07:00 [ScheduleByDay everyNdays=1]` | **MATCH** |
| 13 | `com.signaldeck.tunnel` | *(none — on-demand; runs `~/.local/bin/ngrok http 8322 --domain=…`)* | — | — | **MISSING** (installer: `SKIP SignalDeck Tunnel  no shell script in ProgramArguments`) |
| 14 | `com.signaldeck.web` | *(none — on-demand; runs `web/node_modules/.bin/next start -p 8323`)* | — | — | **MISSING** (installer: `SKIP SignalDeck Web  no shell script in ProgramArguments`) |
| — | *(no plist)* | — | **SignalDeck Daemon Keepalive** | `LogonTrigger` + `TimeTrigger start=2026-08-04T00:00:00-07:00 repeat=PT5M` → `ops\daemon-guard.ps1` | **WINDOWS-ONLY**, no macOS counterpart |
| — | *(no plist)* | — | **SignalDeck Eighty Loop** | `LogonTrigger` + `TimeTrigger start=2026-08-03T00:02:00-07:00 repeat=PT1H` → `ops\eighty-loop.ps1 -Hours 24 -MaxCycles 500` | **WINDOWS-ONLY**, no macOS counterpart |

Non-SignalDeck plists, listed because the installer iterates `com.*.plist` and
counts them in its skip total:

| plist | installer verdict |
|---|---|
| `com.stocktrader.hud.plist` | `SKIP StockTrader Hud   no shell script in ProgramArguments` |
| `com.tickstream.daemon.plist` | `SKIP TickStream Daemon  no shell script in ProgramArguments` |

### (a) Every plist with NO Windows task — 4

| plist | why | operational consequence |
|---|---|---|
| `com.signaldeck.awake` | `ProgramArguments` = `/usr/bin/caffeinate -i`, no `.sh` → installer skip | macOS-only concept. No Windows analogue needed as a *task*; if the host sleeps, the whole schedule stalls. Not a defect, but nothing replaces it either. |
| `com.signaldeck.tunnel` | `ProgramArguments` = ngrok binary, no `.sh` → installer skip | **The public HTTPS ingress for `POST /api/tv-webhook` has no scheduled owner on Windows.** TradingView webhooks reach nothing unless a tunnel is started by hand. `logs/quicktunnel.log` (mtime 2026-08-06 11:50) shows tunnelling is being done ad hoc instead. |
| `com.signaldeck.web` | `ProgramArguments` = `next start`, no `.sh` → installer skip | The Next.js UI on :8323 has no scheduled owner. Manual start only. |
| `com.signaldeck.daemon` | has a `.sh`, but no `StartCalendarInterval`/`StartInterval`/`KeepAlive`/`RunAtLoad` → installer skip `on-demand only (no schedule to translate)` | Skipped *correctly* by the installer — but a **hand-made** `SignalDeck Daemon` task exists anyway and does the wrong thing (D-1). |

### (b) Every cadence that DIFFERS — 3

**D-1 · `com.signaldeck.daemon` — provenance gate bypassed.**
The plist runs `/bin/bash ops/signaldeck-ctl.sh launch`, and the plist's own
comment (`ops/com.signaldeck.daemon.plist`, PROVENANCE GATE block) states the
reason verbatim: the `launch` branch reuses `build_from_head()` — clean-tree /
manifest / `git archive HEAD` / `-ldflags` revision-stamp preflight — and
"exits non-zero instead of exec'ing when any step refuses. Exec'ing
bin/signaldeckd directly bypassed every one of those checks, which is how a
dirty-tree build ended up stamping rows '+dirty'."
The Windows task's action is exactly the forbidden form:

```
ACTION: C:\Users\Nicholas_N\Desktop\claude code\signaldeck\bin\signaldeckd.exe
```

The plist is also `RunAtLoad=false` / on-demand-only; the Windows task carries
a `LogonTrigger`, so the daemon starts unconditionally at logon rather than
inside the market window. Two independent drifts in one task.
Classification: **DIRECT_EVIDENCE** (plist text + exported task XML).

**D-2 · `com.signaldeck.market-open` — `RunAtLoad=true` dropped.**
The plist comment dates the flag and states what it fixes: "a reboot/login
after 06:20 used to leave the stack dead all day (launchd only replays calendar
jobs missed during sleep, not across reboots). Now login fires the guard, which
starts collection only inside the weekday 06:20–13:10 PT window."
`ops/install-windows-tasks.ps1:118` only synthesises an `-AtLogOn` trigger when
**no** other trigger was produced:

```powershell
if (-not $triggers.Count) {
  $keep = $d.ContainsKey('KeepAlive') -and (ConvertTo-Value $d['KeepAlive'])
  $load = $d.ContainsKey('RunAtLoad') -and (ConvertTo-Value $d['RunAtLoad'])
  if ($keep -or $load) { $triggers += New-ScheduledTaskTrigger -AtLogOn; $when += 'at logon' }
}
```

market-open has 5 calendar triggers, so `$triggers.Count` is 5 and the branch
never runs. The exported XML confirms: 5 `ScheduleByWeek` triggers, zero
`LogonTrigger`. The reboot-recovery behaviour does not exist on Windows.
Classification: **DIRECT_EVIDENCE** (installer source + exported task XML).
Mitigating: `SignalDeck Daemon Keepalive` (5-minute `daemon-guard.ps1`) happens
to cover the daemon half of the gap, though it was written for a different
reason and does not run `market-open-guard.sh`.

**D-3 · `com.signaldeck.revalidation` — MONTHLY became DAILY.**
plist: `<dict><key>Day</key><integer>1</integer><key>Hour</key><integer>3</integer><key>Minute</key><integer>20</integer></dict>`
— launchd `Day` is day-of-month, so this is the 1st of each month at 03:20.
The installer's trigger loop (`install-windows-tasks.ps1:99-111`) reads only
`Hour`, `Minute` and `Weekday`; there is **no branch for `Day`**, so the entry
falls through to the `else` and becomes `New-ScheduledTaskTrigger -Daily`.
Confirmed three ways: the dry run prints `SignalDeck Revalidation  daily 03:20`;
the exported XML says `[ScheduleByDay everyNdays=1]`; and
`Get-ScheduledTaskInfo` reports `Last=8/6/2026 3:20:01 AM` — the 6th, not the
1st. `ops/revalidate-structural.sh` is therefore running ~30× its designed
frequency.
Classification: **DIRECT_EVIDENCE**.

**D-4 · (systemic, affects all 10 managed tasks) — plist log sinks dropped.**
Not a *cadence* drift, so it is not counted in (b), but it breaks the audit
trail. The installer never translates `StandardOutPath` / `StandardErrorPath`;
`New-ScheduledTaskAction` (line 133) has no redirection. Scripts that write
their own log are fine (`accuracy-registry.log`, `nightly-bias.log`,
`refresh.log`); everything that relied on the plist redirect gets nothing:

```
-rw-r--r-- 1 Nicholas_N 197121   0 Jul 25 14:05 logs/accuracy.out.log
-rw-r--r-- 1 Nicholas_N 197121 657 Jul 29 14:05 logs/cleanup.sched.log
-rw-r--r-- 1 Nicholas_N 197121   0 Jul 20 13:15 logs/refresh.sched.log
-rw-r--r-- 1 Nicholas_N 197121   0 Jul 20 07:00 logs/schedule.err.log
-rw-r--r-- 1 Nicholas_N 197121 551 Jul 29 13:10 logs/schedule.out.log
ls: cannot access 'logs/restore.out.log': No such file or directory
ls: cannot access 'logs/revalidation.out.log': No such file or directory
ls: cannot access 'logs/structural-liveness.out.log': No such file or directory
ls: cannot access 'logs/research-liveness.out.log': No such file or directory
```

Cleanup ran today at 14:05 with result 0, yet `cleanup.sched.log` has not been
touched since Jul 29. Classification: **DIRECT_EVIDENCE**.

**Latent (not currently drifting): timezone.** `$at = Get-Date -Hour $h -Minute $m`
builds the trigger in the *installing machine's* local time, while launchd reads
the plist in the *Mac's* local time. Both are currently Pacific (task XML
`StartBoundary` carries `-07:00`), so no drift exists today. It appears the
moment either host moves timezone. Classification: **INFERENCE** from
`install-windows-tasks.ps1:102`.

### (c) Which definition is authoritative

**The plists are authoritative for the 10 tasks the installer manages; the
Windows Scheduled Task store is authoritative in practice for the 3 it does
not.** Neither is authoritative alone, and that split is the actual problem.

- The repo asserts plist primacy in two places. `install-windows-tasks.ps1:4-8`:
  "This reads the plists — the same files the Mac uses, so the two schedules
  cannot drift — and registers the equivalents."
  `com.signaldeck.structural-liveness.plist` repeats it: "ops/install-windows-tasks.ps1
  reads this file and rewrites the script path onto whatever repo it is run
  from, so the plist stays the single schedule definition for both platforms
  rather than drifting into two."
- That claim is **false as written**, and D-1/D-2/D-3 are the counterexamples.
  It holds only for the subset of launchd keys the translator implements:
  `Hour`, `Minute`, `Weekday`, `StartInterval`, and `RunAtLoad`/`KeepAlive`
  *when no calendar trigger exists*. `Day`, `Month`, `RunAtLoad`-alongside-a-calendar,
  `StandardOutPath`, `StandardErrorPath`, `EnvironmentVariables`, `Nice`,
  `ProcessType` and `LowPriorityIO` are all read-but-ignored or never read.
- Three live tasks are outside plist governance entirely: `SignalDeck Daemon`
  (contradicts its plist), `SignalDeck Daemon Keepalive` and
  `SignalDeck Eighty Loop` (no plist at all). Re-running the installer with
  `-Install` would not correct `SignalDeck Daemon`, because that plist is
  skipped as on-demand — the wrong task survives untouched, and `-Remove` would
  not remove it either (`$managed` never contains it).
- **Practical ruling:** on this host the Task Scheduler store is what actually
  executes and is what an operator must read. The plists should be treated as
  *design intent* until the translator covers `Day` and calendar+`RunAtLoad`,
  and until `SignalDeck Daemon` is either deleted or brought under
  `signaldeck-ctl.sh launch`.

### Note on the installer's skip behaviour — it is loud, not silent

`ops/install-windows-tasks.ps1` defaults to a report (`DEFAULT IS A REPORT.
Nothing is registered unless -Install is passed.`, line 10). Verbatim dry-run
transcript, run 2026-08-06 from `ops/`:

```
exists (would update) SignalDeck Accuracy                daily 14:05
SKIP   SignalDeck Awake                   no shell script in ProgramArguments
exists (would update) SignalDeck Bias                    daily 02:40
exists (would update) SignalDeck Cleanup                 daily 14:05
SKIP   SignalDeck Daemon                  on-demand only (no schedule to translate)
exists (would update) SignalDeck Daily-Refresh           Monday 13:15, Tuesday 13:15, Wednesday 13:15, Thursday 13:15, Friday 13:15
exists (would update) SignalDeck Market-Close            Monday 13:10, Tuesday 13:10, Wednesday 13:10, Thursday 13:10, Friday 13:10
exists (would update) SignalDeck Market-Open             Monday 06:20, Tuesday 06:20, Wednesday 06:20, Thursday 06:20, Friday 06:20
exists (would update) SignalDeck Research-Liveness       daily 08:15
exists (would update) SignalDeck Restore                 Sunday 07:00
exists (would update) SignalDeck Revalidation            daily 03:20
exists (would update) SignalDeck Structural-Liveness     daily 08:30
SKIP   SignalDeck Tunnel                  no shell script in ProgramArguments
SKIP   SignalDeck Web                     no shell script in ProgramArguments
SKIP   StockTrader Hud                    no shell script in ProgramArguments
SKIP   TickStream Daemon                  no shell script in ProgramArguments

would install: 0 new, 10 existing, 6 skipped
Nothing was changed. Re-run with -Install to apply.
```

Confirmed: **6 skipped**, each with a named reason. Two corrections to the
common framing of this behaviour:

1. Only **4** of the 6 skips are for "no shell script in ProgramArguments"
   among SignalDeck plists — and 2 of those 4 (`StockTrader Hud`,
   `TickStream Daemon`) are not SignalDeck jobs at all. The SignalDeck skips
   for that reason are `Awake`, `Tunnel`, `Web` — 3.
2. `SignalDeck Daemon` is skipped for a **different** reason
   (`on-demand only (no schedule to translate)`), which is the correct decision
   for that plist. The defect is that a conflicting task exists regardless.

Loudness caveat: the message is only loud *when the script is run*. Nothing on
this host runs it on a schedule, so in steady state these 6 gaps are invisible.

### Every scheduled task with `LastTaskResult != 0`, and the state it left

Source: `Get-ScheduledTaskInfo` for each SignalDeck task, 2026-08-06 ~15:20 PT.
The other 10 tasks all report `LastTaskResult = 0`, `NumberOfMissedRuns = 0`.

| Task | LastRunTime | LastTaskResult | Decoded | Severity | State left behind |
|---|---|---|---|---|---|
| SignalDeck Daemon | 8/6/2026 1:35:02 PM | `267009` = `0x00041301` | `SCHED_S_TASK_RUNNING` — success-severity HRESULT, **not a failure** | benign | Task `State=Running`. `signaldeckd.exe` pid **33844**, `StartTime = 8/6/2026 1:35:02 PM`, matching the task's LastRunTime exactly. Healthy, but started via the provenance-bypassing path (D-1). |
| SignalDeck Eighty Loop | 8/6/2026 3:02:01 PM | `2147946720` = `0x800710E0` | `HRESULT_FROM_WIN32(4320)`; `net helpmsg 4320` → "The operator or administrator has refused the request." | benign-by-design, but masks real failures | Task `State=Running` with `MultipleInstances=IgnoreNew` and `ExecutionTimeLimit=PT0S` (unlimited). A long-lived instance is already running, so each hourly `TimeTrigger` fires, is refused, and stamps this code. The **hourly trigger is effectively decorative** — it can never do anything until the running instance exits, and the non-zero result it leaves is indistinguishable from a genuine failure to any dashboard reading `LastTaskResult`. Loop is alive: `logs/eighty-console.log` and `logs/eighty-events.jsonl` both mtime 2026-08-06 14:59. |
| **SignalDeck Daily-Refresh** | **8/6/2026 1:15:00 PM** | **`3221225786` = `0xC000013A`** | **NTSTATUS `STATUS_CONTROL_C_EXIT`** — process killed by a console control event (CTRL_C / CTRL_BREAK), not a script exit code | **REAL FAILURE** | **See F-1 below. The universe is left expanded 9×.** |

> Decoding caveat: `0xC000013A` is an NTSTATUS, not a Win32 code. Masking to the
> low word and asking `net helpmsg 314` yields "The physical resources of this
> disk have been exhausted", which is a **decoding artefact and not the actual
> cause**. The correct reading is `STATUS_CONTROL_C_EXIT`, corroborated by
> `ops/daemon-guard.ps1` naming that exact status for the same class of failure
> on this host (see F-1).

**F-1 · SignalDeck Daily-Refresh died mid-sweep and left the universe expanded.**

`logs/refresh.log`, last two lines of the file:

```
2026-08-06 13:15:00 === sweep start (trading day 2026-08-06, maxwait 3600s) ===
2026-08-06 13:15:01 reactivated full universe: 2950 active
```

Every prior successful day closes with a prune and a `sweep done` line, e.g.:

```
2026-08-05 13:38:28 daemon stopped for a clean prune
2026-08-05 13:38:33 pruned back to 328 active
2026-08-05 13:38:34 daemon restarted (was up before the sweep)
2026-08-05 13:38:34 === sweep done for 2026-08-05 (active=328) ===
```

Today's run has **no prune and no `sweep done`**. Confirmed against the live DB,
read-only:

```
$ sqlite3 "file:.../data/signaldeck.db?mode=ro" "SELECT active, COUNT(*) FROM symbols GROUP BY active;"
1|2950
```

Every row in `symbols` is `active=1`; there is not a single `active=0` row. The
steady-state kept set is **328**. The daemon (pid 33844, restarted 13:35:02 by
`SignalDeck Daemon Keepalive`) has been collecting against a **2950-symbol
universe — 9.0× the intended set — for roughly two hours** and will keep doing
so on every subsequent tick until something prunes it. Storage budget context
from `refresh.log`'s last storage report: `db 2351MB / 4096MB`; the live file is
now **5,285,097,472 bytes (≈5.0 GB)**, i.e. already past the 4096 MB budget
line, with `signaldeck.db-wal` at 67,108,864 bytes.
Classification: **DIRECT_EVIDENCE** for the effect (log + DB query + file sizes).

Cause is **UNVERIFIED** — but not novel. `ops/daemon-guard.ps1` documents the
identical failure mode for a sibling task, twice:

- lines 20-22: "The staleness timeout is the important half: market-close.sh was
  observed dying mid-run (STATUS_CONTROL_C_EXIT, 2026-08-03)."
- lines 28-30: "that script is killed with STATUS_CONTROL_C_EXIT on this machine
  (seen 2026-08-03 and again 2026-08-04), and a console-control kill does not
  run bash EXIT traps, so the lock outlives the holder."

So: long-running Git-Bash tasks on this host are being console-control-killed as
a recurring pathology, at least three occurrences now (market-close 08-03,
market-close 08-04, daily-refresh 08-06). The consequence generalises the same
way it did there — **`signaldeck-refresh.sh`'s cleanup will not run either**,
because a console-control kill skips bash `EXIT` traps. The 9× universe is the
visible half of that.

**Historical blind spot.** There is no failure history to check this against:

```powershell
PS> (Get-WinEvent -ListLog 'Microsoft-Windows-TaskScheduler/Operational').IsEnabled
False
```

The Task Scheduler operational log is **disabled**, so `LastTaskResult` — a
single value, overwritten every run — is the *only* failure signal on this host.
A task that failed yesterday and succeeded today leaves no trace. This audit can
therefore state nothing about how many times these tasks have failed before.
Classification: **BLOCKED** (evidence does not exist to collect).

### Proposed remediation (not applied — no task, ACL or DB row was modified)

1. **F-1, urgent:** prune the universe back to the kept set and rerun the tail of
   `ops/signaldeck-refresh.sh` for 2026-08-06. Requires a DB write → out of
   scope for this swarm; belongs in `drafts/pending-approval/`.
2. **D-3:** add a `Day` branch to `install-windows-tasks.ps1:99-111` emitting
   `New-ScheduledTaskTrigger -Monthly -DaysOfMonth $d -At $at`; re-run
   `-Install` to correct `SignalDeck Revalidation` from daily to monthly.
3. **D-1:** point `SignalDeck Daemon`'s action at
   `bash ops/signaldeck-ctl.sh launch`, or delete the task and let
   `SignalDeck Daemon Keepalive` own daemon lifecycle. As-is it is a standing
   provenance-gate bypass.
4. **D-2:** move the `RunAtLoad`/`KeepAlive` check above the calendar-trigger
   `if`, so a plist with both gets both triggers.
5. **D-4:** append `>> <StandardOutPath> 2>&1` to the generated argument line,
   or drop the dead `StandardOutPath` keys from the plists so nobody trusts
   `cleanup.sched.log` again.
6. **Eighty Loop:** either drop the hourly `TimeTrigger` (the loop self-schedules
   with `-Hours 24`) or accept that `0x800710E0` is its normal state and stop
   treating `LastTaskResult != 0` as an alert for it.
7. **Observability:** enable `Microsoft-Windows-TaskScheduler/Operational`, and
   schedule `install-windows-tasks.ps1` (report mode) so the 6 skips and any new
   drift are surfaced rather than discovered by audit.

---

## PART B — ACL audit

**Paths and permission names only. No file contents were read, and no ACL was
modified.** Every line below is verbatim `icacls` output.

### Legend

`(I)` inherited · `(F)` full control · `(RX)` read & execute ·
`(OI)` object inherit · `(CI)` container inherit · `(S,X)` synchronize + execute

### The claim under test

> local group `CodexSandboxUsers` holds read access to `daemon/.env`,
> `daemon/.env.bak-*`, and `data/signaldeck.db`

**CONFIRMED.** All three carry an inherited `(I)(RX)` ACE for
`Nicholas_Nyaung\CodexSandboxUsers`.

### `daemon/.env*` — 2 files

```
C:\Users\Nicholas_N\Desktop\claude code\signaldeck\daemon\.env Nicholas_Nyaung\CodexSandboxUsers:(I)(RX)
                                                               NT AUTHORITY\SYSTEM:(I)(F)
                                                               BUILTIN\Administrators:(I)(F)
                                                               Nicholas_Nyaung\Nicholas_N:(I)(F)

Successfully processed 1 files; Failed processing 0 files
C:\Users\Nicholas_N\Desktop\claude code\signaldeck\daemon\.env.bak-20260719-175045 Nicholas_Nyaung\CodexSandboxUsers:(I)(RX)
                                                                                   NT AUTHORITY\SYSTEM:(I)(F)
                                                                                   BUILTIN\Administrators:(I)(F)
                                                                                   Nicholas_Nyaung\Nicholas_N:(I)(F)

Successfully processed 1 files; Failed processing 0 files
```

Scope correction to the brief: `daemon/.env.bak-*` matches exactly **one** file,
`.env.bak-20260719-175045`, not a set.

### `data/*.db*` — 3 files

```
C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data\signaldeck.db Nicholas_Nyaung\CodexSandboxUsers:(I)(RX)
                                                                      NT AUTHORITY\SYSTEM:(I)(F)
                                                                      BUILTIN\Administrators:(I)(F)
                                                                      Nicholas_Nyaung\Nicholas_N:(I)(F)

Successfully processed 1 files; Failed processing 0 files
C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data\signaldeck.db-shm Nicholas_Nyaung\CodexSandboxUsers:(I)(RX)
                                                                          NT AUTHORITY\SYSTEM:(I)(F)
                                                                          BUILTIN\Administrators:(I)(F)
                                                                          Nicholas_Nyaung\Nicholas_N:(I)(F)

Successfully processed 1 files; Failed processing 0 files
C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data\signaldeck.db-wal Nicholas_Nyaung\CodexSandboxUsers:(I)(RX)
                                                                          NT AUTHORITY\SYSTEM:(I)(F)
                                                                          BUILTIN\Administrators:(I)(F)
                                                                          Nicholas_Nyaung\Nicholas_N:(I)(F)

Successfully processed 1 files; Failed processing 0 files
```

`-wal` and `-shm` matter as much as the `.db`: uncommitted rows live in the WAL,
so `(RX)` on `signaldeck.db-wal` (67,108,864 bytes) is read access to data not
yet in the main file.

### Where the ACE comes from

Every ACE above is `(I)` — inherited. Walking the chain to find the one explicit
grant:

```
--- C:\Users\Nicholas_N ---
C:\Users\Nicholas_N NT AUTHORITY\SYSTEM:(OI)(CI)(F)
                    BUILTIN\Administrators:(OI)(CI)(F)
                    Nicholas_Nyaung\Nicholas_N:(OI)(CI)(F)
                    S-1-15-3-65536-599108337-2355189375-1353122160-3480128286-3345335107-485756383-4087318168-230526575:(S,X)
--- C:\Users\Nicholas_N\Desktop ---
C:\Users\Nicholas_N\Desktop Nicholas_Nyaung\CodexSandboxUsers:(OI)(CI)(RX)
                            NT AUTHORITY\SYSTEM:(I)(OI)(CI)(F)
                            BUILTIN\Administrators:(I)(OI)(CI)(F)
                            Nicholas_Nyaung\Nicholas_N:(I)(OI)(CI)(F)
--- C:\Users\Nicholas_N\Desktop\claude code ---
C:\Users\Nicholas_N\Desktop\claude code Nicholas_Nyaung\CodexSandboxUsers:(I)(OI)(CI)(RX)
                                        NT AUTHORITY\SYSTEM:(I)(OI)(CI)(F)
                                        BUILTIN\Administrators:(I)(OI)(CI)(F)
                                        Nicholas_Nyaung\Nicholas_N:(I)(OI)(CI)(F)
--- C:\Users\Nicholas_N\Desktop\claude code\signaldeck ---
C:\Users\Nicholas_N\Desktop\claude code\signaldeck Nicholas_Nyaung\CodexSandboxUsers:(I)(OI)(CI)(RX)
                                                   NT AUTHORITY\SYSTEM:(I)(OI)(CI)(F)
                                                   BUILTIN\Administrators:(I)(OI)(CI)(F)
                                                   Nicholas_Nyaung\Nicholas_N:(I)(OI)(CI)(F)
--- C:\Users\Nicholas_N\Desktop\claude code\signaldeck\daemon ---
C:\Users\Nicholas_N\Desktop\claude code\signaldeck\daemon Nicholas_Nyaung\CodexSandboxUsers:(I)(OI)(CI)(RX)
                                                          NT AUTHORITY\SYSTEM:(I)(OI)(CI)(F)
                                                          BUILTIN\Administrators:(I)(OI)(CI)(F)
                                                          Nicholas_Nyaung\Nicholas_N:(I)(OI)(CI)(F)
--- C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data ---
C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data Nicholas_Nyaung\CodexSandboxUsers:(I)(OI)(CI)(RX)
                                                        NT AUTHORITY\SYSTEM:(I)(OI)(CI)(F)
                                                        BUILTIN\Administrators:(I)(OI)(CI)(F)
                                                        Nicholas_Nyaung\Nicholas_N:(I)(OI)(CI)(F)
```

**Single origin: `C:\Users\Nicholas_N\Desktop`.** It is the only path in the
chain where the ACE is explicit (no `(I)` prefix), and it carries `(OI)(CI)` —
object + container inherit — so it propagates to every file and folder under
Desktop. `C:\Users\Nicholas_N` itself has **no** `CodexSandboxUsers` ACE. The
grant is one deliberate ACE on Desktop, not per-file drift, and it covers far
more than SignalDeck: every project under `Desktop\claude code\` inherits it.

### The group

```
Name            : CodexSandboxUsers
SID             : S-1-5-21-209929897-3616501805-2598169953-1002
Description     : Codex sandbox internal group (managed)
PrincipalSource : Local

Name                                ObjectClass PrincipalSource
----                                ----------- ---------------
Nicholas_Nyaung\CodexSandboxOffline User                  Local
Nicholas_Nyaung\CodexSandboxOnline  User                  Local
```

Two members, both local sandbox service accounts. Note `CodexSandboxOnline` —
a sandbox account with network reach **and** `(RX)` on a file holding
`ALPACA_KEY` / `ALPACA_SECRET` / `SIGNALDECK_NVIDIA_KEY` (variable names only;
no value was read). That combination is the finding: `(RX)` on `daemon\.env` is
sufficient to read every credential in it, and the same principal is not
network-isolated.

### `.claude/worktrees/` — 8 repo copies

```
C:\Users\Nicholas_N\Desktop\claude code\signaldeck\.claude\worktrees Nicholas_Nyaung\CodexSandboxUsers:(I)(OI)(CI)(RX)
                                                                     NT AUTHORITY\SYSTEM:(I)(OI)(CI)(F)
                                                                     BUILTIN\Administrators:(I)(OI)(CI)(F)
                                                                     Nicholas_Nyaung\Nicholas_N:(I)(OI)(CI)(F)

Successfully processed 1 files; Failed processing 0 files
```

| worktree | last write | `.git` | `daemon/` | `data/` | secret-bearing files |
|---|---|---|---|---|---|
| `cool-murdock-bc19c7` | 8/5/2026 3:30 PM | yes | yes | no | none (`.env.example` only) |
| `dreamy-elion-231f45` | 7/27/2026 9:39 AM | yes | yes | no | none (`.env.example` only) |
| `elastic-sanderson-3cafb2` | 7/16/2026 12:54 PM | yes | yes | no | none (`.env.example` only) |
| `focused-poincare-69272e` | 8/5/2026 3:22 PM | yes | yes | no | none (`.env.example` only) |
| `gallant-ellis-cb8a27` | 8/5/2026 3:44 PM | yes | yes | no | none (`.env.example` only) |
| `happy-feistel-201107` | 7/24/2026 4:51 PM | yes | yes | no | none (`.env.example` only) |
| `mystifying-perlman-9f3dd4` | 7/24/2026 5:00 PM | yes | yes | no | none (`.env.example` only) |
| `nostalgic-borg-b8e859` | 7/25/2026 10:27 PM | yes | yes | no | none (`.env.example` only) |

All 8 inherit the same `CodexSandboxUsers (RX)` from Desktop. **Good news:** none
carries a real `.env` or a DB — repo-wide enumeration of `.env` / `.env.*`
returns only the two real files in `daemon/`, plus `.env.example` at repo root,
in `web/`, and one per worktree. Worktree exposure is source code, not secrets.
Three worktrees are stale by ≥ 12 days (`elastic-sanderson-3cafb2` since 7/16).

### Part B verdicts

| Claim | Verdict |
|---|---|
| `CodexSandboxUsers` has read access to `daemon/.env` | **CONFIRMED** — `(I)(RX)` |
| `CodexSandboxUsers` has read access to `daemon/.env.bak-*` | **CONFIRMED** — `(I)(RX)` on the single matching file `.env.bak-20260719-175045` |
| `CodexSandboxUsers` has read access to `data/signaldeck.db` | **CONFIRMED** — `(I)(RX)`, and also on `-wal` and `-shm` |
| Grant is per-file / accidental | **REFUTED** — one explicit `(OI)(CI)(RX)` ACE on `C:\Users\Nicholas_N\Desktop`, inherited everywhere below |
| Worktrees leak secrets | **REFUTED** — 8 copies, `.env.example` only, no `.env`, no `.db` |
| `CodexSandboxUsers` can *write* any audited path | **REFUTED** — `(RX)` only; no `(W)`, `(M)`, `(F)` for that group anywhere in the chain |

**Not proven:** that any sandbox process has actually read these files. This is a
permissions audit; it establishes capability, not use. Object-access auditing
(SACL) is not configured on these paths, and with the Task Scheduler operational
log already disabled it is unlikely file-access auditing is on either — so
historical read events almost certainly do not exist to check.

### Proposed ACL remediation (not applied)

Deliberately not executed — Hard Safety Invariant, and ACL changes on `Desktop`
are broad enough to break the Codex sandbox. Sketch only:

1. Break inheritance on `daemon\.env` and `daemon\.env.bak-*` and remove the
   `CodexSandboxUsers` ACE, so credentials stop inheriting a blanket Desktop
   grant. Verify nothing in the daemon's own start path runs as a sandbox
   principal first — `SignalDeck Daemon` runs as `UserId=Nicholas_N`,
   `RunLevel=Limited`, so this looks safe, but confirm before applying.
2. Consider whether `data\signaldeck.db*` needs the same treatment. It is
   business data rather than credentials, so this is a policy call, not a
   clear-cut one.
3. Delete `daemon\.env.bak-20260719-175045` if it is genuinely obsolete — an
   18-day-old credential backup with a broad read ACE is pure downside.
4. Prune the 3 worktrees untouched since ≤ 7/25.
