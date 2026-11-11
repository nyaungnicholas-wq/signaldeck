# fix-task-dst.ps1 -- one elevated click to put every SignalDeck scheduled task
# back on LOCAL wall-clock time.
#
# WHY. The triggers were registered with a fixed UTC offset in their
# StartBoundary (2026-08-12T13:10:00-07:00). Task Scheduler treats a boundary
# that carries an offset as an absolute instant, so when daylight saving ended
# on 2026-11-01 every time-of-day task began firing one hour EARLY in local
# time: Market-Close at 12:10 instead of 13:10 (it stops the daemon 50 minutes
# before the US close), Daily-Refresh at 12:15, Accuracy at 13:05, Market-Open
# at 05:20. A boundary with NO offset is local wall-clock time and follows the
# clock through both changes, which is what these schedules always meant.
#
# WHAT. For each SignalDeck task, every trigger whose StartBoundary carries an
# offset is rewritten to the same instant expressed as local time on that date,
# with the offset dropped. Nothing else about a task is touched.
#
#   -Check   print what would change and exit. Needs no elevation.
#
# Trigger changes on these S4U tasks are access-denied to a non-elevated
# process, so without -Check this re-launches itself elevated, like
# fix-task-settings-20260901.ps1. It transcripts to logs\.
param([switch]$Check)

$offsetRx = '[+-]\d\d:\d\d$'

# The same instant as local wall-clock time on its own date, offset dropped.
function Convert-ToLocalBoundary([string]$Boundary) {
    $dto = [DateTimeOffset]::Parse($Boundary, [Globalization.CultureInfo]::InvariantCulture)
    $local = [TimeZoneInfo]::ConvertTime($dto, [TimeZoneInfo]::Local)
    return $local.ToString('yyyy-MM-ddTHH:mm:ss', [Globalization.CultureInfo]::InvariantCulture)
}

function Get-Plan {
    $plan = @()
    foreach ($t in (Get-ScheduledTask -TaskName 'SignalDeck*')) {
        foreach ($tr in $t.Triggers) {
            $sb = [string]$tr.StartBoundary
            if ($sb -match $offsetRx) {
                $plan += [pscustomobject]@{ Task = $t.TaskName; From = $sb; To = (Convert-ToLocalBoundary $sb) }
            }
        }
    }
    return $plan
}

if ($Check) {
    $plan = @(Get-Plan)
    $plan | Sort-Object Task, From -Unique | Format-Table -AutoSize | Out-String -Width 200 | Write-Host
    Write-Host ("{0} trigger(s) carry a fixed offset" -f $plan.Count)
    exit 0
}

$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$admin = (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    Start-Process powershell -Verb RunAs -ArgumentList @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"")
    exit
}
$ErrorActionPreference = 'Stop'
$log = Join-Path (Split-Path $PSScriptRoot -Parent) 'logs\fix-task-dst.log'
Start-Transcript -Path $log -Append | Out-Null
try {
    # Never re-register a scheduled job underneath itself. The long-running
    # services are expected to be Running and have no time-of-day trigger.
    $services = 'SignalDeck Daemon', 'SignalDeck Web', 'SignalDeck Local Workspace', 'SignalDeck Tunnel', 'SignalDeck Cloudflare Tunnel'
    $busy = @(Get-ScheduledTask -TaskName 'SignalDeck*' | Where-Object { $_.State -eq 'Running' -and $services -notcontains $_.TaskName })
    if ($busy.Count -gt 0) {
        Write-Host ("REFUSED: running right now: {0}. Run this again when they finish." -f (($busy | ForEach-Object { $_.TaskName }) -join ', ')) -ForegroundColor Red
        return
    }
    foreach ($t in (Get-ScheduledTask -TaskName 'SignalDeck*')) {
        $changed = 0
        foreach ($tr in $t.Triggers) {
            $sb = [string]$tr.StartBoundary
            if ($sb -match $offsetRx) { $tr.StartBoundary = (Convert-ToLocalBoundary $sb); $changed++ }
        }
        if ($changed -gt 0) {
            Set-ScheduledTask -TaskName $t.TaskName -Trigger $t.Triggers | Out-Null
            Write-Host ("+ {0} : {1} trigger(s) now local wall-clock time" -f $t.TaskName, $changed)
        }
    }

    Write-Host ''
    Write-Host 'VERIFY (read back from Task Scheduler, not from this script''s intent):'
    $left = @(Get-Plan)
    $want = @{ 'SignalDeck Market-Close' = '13:10'; 'SignalDeck Daily-Refresh' = '13:15'; 'SignalDeck Accuracy' = '14:05'; 'SignalDeck Market-Open' = '06:20' }
    $wrong = @()
    foreach ($name in ($want.Keys | Sort-Object)) {
        $next = (Get-ScheduledTask -TaskName $name | Get-ScheduledTaskInfo).NextRunTime
        $got = if ($next) { $next.ToString('HH:mm') } else { '(none)' }
        Write-Host ("  {0,-26} next run {1}  (want {2} local)" -f $name, $next, $want[$name])
        if ($got -ne $want[$name]) { $wrong += $name }
    }
    if ($left.Count -gt 0 -or $wrong.Count -gt 0) {
        Write-Host ("FIX-TASK-DST FAILED: {0} trigger(s) still carry an offset; wrong next-run time: {1}" -f $left.Count, ($wrong -join ', ')) -ForegroundColor Red
    } else {
        Write-Host 'FIX-TASK-DST OK' -ForegroundColor Green
    }
} finally {
    Stop-Transcript | Out-Null
}
Read-Host 'Enter to close'
