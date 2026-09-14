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

A completeness failure that SURVIVES its restart is reported and left alone
rather than restarted again on the next cycle. The remedy for that is a
rebuild, and a guard that keeps bouncing a server it cannot fix turns one
broken instance into a flapping one.
#>

param([string]$LogPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'logs\web-guard.log'))

$ErrorActionPreference = 'Stop'

$Targets = @(
    @{ Port = 8323; Task = 'SignalDeck Web' },
    @{ Port = 3000; Task = 'SignalDeck Local Workspace' }
)

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
# release path (ops/web-release.ps1) so the gate that guards the servers and
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

try {
    $results = @()
    foreach ($t in $Targets) {
        $port = $t.Port
        $task = $t.Task

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
        } elseif (-not $sawLive) {
            Write-Log ("{0} STILL DOWN after 45 s" -f $port)
            $results += "{0} DOWN" -f $port
        } elseif ($final -eq 2) {
            Write-Log ("{0} back ({1} s) but completeness could not be measured" -f $port, $elapsed)
            $results += "{0} UNVERIFIED" -f $port
        } else {
            # A restart re-indexes .next, so this is where an INCOMPLETE instance
            # is expected to come back whole. Still incomplete after a full
            # restart and 45 s means the build on disk is itself bad, and
            # another restart cannot fix it -- say so and stop, rather than
            # bouncing this task again every five minutes forever.
            Write-Log ("{0} back ({1} s) but STILL INCOMPLETE - the build on disk is bad; rebuild, do not restart" -f $port, $elapsed)
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