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
# no elevation needed).
#
# THE GUARD USED TO BE A SILENT NO-OP. It required
# $old.CommandLine -like '*next*start*-p <port>*', and Win32_Process.CommandLine
# comes back EMPTY whenever the querying session cannot read it, which is the
# ordinary case for a child started under the task principal. The -like was then
# false, nothing was retired, and the launcher fell through to `next start`,
# which died with EADDRINUSE. Measured 2026-09-10 on ports 3000 and 8323: the
# task showed Ready while an orphaned node still held the port.
#
# IDENTIFICATION IS A PREFERENCE, NOT A PRECONDITION. A node.exe listening on
# exactly the port this launcher is defined to own is retired regardless; how it
# was identified is logged, not required. An earlier attempt at this fix made an
# unidentifiable node a refusal and took the web tier DOWN -- strictly worse than
# the silent fall-through it replaced, because the orphan had at least still been
# serving. Only a NON-node holder is refused, and a failed stop throws.
$held = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($held) {
    $old = Get-CimInstance Win32_Process -Filter ("ProcessId={0}" -f $held.OwningProcess) -ErrorAction SilentlyContinue
    if (-not $old) {
        throw "port $Port is held by pid $($held.OwningProcess) which this session cannot inspect."
    }
    if ($old.Name -ne 'node.exe') {
        throw "port $Port is held by $($old.Name) (pid $($old.ProcessId)), not a node listener, refusing to stop it."
    }
    $cmd = $old.CommandLine
    if ($cmd -and $cmd -like ('*next*start*-p ' + $Port + '*')) {
        $why = 'command line matches this launcher'
    } elseif ($cmd) {
        $why = 'node.exe on this port, command line did not match'
    } else {
        try {
            $owner = Invoke-CimMethod -InputObject $old -MethodName GetOwner -ErrorAction Stop
            if ($owner.User) { $why = "node.exe owned by $($owner.User)" }
            else { $why = 'node.exe, identity unreadable' }
        } catch {
            $why = 'node.exe, identity unreadable'
        }
    }
    Write-Host "retiring pid $($old.ProcessId) on port $Port ($why)"
    # $ErrorActionPreference is 'Stop' at script scope, so an access-denied here
    # THROWS rather than printing a non-terminating error and carrying on.
    Stop-Process -Id $old.ProcessId -Force
    # Wait for the port to actually free. The old fixed `Start-Sleep 2` was a
    # guess; a slow exit outlives it and the bind still fails.
    for ($i = 0; $i -lt 20; $i++) {
        $still = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $still) { break }
        Start-Sleep -Milliseconds 500
    }
    $still = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($still) {
        throw "port $Port did not free within 10s after stopping pid $($old.ProcessId)."
    }
}
Set-Location -LiteralPath (Join-Path $sdRepo 'web')
& 'C:\Program Files\nodejs\node.exe' (Join-Path $sdRepo 'web\node_modules\next\dist\bin\next') start -H 127.0.0.1 -p $Port
exit $LASTEXITCODE
