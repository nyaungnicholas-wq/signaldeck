# register-daemon-task.ps1 -- DRAFT. DOES NOT EXECUTE ANYTHING.
#
# ####################################################################
# #  DO NOT RUN THIS TO CHANGE THE TASK. Running it PRINTS the plan  #
# #  and exits 2. Every mutating command below is a string, never a  #
# #  call. Registering or restarting a Windows scheduled task was    #
# #  out of scope for the session that wrote this (SWARM 4a,         #
# #  2026-08-06), so NONE of it has been executed or verified        #
# #  against the live scheduler.                                     #
# ####################################################################
#
# WHAT IT WOULD DO
#   Repoint the "SignalDeck Daemon" task's Exec action from
#     C:\...\signaldeck\bin\signaldeckd.exe
#   to
#     powershell -NoProfile -ExecutionPolicy Bypass -File
#       C:\...\signaldeck\ops\run-daemon-with-provenance.ps1
#   so the binary is checked against git HEAD before it is started.
#
#   It uses Set-ScheduledTask, not Register-ScheduledTask -Force: only the
#   action changes, and the LogonTrigger, RestartOnFailure 999 x PT5M,
#   ExecutionTimeLimit PT0S, MultipleInstancesPolicy IgnoreNew and the
#   InteractiveToken principal all stay exactly as they are. A -Force
#   re-registration would rewrite all of that from defaults, which is a much
#   bigger blast radius for a one-field change. The Register-ScheduledTask
#   form is given too, since it was asked for, but Set-ScheduledTask is the
#   one to use.
#
# ####################################################################
# #  READ THIS BEFORE CHOOSING OPTION A                              #
# #                                                                  #
# #  Making a PowerShell script the task's Exec action REGRESSES     #
# #  graceful stop. PowerShell has no exec(), so signaldeckd becomes  #
# #  a CHILD of the wrapper. ops/daemon-guard.ps1:79-91 records what  #
# #  that costs, from this machine:                                   #
# #                                                                  #
# #    sd_svc_stop is `schtasks /End` on "SignalDeck Daemon", and     #
# #    /End only terminates the process Task Scheduler itself started #
# #    - it does not cascade to children. When the guard launched the #
# #    binary, /End ended the guard and left the daemon running, so   #
# #    every `signaldeck-ctl.sh stop` silently degraded into the      #
# #    caller's kill -9 fallback, skipping the worker drain and the   #
# #    WAL checkpoint.                                                #
# #                                                                  #
# #  That is why the task points straight at the .exe today. It was   #
# #  paid for once. OPTION B below buys the same provenance check for #
# #  nothing.                                                         #
# ####################################################################

$repo = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$task = 'SignalDeck Daemon'
$wrapperInstalled = Join-Path $repo 'ops\run-daemon-with-provenance.ps1'
$backup = Join-Path $repo 'round2-drafts\devops\SignalDeck-Daemon.task.xml.bak'

$plan = @"
# ---------------------------------------------------------------------------
# STEP 0 (do this first, always): capture the current definition verbatim, so
# the undo does not depend on this file being accurate.

  Export-ScheduledTask -TaskName '$task' |
    Set-Content -LiteralPath '$backup' -Encoding Unicode

# ---------------------------------------------------------------------------
# STEP 1: install the wrapper next to the other ops scripts. The task cannot
# point at round2-drafts/.

  Copy-Item '$PSScriptRoot\run-daemon-with-provenance.ps1' '$wrapperInstalled'

# ---------------------------------------------------------------------------
# OPTION A -- repoint the task (see the warning above; prefer OPTION B)
#
# powershell, not pwsh: the rest of the scheduled tasks on this box use Windows
# PowerShell 5.1, and the wrapper is written ASCII-only for exactly that reason.

  `$act = New-ScheduledTaskAction ``
    -Execute 'powershell' ``
    -Argument '-NoProfile -ExecutionPolicy Bypass -File "$wrapperInstalled"' ``
    -WorkingDirectory '$repo\daemon'
  Set-ScheduledTask -TaskName '$task' -Action `$act

# Full-re-registration form, if you insist on Register-ScheduledTask. This
# REPLACES triggers, settings and principal, so they must all be restated:

  `$act = New-ScheduledTaskAction -Execute 'powershell' ``
    -Argument '-NoProfile -ExecutionPolicy Bypass -File "$wrapperInstalled"' ``
    -WorkingDirectory '$repo\daemon'
  `$trg = New-ScheduledTaskTrigger -AtLogOn -User "`$env:USERDOMAIN\`$env:USERNAME"
  `$set = New-ScheduledTaskSettingsSet -StartWhenAvailable ``
    -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries ``
    -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew ``
    -RestartCount 999 -RestartInterval ([TimeSpan]::FromMinutes(5))
  Register-ScheduledTask -TaskName '$task' -Action `$act -Trigger `$trg ``
    -Settings `$set -RunLevel Limited -Force

# ---------------------------------------------------------------------------
# UNDO for OPTION A -- exact, and symmetric with the Set-ScheduledTask form.

  `$act = New-ScheduledTaskAction ``
    -Execute '$repo\bin\signaldeckd.exe' ``
    -WorkingDirectory '$repo\daemon'
  Set-ScheduledTask -TaskName '$task' -Action `$act

# UNDO from the STEP 0 backup, if anything else drifted. -Xml drops the
# principal's SID, so the user has to be restated:

  Register-ScheduledTask -TaskName '$task' ``
    -Xml (Get-Content -LiteralPath '$backup' -Raw) ``
    -User "`$env:USERDOMAIN\`$env:USERNAME" -Force

# Confirm either way:

  (Export-ScheduledTask -TaskName '$task') -match '<Command>'

# ---------------------------------------------------------------------------
# OPTION B -- RECOMMENDED. No task is touched at all.
#
# ops/daemon-guard.ps1 is the only thing that starts the daemon on this box: it
# runs every 5 minutes, and it already knows how to stand down without
# starting. Put the check there and the daemon stays a direct child of Task
# Scheduler, so `schtasks /End` keeps working. Insert immediately before the
# `& `$sd /Run /TN 'SignalDeck Daemon'` line at the foot of that script:
#
#   `$prov = Join-Path `$root 'ops\run-daemon-with-provenance.ps1'
#   if (Test-Path `$prov) {
#       & `$prov -CheckOnly
#       if (`$LASTEXITCODE -ne 0) { exit `$LASTEXITCODE }   # message already logged
#   }
#
# round2-drafts/patches/daemon-guard-provenance-preflight.patch is that insert
# as an applicable patch.
# ---------------------------------------------------------------------------
"@

Write-Output $plan
Write-Output ''
Write-Output 'This file is a plan, not an installer. Nothing above was executed.'
exit 2
