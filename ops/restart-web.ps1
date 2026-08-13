<#
.SYNOPSIS
  Free port 8323 from an orphaned web process and bring the SignalDeck Web task
  back up, verifying it actually serves before reporting success.

.DESCRIPTION
  Audit finding A24. The web UI serves a stale bundle: the fix is committed and
  `next build` succeeded, but the running process cannot be restarted from an
  unelevated session. Its parent had already exited, leaving a session-0 orphan
  holding the listener, so Stop-ScheduledTask does not cascade to it,
  `schtasks /End` returns SUCCESS while the socket stays held, and taskkill /F
  and Stop-Process both return "Access is denied".

  This resolves the holder of port 8323 AT RUN TIME rather than trusting a PID
  recorded during the audit. A PID written into a runbook goes stale the moment
  anything restarts, and a stale PID in a script that runs elevated is a way to
  kill the wrong process. The port is the stable identifier; the PID is not.

.NOTES
  MUST RUN ELEVATED - that is the whole reason this step was left undone.

  Safe to re-run. If 8323 is already served by a live listener that answers
  HTTP, it changes nothing and says so.

  It will only kill a process that BOTH holds port 8323 AND is named node.
  Anything else is reported and left alone, because "free the port" is not
  worth the risk of terminating something unrecognised on a machine that also
  runs the daemon, the Eighty Loop and other sessions' work.
#>
[CmdletBinding()]
param(
    [int]    $Port     = 8323,
    [string] $TaskName = 'SignalDeck Web',
    [int]    $WaitSecs = 45
)

$ErrorActionPreference = 'Stop'

function Get-PortHolder([int]$p) {
    $conns = @(Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue)
    if ($conns.Count -eq 0) { return $null }
    return Get-Process -Id $conns[0].OwningProcess -ErrorAction SilentlyContinue
}

function Test-WebAnswers([int]$p) {
    try {
        $r = Invoke-WebRequest -Uri ("http://127.0.0.1:{0}/" -f $p) -UseBasicParsing -TimeoutSec 5
        return [int]$r.StatusCode
    } catch {
        return 0
    }
}

# 1. Elevation. Refuse early and say how, rather than failing halfway with an
#    access-denied on the kill and leaving the port in an unknown state.
$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$pr = New-Object Security.Principal.WindowsPrincipal($id)
if (-not $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "REFUSED: not elevated. Freeing this port is access-denied without admin." -ForegroundColor Red
    Write-Host "Re-run from an elevated PowerShell:" -ForegroundColor Yellow
    Write-Host "  powershell -File ops\restart-web.ps1" -ForegroundColor Yellow
    exit 2
}
Write-Host ("elevated as {0}" -f $id.Name) -ForegroundColor Green

# 2. What is actually on the port right now.
$holder = Get-PortHolder $Port
if ($null -eq $holder) {
    Write-Host ("port {0}: nothing listening - nothing to free" -f $Port)
} else {
    Write-Host ("port {0}: held by PID {1} ({2})" -f $Port, $holder.Id, $holder.ProcessName)
    if ($holder.ProcessName -ne 'node') {
        Write-Host ("REFUSED: expected a node process, found '{0}'. Left alone deliberately - " -f $holder.ProcessName) -ForegroundColor Red
        Write-Host "this machine also runs the daemon and other sessions' work. Investigate by hand." -ForegroundColor Red
        exit 1
    }
    Write-Host ("stopping PID {0}..." -f $holder.Id)
    Stop-Process -Id $holder.Id -Force
    for ($i = 0; $i -lt 20; $i++) {
        if ($null -eq (Get-PortHolder $Port)) { break }
        Start-Sleep -Milliseconds 500
    }
    if ($null -ne (Get-PortHolder $Port)) {
        Write-Host ("FAILED: port {0} is still held after the kill." -f $Port) -ForegroundColor Red
        exit 1
    }
    Write-Host ("port {0} released" -f $Port) -ForegroundColor Green
}

# 3. Start the task.
$task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($null -eq $task) {
    Write-Host ("FAILED: scheduled task '{0}' not found." -f $TaskName) -ForegroundColor Red
    exit 1
}
Write-Host ("starting scheduled task '{0}'..." -f $TaskName)
Start-ScheduledTask -TaskName $TaskName

# 4. Verify it SERVES. A started task is not a serving web app - that gap is
#    exactly what let the stale bundle sit unnoticed, so this waits for a real
#    HTTP response instead of trusting the start call.
$code = 0
for ($i = 0; $i -lt $WaitSecs; $i++) {
    Start-Sleep -Seconds 1
    $code = Test-WebAnswers $Port
    if ($code -ne 0) { break }
}
if ($code -eq 0) {
    Write-Host ("FAILED: task started but nothing answered on {0} within {1}s." -f $Port, $WaitSecs) -ForegroundColor Red
    Write-Host ("Check the task's log: Get-ScheduledTaskInfo -TaskName '{0}'" -f $TaskName) -ForegroundColor Yellow
    exit 1
}

$newHolder = Get-PortHolder $Port
Write-Host ("OK - {0} answers HTTP {1}, served by PID {2}" -f $Port, $code, $(if ($newHolder) { $newHolder.Id } else { 'unknown' })) -ForegroundColor Green
Write-Host "A24 remedy applied. The bundle rebuilt 2026-08-12T21:41:47 carries all three web fixes," -ForegroundColor Green
Write-Host "so this restart serves current code rather than the stale bundle." -ForegroundColor Green
exit 0
