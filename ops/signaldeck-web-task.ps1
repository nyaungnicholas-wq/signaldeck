<#
================================================================================
 APPROVED AND REGISTERED 2026-08-06. BLOCKED-3 is closed.
================================================================================

 TARGET: replaces ops/com.signaldeck.web.plist on Windows.
 Promoted from drafts/pending-approval/devops/ to ops/ and run with -Install.
 Verified live the same day: task "SignalDeck Web" Running, node.exe listening
 on :8323, GET / -> HTTP 200 text/html (10738 bytes), and /api/version proxying
 through to the daemon on :8322 (revision e97fc91, modified:false).
 Re-running with -Install is idempotent (-Force replaces in place).

 NOTE: `next start` with no -H binds the wildcard address, so the UI is reachable
 from the LAN (verified: http://192.168.4.49:8323/ -> 200), not just localhost.
 That matches the plist, which also passed no -H. Add `-H 127.0.0.1` to $argLine
 if it should be loopback-only -- that is a behaviour change, so it is not done here.

--------------------------------------------------------------------------------
 WHY THIS EXISTS
--------------------------------------------------------------------------------
 ops/install-windows-tasks.ps1 recreates the launchd schedule from the plists,
 but it skips the web app. Verified by running the installer in its default
 report mode (it changes nothing without -Install):

     PS> .\ops\install-windows-tasks.ps1
     ...
     SKIP   SignalDeck Web                     no shell script in ProgramArguments
     ...
     would install: 0 new, 10 existing, 6 skipped

 The reason is NOT the macOS path, which is worth stating plainly because it is
 the intuitive guess and it is wrong. install-windows-tasks.ps1:88 already
 rewrites absolute macOS paths onto the local repo. The skip happens earlier, at
 install-windows-tasks.ps1:82-86, which requires a `.sh` entry in
 ProgramArguments:

     $sh = $pargs | Where-Object { $_ -is [string] -and $_ -match '\.sh$' } | Select-Object -First 1
     if (-not $sh) { Write-Output ("SKIP ... no shell script in ProgramArguments") ... }

 com.signaldeck.web.plist launches `node_modules/.bin/next` directly, not a
 shell script, so it can never match. Four other plists (Awake, Tunnel,
 StockTrader Hud, TickStream Daemon) are skipped for the same reason. Even if
 the .sh check were relaxed, the plist has RunAtLoad=false and no KeepAlive, so
 the installer's trigger logic (lines 118-126) would then skip it a second time
 as "on-demand only".

 CONSEQUENCE, MEASURED: the SignalDeck web UI is not running on this machine.

     $ curl -s -o /dev/null -w "http=%{http_code}\n" --max-time 8 http://127.0.0.1:8323/
     http=000        # connection refused

 The daemon on :8322 is up; only the UI is missing. ops/signaldeck-ctl.sh, the
 normal way to start it, is launchctl-based (4 call sites) and is a no-op here.

--------------------------------------------------------------------------------
 DESIGN — this deliberately has NO trigger
--------------------------------------------------------------------------------
 The plist is on-demand: RunAtLoad=false, no KeepAlive, no StartCalendarInterval.
 Its own header says it "loads idle at login, runs only when started via
 ops/signaldeck-ctl.sh up (or the market-open trigger)".

 The market-open path does not actually start it. ops/market-open-guard.sh ends
 with `exec ... signaldeck-ctl.sh collect`, and `collect` prints "SignalDeck
 collecting - daemon + tunnel up (no web UI)" (signaldeck-ctl.sh:130). So on the
 Mac the web app starts only when a human runs `ctl up`.

 A registered task with no trigger reproduces that exactly: it sits idle until
 someone runs Start-ScheduledTask. Adding a 06:20 weekday trigger would be a
 behaviour change, not a port, so it is not done here. If a human decides they
 want the UI up during market hours, that is one added line and a separate
 decision -- see "OPTIONAL" at the bottom.

--------------------------------------------------------------------------------
 TO INSTALL  (the exact commands a human runs; nothing before this changes state)
--------------------------------------------------------------------------------
 1. Review this file.
 2. Dry run -- prints the exact action and registers nothing:

        cd "C:\Users\Nicholas_N\Desktop\claude code\signaldeck"
        powershell -ExecutionPolicy Bypass -File ".\ops\signaldeck-web-task.ps1"

 3. Register it:

        powershell -ExecutionPolicy Bypass -File ".\ops\signaldeck-web-task.ps1" -Install

 4. Start it and confirm the UI answers:

        Start-ScheduledTask -TaskName "SignalDeck Web"
        curl.exe -s -o NUL -w "http=%{http_code}\n" http://127.0.0.1:8323/

    Expect http=200. If it is 000, read logs\web.err.log.

 5. Stop it when done:

        Stop-ScheduledTask -TaskName "SignalDeck Web"

--------------------------------------------------------------------------------
 TO UNDO  (complete removal -- leaves the machine as it is right now)
--------------------------------------------------------------------------------
        Stop-ScheduledTask       -TaskName "SignalDeck Web" -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName "SignalDeck Web" -Confirm:$false

 or equivalently:

        powershell -ExecutionPolicy Bypass -File ".\ops\signaldeck-web-task.ps1" -Remove

 Confirm it is gone:

        Get-ScheduledTask -TaskName "SignalDeck Web" -ErrorAction SilentlyContinue

 (no output = removed). Unregistering does not touch the repo, the build, the
 daemon, or the database. If a `next start` process somehow survives the stop,
 find and end it explicitly:

        Get-CimInstance Win32_Process -Filter "Name='node.exe'" |
          Where-Object { $_.CommandLine -like '*next*start*8323*' } |
          Select-Object ProcessId, CommandLine
#>
[CmdletBinding()]
param(
  # Nothing is registered unless -Install is passed. Default is a report,
  # matching the convention in ops/install-windows-tasks.ps1.
  [switch]$Install,
  [switch]$Remove
)

$ErrorActionPreference = 'Stop'

$TaskName = 'SignalDeck Web'   # matches Get-TaskName('com.signaldeck.web') in
                               # install-windows-tasks.ps1, so if that script is
                               # ever taught to handle non-.sh plists it updates
                               # this task instead of creating a duplicate.
$Port = 8323

# Resolve the repo from this script's location: drafts/pending-approval/devops
# -> repo root. Kept correct if the file is later moved to ops/ (one level down).
$here = $PSScriptRoot
$repo = if ((Split-Path $here -Leaf) -eq 'ops') { Split-Path $here -Parent }
        else { (Get-Item $here).Parent.Parent.Parent.FullName }
$web  = Join-Path $repo 'web'
$logs = Join-Path $repo 'logs'

if ($Remove) {
  if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask       -TaskName $TaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Output "REMOVED  $TaskName"
  } else {
    Write-Output "not registered: $TaskName (nothing to remove)"
  }
  exit 0
}

