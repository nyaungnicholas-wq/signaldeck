<#
.SYNOPSIS
  Detach the SignalDeck scheduled-task fleet from the interactive console
  session, turn on the log that records what happens to it, and give the Web
  task a trigger so it can recover on its own.

.DESCRIPTION
  Fixes the cause of the 0xC000013A (STATUS_CONTROL_C_EXIT) task failures.

  All 14 SignalDeck tasks run with LogonType=Interactive, so they live inside
  the user's desktop session attached to a console. Any console control event
  in that session - Ctrl+C, a console window closing, a logoff - is delivered
  to them. Go processes handle it and exit 0 ("shutdown signal received", see
  daemon/cmd/signaldeckd/main.go:145); PowerShell and node processes just die
  with 0xC000013A. One cause, two signatures, which is why the exit codes
  across the fleet never looked related.

  S4U ("run whether the user is logged on or not") moves them out of the
  interactive session entirely. A console event cannot reach a task that is not
  attached to a console.

  Also enables Microsoft-Windows-TaskScheduler/Operational, which is currently
  disabled - the reason these failures left no forensic trail and had to be
  diagnosed from exit codes and process timing.

.NOTES
  MUST RUN ELEVATED. Both operations are access-denied to a non-elevated
  process even when the account is an administrator.

  Safe to re-run: every step is idempotent and reports what it found.

  Rollback: task definitions are exported before any change. Re-import with
    Register-ScheduledTask -Xml (Get-Content <backup>.xml -Raw) -TaskName <name> -Force
  and disable the log again with
    wevtutil sl "Microsoft-Windows-TaskScheduler/Operational" /e:false

  TRADE-OFF, stated so it is a decision and not a surprise: an S4U task runs in
  session 0. It gets no desktop and no console window, and it cannot reach
  network resources that need the user's credentials (mapped drives, SMB
  shares). This fleet is background automation - a Go daemon on 127.0.0.1, a
  node server, and PowerShell/Python batch jobs reading local files and calling
  outbound HTTPS - so none of that applies. If you ever add a task that must
  draw on screen, leave that one Interactive.
#>
[CmdletBinding()]
param(
    # Restrict to a subset by name; default is the whole SignalDeck fleet.
    [string] $Match = 'SignalDeck',
    # Skip the daemon restart at the end (the principal change only takes
    # effect on a task's NEXT start, so a running daemon keeps its old token).
    [switch] $NoRestart
)

$ErrorActionPreference = 'Stop'

# Always leave evidence of what this run did. `Start-Process -Verb RunAs` opens
# a window that closes the instant the script ends, so on 2026-08-06 a refusal
# scrolled past unseen and the run was reported as done when nothing had been
# applied. A transcript makes "did it actually run?" answerable afterwards
# instead of inferred from side effects.
$logDir = Join-Path $env:LOCALAPPDATA 'SignalDeck\logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$transcript = Join-Path $logDir ("fix-task-principals-{0}.log" -f (Get-Date -Format 'yyyyMMdd-HHmmss'))
try { Start-Transcript -Path $transcript -ErrorAction Stop | Out-Null } catch { }
Write-Host "transcript: $transcript"

function Assert-Elevated {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    if (-not $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Write-Host "REFUSED: not elevated. Both operations need admin." -ForegroundColor Red
        Write-Host "Re-run from an elevated PowerShell:" -ForegroundColor Yellow
        Write-Host "  Start-Process pwsh -Verb RunAs -ArgumentList '-NoProfile','-NoExit','-File','$PSCommandPath'"
        try { Stop-Transcript | Out-Null } catch { }
        exit 1
    }
    Write-Host "elevated as $($id.Name)" -ForegroundColor Green
}

Assert-Elevated

# -- 1. back up every task definition before touching anything --------------
# Deliberately OUTSIDE the repo. ops/manifest-check.sh treats any untracked
# path under ops/ as a load-bearing file missing from git and refuses the
# deploy, so writing rollback artifacts into the working tree would quietly
# block the next `signaldeck-ctl.sh deploy`.
$stamp  = Get-Date -Format 'yyyyMMdd-HHmmss'
$backup = Join-Path $env:LOCALAPPDATA "SignalDeck\task-backups\$stamp"
New-Item -ItemType Directory -Force -Path $backup | Out-Null

