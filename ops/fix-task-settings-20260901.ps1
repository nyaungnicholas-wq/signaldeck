# fix-task-settings-20260901.ps1 -- one elevated click to make three SignalDeck
# scheduled tasks runnable on this host's actual uptime pattern.
#
# WHY. On 2026-08-31 'SignalDeck Anchor-Publish' and 'SignalDeck Revalidation'
# were re-registered from a NON-elevated shell, which silently gave them
# LogonType=Interactive, StartWhenAvailable=False and DisallowStartIfOnBatteries
# (ops/install-windows-tasks.ps1 documents exactly this regression). This host is
# powered off through every US session, so a 14:35 Interactive trigger with no
# catch-up never fires: Task Scheduler logs Event 153 "missed its schedule" and
# 0x800710E0, and the public anchor chain goes stale at 48h. The Daemon Keepalive
# task will not restart a dead daemon on battery for the same DisallowStart flag.
#
# WHAT. For Anchor-Publish and Revalidation: S4U principal (runs whether the user
# is logged on or not, out of reach of console-control kills), StartWhenAvailable
# (catch up after a late boot), allowed on battery, 6h execution limit. The Anchor
# trigger moves 14:35 -> 19:30 local, inside the hours the host is actually on.
# Keepalive: battery flags only; its PT10M limit and triggers are untouched.
#
# Principal and settings changes are access-denied to a non-elevated process even
# when the account is an administrator, which is why this ships as a script that
# re-launches itself elevated. It transcripts to logs\ so "did it actually run?"
# is answerable afterwards (a refusal in a self-closing window is invisible).
$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$admin = (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    Start-Process powershell -Verb RunAs -ArgumentList @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"")
    exit
}
$ErrorActionPreference = 'Stop'
$log = Join-Path (Split-Path $PSScriptRoot -Parent) 'logs\fix-task-settings-20260901.log'
Start-Transcript -Path $log -Append | Out-Null
try {
    $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" `
        -LogonType S4U -RunLevel Limited
    foreach ($name in 'SignalDeck Anchor-Publish', 'SignalDeck Revalidation') {
        $t = Get-ScheduledTask -TaskName $name
        $s = $t.Settings
        $s.StartWhenAvailable = $true
        $s.DisallowStartIfOnBatteries = $false
        $s.StopIfGoingOnBatteries = $false
        $s.ExecutionTimeLimit = 'PT6H'
        Set-ScheduledTask -TaskName $name -Principal $principal -Settings $s | Out-Null
        Write-Host "+ $name : principal S4U, StartWhenAvailable, on-battery, PT6H"
    }
    Set-ScheduledTask -TaskName 'SignalDeck Anchor-Publish' `
        -Trigger (New-ScheduledTaskTrigger -Daily -At 19:30) | Out-Null
    Write-Host "+ SignalDeck Anchor-Publish : trigger daily 19:30 (was 14:35, inside the off-window)"

    $k = Get-ScheduledTask -TaskName 'SignalDeck Daemon Keepalive'
    $ks = $k.Settings
    $ks.DisallowStartIfOnBatteries = $false
    $ks.StopIfGoingOnBatteries = $false
    Set-ScheduledTask -TaskName $k.TaskName -Settings $ks | Out-Null
    Write-Host "+ SignalDeck Daemon Keepalive : allowed on battery (limit and triggers untouched)"

    Write-Host ""
    Write-Host "VERIFY (read back from Task Scheduler, not from this script's intent):"
    foreach ($name in 'SignalDeck Anchor-Publish', 'SignalDeck Revalidation', 'SignalDeck Daemon Keepalive') {
        $t = Get-ScheduledTask -TaskName $name
        $tr = ($t.Triggers | ForEach-Object { "$($_.StartBoundary)" }) -join '; '
        Write-Host ("  {0,-28} LogonType={1} StartWhenAvailable={2} DisallowStartIfOnBatteries={3} Limit={4} Trigger={5}" -f `
            $name, $t.Principal.LogonType, $t.Settings.StartWhenAvailable,
            $t.Settings.DisallowStartIfOnBatteries, $t.Settings.ExecutionTimeLimit, $tr)
    }
    $bad = @('SignalDeck Anchor-Publish', 'SignalDeck Revalidation' | Where-Object {
        $x = Get-ScheduledTask -TaskName $_
        $x.Principal.LogonType -ne 'S4U' -or -not $x.Settings.StartWhenAvailable -or $x.Settings.DisallowStartIfOnBatteries })
    if ($bad.Count -gt 0) { Write-Host "FIX-TASK-SETTINGS FAILED for: $($bad -join ', ')" -ForegroundColor Red }
    else { Write-Host "FIX-TASK-SETTINGS OK" -ForegroundColor Green }
} finally {
    Stop-Transcript | Out-Null
}
Read-Host "Enter to close"
