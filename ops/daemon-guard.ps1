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

# THIS SCRIPT WRITES ITS OWN LOG, because its task cannot be made to.
#
# The Keepalive task was registered by hand and so never got the log redirection
# ops\install-windows-tasks.ps1 gives every .ps1 job: its action is a bare -File
# with no redirect, and it runs S4U in session 0 where there is no console. Every
# 5 minutes this script printed the maintenance-lock decision, the universe
# fail-safe's output, "failed to start SignalDeck Daemon task", and the WARNING
# that the provenance preflight did NOT run -- a message that exists precisely so
# "the check is missing" cannot go on looking like "the check passed" -- into
# nothing at all.
#
# Fixing the TASK needs elevation (Set-ScheduledTask and schtasks /Change both
# return Access is denied unelevated; both were tried). Fixing the SCRIPT does
# not, and a transcript here makes the redirect unnecessary rather than merely
# pending. ops\fix-task-logging.ps1 still ships for an operator who wants the
# task itself corrected, and a doubled log is harmless.
#
# Never fatal: a guard that cannot open its log must still start the daemon.
try {
    $guardLog = Join-Path $root 'logs\daemon-guard.log'
    if (-not (Test-Path (Split-Path -Parent $guardLog))) {
        New-Item -ItemType Directory -Force (Split-Path -Parent $guardLog) | Out-Null
    }
    Start-Transcript -Path $guardLog -Append -ErrorAction Stop | Out-Null
    $transcribing = $true
} catch {
    $transcribing = $false
}

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

# UNIVERSE FAIL-SAFE. This must run BEFORE the "daemon is up, nothing to do"
# exit below, because the failure it repairs happens while the daemon is
# perfectly healthy - and is in fact worst then.
#
# ops/signaldeck-refresh.sh widens symbols.active from ~328 to ~2950 for up to
# an hour a day, then prunes back. A kill inside that window leaves the universe
# MAXIMALLY OPEN, which is the fail-DANGEROUS direction, and nothing else on the
# box notices: on 2026-08-06 at 13:15 the sweep died with STATUS_CONTROL_C_EXIT
# and every worker afterwards ran against 2950 symbols instead of 328.
#
# It lives in this script because this is the only 5-minute tick on the machine,
# so it is where an unowned dangerous state gets found soonest. That is the same
# reason the maintenance-lock logic above lives here.
#
# Detection is a DB marker, not a heuristic: the sweep writes meta 'sweep_open'
# in the same transaction that widens the universe, so the marker is present
# exactly when the universe is wide. Repair is delegated to the sweep's own
# `prune-only` mode rather than reimplemented in SQL here - the keep criterion
# must exist once, in one language, or recovery and a normal sweep will
# eventually disagree about which symbols survive.
#
# Same holder-plus-expiry shape as the maintenance lock, for the same reason: a
# live sweep must not be pruned out from under itself, and a hung one must not
# hold the universe open forever. A live sweep raises the bar to 2h (MAXWAIT is
# 1h, so this is well clear of a healthy run); with no sweep alive the marker is
# repaired immediately.
$refresh = Join-Path $root 'ops\signaldeck-refresh.sh'
# Same resolution order ops\install-windows-tasks.ps1 and
# ops\run-daemon-with-provenance.ps1 use. This was a hardcoded
# 'C:\Program Files\Git\bin\bash.exe' inside the same Test-Path that gated the
# block, with no else -- so a 32-bit or relocated Git turned the universe
# fail-safe into a silent no-op on every 5-minute tick, forever, printing
# nothing. That is the exact shape of the migration bugs this repo has already
# been bitten by twice: a guard that answers "fine" because it never ran.
$bash = @(
  (Join-Path $env:ProgramFiles 'Git\bin\bash.exe'),
  (Join-Path ${env:ProgramFiles(x86)} 'Git\bin\bash.exe')
) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $bash) { $bash = (Get-Command bash -ErrorAction SilentlyContinue).Source }

# Still never fatal -- a universe check must not be able to stop the daemon
# guard -- but no longer silent. Absence is reported and the guard continues.
if (-not (Test-Path $refresh)) {
    Write-Output "universe fail-safe SKIPPED: $refresh not found"
} elseif (-not $bash) {
    Write-Output 'universe fail-safe SKIPPED: no bash found (ProgramFiles, ProgramFiles(x86), PATH)'
} else {
    $sweeping = Get-CimInstance Win32_Process -Filter "Name='bash.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.CommandLine -and $_.CommandLine -like '*signaldeck-refresh*' } |
        Select-Object -First 1
    $minAge = if ($sweeping) { 7200 } else { 0 }
    try {
        & $bash ($refresh -replace '\\', '/') 'prune-only' $minAge 2>&1 | ForEach-Object { Write-Output $_ }
    } catch {
        Write-Output "universe fail-safe check failed: $_"
    }
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
# A Test-Path guard around a check FAILS OPEN, and this one has been failing
# open since it was written: measured 2026-08-11,
# ops\run-daemon-with-provenance.ps1 does not exist - the only copy in the repo
# is round2-drafts\devops\run-daemon-with-provenance.ps1, never moved into ops\.
# So `Test-Path` was false on every run, the whole block was skipped, and the
# stale-binary check the comment above describes as the fix for the 22-commits-
# behind incident HAS NEVER EXECUTED. Nothing logged the skip, which is why it
# looked correct in source for five days.
#
# Now the absence is reported rather than silently tolerated. It still starts
# the daemon - an unavailable preflight is not a reason to leave the platform
# down, and that matches the block's documented warn-and-start default - but it
# says so every run, so "the check is missing" cannot go on looking like "the
# check passed".
$prov = Join-Path $root 'ops\run-daemon-with-provenance.ps1'
if (Test-Path $prov) {
    & $prov -CheckOnly
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }   # it has already said why
} else {
    Write-Output "WARNING: provenance preflight NOT RUN - $prov is missing (a copy exists at round2-drafts\devops\). The daemon is starting WITHOUT a stale-binary check."
}

$sd = Join-Path $env:SystemRoot 'System32\schtasks.exe'
& $sd /Run /TN 'SignalDeck Daemon' | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Output "failed to start SignalDeck Daemon task (schtasks exit $LASTEXITCODE)"
    exit 1
}
Write-Output "started signaldeckd via its scheduled task"

# Close the transcript on the fall-through path. The `exit` calls above leave it
# to the process teardown, which flushes it the same way.
if ($transcribing) { try { Stop-Transcript | Out-Null } catch { } }