# --- preflight ---------------------------------------------------------------
# These are the three things that make the task register fine and then fail at
# run time, which is the worst outcome because Scheduled Tasks reports success
# for a process that exited 1. Check them up front.
$problems = @()

$node = (Get-Command node -ErrorAction SilentlyContinue).Source
if (-not $node) { $problems += 'node is not on PATH (install Node, or hardcode the full path below)' }

# Run next through node directly rather than next.cmd: the .cmd shim spawns a
# child that Stop-ScheduledTask may not reach, leaving :8323 bound after a stop.
$nextBin = Join-Path $web 'node_modules\next\dist\bin\next'
if (-not (Test-Path $nextBin)) { $problems += "missing $nextBin (run: cd web; npm ci)" }

# `next start` serves a prebuilt app and exits immediately without one.
$buildId = Join-Path $web '.next\BUILD_ID'
if (-not (Test-Path $buildId)) { $problems += "no production build (run: cd web; npm run build)" }

if (-not (Test-Path $logs)) { $problems += "missing $logs (mkdir it, or the redirection fails)" }

$portBusy = $null -ne (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)

# --- the action --------------------------------------------------------------
# cmd.exe wraps node purely to get the plist's StandardOutPath/StandardErrorPath
# behaviour; Register-ScheduledTask has no log-redirection option of its own.
# The doubled outer quotes are cmd's documented /c form for a command whose own
# arguments are quoted -- verified with a `node --version` probe.
$outLog  = Join-Path $logs 'web.out.log'
$errLog  = Join-Path $logs 'web.err.log'
$argLine = '/c ""{0}" "{1}" start -p {2} >>"{3}" 2>>"{4}""' -f $node, $nextBin, $Port, $outLog, $errLog

