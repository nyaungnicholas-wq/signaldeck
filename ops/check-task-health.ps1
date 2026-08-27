<#
.SYNOPSIS
  Read-only health report for the SignalDeck scheduled-task fleet.

.DESCRIPTION
  Watches for the failure mode diagnosed on 2026-08-06: tasks terminated by a
  console control event.

  0xC000013A is STATUS_CONTROL_C_EXIT - Ctrl+C, a console window closing, or a
  logoff reached the process. It is not a crash and not an application error.
  Tasks running with LogonType=Interactive live in the user's desktop session
  attached to a console, which is what makes them reachable by those events.
  LogonType=S4U runs them in session 0 with no console, which is the fix
  (ops\fix-task-principals.ps1).

  A Go process that handles the signal exits 0 and logs a graceful shutdown, so
  the same event shows up as 0x00000000 on the daemon and 0xC000013A on the
  PowerShell and node tasks. Read the two together, not separately.

  Exit 0 = no console-kill signature found. Exit 1 = at least one task shows it.
  Needs no elevation and changes nothing.
#>

$ErrorActionPreference = 'Stop'

# Exact-name matching is wrong here: there is no task literally called
# "SignalDeck", so -TaskName 'SignalDeck' returns nothing and the report
# silently reads as an all-clear on a broken fleet.
$tasks = @(Get-ScheduledTask | Where-Object { $_.TaskName -match 'SignalDeck' } | Sort-Object TaskName)

# This script runs AS one of the tasks it inspects, so its own LastTaskResult is
# its own previous verdict. See the SELF-LATCH note below for why that one check
# is skipped for this task and no other.
$SelfTaskName = 'SignalDeck Check-Task-Health'

if ($tasks.Count -eq 0) {
    Write-Host "no SignalDeck scheduled tasks found - is this the right machine?" -ForegroundColor Red
    exit 1
}

$rows = @()
$troubled = @()
$failed = @()
$stale = @()
$unreadable = @()
$disabled = @()

# Tasks that are started by something OTHER than their own trigger, and are
# stopped on purpose. Declared once, used by both the result check and the
# no-next-run check below. Explicit rather than inferred: "meant to be started
# by something else" is a design fact, and guessing it from a missing trigger is
# exactly how a trigger that got LOST would be excused.
$onDemandStoppable = @('SignalDeck Web', 'SignalDeck Daemon')

