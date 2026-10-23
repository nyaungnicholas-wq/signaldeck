<#
.SYNOPSIS
Run SignalDeck locally on SYNTHETIC demo data, for recording the demo video.

.DESCRIPTION
SignalDeck's real prices, forecasts and accuracy figures derive from Alpaca market
data and may not be shown publicly. This builds the web app with
NEXT_PUBLIC_DEMO_DATA=1 (a fixed DEMO DATA banner on every page and a title
suffix) into web\.next-demo -- never web\.next -- and serves it on spare ports
against tools\demo\demo_daemon.py, a stub that answers with seeded synthetic
values for fictional tickers DEMOA..DEMOF. It reads no database and talks to no
real daemon. ops\web-release.ps1 refuses any build that carries the banner.

Scratch state (pids, logs, the synthetic registry file) lives in
%TEMP%\signaldeck-demo. The guide is signaldeck-owner-kit\cac-2026\DEMO_MODE.md.

.EXAMPLE
    powershell -NoProfile -File ops\demo-run.ps1            # build, start, print the URL
    powershell -NoProfile -File ops\demo-run.ps1 -NoBuild   # reuse web\.next-demo
    powershell -NoProfile -File ops\demo-run.ps1 -BuildOnly # build and check the banner only
    powershell -NoProfile -File ops\demo-run.ps1 -Stop
#>
param(
    [int]$WebPort = 8351,
    [int]$StubPort = 8352,
    [switch]$Stop,
    [switch]$NoBuild,
    [switch]$BuildOnly
)

$ErrorActionPreference = 'Stop'
$Repo = Split-Path -Parent $PSScriptRoot
$Web = Join-Path $Repo 'web'
$Dist = '.next-demo'
$DistDir = Join-Path $Web $Dist
$State = Join-Path $env:TEMP 'signaldeck-demo'
$PidFile = Join-Path $State 'pids.json'
$Marker = 'synthetic values for illustration, not market data'

function Stop-Demo {
    if (Test-Path -LiteralPath $PidFile) {
        $pids = Get-Content -LiteralPath $PidFile -Raw | ConvertFrom-Json
        foreach ($p in @($pids.stub, $pids.web)) {
            if ($p) { Stop-Process -Id $p -Force -ErrorAction SilentlyContinue }
        }
        Remove-Item -LiteralPath $PidFile -Force
        Write-Host ("demo stopped (pids {0}, {1})" -f $pids.stub, $pids.web)
    } else {
        Write-Host 'no demo is recorded as running'
    }
}

if ($Stop) { Stop-Demo; exit 0 }

if ($Dist -eq '.next') { throw 'refusing: the demo must never build into web\.next' }
if (-not (Test-Path -LiteralPath (Join-Path $Web 'node_modules\next\dist\bin\next'))) {
    throw ("no Next CLI under {0}\node_modules; run npm ci in web first" -f $Web)
}
if (-not $BuildOnly) {
    foreach ($p in @($WebPort, $StubPort)) {
        if (Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue) {
            throw ("port {0} is already in use; stop what holds it (or ops\demo-run.ps1 -Stop) or pass -WebPort/-StubPort" -f $p)
        }
    }
}

# 1 - BUILD with the flag, into web\.next-demo only.
if (-not $NoBuild -or -not (Test-Path -LiteralPath $DistDir)) {
    $saved = @{}
    foreach ($k in 'NEXT_PUBLIC_DEMO_DATA', 'NEXT_PUBLIC_SIGNALDECK_PUBLIC', 'SIGNALDECK_DIST_DIR') {
        $saved[$k] = [Environment]::GetEnvironmentVariable($k)
    }
    # next build rewrites tsconfig.json for the distDir it was given; put the bytes back
    # (the same reason and method as ops\web-release.ps1).
    $TsConfig = Join-Path $Web 'tsconfig.json'
    $TsWas = [System.IO.File]::ReadAllBytes($TsConfig)
    Push-Location $Web
    try {
        $env:NEXT_PUBLIC_DEMO_DATA = '1'
        $env:NEXT_PUBLIC_SIGNALDECK_PUBLIC = '1'
        $env:SIGNALDECK_DIST_DIR = $Dist
        Write-Host ("building the DEMO web into web\{0} ..." -f $Dist)
        & npm run build
        if ($LASTEXITCODE -ne 0) { throw ("npm run build exited {0}" -f $LASTEXITCODE) }
    } finally {
        Pop-Location
        [System.IO.File]::WriteAllBytes($TsConfig, $TsWas)
        foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
    }
}

