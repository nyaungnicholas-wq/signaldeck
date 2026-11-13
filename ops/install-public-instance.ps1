# install-public-instance.ps1 -- register the two scheduled tasks that keep the
# PUBLIC (IEX-fed) SignalDeck instance alive and current.
#
#   SignalDeck Public Daemon   every 5 minutes and at logon: start the public
#                              daemon if it is not listening (public-instance.ps1
#                              -Mode keepalive).
#   SignalDeck Public Daily    06:30 local every day: decode the previous day's
#                              IEX file and rebuild the public bars (-Mode daily).
#
# Run it ELEVATED and the tasks run whether or not anyone is logged on (S4U).
# Run it without elevation and Task Scheduler registers them to run only while
# this user is logged on; the script says which it did. Re-running is safe.
#
#   -Remove   unregister both tasks
param([switch]$Remove)
$ErrorActionPreference = 'Stop'
$ps = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$script = Join-Path $PSScriptRoot 'public-instance.ps1'
$names = 'SignalDeck Public Daemon', 'SignalDeck Public Daily'

if ($Remove) {
    foreach ($n in $names) {
        if (Get-ScheduledTask -TaskName $n -ErrorAction SilentlyContinue) {
            Unregister-ScheduledTask -TaskName $n -Confirm:$false
            Write-Host "- removed $n"
        }
    }
    exit 0
}

$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$admin = (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
$logon = if ($admin) { 'S4U' } else { 'Interactive' }
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType $logon -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -StartWhenAvailable -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Hours 6)

function Arg([string]$mode) {
    return ('-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "{0}" -Mode {1}' -f $script, $mode)
}

# Keepalive: a local-time trigger that repeats every 5 minutes for ever, plus
# one at logon so the daemon is back soon after a restart.
$every = New-ScheduledTaskTrigger -Once -At (Get-Date).Date -RepetitionInterval (New-TimeSpan -Minutes 5)
$atLogon = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
Register-ScheduledTask -TaskName $names[0] -Force -Principal $principal -Settings $settings `
    -Action (New-ScheduledTaskAction -Execute $ps -Argument (Arg 'keepalive')) `
    -Trigger @($every, $atLogon) `
    -Description 'Keeps the public (IEX-fed) SignalDeck daemon running on 127.0.0.1:8422.' | Out-Null

$daily = New-ScheduledTaskTrigger -Daily -At '06:30'
Register-ScheduledTask -TaskName $names[1] -Force -Principal $principal -Settings $settings `
    -Action (New-ScheduledTaskAction -Execute $ps -Argument (Arg 'daily')) `
    -Trigger $daily `
    -Description 'Daily IEX historical data update for the public SignalDeck instance.' | Out-Null

Write-Host ''
Write-Host 'VERIFY (read back from Task Scheduler):'
$bad = 0
foreach ($n in $names) {
    $t = Get-ScheduledTask -TaskName $n
    $i = $t | Get-ScheduledTaskInfo
    Write-Host ("  {0,-26} logon={1} state={2} next={3}" -f $n, $t.Principal.LogonType, $t.State, $i.NextRunTime)
    if ($t.State -eq 'Disabled') { $bad++ }
}
if ($bad -gt 0) { Write-Host 'INSTALL-PUBLIC-INSTANCE FAILED' -ForegroundColor Red; exit 1 }
if ($admin) {
    Write-Host 'INSTALL-PUBLIC-INSTANCE OK (S4U: runs whether or not you are logged on)' -ForegroundColor Green
} else {
    Write-Host 'INSTALL-PUBLIC-INSTANCE OK (Interactive: runs only while you are logged on; re-run elevated for S4U)' -ForegroundColor Yellow
}