$action = New-ScheduledTaskAction -Execute $env:ComSpec -Argument $argLine -WorkingDirectory $web

# ExecutionTimeLimit 0 = no limit: this is a long-running server, and the
# Scheduled Tasks default (3 days) would kill it mid-session.
# IgnoreNew: a second Start-ScheduledTask must not launch a second server that
# fails to bind :8323 and leaves a confusing error in the log.
# No RestartCount/RestartInterval: the plist has no KeepAlive, so a crash stays
# crashed rather than silently flapping. Same behaviour, deliberately.
$settings = New-ScheduledTaskSettingsSet `
  -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
  -ExecutionTimeLimit ([TimeSpan]::Zero) `
  -MultipleInstances IgnoreNew

$exists = $null -ne (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue)

Write-Output ''
Write-Output "task        : $TaskName"
Write-Output "state       : $(if ($exists) { 'already registered (would be replaced)' } else { 'not registered' })"
Write-Output "execute     : $env:ComSpec"
Write-Output "argument    : $argLine"
Write-Output "workingdir  : $web"
Write-Output "trigger     : (none) - on-demand, matches RunAtLoad=false + no KeepAlive in the plist"
Write-Output "runlevel    : Limited (no admin needed; port $Port is above 1024)"
Write-Output "port $Port  : $(if ($portBusy) { 'ALREADY IN USE - starting the task will fail to bind' } else { 'free' })"
Write-Output ''

if ($problems.Count) {
  Write-Output 'PREFLIGHT FAILED:'
  $problems | ForEach-Object { Write-Output "  - $_" }
  Write-Output ''
  Write-Output 'Nothing was changed. Fix the above, then re-run.'
  exit 1
}
Write-Output 'preflight   : ok (node, next, production build, logs dir all present)'

if (-not $Install) {
  Write-Output ''
  Write-Output 'DRY RUN - nothing was registered. Re-run with -Install to apply.'
  exit 0
}

Register-ScheduledTask -TaskName $TaskName -Action $action -Settings $settings `
  -Description 'SignalDeck web UI (Next.js production server, port 8323). Windows port of ops/com.signaldeck.web.plist. On-demand: start with Start-ScheduledTask, stop with Stop-ScheduledTask.' `
  -RunLevel Limited -Force | Out-Null

Write-Output ''
Write-Output "$(if ($exists) { 'UPDATED' } else { 'CREATED' })  $TaskName"
Write-Output 'Start it with:  Start-ScheduledTask -TaskName "SignalDeck Web"'
Write-Output 'Undo with:      Unregister-ScheduledTask -TaskName "SignalDeck Web" -Confirm:$false'

<#
 OPTIONAL - only if a human decides the UI should follow market hours. This is a
 behaviour change from the plist, not part of the port, which is why it is not
 wired in. It would go immediately before Register-ScheduledTask, and the call
 would gain  -Trigger $trigger :

     $trigger = New-ScheduledTaskTrigger -Weekly `
       -DaysOfWeek Monday,Tuesday,Wednesday,Thursday,Friday `
       -At (Get-Date -Hour 6 -Minute 20 -Second 0)

 Note the timezone trap: the plists schedule in PT (market-open-guard.sh sets
 TZ=America/Los_Angeles explicitly), while Scheduled Tasks fires in the
 machine's local timezone. If this box is not on PT the hour must be converted,
 and it will drift twice a year at the DST boundaries because PT and the local
 zone can switch on different dates. The existing SignalDeck Market-Open task
 registered by install-windows-tasks.ps1 has this same unresolved issue -- it
 reads Hour/Minute straight out of the plist and passes them to
 New-ScheduledTaskTrigger with no conversion (install-windows-tasks.ps1:100-109).
 Out of scope here; flagged rather than silently copied.
#>