# A demo build without its banner would show synthetic numbers unlabelled. Refuse it.
$labelled = Get-ChildItem -LiteralPath (Join-Path $DistDir 'server\app') -Filter '*.html' -File -ErrorAction SilentlyContinue |
    Select-String -SimpleMatch -Pattern $Marker -List | Select-Object -First 1
if (-not $labelled) { throw ("web\{0} does not carry the DEMO DATA banner; rebuild without -NoBuild" -f $Dist) }
Write-Host ("demo build labelled: banner found in {0}" -f (Split-Path -Leaf $labelled.Path))
if ($BuildOnly) { exit 0 }

# 2 - SCRATCH. next start runs with its cwd in here, so the /accuracy page's
# <cwd>\..\data\accuracy_registry.json is the stub's synthetic file and never a
# checkout's real one.
$CwdDir = Join-Path $State 'web'
$DataDir = Join-Path $State 'data'
$Logs = Join-Path $State 'logs'
foreach ($d in @($CwdDir, $DataDir, $Logs)) { New-Item -ItemType Directory -Path $d -Force | Out-Null }
$Registry = Join-Path $DataDir 'accuracy_registry.json'

$Py = Join-Path $Repo '.venv\Scripts\python.exe'
if (-not (Test-Path -LiteralPath $Py)) { $Py = (Get-Command python -ErrorAction Stop).Source }
$Stub = Join-Path $Repo 'tools\demo\demo_daemon.py'

# 3 - START the stub, then the web against it. Paths are quoted because
# Start-Process does not quote arguments ("claude code" has a space).
$stubProc = Start-Process -FilePath $Py -PassThru -WindowStyle Hidden `
    -ArgumentList @('-I', ('"{0}"' -f $Stub), '--port', "$StubPort", '--registry', ('"{0}"' -f $Registry)) `
    -RedirectStandardOutput (Join-Path $Logs 'stub.out') -RedirectStandardError (Join-Path $Logs 'stub.err')

$envWas = @{}
foreach ($k in 'SIGNALDECK_DIST_DIR', 'SIGNALDECK_DAEMON', 'SIGNALDECK_LOCAL_ONLY_PROXY', 'SIGNALDECK_LOCAL_PROXY_KEY') {
    $envWas[$k] = [Environment]::GetEnvironmentVariable($k)
}
try {
    $env:SIGNALDECK_DIST_DIR = $Dist
    $env:SIGNALDECK_DAEMON = "http://127.0.0.1:$StubPort"
    $env:SIGNALDECK_LOCAL_ONLY_PROXY = $null
    $env:SIGNALDECK_LOCAL_PROXY_KEY = $null
    $webProc = Start-Process -FilePath 'node' -PassThru -WindowStyle Hidden -WorkingDirectory $CwdDir `
        -ArgumentList @(('"{0}"' -f (Join-Path $Web 'node_modules\next\dist\bin\next')), 'start', ('"{0}"' -f $Web), '-p', "$WebPort", '-H', '127.0.0.1') `
        -RedirectStandardOutput (Join-Path $Logs 'web.out') -RedirectStandardError (Join-Path $Logs 'web.err')
} finally {
    foreach ($k in $envWas.Keys) { [Environment]::SetEnvironmentVariable($k, $envWas[$k]) }
}
@{ stub = $stubProc.Id; web = $webProc.Id; webPort = $WebPort; stubPort = $StubPort } | ConvertTo-Json |
    Set-Content -LiteralPath $PidFile -Encoding ASCII

# 4 - PROVE it: the home page answers and carries the banner.
$ok = $false
for ($i = 0; $i -lt 45 -and -not $ok; $i++) {
    Start-Sleep -Seconds 2
    try {
        $r = Invoke-WebRequest -Uri ("http://127.0.0.1:{0}/" -f $WebPort) -UseBasicParsing -TimeoutSec 10
        $ok = ($r.StatusCode -eq 200) -and $r.Content.Contains($Marker)
    } catch { }
}
if (-not $ok) {
    foreach ($f in 'stub.err', 'web.out', 'web.err') {
        $p = Join-Path $Logs $f
        if (Test-Path -LiteralPath $p) { Write-Host ("{0}: {1}" -f $f, ((Get-Content -LiteralPath $p -Tail 10) -join ' | ')) }
    }
    Stop-Demo
    throw 'the demo did not come up with its banner; nothing is left running'
}
Write-Host ''
Write-Host ("DEMO RUNNING: http://127.0.0.1:{0}/   (synthetic data; stub on :{1}; logs {2})" -f $WebPort, $StubPort, $Logs)
Write-Host 'Stop it with: powershell -NoProfile -File ops\demo-run.ps1 -Stop'
