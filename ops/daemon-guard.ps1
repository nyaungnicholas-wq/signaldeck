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

# ponytail: 90m stale-lock ceiling. The offline backup takes single-digit
# minutes; if the lock outlives that by an order of magnitude the holder is
# gone, not slow.
if (Test-Path $lock) {
    $ageMin = [math]::Round(((Get-Date) - (Get-Item $lock).LastWriteTime).TotalMinutes, 1)
    if ($ageMin -lt 90) {
        Write-Output "maintenance lock held ($ageMin min old): leaving daemon down"
        exit 0
    }
    Write-Output "maintenance lock is stale ($ageMin min): clearing and starting"
    Remove-Item $lock -Force -ErrorAction SilentlyContinue
}

if (Get-Process -Name signaldeckd -ErrorAction SilentlyContinue) {
    Write-Output "signaldeckd already running: nothing to do"
    exit 0
}

Start-Process -FilePath $exe -WorkingDirectory $cwd -WindowStyle Hidden
Write-Output "started signaldeckd"
