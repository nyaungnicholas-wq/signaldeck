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
        if (Test-Web -Port $port) {
            Write-Log ("{0} ok" -f $port)
            $results += "{0} ok" -f $port
            continue
        }
        Write-Log ("{0} DOWN - restarting task '{1}'" -f $port, $task)
        Stop-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 3
        Start-ScheduledTask -TaskName $task
        $back = $false
        $elapsed = 0
        while ($elapsed -lt 45) {
            Start-Sleep -Seconds 3
            $elapsed += 3
            if (Test-Web -Port $port) { $back = $true; break }
        }
        if ($back) {
            Write-Log ("{0} back ({1} s)" -f $port, $elapsed)
            $results += "{0} ok" -f $port
        } else {
            Write-Log ("{0} STILL DOWN after 45 s" -f $port)
            $results += "{0} DOWN" -f $port
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