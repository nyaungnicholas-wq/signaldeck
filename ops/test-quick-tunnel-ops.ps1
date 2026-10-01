<#
.SYNOPSIS
The quick-tunnel ops gaps of 2026-09-30, driven through the real scripts.

.DESCRIPTION
At 22:25:58 PT on 2026-09-30 the cloudflared behind the public URL died on a
console signal (0xC000013A) and nothing restarted it for ~12 min. Three gaps:

  A  install-windows-tasks.ps1 only walked ops\tasks\*.xml, so a live task with
     no xml was invisible - it now prints ORPHAN for one.
  B  check-task-health.ps1 called the logon-triggered tunnel "no NextRunTime ...
     nothing will start this again" whenever it was down, and did not parse
     ORPHAN. A 0xC000013A while not running must STILL be reported, and a task
     with no trigger at all must still be stale.
  C  web-guard.ps1 (every 5 min) now starts 'SignalDeck Quick Tunnel' when no
     cloudflared serves it - and only then.

Each script runs as a COPY inside a temp repo, so its lock, log and state paths
never touch this checkout, with the ScheduledTasks / CIM / web cmdlets replaced
by the functions below. PowerShell resolves a function before a cmdlet of the
same name, and a script invoked with & sees the caller's functions. The canary
refuses to run anything if that stops being true, because the guard would then
call the REAL Start-ScheduledTask.

Run: powershell -NoProfile -ExecutionPolicy Bypass -File ops\test-quick-tunnel-ops.ps1
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$script:fail = 0

function Check([string]$what, [bool]$ok, [string]$detail = '') {
    if ($ok) { Write-Output ("  PASS  {0}" -f $what) }
    else { Write-Output ("  FAIL  {0} {1}" -f $what, $detail); $script:fail++ }
}

# ---- fakes: all state is $global:, because $script: inside a function called
# from the script under test would resolve to THAT script's scope ----------
$global:QtFleet = @()
$global:QtInfo = @{}
$global:QtProcs = @()
$global:QtProcsAfterStart = $null
$global:QtStarted = @()
$global:QtExportXml = ''

function New-FakeTask([string]$Name, [string]$State, [string[]]$Triggers) {
    [pscustomobject]@{
        TaskName  = $Name
        TaskPath  = '\'
        State     = $State
        Principal = [pscustomobject]@{ LogonType = 'S4U' }
        Triggers  = @($Triggers | Where-Object { $_ } | ForEach-Object { [pscustomobject]@{ CimClass = [pscustomobject]@{ CimClassName = $_ } } })
    }
}
function New-FakeInfo([uint32]$Result, $Next) {
    [pscustomobject]@{ LastTaskResult = $Result; LastRunTime = [datetime]'2026-09-30T22:25:58'; NextRunTime = $Next }
}
function Get-ScheduledTask {
    [CmdletBinding()] param([string]$TaskName)
    if ($TaskName) { return $global:QtFleet | Where-Object { $_.TaskName -eq $TaskName } }
    return $global:QtFleet
}
function Get-ScheduledTaskInfo { [CmdletBinding()] param([string]$TaskName, [string]$TaskPath) $global:QtInfo[$TaskName] }
function Export-ScheduledTask { [CmdletBinding()] param([string]$TaskName) $global:QtExportXml }
function Start-ScheduledTask {
    [CmdletBinding()] param([string]$TaskName)
    $global:QtStarted += $TaskName
    if ($null -ne $global:QtProcsAfterStart) { $global:QtProcs = $global:QtProcsAfterStart }
}
function Stop-ScheduledTask { [CmdletBinding()] param([string]$TaskName) throw "test must not stop $TaskName" }
function Register-ScheduledTask { throw 'test must not register a task' }
function Unregister-ScheduledTask { throw 'test must not unregister a task' }
function Get-CimInstance { [CmdletBinding()] param([string]$ClassName, [string]$Filter) $global:QtProcs }
function Get-NetTCPConnection { [CmdletBinding()] param($State, $LocalPort) [pscustomobject]@{ LocalPort = $LocalPort } }
function Invoke-WebRequest { [CmdletBinding()] param($Uri, [switch]$UseBasicParsing, $TimeoutSec) [pscustomobject]@{ StatusCode = 200 } }
function Start-Sleep { [CmdletBinding()] param([int]$Seconds) }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ('sd-qt-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null

# CANARY: the stubs must win inside a called script, or stop here.
$canary = Join-Path $tmp 'canary.ps1'
Set-Content -LiteralPath $canary -Encoding ASCII -Value @'
'Get-ScheduledTask','Get-ScheduledTaskInfo','Export-ScheduledTask','Start-ScheduledTask','Stop-ScheduledTask','Register-ScheduledTask','Unregister-ScheduledTask','Get-CimInstance' |
    ForEach-Object { '{0}={1}' -f $_, (Get-Command $_).CommandType }
'@
$seen = @(& $canary)
$real = @($seen | Where-Object { $_ -notmatch '=Function$' })
if ($real.Count -gt 0 -or $seen.Count -ne 8) {
    Write-Output ("test-quick-tunnel-ops: UNSAFE - a real cmdlet would run: {0}" -f ($seen -join ', '))
    Remove-Item -LiteralPath $tmp -Recurse -Force
    exit 2
}

function New-FakeRepo([string]$Name, [string[]]$Copy) {
    $ops = Join-Path (Join-Path $tmp $Name) 'ops'
    New-Item -ItemType Directory -Path $ops -Force | Out-Null
    foreach ($f in $Copy) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $f) -Destination $ops }
    return $ops
}
function Invoke-Copy([string]$Path, [hashtable]$Arguments = @{}) {
    $global:LASTEXITCODE = 0
    $text = & $Path @Arguments *>&1 | Out-String
    return [pscustomobject]@{ Text = $text; Code = $LASTEXITCODE }
}

