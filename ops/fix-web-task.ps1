# Defect: The scheduled task 'SignalDeck Web' runs node via cmd.exe without -H,
# causing node to bind all interfaces and survive as an orphan when the task ends,
# holding port 8323 and preventing unelevated stop.
# This script re-points the task to use the loopback-only launcher
# ops\start-local-workspace.ps1 with -Port 8323 and sets the principal to Interactive
# so the process can be stopped. Must be run from an elevated PowerShell session.
param([string]$TaskName = 'SignalDeck Web', [int]$Port = 8323)
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$launcher = Join-Path $repo 'ops\start-local-workspace.ps1'
if (-not (Test-Path -LiteralPath $launcher)) { Write-Host ('FAILED: launcher not found: {0}' -f $launcher); exit 1 }
# elevation check
$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$pr = New-Object Security.Principal.WindowsPrincipal($id)
if (-not $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host 'REFUSED: not elevated. Re-run from an elevated PowerShell:'
    Write-Host ('  powershell -File "{0}"' -f $PSCommandPath)
    exit 2
}
$task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($null -eq $task) { Write-Host ('FAILED: task {0} not found' -f $TaskName); exit 1 }
Write-Host ('current action: {0} {1}' -f $task.Actions[0].Execute, $task.Actions[0].Arguments)
Write-Host ('current principal: {0} {1}' -f $task.Principal.LogonType, $task.Principal.RunLevel)
$backupDir = Join-Path $env:LOCALAPPDATA 'SignalDeck\task-backups'
New-Item -ItemType Directory -Force -Path $backupDir | Out-Null
$backup = Join-Path $backupDir ('{0}-SignalDeck-Web.xml' -f (Get-Date -Format 'yyyyMMdd-HHmmss'))
Export-ScheduledTask -TaskName $TaskName | Set-Content -LiteralPath $backup -Encoding Unicode
Write-Host ('backup written: {0}' -f $backup)
$argLine = ('-NoProfile -WindowStyle Hidden -File "{0}" -Port {1}' -f $launcher, $Port)
$action = New-ScheduledTaskAction -Execute 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe' -Argument $argLine -WorkingDirectory (Join-Path $repo 'web')
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited
Set-ScheduledTask -TaskName $TaskName -Action $action -Principal $principal | Out-Null
$after = Get-ScheduledTask -TaskName $TaskName
Write-Host ('new action: {0} {1}' -f $after.Actions[0].Execute, $after.Actions[0].Arguments)
Write-Host ('new principal: {0} {1}' -f $after.Principal.LogonType, $after.Principal.RunLevel)
$expected = ('-Port {0}' -f $Port)
if (($after.Actions[0].Arguments -notlike ('*' + $expected + '*')) -or ($after.Actions[0].Arguments -notlike ('*start-local-workspace.ps1*'))) { Write-Host 'FAILED: task action did not update'; exit 1 }
Write-Host ('OK - task re-pointed. Now free the orphaned port and start it: powershell -File "{0}"' -f (Join-Path $repo 'ops\restart-web.ps1'))
exit 0