$tasks = @(Get-ScheduledTask | Where-Object { $_.TaskName -match $Match })
if ($tasks.Count -eq 0) { Write-Host "no tasks match '$Match' - nothing to do."; exit 0 }

foreach ($t in $tasks) {
    $safe = $t.TaskName -replace '[^\w\-]', '_'
    Export-ScheduledTask -TaskName $t.TaskName |
        Set-Content -Path (Join-Path $backup "$safe.xml") -Encoding UTF8
}
Write-Host "backed up $($tasks.Count) task definitions -> $backup" -ForegroundColor Green

# -- 2. enable the operational log -----------------------------------------
$log      = 'Microsoft-Windows-TaskScheduler/Operational'
$wevtutil = Join-Path $env:SystemRoot 'System32\wevtutil.exe'
$before   = (& $wevtutil gl $log | Select-String '^\s*enabled:').Line.Trim()
if ($before -match 'true') {
    Write-Host "operational log already enabled" -ForegroundColor Green
} else {
    & $wevtutil sl $log /e:true
    if ($LASTEXITCODE -ne 0) { throw "failed to enable $log (exit $LASTEXITCODE)" }
    $after = (& $wevtutil gl $log | Select-String '^\s*enabled:').Line.Trim()
    Write-Host "operational log: $before -> $after" -ForegroundColor Green
}

