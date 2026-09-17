<#
.SYNOPSIS
Build a web candidate somewhere else, prove it, and only then put it in place.

.DESCRIPTION
ops/web-guard.ps1 already refers to "the release path (ops/web-release.ps1)".
It did not exist. What stood in for it was running `npm run build` in web/,
which replaces web\.next IN PLACE while `next start` instances are serving out
of it. `next start` indexes .next\static once at boot and Turbopack names chunks
by content hash, so a running instance keeps answering for every chunk whose
content did not change and 404s every one that did. Measured 2026-09-13: 4 of
the 14 assets the landing page references, and :3000 served a 200 with no
stylesheet for 24 hours while the guard logged "3000 ok" every five minutes.

Separately, the running frontend was found to be behind the source tree with no
way to tell from outside. A build's files being present proves the build is
complete, not that it is current, and nothing recorded which commit produced it.

So this builds into web\.next-release-<shortrev>, runs both gates against that
candidate on spare ports, and only then swaps it in and records the revision in
logs\web-release.json. web\.next is never deleted, only moved aside, because a
failed promotion has to leave the previous build recoverable.

-BuildOnly runs every gate and stops before promoting. That is the form to run
when you want to know whether the tree is releasable without touching a server.

.EXAMPLE
    powershell -NoProfile -File ops\web-release.ps1 -BuildOnly
    powershell -NoProfile -File ops\web-release.ps1 -Port 8323 -Task 'SignalDeck Web'
#>

param(
    [int]$Port = 8323,
    [string]$Task = 'SignalDeck Web',
    [switch]$BuildOnly,
    [string]$LogPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'logs\web-release.log')
)

$ErrorActionPreference = 'Stop'

$Repo = Split-Path -Parent $PSScriptRoot
$Web = Join-Path $Repo 'web'
$AssetsCheck = Join-Path $PSScriptRoot 'web-assets-check.ps1'
$StagePort = 8331

