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

if ($tasks.Count -eq 0) {
    Write-Host "no SignalDeck scheduled tasks found - is this the right machine?" -ForegroundColor Red
    exit 1
}

$rows = @()
$troubled = @()

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
    } catch {
        # One unreadable task must not abort the whole report.
        $rows += [PSCustomObject]@{
            Task      = $task.TaskName
            State     = $task.State
            LogonType = $task.Principal.LogonType
            LastRun   = $null
            Result    = 'UNREADABLE'
        }
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

if ($troubled.Count -gt 0) {
    Write-Host ""
    Write-Host "WARNING - terminated by a console control event (0xC000013A):" -ForegroundColor Red
    $troubled | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    exit 1
}

Write-Host ""
Write-Host "OK - no console-kill signature in the fleet" -ForegroundColor Green
exit 0