# -- 3. convert each task to S4U -------------------------------------------
# RunLevel is preserved per task rather than forced. Everything in this fleet
# is Limited today, and silently elevating a background job while "fixing" its
# logon type would be a security change smuggled in under a reliability fix.
$changed = 0; $skipped = 0; $failed = @()
foreach ($t in $tasks) {
    if ($t.Principal.LogonType -eq 'S4U') {
        Write-Host ("  = {0,-34} already S4U" -f $t.TaskName); $skipped++; continue
    }
    try {
        $principal = New-ScheduledTaskPrincipal `
            -UserId    $t.Principal.UserId `
            -LogonType S4U `
            -RunLevel  $t.Principal.RunLevel
        Set-ScheduledTask -TaskName $t.TaskName -Principal $principal | Out-Null

        $now = (Get-ScheduledTask -TaskName $t.TaskName).Principal.LogonType
        if ($now -ne 'S4U') { throw "still $now after Set-ScheduledTask" }
        Write-Host ("  + {0,-34} Interactive -> S4U" -f $t.TaskName) -ForegroundColor Green
        $changed++
    } catch {
        Write-Host ("  ! {0,-34} FAILED: {1}" -f $t.TaskName, $_.Exception.Message) -ForegroundColor Red
        $failed += $t.TaskName
    }
}

# -- 4. report -------------------------------------------------------------
Write-Host ""
Write-Host "changed $changed, already-S4U $skipped, failed $($failed.Count)"
Get-ScheduledTask | Where-Object { $_.TaskName -match $Match } |
    Group-Object { $_.Principal.LogonType } |
    Select-Object Count, Name | Format-Table -AutoSize

if ($failed.Count) {
    Write-Host "FAILED tasks (restore from $backup if needed): $($failed -join ', ')" -ForegroundColor Red
    try { Stop-Transcript | Out-Null } catch { }
    exit 1
}

# -- 4b. give SignalDeck Web a self-healing trigger -------------------------
# Web had NO trigger at all: on-demand only. So when a console event killed it
# on 2026-08-06 nothing was ever going to bring it back, and the UI stayed down
# until someone noticed and started it by hand. Every other task in the fleet
# recovers on a schedule; this one could not.
#
# AtStartup + a 5-minute repetition is the whole fix, and it needs no second
# task and no guard script: MultipleInstances=IgnoreNew means a firing while the
# server is already up is discarded, so the repetition is a no-op until the
# moment the server is gone, and then it is a restart. Same effect the daemon
# gets from its separate Keepalive task, with nothing extra to maintain.
#
# The IgnoreNew check below is a HARD PRECONDITION, not decoration. Under
# Parallel or Queue, a 5-minute repetition would launch a new `next start -p
# 8323` every 5 minutes forever - a pile of node processes fighting over one
# port. Refuse rather than create that.
$webName = 'SignalDeck Web'
$web = Get-ScheduledTask -TaskName $webName -ErrorAction SilentlyContinue
if (-not $web) {
    Write-Host "'$webName' not found - skipping trigger step" -ForegroundColor Yellow
} elseif ($web.Settings.MultipleInstances -ne 'IgnoreNew') {
    Write-Host "REFUSED to add a repeating trigger: '$webName' is MultipleInstances=$($web.Settings.MultipleInstances), not IgnoreNew." -ForegroundColor Red
    Write-Host "  A repetition under that policy would stack node servers on port 8323." -ForegroundColor Red
} elseif (@($web.Triggers).Where({ $_ }).Count -gt 0) {
    Write-Host "'$webName' already has a trigger - leaving it alone" -ForegroundColor Green
} else {
    try {
        $trigger = New-ScheduledTaskTrigger -AtStartup
        $rep = (New-ScheduledTaskTrigger -Once -At (Get-Date) `
                  -RepetitionInterval (New-TimeSpan -Minutes 5)).Repetition
        # Duration must be ABSENT for "repeat indefinitely". [TimeSpan]::MaxValue
        # serialises to P99999999DT23H59M59S, which Task Scheduler rejects.
        $rep.Duration          = $null
        $rep.StopAtDurationEnd = $false
        $trigger.Repetition    = $rep

        Set-ScheduledTask -TaskName $webName -Trigger $trigger | Out-Null

        $check = (Get-ScheduledTask -TaskName $webName).Triggers | Select-Object -First 1
        if (-not $check) { throw "trigger did not persist" }
        Write-Host ("  + {0,-34} AtStartup, repeating every {1}" -f $webName, $check.Repetition.Interval) -ForegroundColor Green
    } catch {
        Write-Host "  ! $webName trigger FAILED: $($_.Exception.Message)" -ForegroundColor Red
    }
}

# -- 5. restart the daemon so it runs under the new principal ---------------
# The token is chosen when a task STARTS, so a daemon that was already running
# keeps its old interactive token - and stays killable by a console event -
# until it is restarted. Skipping this would report success while leaving the
# actual bug live in the running process.
if (-not $NoRestart) {
    $daemon = 'SignalDeck Daemon'
    if (Get-ScheduledTask -TaskName $daemon -ErrorAction SilentlyContinue) {
        Write-Host ""
        Write-Host "restarting '$daemon' so it picks up the S4U token..."
        $schtasks = Join-Path $env:SystemRoot 'System32\schtasks.exe'
        & $schtasks /End /TN $daemon 2>&1 | Out-Null
        Start-Sleep -Seconds 3
        & $schtasks /Run /TN $daemon 2>&1 | Out-Null

        $ok = $false
        foreach ($i in 1..30) {
            Start-Sleep -Seconds 2
            try {
                Invoke-WebRequest -Uri 'http://127.0.0.1:8322/api/health' -TimeoutSec 3 -UseBasicParsing | Out-Null
                $ok = $true; break
            } catch { }
        }
        if ($ok) {
            Write-Host "daemon healthy on 127.0.0.1:8322" -ForegroundColor Green
        } else {
            Write-Host "daemon did NOT answer /api/health within 60s - check logs\signaldeckd.log" -ForegroundColor Red
            try { Stop-Transcript | Out-Null } catch { }
            exit 1
        }
    }
}

Write-Host ""
Write-Host "done. Watch for recurrence with ops\check-task-health.ps1" -ForegroundColor Cyan
Write-Host "Any fresh 0xC000013A after this means something is killing tasks by a route"
Write-Host "other than the console session - and the operational log will now show it."

try { Stop-Transcript | Out-Null } catch { }