function Write-Log([string]$Line) {
    $dir = Split-Path -Parent $LogPath
    if ($dir -and -not (Test-Path -LiteralPath $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    $stamp = (Get-Date).ToString('yyyy-MM-ddTHH:mm:ss')
    $text = "{0} {1}" -f $stamp, $Line
    Add-Content -LiteralPath $LogPath -Value $text -Encoding UTF8
    Write-Host $text
}

function Fail([string]$Why) {
    Write-Log ("RELEASE ABORTED: {0}" -f $Why)
    exit 1
}

if (-not (Test-Path -LiteralPath (Join-Path $Web 'package.json'))) {
    Fail ("no web\package.json under {0}" -f $Repo)
}
if (-not (Test-Path -LiteralPath $AssetsCheck)) {
    Fail ("the asset check is missing at {0}; the completeness gate cannot run" -f $AssetsCheck)
}

# 1 - the revision this release is FOR. A checkout without git is still
# deployable, so this degrades to "unknown" and says so rather than refusing.
$Revision = 'unknown'
$ShortRev = 'unknown'
try {
    $Revision = (& git -C $Repo rev-parse HEAD 2>$null).Trim()
    $ShortRev = (& git -C $Repo rev-parse --short HEAD 2>$null).Trim()
} catch {
    $Revision = 'unknown'
    $ShortRev = 'unknown'
}
if (-not $Revision) { $Revision = 'unknown'; $ShortRev = 'unknown' }
if ($Revision -eq 'unknown') {
    Write-Log 'git could not name a revision here; this release will not be attributable'
} else {
    Write-Log ("releasing {0}" -f $Revision)
}

$CandidateName = ".next-release-$ShortRev"
$CandidateDir = Join-Path $Web $CandidateName

$PrevDist = $env:SIGNALDECK_DIST_DIR
$StageProc = $null
try {
    # 2 - a half-built leftover must never be promotable.
    if (Test-Path -LiteralPath $CandidateDir) {
        Write-Log ("removing an earlier candidate at {0}" -f $CandidateDir)
        Remove-Item -LiteralPath $CandidateDir -Recurse -Force
    }

    # 3 - BUILD, into the candidate. web\.next is untouched by this, which is
    # the entire point: the running instances keep serving the build they
    # indexed at boot.
    $env:SIGNALDECK_DIST_DIR = $CandidateName
    Write-Log ("building into {0}" -f $CandidateName)
    # `next build` REWRITES web\tsconfig.json, adding type includes for whatever
    # distDir it was pointed at. Left alone, every candidate leaves a permanent
    # ".next-release-<rev>/types/**" entry behind and the file drifts one release
    # at a time. The candidate's type includes are build output, not a source
    # change, so the file is snapshotted and put back.
    # Snapshot the BYTES, not the text. Set-Content -Encoding UTF8 on Windows
    # PowerShell writes a BOM, so a text round-trip "restores" a file that git
    # then reports as modified on its first line - measured, and it would have
    # been committed by whoever ran a release next.
    $TsConfig = Join-Path $Web 'tsconfig.json'
    $TsConfigWas = $null
    if (Test-Path -LiteralPath $TsConfig) {
        $TsConfigWas = [System.IO.File]::ReadAllBytes($TsConfig)
    }
    Push-Location $Web
    try {
        & npm run build
        if ($LASTEXITCODE -ne 0) { Fail ("npm run build exited {0}" -f $LASTEXITCODE) }
    } finally {
        Pop-Location
        if ($TsConfigWas -ne $null) {
            [System.IO.File]::WriteAllBytes($TsConfig, $TsConfigWas)
        }
    }

    # 4 - ASSET GATE. Serve the candidate on a spare port and make it prove it
    # can produce every chunk and font its own markup names.
    # node on the Next CLI directly, NOT `npx`. Start-Process does not go
    # through a shell, so on Windows it does not resolve npx.cmd from PATHEXT:
    # measured, the call returned a process object and nothing ever listened on
    # the port, and the script sat in the poll below until it timed out. Going
    # straight to node also gives ONE process to stop in the finally block
    # rather than a launcher whose child outlives it.
    $NextBin = Join-Path $Web 'node_modules\next\dist\bin\next'
    if (-not (Test-Path -LiteralPath $NextBin)) {
        Fail ("the Next CLI is not at {0}; run npm install in web first" -f $NextBin)
    }
    # QUOTE THE PATH. Start-Process joins -ArgumentList with spaces and does not
    # quote anything, so an unquoted path containing a space ("claude code")
    # arrives at node as two arguments and it silently serves nothing. Measured:
    # the poll below sat out its full 90 seconds twice with no error anywhere.
    #
    # Its output goes to a file for the same reason: a staged server that fails
    # to boot must be able to say why, and -WindowStyle Hidden discards it.
    $StageLog = Join-Path (Split-Path -Parent $LogPath) 'web-release-stage.log'
    Write-Log ("starting the candidate on {0} for the asset gate (its log: {1})" -f $StagePort, $StageLog)
    $StageProc = Start-Process -FilePath 'node' `
        -ArgumentList @(('"{0}"' -f $NextBin), 'start', '-p', "$StagePort") `
        -WorkingDirectory $Web -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput $StageLog -RedirectStandardError ($StageLog + '.err')
    $up = $false
    for ($i = 0; $i -lt 45; $i++) {
        Start-Sleep -Seconds 2
        try {
            $r = Invoke-WebRequest -Uri ("http://127.0.0.1:{0}/login" -f $StagePort) -UseBasicParsing -TimeoutSec 5
            if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 400) { $up = $true; break }
        } catch {
            # not up yet
        }
    }
    if (-not $up) {
        foreach ($f in @($StageLog, ($StageLog + '.err'))) {
            if (Test-Path -LiteralPath $f) {
                $tail = Get-Content -LiteralPath $f -Tail 15
                if ($tail) { Write-Log ("{0}: {1}" -f (Split-Path -Leaf $f), ($tail -join ' | ')) }
            }
        }
        Fail ("the candidate never answered on {0}" -f $StagePort)
    }

    & powershell -NoProfile -ExecutionPolicy Bypass -File $AssetsCheck -Port $StagePort
    $assets = $LASTEXITCODE
    if ($assets -eq 1) { Fail 'the candidate is INCOMPLETE: it serves markup naming assets it cannot produce' }
    if ($assets -ne 0) { Fail ("the asset check could not run (exit {0}); an unmeasured candidate is not promotable" -f $assets) }
    Write-Log 'asset gate PASSED'

    # 5 - BROWSER GATE. This is the one that fails on a page rendering its error
    # boundary, which the asset check cannot see: the boundary is served with a
    # 200 and references every asset correctly.
    # The gate needs 8329 (playwright.config.ts webServer, reuseExistingServer
    # false). A leftover `next start` there makes playwright exit 1 with
    # "already used" - the SAME exit code a real test failure gives. Measured:
    # the first run of this script reported "the candidate does not render" when
    # the truth was an orphaned server from an earlier run. A check that cannot
    # run must say so; publishing an outage as a finding is the thing this
    # repository refuses everywhere else.
    if (Get-NetTCPConnection -LocalPort 8329 -State Listen -ErrorAction SilentlyContinue) {
        Fail 'CHECK UNAVAILABLE: the browser gate needs 127.0.0.1:8329 and something is already listening there (usually an orphaned next start from an earlier playwright run). Stop it and retry. This is NOT a statement about the candidate.'
    }
    Write-Log 'running web/e2e/release-smoke.spec.ts against the candidate'
    Push-Location $Web
    try {
        & npx playwright test release-smoke --reporter=list
        if ($LASTEXITCODE -ne 0) { Fail ("the browser gate FAILED (exit {0}). Read the playwright output above before concluding anything: a timeout in the harness and a page that does not render are different findings." -f $LASTEXITCODE) }
    } finally {
        Pop-Location
    }
    Write-Log 'browser gate PASSED'

    if ($BuildOnly) {
        Write-Log ("BUILD-ONLY: every gate passed for {0}; candidate is at {1}. Nothing was replaced." -f $Revision, $CandidateDir)
        exit 0
    }

    # 6 - PROMOTE. Stop, move aside, move in, start. Never delete.
    #
    # Take the maintenance lock first. ops/web-guard.ps1 runs every five minutes
    # and, without this, can arrive between the stop and the start, find the port
    # down or the directory half-swapped, and restart the task underneath this
    # one - or file a suppression against a build that was never broken.
    $Lock = Join-Path $PSScriptRoot '.maintenance'
    Set-Content -LiteralPath $Lock -Value ("web-release {0} {1}" -f $Revision, (Get-Date).ToString('o')) -Encoding UTF8
    Write-Log 'maintenance lock taken; the web keepalive will stand down'

    $LiveDist = Join-Path $Web '.next'
    $stamp = (Get-Date).ToString('yyyyMMdd-HHmmss')
    $Kept = Join-Path $Web (".next-prev-" + $stamp)

    Write-Log ("stopping task '{0}'" -f $Task)
    Stop-ScheduledTask -TaskName $Task -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 3

    if (Test-Path -LiteralPath $LiveDist) {
        Write-Log ("moving the previous build to {0}" -f $Kept)
        Move-Item -LiteralPath $LiveDist -Destination $Kept
    } else {
        Write-Log 'there was no web\.next to keep'
        $Kept = '(none)'
    }
    Move-Item -LiteralPath $CandidateDir -Destination $LiveDist
    Write-Log ("starting task '{0}'" -f $Task)
    Start-ScheduledTask -TaskName $Task

    # 7 - the promotion is not done until the port serves a COMPLETE build.
    $ok = $false
    for ($i = 0; $i -lt 15; $i++) {
        Start-Sleep -Seconds 3
        & powershell -NoProfile -ExecutionPolicy Bypass -File $AssetsCheck -Port $Port | Out-Null
        if ($LASTEXITCODE -eq 0) { $ok = $true; break }
    }
    if (-not $ok) {
        Write-Log ("PROMOTION DID NOT COME UP COMPLETE on {0}. The previous build is at {1} - move it back to web\.next by hand and restart '{2}'." -f $Port, $Kept, $Task)
        exit 1
    }
    Write-Log ("promoted and complete on {0}" -f $Port)

    # 8 - RECORD. This is what answers "which commit is the running frontend"
    # without anyone guessing from file timestamps.
    $buildId = ''
    try { $buildId = (Get-Content -LiteralPath (Join-Path $LiveDist 'BUILD_ID') -TotalCount 1).Trim() } catch { $buildId = '' }
    $record = [ordered]@{
        revision      = $Revision
        shortRevision = $ShortRev
        buildId       = $buildId
        promotedAt    = (Get-Date).ToString('o')
        port          = $Port
        keptPrevious  = $Kept
    }
    $recordPath = Join-Path $Repo 'logs\web-release.json'
    Set-Content -LiteralPath $recordPath -Value ($record | ConvertTo-Json) -Encoding UTF8
    Write-Log ("recorded {0} (build {1}) in {2}" -f $Revision, $buildId, $recordPath)
    exit 0
}
finally {
    if ($StageProc -ne $null) {
        try { Stop-Process -Id $StageProc.Id -Force -ErrorAction SilentlyContinue } catch { }
    }
    # Release the lock whatever happened. A release that dies holding it would
    # leave the web tier unguarded until the guard's own 30 minute ceiling.
    $lockPath = Join-Path $PSScriptRoot '.maintenance'
    if (Test-Path -LiteralPath $lockPath) {
        Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue
    }
    $env:SIGNALDECK_DIST_DIR = $PrevDist
}
