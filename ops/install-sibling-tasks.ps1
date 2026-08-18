# Register the SIBLING-PROJECT daemon tasks. RUN ELEVATED.
#
# install-windows-tasks.ps1 cannot create these: it walks ops/com.*.plist and
# SKIPs any whose ProgramArguments is not a shell script ("no shell script in
# ProgramArguments"), because it rewrites script paths onto THIS repo's ops/
# directory - which a sibling-repo binary or module can never be.
# com.tickstream.daemon.plist names a binary and com.stocktrader.hud.plist names
# `.venv/bin/python dashboard/server.py`; both carry macOS paths
# (/Users/natalienyaung/...), so neither was ever registered on this machine.
#
# SignalDeck polls both regardless: crypto-live hits 127.0.0.1:8321 and logged 55
# errors/hour, hud-sync hits 127.0.0.1:8787 and backs off to 4-minute retries
# reporting "trader-hud down". Two workers permanently unhealthy for a reason no
# SignalDeck change could fix.
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
$base = 'C:\Users\Nicholas_N\Desktop\claude code'
$services = @(
  @{ Name = 'TickStream Daemon'; Exe = "$base\tickstream\bin\tickstreamd.exe"; Arg = $null
    Wd = "$base\tickstream"
    Desc = 'Consolidated crypto order book on 127.0.0.1:8321; SignalDeck crypto-live polls /api/snapshot.' }
  @{ Name = 'TraderHud Daemon'; Exe = "$base\stock-trader\.venv\Scripts\python.exe"; Arg = 'dashboard/server.py'
    Wd = "$base\stock-trader"
    Desc = 'stock-trader PUSH-20 dashboard on 127.0.0.1:8787; SignalDeck hud-sync polls /api/summary.' }
)
foreach ($s in $services) {
  if (-not (Test-Path $s.Exe)) { Write-Output "SKIP $($s.Name): missing $($s.Exe)"; continue }
  if (Get-ScheduledTask -TaskName $s.Name -ErrorAction SilentlyContinue) {
    Write-Output "already registered: $($s.Name)"; continue
  }
  $action = if ($s.Arg) { New-ScheduledTaskAction -Execute $s.Exe -Argument $s.Arg -WorkingDirectory $s.Wd }
  else { New-ScheduledTaskAction -Execute $s.Exe -WorkingDirectory $s.Wd }
  Register-ScheduledTask -TaskName $s.Name -Description $s.Desc -Action $action `
    -Principal (New-ScheduledTaskPrincipal -UserId 'Nicholas_N' -LogonType S4U -RunLevel Limited) `
    -Trigger (New-ScheduledTaskTrigger -AtLogOn -User 'Nicholas_N') `
    -Settings (New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 `
      -RestartInterval (New-TimeSpan -Minutes 5) -MultipleInstances IgnoreNew -StartWhenAvailable `
      -DontStopOnIdleEnd -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries) | Out-Null
  $t = Get-ScheduledTask -TaskName $s.Name
  if ($t.Principal.LogonType -ne 'S4U') { throw "PRINCIPAL REGRESSION on $($s.Name) - re-register elevated" }
  Write-Output "registered $($s.Name): LogonType=$($t.Principal.LogonType)"
}