foreach ($task in $tasks) {
    try {
        $info = Get-ScheduledTaskInfo -TaskName $task.TaskName -TaskPath $task.TaskPath -ErrorAction Stop

        # LastTaskResult is UInt32, so `-eq 0xC000013A` against a PowerShell
        # integer literal does NOT match. Compare the formatted hex string
        # instead - it is correct regardless of the underlying type or sign.
        $hex = if ($null -ne $info.LastTaskResult) { '0x{0:X8}' -f $info.LastTaskResult } else { 'N/A' }

        $rows += [PSCustomObject]@{
            Task      = $task.TaskName
            State     = $task.State
            LogonType = $task.Principal.LogonType
            LastRun   = $info.LastRunTime
            Result    = $hex
        }
        if ($hex -eq '0xC000013A') { $troubled += $task.TaskName }

        # BEYOND THE CONSOLE-KILL SIGNATURE. 0xC000013A was the only condition
        # that could ever set a non-zero exit, so this script reported "OK - no
        # console-kill signature in the fleet" over a table that measured
        # 2026-08-11 contained `SignalDeck Web` terminated (0x00041306) with a
        # LastRun three days stale, and `SignalDeck Eighty Loop` refused
        # (0x800710E0). A fleet health gate that renders the failures and then
        # says OK is one nobody reads.
        #
        # Benign codes are enumerated rather than guessed: 0x0 success,
        # 0x00041301 currently running, 0x00041303 has never run, 0x00041325
        # queued. Everything else is a real result worth a human.
        #
        # 0x800710E0 ("the operator or administrator has refused the request")
        # is benign ONLY while the task is Running: that is MultipleInstances=
        # IgnoreNew declining a trigger because the previous instance is still
        # going, which is exactly how the long-lived Daemon and Eighty Loop
        # tasks are meant to behave. Measured 2026-08-11, both sat in that state
        # legitimately. On a task that is NOT running, the same code means a
        # start was genuinely refused, and that is worth a human.
        $benign = @('0x00000000', '0x00041301', '0x00041303', '0x00041325')
        if ($hex -eq '0x800710E0' -and $task.State -eq 'Running') { $benign += $hex }
        # 0x00041306 is SCHED_S_TASK_TERMINATED - "someone ended this task",
        # which is the NORMAL terminal state for the services that are stopped
        # on purpose: signaldeck-ctl.sh stop and market-close.sh both end the
        # Web and Daemon tasks with `schtasks /End`. Benign for those two only;
        # on a batch job it still means something killed it mid-run.
        #
        # NOTE this is why task state cannot judge whether the web is UP: an
        # on-demand task that was stopped normally is indistinguishable here
        # from one that died. The liveness signal for the web is the PORT, and
        # that is what signaldeck-ctl.sh now asks (sd_port_listening 8323).
        if ($hex -eq '0x00041306' -and $onDemandStoppable -contains $task.TaskName) { $benign += $hex }
        # SELF-LATCH. This script's own task matches the 'SignalDeck' filter on
        # line 29, so its LastTaskResult is its OWN previous verdict: `exit 1`
        # below sets 0x00000001, which is not benign, so the next run flags
        # itself and exits 1 again -- forever, regardless of the fleet. Measured
        # 2026-08-13: `SignalDeck Check-Task-Health` was the ONLY task at 0x1;
        # the real fault it first caught (`SignalDeck Web`, 8/12 19:59) had long
        # since cleared, but the gate stayed red and a permanently-red alarm is
        # indistinguishable from a real one.
        #
        # Judging its own exit code is circular by construction, so skip only
        # THAT check for itself. The non-circular checks below (missing trigger,
        # NO NEXT RUN, console-kill signature, port liveness) still cover this
        # task, so a genuinely dead health checker is still caught.
        $isSelf = $task.TaskName -eq $SelfTaskName
        # DISABLED tasks: LastTaskResult is history, not a live verdict. Nothing
        # will ever run them again, so an old non-zero exit can never clear.
        # `SignalDeck Eighty Loop` was disabled on purpose (the 80%-accuracy
        # program is refuted, see the memory note) and its final 0x1 has held
        # this gate red ever since - the same permanently-red-alarm failure the
        # SELF-LATCH note above describes. The NO NEXT RUN check below already
        # exempts Disabled for exactly this reason; this makes the result check
        # agree with it. Disabled tasks are still listed in the table and named
        # in their own line below, so one disabled BY ACCIDENT stays visible.
        $isDisabled = $task.State -eq 'Disabled'
        if ($isDisabled) { $disabled += "$($task.TaskName) (last result $hex, last ran $($info.LastRunTime))" }
        if (-not $isSelf -and -not $isDisabled -and $hex -ne 'N/A' -and $benign -notcontains $hex -and $hex -ne '0xC000013A') {
            $failed += "$($task.TaskName) (result $hex)"
        }

        # NO NEXT RUN is the honest test for "this will never run again", and it
        # is the one that catches `SignalDeck Web`: measured 2026-08-11 it had
        # NO TRIGGERS AT ALL, an empty NextRunTime, and a LastRun 95h stale, so
        # nothing would ever have restarted it - while its recorded result
        # (0x00041306, terminated) looks like an ordinary stop.
        #
        # An elapsed-time threshold cannot do this job: `SignalDeck Restore` is
        # WEEKLY and legitimately idles ~7 days, so a 48h rule flags a perfectly
        # healthy task. Asking whether the scheduler still intends to run it is
        # cadence-independent, and a check that cries wolf at a weekly task is
        # one nobody reads.
        # ON-DEMAND tasks legitimately have no NextRunTime and must be exempt,
        # or this check cries wolf forever at a correct configuration:
        #   SignalDeck Web    - registered trigger-less ON PURPOSE by
        #                       ops\signaldeck-web-task.ps1, which prints
        #                       "Start it with: Start-ScheduledTask" and carries
        #                       the weekly trigger as an explicitly-NOT-wired
        #                       optional block. Started by signaldeck-ctl.sh up.
        #   SignalDeck Daemon - started by ops\daemon-guard.ps1 (the Keepalive
        #                       task's 5-minute tick), never by its own trigger.
        if (-not $info.NextRunTime -and $task.State -ne 'Running' -and $task.State -ne 'Disabled' `
                -and $onDemandStoppable -notcontains $task.TaskName) {
            $stale += "$($task.TaskName) (state $($task.State), last ran $($info.LastRunTime)) - no NextRunTime and not a known on-demand task: nothing will start this again"
        }
    } catch {
        # One unreadable task must not abort the whole report -- but it must not
        # pass, either. This rendered UNREADABLE and left $bad untouched, so the
        # gate could print "OK - no console-kill signature, and both service
        # ports are listening" and exit 0 while it had no idea what state a task
        # was in. Its sibling ops/check-grader-health.ps1 states the doctrine
        # this contradicted: "Every unknown resolves to unhealthy ... the one
        # state this check must never report is 'fine, probably'."
        $rows += [PSCustomObject]@{
            Task      = $task.TaskName
            State     = $task.State
            LogonType = $task.Principal.LogonType
            LastRun   = $null
            Result    = 'UNREADABLE'
        }
        # Collected, not flagged here: $bad is initialised to $false further
        # down, after this loop, so setting it in this catch would be silently
        # overwritten and the fix would be a no-op. The verdict block below is
        # the only place that can decide.
        $unreadable += "$($task.TaskName): $($_.Exception.Message)"
        Write-Warning "could not read info for '$($task.TaskName)': $($_.Exception.Message)"
    }
}

$rows | Format-Table -AutoSize

$interactive = @($rows | Where-Object { $_.LogonType -eq 'Interactive' }).Count
$s4u         = @($rows | Where-Object { $_.LogonType -eq 'S4U' }).Count
Write-Host ("logon types: Interactive={0}, S4U={1}, total={2}" -f $interactive, $s4u, $rows.Count)

if ($interactive -gt 0) {
    Write-Host "$interactive task(s) still Interactive - reachable by console control events. Fix: ops\fix-task-principals.ps1" -ForegroundColor Yellow
}

# Reported BEFORE any exit, so a failing fleet does not hide whether the log
# that would explain it is even switched on.
try {
    $line = & "$env:SystemRoot\System32\wevtutil.exe" gl 'Microsoft-Windows-TaskScheduler/Operational' 2>$null |
            Select-String '^\s*enabled:' | Select-Object -First 1
    if ($line -and $line.Line -match 'true') {
        Write-Host "task history: enabled"
    } else {
        Write-Host "task history: DISABLED (no forensic record)" -ForegroundColor Yellow
    }
} catch {
    Write-Host "task history: DISABLED (no forensic record)" -ForegroundColor Yellow
}

$bad = $false
if ($troubled.Count -gt 0) {
    Write-Host ""
    Write-Host "WARNING - terminated by a console control event (0xC000013A):" -ForegroundColor Red
    $troubled | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    $bad = $true
}
if ($failed.Count -gt 0) {
    Write-Host ""
    Write-Host "WARNING - task(s) whose last result was not success:" -ForegroundColor Red
    $failed | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    $bad = $true
}
if ($disabled.Count -gt 0) {
    Write-Host ""
    Write-Host "note - disabled task(s), not judged on their last result:" -ForegroundColor Yellow
    $disabled | ForEach-Object { Write-Host "  - $_" -ForegroundColor Yellow }
}
if ($unreadable.Count -gt 0) {
    Write-Host ""
    Write-Host "WARNING - task(s) whose state could not be read at all:" -ForegroundColor Red
    $unreadable | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    $bad = $true
}
if ($stale.Count -gt 0) {
    Write-Host ""
    Write-Host "WARNING - task(s) with no scheduled next run (nothing will start them again):" -ForegroundColor Red
    $stale | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    $bad = $true
}
# SERVICE PORTS. The two long-running services are deliberately exempt from the
# task-result and next-run checks above ($onDemandStoppable): they are stopped
# on demand, so 0x00041306 and an empty NextRunTime are normal for them and
# flagging those would cry wolf. But that exemption left NOTHING here watching
# them at all, and the comment 50 lines up already names the honest signal --
# "that is what signaldeck-ctl.sh now asks (sd_port_listening 8323)" -- without
# this script ever asking it.
#
# Measured 2026-08-12: port 8323 had been dead since 08-07 and this gate
# reported "OK - no console-kill signature in the fleet" every time it was run.
# A fleet gate that is green while the product's own UI is unreachable is the
# defect it exists to prevent, one level up.
#
# Asking the PORT rather than the task is the whole point: the web process
# outlives the task that started it (schtasks /End does not cascade to the
# child holding the socket), so task state cannot answer this question.
$portsDown = @()
foreach ($svc in @(@{n = 'daemon (API)'; p = 8322 }, @{n = 'web (UI)'; p = 8323 })) {
    # One instant sample turns the nightly fleet restart into a red that
    # sticks until the next scheduled run: measured 2026-08-25, this gate said
    # "nothing listening on 8322" while the port was bound and answering 401 —
    # it had sampled inside the 18:45 restart. Re-ask across that window
    # before declaring a service down; a genuinely dead port is dead on all
    # four samples and still goes red, 90 seconds later.
    $listening = $false
    foreach ($attempt in 1..4) {
        $listening = @(Get-NetTCPConnection -State Listen -LocalPort $svc.p -ErrorAction SilentlyContinue).Count -gt 0
        if ($listening) { break }
        if ($attempt -lt 4) { Start-Sleep -Seconds 30 }
    }
    if ($listening) {
        Write-Host ("  = {0,-14} listening on {1}" -f $svc.n, $svc.p)
    }
    else {
        $portsDown += ("{0} - nothing listening on {1}" -f $svc.n, $svc.p)
    }
}
if ($portsDown.Count -gt 0) {
    Write-Host ""
    Write-Host "WARNING - service port(s) not listening:" -ForegroundColor Red
    $portsDown | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    $bad = $true
}

if ($bad) { exit 1 }

Write-Host ""
Write-Host "OK - no console-kill signature, and both service ports are listening" -ForegroundColor Green
exit 0
