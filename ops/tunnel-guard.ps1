param([string]$Mode = '')

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

# The ONE window test. Both the live path and __selfcheck call this function -
# they must never be two copies, or the self-check passes while the guard that
# actually runs is wrong.
function Test-InWindow([DateTime]$pt) {
    if ($pt.DayOfWeek -eq [DayOfWeek]::Saturday -or $pt.DayOfWeek -eq [DayOfWeek]::Sunday) {
        return $false
    }
    $hm = $pt.Hour * 100 + $pt.Minute
    return ($hm -ge 620 -and $hm -lt 1310)
}

if ($Mode -eq '__selfcheck') {
    $testCases = @(
        @{ dt = (Get-Date -Year 2026 -Month 9 -Day 14 -Hour 6 -Minute 19 -Second 0); expected = $false }
        @{ dt = (Get-Date -Year 2026 -Month 9 -Day 14 -Hour 6 -Minute 20 -Second 0); expected = $true }
        @{ dt = (Get-Date -Year 2026 -Month 9 -Day 14 -Hour 12 -Minute 0 -Second 0); expected = $true }
        @{ dt = (Get-Date -Year 2026 -Month 9 -Day 14 -Hour 13 -Minute 9 -Second 0); expected = $true }
        @{ dt = (Get-Date -Year 2026 -Month 9 -Day 14 -Hour 13 -Minute 10 -Second 0); expected = $false }
        @{ dt = (Get-Date -Year 2026 -Month 9 -Day 19 -Hour 10 -Minute 0 -Second 0); expected = $false }
        @{ dt = (Get-Date -Year 2026 -Month 9 -Day 20 -Hour 10 -Minute 0 -Second 0); expected = $false }
        @{ dt = (Get-Date -Year 2026 -Month 9 -Day 18 -Hour 10 -Minute 0 -Second 0); expected = $true }
    )

    $failed = $false
    foreach ($tc in $testCases) {
        $result = Test-InWindow $tc.dt
        if ($result -ne $tc.expected) {
            Write-Host ("FAIL: {0} expected {1} got {2}" -f $tc.dt, $tc.expected, $result)
            $failed = $true
        }
    }
    if ($failed) {
        exit 1
    } else {
        Write-Host "SELFCHECK OK"
        exit 0
    }
}

$transcribing = $false
try {
    $logDir = Join-Path $root 'logs'
    if (-not (Test-Path $logDir)) {
        New-Item -ItemType Directory -Path $logDir | Out-Null
    }
    $logFile = Join-Path $logDir 'tunnel-guard.log'
    if (Test-Path $logFile) {
        $size = (Get-Item $logFile).Length
        if ($size -gt 5MB) {
            $tmp = "$logFile.tmp"
            Get-Content $logFile -Tail 2000 | Set-Content -Path $tmp -Encoding ASCII
            Move-Item -Path $tmp -Destination $logFile -Force
        }
    }
    Start-Transcript -Path $logFile -Append
    $transcribing = $true
} catch {
    $transcribing = $false
}

try {
    $tz = [System.TimeZoneInfo]::FindSystemTimeZoneById('Pacific Standard Time')
    $pt = [System.TimeZoneInfo]::ConvertTimeFromUtc([DateTime]::UtcNow, $tz)
} catch {
    Write-Error "Unable to determine Pacific Standard Time zone; refusing to guess at collection window."
    if ($transcribing) { try { Stop-Transcript } catch {} }
    exit 3
}

$inside = Test-InWindow $pt

if (-not $inside) {
    Write-Host ("Tunnel guard is outside the collection window at {0:yyyy-MM-dd HH:mm} Pacific time." -f $pt)
    if ($transcribing) { try { Stop-Transcript } catch {} }
    exit 0
}

$task = Get-ScheduledTask -TaskName 'SignalDeck Tunnel' -ErrorAction SilentlyContinue
if (-not $task) {
    Write-Host "The scheduled task 'SignalDeck Tunnel' is not registered on this machine."
    if ($transcribing) { try { Stop-Transcript } catch {} }
    exit 0
}
if ($task.State -eq 'Running') {
    Write-Host "The tunnel is already running."
    if ($transcribing) { try { Stop-Transcript } catch {} }
    exit 0
}
Write-Host "Restarting the tunnel."
Start-ScheduledTask -TaskName 'SignalDeck Tunnel' | Out-Null
$task = Get-ScheduledTask -TaskName 'SignalDeck Tunnel' -ErrorAction SilentlyContinue
if ($task) {
    Write-Host ("Tunnel state after start: {0}" -f $task.State)
} else {
    Write-Host "Unable to query the task after start."
}
if ($transcribing) { try { Stop-Transcript } catch {} }
exit 0