<#
.SYNOPSIS
Keepalive for the two local Next.js web instances of SignalDeck.
.DESCRIPTION
Runs every 5 minutes via Windows Scheduled Task, unelevated.
The "SignalDeck Web" task on port 8323 runs with an Interactive principal
and dies when a console control event reaches it (observed 2026-09-08:
exit 0xC000013A after a Playwright run ended its own server).
This script probes each web port and, on failure, restarts the matching
scheduled task. It never kills processes directly.
Mirrors ops/daemon-guard.ps1 which does the same for port 8322.

TWO CHECKS, NOT ONE (2026-09-13). Until now this probed only
GET /login for a 2xx/3xx status, and that is blind to the failure that was
actually live on this machine: port 3000 answered 200 on /login AND on /, and
served an unstyled page, because four of the fourteen assets / referenced
404'd. `next start` indexes .next/static at boot, both instances serve out of
the same mutable web/.next, and a rebuild mints new content-hash filenames that
an already-running instance will never answer for. The guard logged "3000 ok"
across the whole outage.

So liveness and completeness are now separate questions:

  LIVENESS   GET /login, status only. Cheap, every cycle. Failure = the
             instance is down; restart it. Unchanged behaviour.
  COMPLETE   ops/web-assets-check.ps1, which drives off the page's own markup
             and fetches every chunk and font it names. Failure = the instance
             is up but serving a half-build; restart it too, because a restart
             re-indexes .next and is exactly the remedy.

Deliberately NOT here: anything that needs a browser. Hydration, client
navigation and interaction are proven by web/e2e/release-smoke.spec.ts at
release time, not by a task that runs every five minutes.

A completeness failure that SURVIVES its restart is RECORDED against the build
identity, and further restarts for that same build are suppressed. The remedy
is a rebuild, and a guard that keeps bouncing a server it cannot fix turns one
broken instance into a flapping one.

That paragraph used to describe an intention rather than the code. The surviving
failure was written to the log and nothing else, so the next scheduled run
started with no memory of it, found the same incomplete instance and stopped and
started the task again - every five minutes, forever, against a build no restart
could fix. The state now lives in logs\web-guard-state.json and the suppression
lifts when the build id changes, when the instance comes back complete, or - if
web\.next\BUILD_ID cannot be read at all - six hours after it was recorded.
#>

param([string]$LogPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'logs\web-guard.log'))

$ErrorActionPreference = 'Stop'

$Targets = @(
    @{ Port = 8323; Task = 'SignalDeck Web' },
    @{ Port = 3000; Task = 'SignalDeck Local Workspace' }
)

# What this guard remembers between runs, and the only thing it remembers.
$StatePath = Join-Path (Split-Path -Parent $LogPath) 'web-guard-state.json'
$SuppressHours = 6

function Test-Web([int]$Port) {
    try {
        $r = Invoke-WebRequest -Uri ("http://127.0.0.1:{0}/login" -f $Port) -UseBasicParsing -TimeoutSec 15
        return ($r.StatusCode -ge 200 -and $r.StatusCode -lt 400)
    } catch {
        return $false
    }
}

# Returns 0 complete, 1 incomplete (up, but serving assets it cannot produce),
# 2 could not run. The check itself lives in one place and is shared with the
# release path (ops/web-release.ps1, which builds and tests a candidate in an
# the gate that ships to them cannot drift apart.
$AssetsCheck = Join-Path $PSScriptRoot 'web-assets-check.ps1'
function Test-WebAssets([int]$Port) {
    if (-not (Test-Path -LiteralPath $AssetsCheck)) {
        Write-Log ("{0} asset check MISSING at {1} - completeness not verified" -f $Port, $AssetsCheck)
        return 2
    }
    $out = & powershell -NoProfile -ExecutionPolicy Bypass -File $AssetsCheck -Port $Port 2>&1
    $rc = $LASTEXITCODE
    foreach ($line in @($out)) { Write-Log ("{0} {1}" -f $Port, $line) }
    return $rc
}

