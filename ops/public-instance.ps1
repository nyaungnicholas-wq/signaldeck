# public-instance.ps1 -- keep the PUBLIC (IEX-fed) SignalDeck instance running,
# and run its daily data update.
#
# The public instance is a second daemon with its own root, database, logs and
# key under <parent>\signaldeck-public. It holds only IEX historical data
# ("Data provided for free by IEX." https://www.iex.io/legal/hist-data-terms)
# and no Alpaca key. Nothing here touches the main daemon on 8322.
#
#   -Mode keepalive   start the public daemon if nothing listens on its port
#                     (run every few minutes by 'SignalDeck Public Daemon')
#   -Mode daily       decode any new IEX day and rebuild the public bars
#                     (run once a day by 'SignalDeck Public Daily')
#   -Mode status      print what is running; changes nothing
param(
    [ValidateSet('keepalive', 'daily', 'status')][string]$Mode = 'status',
    [int]$Port = 8422
)
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$root = Join-Path (Split-Path -Parent $repo) 'signaldeck-public'
$exe = Join-Path $root 'bin\signaldeckd.exe'
$logDir = Join-Path $root 'signaldeck\logs'
$log = Join-Path $logDir 'public-instance.log'

function Say([string]$line) {
    $text = "{0} {1}" -f (Get-Date).ToString('yyyy-MM-ddTHH:mm:ss'), $line
    try { Add-Content -LiteralPath $log -Value $text -Encoding UTF8 } catch { }
    Write-Host $text
}

function Test-Listening([int]$p) {
    return $null -ne (Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1)
}

if (-not (Test-Path -LiteralPath $exe)) {
    Write-Host "no public instance at $root (missing bin\signaldeckd.exe)"
    exit 1
}
if (-not (Test-Path -LiteralPath $logDir)) { New-Item -ItemType Directory -Path $logDir -Force | Out-Null }

if ($Mode -eq 'status') {
    Write-Host ("public daemon on {0}: {1}" -f $Port, $(if (Test-Listening $Port) { 'listening' } else { 'NOT listening' }))
    exit 0
}

# The public daemon reads ONLY its own root. These two variables are what keep
# it away from the main instance's .env, logs, caches, notifier and signing key.
$env:SIGNALDECK_ROOT = $root
$env:SIGNALDECK_LEDGER_ANCHOR_KEY = Join-Path $root 'keys\ledger_anchor.key'

if ($Mode -eq 'keepalive') {
    if (Test-Listening $Port) { exit 0 }
    if (Test-Path -LiteralPath (Join-Path $root '.stop')) { Say 'a .stop file is present; not starting the public daemon'; exit 0 }
    Say "public daemon not listening on $Port; starting it"
    $out = Join-Path $logDir 'daemon.out.log'
    $err = Join-Path $logDir 'daemon.err.log'
    Start-Process -FilePath $exe -WorkingDirectory $root -WindowStyle Hidden `
        -RedirectStandardOutput $out -RedirectStandardError $err | Out-Null
    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Seconds 2
        if (Test-Listening $Port) { Say 'public daemon is listening'; exit 0 }
    }
    Say 'public daemon did NOT start listening within 60s; see daemon.err.log and signaldeckd.log in its logs folder'
    exit 1
}

if ($Mode -eq 'daily') {
    $bash = 'C:\Program Files\Git\usr\bin\bash.exe'
    if (-not (Test-Path -LiteralPath $bash)) { Say "bash not found at $bash"; exit 1 }
    $env:SD_PUBLIC_ROOT = ($root -replace '\\', '/')
    $env:SD_IEX_DIR = ((Join-Path $repo 'data\iexhist') -replace '\\', '/')
    $env:SD_PYTHON = ((Join-Path $repo '.venv\Scripts\python.exe') -replace '\\', '/')
    $script = ((Join-Path $PSScriptRoot 'iexhist-daily.sh') -replace '\\', '/')
    Say 'daily IEX update starting'
    & $bash $script
    $code = $LASTEXITCODE
    Say "daily IEX update finished with exit code $code"
    exit $code
}