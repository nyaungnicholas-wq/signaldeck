# Restore a missing daemon task without changing an existing registration.
# The daemon must be the direct task action so stop/deploy target the process.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$existing = Get-ScheduledTask -TaskName 'SignalDeck Daemon' -ErrorAction SilentlyContinue
if ($existing) {
    Write-Output 'SignalDeck Daemon already registered; preserved existing settings.'
    exit 0
}
& "$PSScriptRoot\run-daemon-with-provenance.ps1" -CheckOnly
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$action = New-ScheduledTaskAction -Execute (Join-Path $repo 'bin\signaldeckd.exe') -WorkingDirectory (Join-Path $repo 'daemon')
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
$principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName 'SignalDeck Daemon' -Action $action -Settings $settings -Principal $principal | Out-Null
Write-Output 'Registered SignalDeck Daemon for the current signed-in user. The existing keepalive task starts it.'