function Write-Log([string]$Line) {
    $dir = Split-Path -Parent $LogPath
    if ($dir -and -not (Test-Path -LiteralPath $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    $stamp = (Get-Date).ToString('yyyy-MM-ddTHH:mm:ss')
    Add-Content -LiteralPath $LogPath -Value ("{0} {1}" -f $stamp, $Line) -Encoding UTF8
    if ((Test-Path -LiteralPath $LogPath) -and ((Get-Item -LiteralPath $LogPath).Length -gt 2MB)) {
        $tail = Get-Content -LiteralPath $LogPath -Tail 2000
        Set-Content -LiteralPath $LogPath -Value $tail -Encoding UTF8
    }
}

# The state file is a convenience, never a dependency: a guard that cannot read
# its own memory must behave as one with none rather than throw and stop
# checking the web tier. Every accessor below fails to an empty answer.
function Get-GuardState {
    try {
        if (-not (Test-Path -LiteralPath $StatePath)) { return @{} }
        $raw = Get-Content -LiteralPath $StatePath -Raw
        if (-not $raw) { return @{} }
        $state = @{}
        foreach ($p in (ConvertFrom-Json $raw).PSObject.Properties) { $state[$p.Name] = $p.Value }
        return $state
    } catch {
        return @{}
    }
}

function Set-GuardState([hashtable]$State) {
    try {
        Set-Content -LiteralPath $StatePath -Value ($State | ConvertTo-Json -Depth 3) -Encoding UTF8
    } catch {
        Write-Log ("could not write {0}: {1}" -f $StatePath, $_.Exception.Message)
    }
}

# The identity of the build on disk. A rebuild changes it, which is the event
# that makes another restart worth trying. $null when it cannot be read - then
# the suppression falls back to time, because "unknown" must not mean "retry
# forever".
function Get-BuildId {
    try {
        $p = Join-Path (Split-Path -Parent $PSScriptRoot) 'web\.next\BUILD_ID'
        if (-not (Test-Path -LiteralPath $p)) { return $null }
        $line = Get-Content -LiteralPath $p -TotalCount 1
        if (-not $line) { return $null }
        return $line.Trim()
    } catch {
        return $null
    }
}

try {
    # STAND DOWN DURING A RELEASE. ops/web-release.ps1 stops the task, renames
    # web\.next and starts it again; a guard arriving in that window sees a port
    # that is down or serving a half-swapped directory and "repairs" it by
    # restarting the task underneath the release, or records a suppression entry
    # against a build that was never actually broken.
    #
    # ops/daemon-guard.ps1 has honoured this same lock since 2026-08 and this
    # one never did. The ceiling matters as much as the lock: a release that
    # dies without cleaning up must not leave the web tier unguarded forever, so
    # a stale lock is reported and ignored rather than obeyed.
    $lock = Join-Path (Split-Path -Parent $PSScriptRoot) 'ops\.maintenance'
    if (Test-Path -LiteralPath $lock) {
        $ageMin = [math]::Round(((Get-Date) - (Get-Item -LiteralPath $lock).LastWriteTime).TotalMinutes, 1)
        if ($ageMin -lt 30) {
            Write-Log ("maintenance lock held ({0} min): standing down, not touching the web tier" -f $ageMin)
            Write-Output "web-guard: maintenance lock held"
            exit 0
        }
        Write-Log ("maintenance lock is {0} min old, past the 30 min ceiling: ignoring it" -f $ageMin)
    }

    $results = @()
    $state = Get-GuardState
    foreach ($t in $Targets) {
        $port = $t.Port
        $task = $t.Task
        $portKey = [string]$port

        # --- liveness -------------------------------------------------------
        $live = Test-Web -Port $port
        $why = 'DOWN'
        if ($live) {
            # Up. Now the question the old guard never asked.
            $assets = Test-WebAssets -Port $port
            if ($assets -eq 0) {
                Write-Log ("{0} ok" -f $port)
                $results += "{0} ok" -f $port
                continue
            }
            if ($assets -eq 2) {
                # The check could not run. Restarting on an unrunnable check is
                # how a guard turns its own outage into a flapping web tier.
                # Report it instead; unknown is not ok, but it is not DOWN.
                Write-Log ("{0} UNVERIFIED - liveness ok, completeness could not be measured" -f $port)
                $results += "{0} UNVERIFIED" -f $port
                continue
            }
            $why = 'INCOMPLETE (serving assets it cannot produce)'
            # Have we already restarted this exact build and watched it come
            # back just as incomplete? Then the build is the fault and another
            # restart is the flapping this guard promised not to do. Only the
            # INCOMPLETE path consults this: a DOWN server is a different
            # failure and is always worth a restart.
            if ($state.ContainsKey($portKey)) {
                $buildId = Get-BuildId
                $entry = $state[$portKey]
                if ($buildId -and $entry.BuildId -eq $buildId) {
                    Write-Log ("{0} STILL INCOMPLETE on the same build {1} - not restarting; rebuild is the remedy" -f $port, $buildId)
                    $results += "{0} INCOMPLETE" -f $port
                    continue
                }
                if (-not $buildId) {
                    $lifts = ([datetime]$entry.MarkedAt).AddHours($SuppressHours)
                    if ((Get-Date) -lt $lifts) {
                        Write-Log ("{0} STILL INCOMPLETE and the build id is unreadable - suppressed until {1}" -f $port, $lifts.ToString('yyyy-MM-ddTHH:mm:ss'))
                        $results += "{0} INCOMPLETE" -f $port
                        continue
                    }
                }
            }
        }

        Write-Log ("{0} {1} - restarting task '{2}'" -f $port, $why, $task)
        Stop-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 3
        Start-ScheduledTask -TaskName $task
        # WAIT ON THE REAL CONDITION, NOT ON A STATUS CODE. The first version of
        # this block polled Test-Web and then asset-checked once, the moment
        # anything answered. Measured 2026-09-13: the restart at 21:28:08
        # succeeded, but /login answered 3 s later from the OLD process still
        # shutting down, the single asset check ran against it, and the guard
        # filed "back but STILL INCOMPLETE" for a server that was healthy 9 s
        # after that. A one-shot check on a racing port reports the race.
        #
        # "Back" therefore means "serving a complete build", which is the thing
        # this guard exists to assert, polled to the same 45 s budget.
        $elapsed = 0
        $final = 2
        $sawLive = $false
        while ($elapsed -lt 45) {
            Start-Sleep -Seconds 3
            $elapsed += 3
            if (-not (Test-Web -Port $port)) { continue }
            $sawLive = $true
            $final = Test-WebAssets -Port $port
            if ($final -eq 0) { break }
        }

        if ($final -eq 0) {
            Write-Log ("{0} back and complete ({1} s)" -f $port, $elapsed)
            $results += "{0} ok" -f $port
            # Fixed. Forget the suppression so the next real incompleteness is
            # restarted immediately instead of inheriting this one's verdict.
            if ($state.ContainsKey($portKey)) { $state.Remove($portKey); Set-GuardState $state }
        } elseif (-not $sawLive) {
            Write-Log ("{0} STILL DOWN after 45 s" -f $port)
            $results += "{0} DOWN" -f $port
            # A different failure entirely, and one a restart CAN fix. Clearing
            # it stops a stale incompleteness from muting the down path.
            if ($state.ContainsKey($portKey)) { $state.Remove($portKey); Set-GuardState $state }
        } elseif ($final -eq 2) {
            Write-Log ("{0} back ({1} s) but completeness could not be measured" -f $port, $elapsed)
            $results += "{0} UNVERIFIED" -f $port
        } else {
            # A restart re-indexes .next, so this is where an INCOMPLETE instance
            # is expected to come back whole. Still incomplete after a full
            # restart and 45 s means the build on disk is itself bad, and
            # another restart cannot fix it -- say so and stop, rather than
            # bouncing this task again every five minutes forever.
            # RECORDED, not just logged. This is the branch whose promise the
            # file already made and did not keep: without writing it down the
            # next invocation five minutes from now starts over and restarts
            # the same bad build again.
            $state[$portKey] = @{ BuildId = Get-BuildId; MarkedAt = (Get-Date).ToString('o') }
            Set-GuardState $state
            Write-Log ("{0} back ({1} s) but STILL INCOMPLETE - the build on disk is bad; rebuild, do not restart. Further restarts for this build are suppressed." -f $port, $elapsed)
            $results += "{0} INCOMPLETE" -f $port
        }
    }

    $allOk = $true
    foreach ($r in $results) { if ($r -notlike '*ok') { $allOk = $false; break } }
    Write-Output ("web-guard: {0}" -f ($results -join ', '))
    if ($allOk) { exit 0 } else { exit 1 }
}
catch {
    $msg = $_.Exception.Message
    try { Write-Log ("FATAL: {0}" -f $msg) } catch { }
    Write-Output ("web-guard: FATAL {0}" -f $msg)
    exit 2
}