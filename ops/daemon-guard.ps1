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

# -Wait, deliberately: this script must STAY the task's running process for as
# long as the daemon lives. Detaching broke `signaldeck-ctl.sh stop`, whose
# sd_svc_stop is `schtasks /End` on the Daemon task - with the daemon detached,
# /End ended a guard that had already exited and left the daemon running, so
# every graceful stop silently degraded into the caller's kill -9 fallback and
# the daemon never got to drain its workers or checkpoint the WAL.
#
# Staying attached also makes the 5-minute keepalive trigger self-limiting:
# the task is still Running while the daemon is healthy, and MultipleInstances
# is IgnoreNew, so each tick is a no-op until the daemon actually exits.
# Start the daemon by running ITS task, rather than launching the binary here.
#
# This is the whole reason the guard is a separate task. sd_svc_stop is
# `schtasks /End` on "SignalDeck Daemon", and /End only terminates the process
# Task Scheduler itself started for that task - it does not cascade to
# children. When the guard launched the binary (either detached via
# Start-Process or as a direct child), the daemon was no longer the task's own
# process, so /End ended the guard and left the daemon running: every
# `signaldeck-ctl.sh stop` silently degraded into the caller's kill -9
# fallback, skipping the worker drain and the WAL checkpoint.
#
# Keeping "SignalDeck Daemon" pointed straight at bin/signaldeckd.exe preserves
# graceful stop; this task only decides WHEN to (re)start it.

# Provenance preflight. The Windows Daemon task execs bin\signaldeckd.exe
# directly, so unlike the launchd path (ops/com.signaldeck.daemon.plist ->
# signaldeck-ctl.sh launch -> build_from_head) a restart never rebuilds, and a
# binary can outlive the commit it was built from indefinitely. The daemon's own
# gate refuses an UNATTRIBUTABLE build but says nothing about a stale one, which
# is how bin\signaldeckd.exe came to sit 22 commits behind HEAD on 2026-08-06
# across two restarts that changed nothing.
#
# The check belongs here and not in the Daemon task's action, for the reason
# spelled out just above: this script must keep starting the daemon THROUGH its
# own task, or schtasks /End stops reaching it. Default is warn-and-start; it
# only refuses when SIGNALDECK_ON_STALE_BINARY=refuse says an operator has
# chosen an outage over stale data.
$prov = Join-Path $root 'ops\run-daemon-with-provenance.ps1'
if (Test-Path $prov) {
    & $prov -CheckOnly
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }   # it has already said why
}

$sd = Join-Path $env:SystemRoot 'System32\schtasks.exe'
& $sd /Run /TN 'SignalDeck Daemon' | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Output "failed to start SignalDeck Daemon task (schtasks exit $LASTEXITCODE)"
    exit 1
}
Write-Output "started signaldeckd via its scheduled task"
