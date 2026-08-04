# Task Scheduler entry point for signaldeckd: start it unless downtime is planned.
#
# ASCII ONLY in this file. Task Scheduler invokes Windows PowerShell 5.1, which
# reads .ps1 as ANSI, so a UTF-8 em-dash in a comment is enough to make the
# whole script a parse error. That is not hypothetical - it happened here.
#
# Why this exists: the daemon exits 0 on any clean stop, and Task Scheduler's
# restart-on-failure policy only fires on a NON-zero exit, so a graceful stop
# left the product down until the next logon. The task now also carries a
# 5-minute repeating trigger; this script is what makes that trigger safe.
#
# The complication is market-close.sh, which deliberately stops the daemon so
# the daily backup can VACUUM INTO with zero contention - and whose safety
# check SKIPS the backup entirely if the daemon is alive. A blind 5-minute
# keepalive would restart the daemon mid-backup and silently cost us the
# backup. So planned downtime takes the lock and stays down; anything else
# self-heals within 5 minutes.
#
# The staleness timeout is the important half: market-close.sh was observed
# dying mid-run (STATUS_CONTROL_C_EXIT, 2026-08-03). A lock with no expiry
# would have turned that crash into permanent downtime - trading one silent
# failure for a worse one.
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$lock = Join-Path $root 'ops\.maintenance'
$exe  = Join-Path $root 'bin\signaldeckd.exe'
$cwd  = Join-Path $root 'daemon'

# Deciding whether the lock still has a live holder.
#
# A trap in market-close.sh is not enough: that script is killed with
# STATUS_CONTROL_C_EXIT on this machine (seen 2026-08-03 and again 2026-08-04),
# and a console-control kill does not run bash EXIT traps, so the lock outlives
# the holder. The 90m ceiling alone would then leave the daemon down for 90
# minutes after a two-minute backup died.
#
# So: look for an actual holder. The lock is only honoured while a process that
# takes it is running, with a short grace so we never race a holder that has
# taken the lock but not yet appeared in the process table.
$LOCK_HOLDERS = 'market-close', 'backup-offline', 'signaldeck-ctl'
if (Test-Path $lock) {
    $ageMin = [math]::Round(((Get-Date) - (Get-Item $lock).LastWriteTime).TotalMinutes, 1)
    $holder = Get-CimInstance Win32_Process -Filter "Name='bash.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $c = $_.CommandLine; $c -and ($LOCK_HOLDERS | Where-Object { $c -like "*$_*" }) } |
        Select-Object -First 1

    if ($ageMin -lt 2) {
        Write-Output "maintenance lock just taken ($ageMin min): leaving daemon down"
        exit 0
    }
    if ($holder -and $ageMin -lt 90) {
        Write-Output "maintenance lock held by pid $($holder.ProcessId) ($ageMin min): leaving daemon down"
        exit 0
    }
    if ($holder) {
        Write-Output "maintenance lock held past the 90 min ceiling by pid $($holder.ProcessId): clearing anyway"
    } else {
        Write-Output "maintenance lock has no live holder ($ageMin min): clearing and starting"
    }
    Remove-Item $lock -Force -ErrorAction SilentlyContinue
}

if (Get-Process -Name signaldeckd -ErrorAction SilentlyContinue) {
    Write-Output "signaldeckd already running: nothing to do"
    exit 0
}

Start-Process -FilePath $exe -WorkingDirectory $cwd -WindowStyle Hidden
Write-Output "started signaldeckd"
