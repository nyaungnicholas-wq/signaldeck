<#
.SYNOPSIS
  Re-register the two scheduled tasks whose output goes nowhere. RUN ELEVATED.

.DESCRIPTION
  Ships as a script, like ops\install-sibling-tasks.ps1, for the same reason:
  Set-ScheduledTask on an S4U-registered task returns "Access is denied" without
  elevation, so this cannot be done from an ordinary session.

  TASK 1 - SignalDeck Daemon Keepalive (audit finding A37).
  Registered by hand, so it never received the log-redirection handling
  ops\install-windows-tasks.ps1 gives every .ps1 job. Its action is a bare
  -File with no redirect, and it runs S4U in session 0 where there is no
  console. Everything ops\daemon-guard.ps1 prints every 5 minutes is discarded:
  the maintenance-lock decisions, the universe fail-safe's output, the
  "failed to start SignalDeck Daemon task" line, and - most pointedly - the
  "WARNING: provenance preflight NOT RUN" message, which exists precisely so
  that "the check is missing" cannot go on looking like "the check passed".
  It looks like the check passed.

  TASK 2 - SignalDeck Revalidation (audit finding A38).
  ops\com.signaldeck.revalidation.plist declares Day=1 (MONTHLY) plus
  StandardOutPath/StandardErrorPath. The live task predates both and was never
  re-registered: it carries a DAILY trigger and a bare bash action with no -lc
  redirect, so logs\revalidation.out.log does not exist and a monthly gate's
  refusals ("REVALIDATION FAILED", "produced an unusable snapshot") print to
  nothing. Re-running the installer regenerates it correctly.

  Safe to re-run. Verifies each change by reading the task back, because
  Register-ScheduledTask has silently registered something other than what was
  asked for before - see the read-back check in install-windows-tasks.ps1.
#>

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot

if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
      ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  Write-Output 'REFUSING: not elevated. Set-ScheduledTask on an S4U task needs an administrator.'
  Write-Output 'Re-run from an elevated PowerShell:  .\ops\fix-task-logging.ps1'
  exit 1
}

# --- 1. Keepalive: give daemon-guard.ps1 somewhere to speak ----------------
$name = 'SignalDeck Daemon Keepalive'
$task = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
if (-not $task) {
  Write-Output "SKIP  $name - not registered on this machine"
} else {
  $log = Join-Path $repo 'logs\daemon-guard.log'
  # The -Command *>> form install-windows-tasks.ps1 already emits for .ps1 jobs.
  $arg = '-NoProfile -ExecutionPolicy Bypass -Command "& ''{0}\ops\daemon-guard.ps1'' *>> ''{1}''"' -f $repo, $log
  Set-ScheduledTask -TaskName $name -Action (New-ScheduledTaskAction -Execute 'powershell' -Argument $arg) | Out-Null
  $back = (Get-ScheduledTask -TaskName $name).Actions[0].Arguments
  if ($back -like '*daemon-guard.log*') { Write-Output "OK    $name - output now lands in logs\daemon-guard.log" }
  else { Write-Output "FAIL  $name - read-back does not show the redirect: $back" }
}

# --- 2. Revalidation: monthly, with the logs its plist declares ------------
Write-Output ''
Write-Output 'Re-running ops\install-windows-tasks.ps1 -Install to regenerate SignalDeck Revalidation.'
Write-Output '(It skips the Daemon and sets S4U principals, so the historical hazards of re-running are closed.)'
& (Join-Path $PSScriptRoot 'install-windows-tasks.ps1') -Install

$rev = Get-ScheduledTask -TaskName 'SignalDeck Revalidation' -ErrorAction SilentlyContinue
if ($rev) {
  $trig = $rev.Triggers[0].CimClass.CimClassName
  Write-Output ''
  if ($trig -eq 'MSFT_TaskMonthlyTrigger') { Write-Output "OK    SignalDeck Revalidation - trigger is $trig (monthly, as the plist declares)" }
  else { Write-Output "CHECK SignalDeck Revalidation - trigger reads $trig, expected MSFT_TaskMonthlyTrigger" }
}
