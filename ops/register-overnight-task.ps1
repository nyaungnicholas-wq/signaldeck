<#
.SYNOPSIS
  Registers the nightly overnight accuracy loop as a scheduled task.
  RUN THIS FROM AN ELEVATED POWERSHELL -- registering an S4U principal is denied
  without it.

.DESCRIPTION
  S4U, not Interactive, and deliberately so: every other SignalDeck task on this
  box runs S4U because Interactive-logon tasks were being killed by console
  control events (0xC000013A) whenever a console session closed. An overnight
  loop is exactly the workload that would hit that.

  Idempotent -- re-running replaces the task.

.EXAMPLE
  # In an ELEVATED PowerShell:
  powershell -NoProfile -ExecutionPolicy Bypass -File ops/register-overnight-task.ps1
  powershell -NoProfile -ExecutionPolicy Bypass -File ops/register-overnight-task.ps1 -At 10:30PM -Hours 9
#>
param(
  [string]$TaskName = 'SignalDeck Overnight Accuracy',
  [datetime]$At = '11:00PM',
  [double]$Hours = 8,
  [switch]$Unregister
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot

$elevated = ([Security.Principal.WindowsPrincipal] `
  [Security.Principal.WindowsIdentity]::GetCurrent()
).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $elevated) {
  Write-Error "Not elevated. Registering an S4U task will fail with 'Access is denied'. Re-run this from an Administrator PowerShell."
  exit 1
}

if ($Unregister) {
  Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
  Write-Host "removed: $TaskName"
  exit 0
}

$action = New-ScheduledTaskAction -Execute 'powershell.exe' `
  -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$repo\ops\overnight.ps1`" -Hours $Hours" `
  -WorkingDirectory $repo
$trigger = New-ScheduledTaskTrigger -Daily -At $At
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" `
  -LogonType S4U -RunLevel Limited
# ExecutionTimeLimit sits above -Hours so the task never races the loop's own
# cap; StartWhenAvailable so a machine asleep at 23:00 still runs on waking.
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
  -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Hours ($Hours + 2)) `
  -MultipleInstances IgnoreNew

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
  -Principal $principal -Settings $settings -Force | Out-Null

$t = Get-ScheduledTask -TaskName $TaskName
$i = $t | Get-ScheduledTaskInfo
Write-Host "registered: $($t.TaskName)"
Write-Host "  principal : $($t.Principal.LogonType) / $($t.Principal.RunLevel)"
Write-Host "  next run  : $($i.NextRunTime)"
Write-Host "  runs      : ops/overnight.ps1 -Hours $Hours"
Write-Host "  stop with : New-Item '$repo\ops\STOP-OVERNIGHT'"
