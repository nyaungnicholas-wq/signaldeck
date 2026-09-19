<#
.SYNOPSIS
The staleness rule in ops/check-grader-health.ps1, driven over its whole matrix.

.DESCRIPTION
A powered-off machine and a broken grader produce the IDENTICAL symptom -- a
stale heartbeat -- and until 2026-09-14 this check reported both the same way.

Measured on this box: it was off from 2026-09-11 00:21 to 2026-09-12 21:18
(System log 6006 -> 6005, ~45 h). "SignalDeck Accuracy" fires daily at 14:05
with WakeToRun=False, so it could not run on either day. The heartbeat reached
67 h, check-grader-health went red at 09:20 on 09-13, and Check-Task-Health went
red behind it as a pure cascade. The grader was fine: it ran at 14:05 the same
day and recorded a normal refusal.

The danger in fixing that is obvious -- an excuse for staleness is one edit away
from a fail-open, and this is the check that tells you the grader stopped. So
the rule is bounded twice, and BOTH bounds are asserted here:

  * forgiven only while UPTIME is under one full window (the grader can only run
    while the machine is up, so uptime is the window in which it had a chance);
  * NEVER past a hard ceiling of three windows, so a box rebooting often enough
    to keep uptime low cannot hide a real outage indefinitely;
  * an UNREADABLE uptime is never an excuse.

Run: powershell -NoProfile -ExecutionPolicy Bypass -File ops\test-check-grader-health.ps1
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$script:fail = 0

# Load the function under test without running the check itself. The script
# reads a live database and exits, so it is dot-sourced only up to its function
# definitions -- the parser gives us the function body directly.
$path = Join-Path $PSScriptRoot 'check-grader-health.ps1'
$ast = [System.Management.Automation.Language.Parser]::ParseFile($path, [ref]$null, [ref]$null)
$fn = $ast.Find({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and
                       $n.Name -eq 'Get-HeartbeatVerdict' }, $true)
if (-not $fn) {
    Write-Output "test-check-grader-health: Get-HeartbeatVerdict not found in $path"
    exit 2
}
. ([scriptblock]::Create($fn.Extent.Text))

function Check([string]$what, [string]$expected, [double]$age, [int]$max, [double]$uptime) {
    $got = Get-HeartbeatVerdict -AgeMinutes $age -MaxAgeMinutes $max -UptimeMinutes $uptime
    if ($got -eq $expected) {
        Write-Output ("  PASS  {0}" -f $what)
    } else {
        Write-Output ("  FAIL  {0} -- expected '{1}', got '{2}'" -f $what, $expected, $got)
        $script:fail++
    }
}

$MAX = 1560   # the shipped default: 26 h

Write-Output 'the ordinary cases'
Check 'a fresh heartbeat is fresh'                       'fresh' 60 $MAX 100000
Check 'exactly at the limit is still fresh'              'fresh' $MAX $MAX 100000
Check 'stale on a long-running machine is a FAULT'       'stale' 2000 $MAX 100000

Write-Output 'the incident this rule was written for'
# 09-13 09:20: heartbeat 4034 min old, machine up ~12 h after a 45 h outage.
Check 'stale right after a long outage is EXPECTED'      'expected-downtime' 4034 $MAX 720

Write-Output 'the bounds that stop it becoming a fail-open'
Check 'uptime past one window means the grader had its chance' 'stale' 2000 $MAX ($MAX + 1)
Check 'age past the 3x ceiling is a fault however short the uptime' 'stale' (($MAX * 3) + 1) $MAX 10
Check 'age exactly at the ceiling is a fault'            'stale' ($MAX * 3) $MAX 10
Check 'unreadable uptime is NOT an excuse'               'stale' 4034 $MAX -1

Write-Output 'boundaries'
Check 'uptime one minute under the window is forgiven'   'expected-downtime' 2000 $MAX ($MAX - 1)
Check 'a fresh heartbeat on a just-booted machine is still fresh' 'fresh' 5 $MAX 5

Write-Output ''
if ($script:fail -gt 0) {
    Write-Output ("test-check-grader-health: FAILED ({0})" -f $script:fail)
    exit 1
}
Write-Output 'test-check-grader-health: OK - 10 assertions passed'
exit 0
