# Private local workspace: the proxy exception and loopback bind are inseparable.
# -Port lets the same launcher serve the canonical 8323 address (SignalDeck Web
# task) and the 3000 workspace identically: loopback-only, keyed, no cmd wrapper
# (a cmd-wrapped node orphans on task stop and holds the port - 2026-09-06).
param([int]$Port = 3000)
$ErrorActionPreference = 'Stop'
$sdRepo = Split-Path -Parent $PSScriptRoot
$env:SIGNALDECK_LOCAL_ONLY_PROXY = '1'
# The daemon grants loopback-only raw data to this launcher via a shared key
# (SIGNALDECK_LOCAL_PROXY_KEY in daemon/.env). Read it here so the proxy can
# present it; if it is unset the daemon simply refuses raw bars (fails closed).
$envFile = Join-Path $sdRepo 'daemon\.env'
if (Test-Path -LiteralPath $envFile) {
    $line = Get-Content -LiteralPath $envFile | Where-Object { $_ -match '^SIGNALDECK_LOCAL_PROXY_KEY=' } | Select-Object -First 1
    if ($line) { $env:SIGNALDECK_LOCAL_PROXY_KEY = $line.Substring('SIGNALDECK_LOCAL_PROXY_KEY='.Length).Trim() }
}
# Task Scheduler ends only this powershell, never its node child, so a restart of
# the task must first retire the previous listener it started (same user session,
# no elevation needed). Only a node whose command line is this launcher's is touched.
$held = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($held) {
    $old = Get-CimInstance Win32_Process -Filter ("ProcessId={0}" -f $held.OwningProcess) -ErrorAction SilentlyContinue
    if ($old -and $old.Name -eq 'node.exe' -and $old.CommandLine -like ('*next*start*-p ' + $Port + '*')) { Stop-Process -Id $old.ProcessId -Force; Start-Sleep -Seconds 2 }
}
Set-Location -LiteralPath (Join-Path $sdRepo 'web')
& 'C:\Program Files\nodejs\node.exe' (Join-Path $sdRepo 'web\node_modules\next\dist\bin\next') start -H 127.0.0.1 -p $Port
exit $LASTEXITCODE
