# Register the TickStream Daemon scheduled task. RUN ELEVATED.
#
# install-windows-tasks.ps1 cannot create this one: it walks ops/com.*.plist and
# SKIPs any whose ProgramArguments is not a shell script ("no shell script in
# ProgramArguments"). com.tickstream.daemon.plist names a BINARY in a sibling
# repo, and its paths are macOS (/Users/natalienyaung/...), so nothing on this
# machine ever registered it. SignalDeck's crypto-live worker polls
# 127.0.0.1:8321 regardless and logged 55 errors/hour while it was absent.
#
# S4U is set EXPLICITLY. Register-ScheduledTask defaults to Interactive when
# -RunLevel is passed without -Principal, and that once took this fleet from
# S4U=14 to Interactive=11, re-exposing batch tasks to 0xC000013A console kills.
# Setting it afterwards needs elevation too, so getting it right here is cheaper.
#
# The binary is exec'd DIRECTLY, not through cmd.exe. install-windows-tasks.ps1
# lines 81-88 record why: a wrapper makes the process a grandchild and
# `schtasks /End` does not cascade, so a stop leaves an orphan holding port 8321
# and every later start fails to bind. That costs the stdout/stderr redirection
# the plist asks for; capturing those belongs upstream in tickstream itself.
$ErrorActionPreference = 'Stop'
$repo = 'C:\Users\Nicholas_N\Desktop\claude code\tickstream'
$exe = Join-Path $repo 'bin\tickstreamd.exe'
if (-not (Test-Path $exe)) { throw "binary missing: $exe" }
if (Get-ScheduledTask -TaskName 'TickStream Daemon' -ErrorAction SilentlyContinue) {
  Write-Output 'TickStream Daemon already registered - nothing to do.'; return
}
Register-ScheduledTask -TaskName 'TickStream Daemon' `
  -Principal (New-ScheduledTaskPrincipal -UserId 'Nicholas_N' -LogonType S4U -RunLevel Limited) `
  -Action (New-ScheduledTaskAction -Execute $exe -WorkingDirectory $repo) `
  -Trigger (New-ScheduledTaskTrigger -AtLogOn -User 'Nicholas_N') `
  -Settings (New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) `
      -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 5) `
      -MultipleInstances IgnoreNew -StartWhenAvailable -DontStopOnIdleEnd `
      -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries) `
  -Description 'Consolidated crypto order book on 127.0.0.1:8321; SignalDeck crypto-live polls /api/snapshot.' | Out-Null
$t = Get-ScheduledTask -TaskName 'TickStream Daemon'
Write-Output "registered: LogonType=$($t.Principal.LogonType) RunLevel=$($t.Principal.RunLevel)"
if ($t.Principal.LogonType -ne 'S4U') { throw 'PRINCIPAL REGRESSION: not S4U - remove and re-register elevated' }