$qtName = 'SignalDeck Quick Tunnel'
$qtCmd = '"C:\Program Files (x86)\cloudflared\cloudflared.exe" tunnel --url http://127.0.0.1:8323 --no-autoupdate --logfile "C:\x\logs\quicktunnel.log"'
$healthy = New-FakeTask 'SignalDeck Alpha' 'Ready' @('MSFT_TaskDailyTrigger')

try {
    # ------------------------------------------------------------------ A
    Write-Output 'A  install-windows-tasks.ps1 report mode'
    $opsA = New-FakeRepo 'a' @('install-windows-tasks.ps1', 'lib-tasks.ps1')
    New-Item -ItemType Directory -Path (Join-Path $opsA 'tasks') | Out-Null
    $global:QtExportXml = '<Task><Command>x</Command></Task>'
    Set-Content -LiteralPath (Join-Path $opsA 'tasks\SignalDeck Alpha.xml') -Value $global:QtExportXml -Encoding ASCII
    $global:QtFleet = @($healthy, (New-FakeTask 'SignalDeck Ghost' 'Running' @('MSFT_TaskLogonTrigger')), (New-FakeTask 'Other Task' 'Ready' @()))
    $r = Invoke-Copy (Join-Path $opsA 'install-windows-tasks.ps1')
    Check 'a live SignalDeck task with no xml is printed as ORPHAN' ($r.Text -match '(?m)^ORPHAN\s+SignalDeck Ghost\b') $r.Text
    Check 'a task WITH an xml, or outside the fleet, is not' ($r.Text -notmatch '(?m)^ORPHAN\s+(SignalDeck Alpha|Other Task)') $r.Text
    Check 'report mode still exits 0' ($r.Code -eq 0) ("exit " + $r.Code)
    $orphanLine = @($r.Text -split "`r?`n" | Where-Object { $_ -match '^ORPHAN' })[0]
    if (-not $orphanLine) { $orphanLine = '{0,-8} {1,-34} {2}' -f 'ORPHAN', 'SignalDeck Ghost', 'live, no ops\tasks xml' }

    # ------------------------------------------------------------------ B
    Write-Output 'B  check-task-health.ps1'
    $opsB = New-FakeRepo 'b' @('check-task-health.ps1')
    Set-Content -LiteralPath (Join-Path $opsB 'lib-notify.ps1') -Encoding ASCII -Value 'function Send-SdAlert { param($Title, $Body, $Repo) }'
    $stubInstaller = Join-Path $opsB 'install-windows-tasks.ps1'
    $health = Join-Path $opsB 'check-task-health.ps1'
    $tomorrow = (Get-Date).AddDays(1)
    function Set-Case([object[]]$Fleet, [hashtable]$Info, [string[]]$InstallerLines) {
        $global:QtFleet = $Fleet
        $global:QtInfo = $Info
        $body = @($InstallerLines | ForEach-Object { "Write-Output '" + $_.Replace("'", "''") + "'" }) + 'exit 0'
        Set-Content -LiteralPath $stubInstaller -Encoding ASCII -Value $body
    }

    Set-Case @($healthy) @{ 'SignalDeck Alpha' = (New-FakeInfo 0 $tomorrow) } @()
    $r = Invoke-Copy $health
    Check 'sanity: a healthy fleet is OK (exit 0)' ($r.Code -eq 0 -and $r.Text -match 'OK - no console-kill') ("exit " + $r.Code + "`n" + $r.Text)

    $qtReady = New-FakeTask $qtName 'Ready' @('MSFT_TaskLogonTrigger')
    Set-Case @($healthy, $qtReady) @{ 'SignalDeck Alpha' = (New-FakeInfo 0 $tomorrow); $qtName = (New-FakeInfo 0 $null) } @()
    $r = Invoke-Copy $health
    Check 'a logon-triggered task is not "no NextRunTime ... nothing will start this again"' `
        ($r.Code -eq 0 -and $r.Text -notmatch 'Quick Tunnel[^\r\n]*no NextRunTime') ("exit " + $r.Code + "`n" + $r.Text)

    Set-Case @($healthy, $qtReady) @{ 'SignalDeck Alpha' = (New-FakeInfo 0 $tomorrow); $qtName = (New-FakeInfo 3221225786 $null) } @()
    $r = Invoke-Copy $health
    Check '0xC000013A while NOT running is still reported' `
        ($r.Code -eq 1 -and $r.Text -match 'console control event' -and $r.Text -match '- SignalDeck Quick Tunnel') ("exit " + $r.Code + "`n" + $r.Text)

    Set-Case @($healthy, (New-FakeTask 'SignalDeck Lost' 'Ready' @())) @{ 'SignalDeck Alpha' = (New-FakeInfo 0 $tomorrow); 'SignalDeck Lost' = (New-FakeInfo 0 $null) } @()
    $r = Invoke-Copy $health
    Check 'a task with NO trigger is still "no NextRunTime"' `
        ($r.Code -eq 1 -and $r.Text -match 'SignalDeck Lost[^\r\n]*no NextRunTime') ("exit " + $r.Code + "`n" + $r.Text)

    Set-Case @($healthy) @{ 'SignalDeck Alpha' = (New-FakeInfo 0 $tomorrow) } @($orphanLine)
    $r = Invoke-Copy $health
    Check "the installer's ORPHAN line is reported as definition drift" `
        ($r.Code -eq 1 -and $r.Text -match 'differ from ops' -and $r.Text -match '- ORPHAN SignalDeck Ghost') ("exit " + $r.Code + "`n" + $r.Text)

    # ------------------------------------------------------------------ C
    Write-Output 'C  web-guard.ps1 quick tunnel'
    $opsC = New-FakeRepo 'c' @('web-guard.ps1')
    # Both web ports answer and are complete, so the exit code is the tunnel's.
    Set-Content -LiteralPath (Join-Path $opsC 'web-assets-check.ps1') -Encoding ASCII -Value 'param($Port) "stub: complete"; exit 0'
    $guard = Join-Path $opsC 'web-guard.ps1'
    $log = Join-Path $tmp 'c\logs\web-guard.log'
    $lock = Join-Path $opsC '.maintenance'
    $cf = [pscustomobject]@{ Name = 'cloudflared.exe'; ProcessId = 4242; CommandLine = $qtCmd }
    $cfHidden = [pscustomobject]@{ Name = 'cloudflared.exe'; ProcessId = 4243; CommandLine = $null }
    function Invoke-Guard([object]$State, [object[]]$Procs, $After) {
        $global:QtFleet = @((New-FakeTask $qtName $State @('MSFT_TaskLogonTrigger')))
        $global:QtProcs = $Procs
        $global:QtProcsAfterStart = $After
        $global:QtStarted = @()
        if (Test-Path -LiteralPath $log) { Remove-Item -LiteralPath $log }
        $res = Invoke-Copy $guard @{ LogPath = $log }
        $logText = if (Test-Path -LiteralPath $log) { Get-Content -LiteralPath $log -Raw } else { '' }
        return [pscustomobject]@{ Text = $res.Text; Code = $res.Code; Log = $logText; Started = @($global:QtStarted) }
    }

    $r = Invoke-Guard 'Ready' @() @($cf)
    Check 'no cloudflared: the task is started exactly once' ($r.Started.Count -eq 1 -and $r.Started[0] -eq $qtName) ("started: " + ($r.Started -join ',') + "`n" + $r.Text + $r.Log)
    Check '... the recovery is logged and reported ok' ($r.Log -match 'quicktunnel DOWN' -and $r.Log -match 'quicktunnel back' -and $r.Text -match 'quicktunnel ok') ($r.Text + $r.Log)

    $r = Invoke-Guard 'Ready' @() @()
    Check 'a start that does not bring it back is DOWN and fails the run' `
        ($r.Started.Count -eq 1 -and $r.Text -match 'quicktunnel DOWN' -and $r.Code -eq 1) ("exit " + $r.Code + "`n" + $r.Text + $r.Log)

    $r = Invoke-Guard 'Running' @($cf) $null
    Check 'a running quick tunnel is left alone, and the run is green' ($r.Started.Count -eq 0 -and $r.Log -match 'quicktunnel ok' -and $r.Code -eq 0) ("exit " + $r.Code + "`n" + $r.Text + $r.Log)

    # From the guard's S4U/Limited token a SYSTEM process's command line reads
    # empty, so an unreadable cloudflared may be the named-tunnel SERVICE. With the
    # task not running it is not the quick tunnel: start the task once.
    $r = Invoke-Guard 'Ready' @($cfHidden) @($cfHidden, $cf)
    Check 'an unreadable cloudflared (e.g. the SYSTEM named-tunnel service) is not the quick tunnel (started once)' ($r.Started.Count -eq 1 -and $r.Started[0] -eq $qtName) ("started: " + ($r.Started -join ',') + "`n" + $r.Text + $r.Log)

    # A RUNNING task is the tunnel itself (its action waits on cloudflared), even
    # when no command line is readable: never start a second instance.
    $r = Invoke-Guard 'Running' @($cfHidden) $null
    Check 'a running task is a live quick tunnel even with no readable command line (no second instance)' ($r.Started.Count -eq 0 -and $r.Code -eq 0) ("exit " + $r.Code + "`n" + $r.Text + $r.Log)

    # The NAMED tunnel (staged in .pending) also runs cloudflared. Only the quick
    # tunnel's command line counts, so with just the named one alive the quick
    # tunnel is still down and must be started.
    $cfNamed = [pscustomobject]@{ Name = 'cloudflared.exe'; ProcessId = 4244; CommandLine = '"C:\Program Files (x86)\cloudflared\cloudflared.exe" tunnel --logfile "C:\x\logs\cloudflared.log" --loglevel warn run signaldeck' }
    $r = Invoke-Guard 'Ready' @($cfNamed) @($cfNamed, $cf)
    Check 'the named tunnel''s cloudflared is not mistaken for the quick tunnel (started once)' ($r.Started.Count -eq 1 -and $r.Started[0] -eq $qtName) ("started: " + ($r.Started -join ',') + "`n" + $r.Text + $r.Log)

    $r = Invoke-Guard 'Disabled' @() $null
    Check 'a DISABLED task is not started (the deliberate off switch)' ($r.Started.Count -eq 0) ($r.Text + $r.Log)

    Set-Content -LiteralPath $lock -Value '' -Encoding ASCII
    $r = Invoke-Guard 'Ready' @() @($cf)
    Remove-Item -LiteralPath $lock
    Check 'the maintenance lock is honoured' ($r.Started.Count -eq 0 -and $r.Text -match 'maintenance lock held') ($r.Text + $r.Log)

    $r = Invoke-Guard 'Ready' @($cf) $null
    Check 'sanity: the guard ran to its end, not into its FATAL catch' ($r.Code -ne 2 -and $r.Text -match 'web-guard: ' -and $r.Text -notmatch 'FATAL') ("exit " + $r.Code + "`n" + $r.Text)
}
finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Output ''
if ($script:fail -gt 0) {
    Write-Output ("test-quick-tunnel-ops: FAILED ({0})" -f $script:fail)
    exit 1
}
Write-Output 'test-quick-tunnel-ops: OK'
exit 0
