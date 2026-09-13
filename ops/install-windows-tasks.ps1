<#
Recreate the launchd schedule as Windows Scheduled Tasks.

The repo's automation is defined by ops/com.*.plist. On the 2026-07-31 move to
Windows only ONE of those 14 jobs was recreated (the daemon), so the nightly
backup, accuracy grading, cleanup, restore rehearsal, research liveness and
bias regression had not run since. This reads the plists -- the same files the
Mac uses, so the two schedules cannot drift -- and registers the equivalents.

DEFAULT IS A REPORT. Nothing is registered unless -Install is passed.

  .\install-windows-tasks.ps1            # show what would happen
  .\install-windows-tasks.ps1 -Install   # register / update
  .\install-windows-tasks.ps1 -Remove    # unregister the managed tasks
#>
[CmdletBinding()]
param([switch]$Install, [switch]$Remove)

$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent

# Git Bash, never WSL. WSL has its own filesystem and cannot see C:\Users\...
# the way these scripts expect -- the same defect that broke test_accuracy_registry.
$bash = @(
  (Join-Path $env:ProgramFiles 'Git\bin\bash.exe'),
  (Join-Path ${env:ProgramFiles(x86)} 'Git\bin\bash.exe')
) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $bash) { $bash = (Get-Command bash -ErrorAction SilentlyContinue).Source }
if (-not $bash) { Write-Output 'FATAL: no bash found; install Git for Windows.'; exit 1 }

# C:\path\to\repo -> /c/path/to/repo
$repoPosix = '/' + $repo.Substring(0, 1).ToLower() + ($repo.Substring(2) -replace '\\', '/')

function Get-PlistDict {
  # Apple plists are <key> followed by its value sibling. Flatten one dict.
  param($DictNode)
  $out = @{}
  $kids = @($DictNode.ChildNodes | Where-Object { $_.NodeType -eq 'Element' })
  for ($i = 0; $i -lt $kids.Count; $i += 2) {
    $k = $kids[$i]; $v = $kids[$i + 1]
    if ($k.Name -ne 'key' -or $null -eq $v) { continue }
    $out[$k.InnerText] = $v
  }
  return $out
}

function ConvertTo-Value {
  param($Node)
  switch ($Node.Name) {
    'string'  { $Node.InnerText }
    'integer' { [int]$Node.InnerText }
    'true'    { $true }
    'false'   { $false }
    'array'   { , @($Node.ChildNodes | Where-Object { $_.NodeType -eq 'Element' } | ForEach-Object { ConvertTo-Value $_ }) }
    'dict'    { Get-PlistDict $Node }
    default   { $Node.InnerText }
  }
}

function Get-TaskName {
  param([string]$Label)
  $leaf = ($Label -split '\.')[-1]
  $name = ($leaf -split '-' | ForEach-Object {
      if ($_.Length -gt 0) { $_.Substring(0, 1).ToUpper() + $_.Substring(1) } else { $_ }
    }) -join '-'
  if ($Label -like 'com.tickstream.*') { return "TickStream $name" }
  if ($Label -like 'com.stocktrader.*') { return "StockTrader $name" }
  return "SignalDeck $name"
}

$created = 0; $updated = 0; $skipped = 0; $managed = @()

foreach ($f in (Get-ChildItem (Join-Path $repo 'ops') -Filter 'com.*.plist' | Sort-Object Name)) {
  try { $xml = [xml](Get-Content $f.FullName -Raw) }
  catch { Write-Output ("SKIP   {0,-34} unparseable: {1}" -f $f.Name, $_.Exception.Message); $skipped++; continue }

  $d = Get-PlistDict $xml.plist.dict
  $label = if ($d.ContainsKey('Label')) { ConvertTo-Value $d['Label'] } else { $f.BaseName }
  $task = Get-TaskName $label

  # NEVER REWRITE THE DAEMON'S ACTION. com.signaldeck.daemon.plist routes
  # launchd through `signaldeck-ctl.sh launch`, so translating it would register
  # the Daemon task as `bash ... signaldeck-ctl.sh launch` -- but the LIVE task
  # execs bin\signaldeckd.exe directly, and ops/daemon-guard.ps1:130 records why
  # that must stay so: sd_svc_stop is `schtasks /End`, /End only terminates the
  # process Task Scheduler itself started and does NOT cascade to children, so
  # routing through bash makes the daemon a grandchild and every
  # `signaldeck-ctl.sh stop` silently degrades to the kill -9 fallback --
  # skipping the worker drain and the WAL checkpoint. Register-ScheduledTask
  # here uses -Force, so following this file's own documented recovery path
  # (`.\install-windows-tasks.ps1 -Install`) would have bought that regression
  # back. The ExecutionTimeLimit branch below already special-cases the daemon
  # for an adjacent reason; the action shape needed the same protection and did
  # not have it.
  if ($label -like 'com.signaldeck.daemon') {
    Write-Output ("SKIP   {0,-34} service task: its direct-exec action preserves graceful stop; refusing to repoint it through bash" -f $task)
    $skipped++; continue
  }


  $pargs = if ($d.ContainsKey('ProgramArguments')) { @(ConvertTo-Value $d['ProgramArguments']) } else { @() }
  # .ps1 is a first-class job here, not an oddity. The fleet's two health gates
  # (check-task-health.ps1, check-grader-health.ps1) are PowerShell, and this
  # translator accepted only .sh -- so they had no plist worth writing, no task,
  # and ran ONLY when a human remembered. fix-task-principals.ps1:233 even tells
  # the operator to "watch for recurrence with ops\check-task-health.ps1" as a
  # manual instruction. Correct scripts that never run unattended are the same
  # blind spot as a check that always passes.
  $sh = $pargs | Where-Object { $_ -is [string] -and $_ -match '\.(sh|ps1)$' } | Select-Object -First 1
  if (-not $sh) {
    Write-Output ("SKIP   {0,-34} no shell script in ProgramArguments" -f $task)
    $skipped++; continue
  }
  $isPs = $sh -match '\.ps1$'
  # Rewrite any absolute macOS path onto THIS repo.
  $scriptPosix = "$repoPosix/ops/" + (Split-Path $sh -Leaf)
  $scriptWin = Join-Path $repo ('ops\' + (Split-Path $sh -Leaf))
  $rest = @($pargs | Where-Object {
      $_ -is [string] -and $_ -ne $sh -and $_ -notmatch '\.(sh|ps1)$' -and
      $_ -notmatch '(^|/)(bash|sh)$' -and $_ -notmatch '(?i)(^|\\|/)(pwsh|powershell)(\.exe)?$' -and
      $_ -notmatch '^-(NoProfile|ExecutionPolicy|File)$' -and $_ -ne 'Bypass'
    })
  # The exec differs by kind: bash for .sh, PowerShell for .ps1.
  $exeForTask = if ($isPs) { 'powershell.exe' } else { $bash }
  $argLine = if ($isPs) {
    '-NoProfile -ExecutionPolicy Bypass -File "{0}"' -f $scriptWin
  }
  else {
    ('"{0}"' -f $scriptPosix)
  }
  if ($rest.Count) { $argLine += ' ' + ($rest -join ' ') }

  # LOG REDIRECTION. Every plist declares StandardOutPath/StandardErrorPath and
  # this translator read NEITHER, so launchd's redirection was silently dropped
  # on the move to Windows. Scripts that open their own log survived; scripts
  # that relied on the redirect now write to a console that does not exist.
  # Measured 2026-08-12: SignalDeck Cleanup reported 0x00000000 daily for 14 days
  # with logs/cleanup.sched.log last written 07-29, and market-open-guard's
  # `exit 3` refusal message went nowhere. "Ran and did the work" became
  # indistinguishable from "ran and did nothing".
  #
  # New-ScheduledTaskAction cannot redirect, and `bash script.sh >> log` does NOT
  # redirect either -- with -Execute bash.exe the operators are passed to the
  # SCRIPT as arguments, not interpreted. Only a shell performs a redirect, so
  # when the plist asks for one the action becomes `bash -lc "<script> ... >> log"`.
  # Paths are rewritten onto THIS repo's logs/ (the plists carry macOS paths).
  $logLeaf = ''
  foreach ($k in @('StandardOutPath', 'StandardErrorPath')) {
    if (-not $logLeaf -and $d.ContainsKey($k)) {
      $v = ConvertTo-Value $d[$k]
      if ($v) { $logLeaf = Split-Path $v -Leaf }
    }
  }
  if ($logLeaf) {
    if ($isPs) {
      # PowerShell needs -Command for a redirect too. `*>&1 | Out-File`
      # rather than a bare `*>>`: WinPS 5.1's `*>>` is Out-File's UNICODE
      # default, so it writes UTF-16LE with a BOM and NUL-interleaved bytes
      # and `grep` returns 0 matches on lines that demonstrably exist.
      # Measured 2026-09-13: logs/check-grader-health.log and
      # logs/check-task-health.log were both UTF-16LE on disk, while every
      # bash-launched task's log (redirected inside bash) was UTF-8. A health
      # log the usual tools cannot read is a health log nobody reads.
      #
      # `*>&1` keeps the all-streams capture the bare `*>>` gave, so this is
      # still the -File equivalent of `>> log 2>&1`; only the encoding moves.
      $logWin = Join-Path $repo ('logs\' + $logLeaf)
      $inner = "& '$scriptWin'" + $(if ($rest.Count) { ' ' + ($rest -join ' ') } else { '' }) +
      " *>&1 | Out-File -FilePath '$logWin' -Append -Encoding utf8"
      $argLine = '-NoProfile -ExecutionPolicy Bypass -Command "' + $inner + '"'
    }
    else {
      $logPosix = "$repoPosix/logs/$logLeaf"
      $inner = "'$scriptPosix'" + $(if ($rest.Count) { ' ' + ($rest -join ' ') } else { '' }) +
      " >> '$logPosix' 2>&1"
      $argLine = '-lc "' + $inner + '"'
    }
  }

  # --- triggers -----------------------------------------------------------
  $triggers = @(); $when = @()
  if ($d.ContainsKey('StartCalendarInterval')) {
    $cal = ConvertTo-Value $d['StartCalendarInterval']
    $items = if ($cal -is [System.Collections.IDictionary]) { @($cal) } else { @($cal) }
    foreach ($it in $items) {
      $h = if ($it.ContainsKey('Hour')) { [int](ConvertTo-Value $it['Hour']) } else { 0 }
      $m = if ($it.ContainsKey('Minute')) { [int](ConvertTo-Value $it['Minute']) } else { 0 }
      $at = (Get-Date -Hour $h -Minute $m -Second 0)
      # launchd fires on every field a StartCalendarInterval dict OMITS, so a
      # dict carrying only Minute means "every hour at :MM", not "daily at
      # 00:MM". No plist in ops/ does that today; say so rather than silently
      # registering a schedule 24x slower than the plist asks for.
      if (-not $it.ContainsKey('Hour')) {
        Write-Output ("WARN   {0,-34} no Hour in StartCalendarInterval (launchd runs this HOURLY); registered daily {1:HH:mm}" -f $task, $at)
      }
      $untranslated = @($it.Keys | Where-Object { $_ -notin @('Day', 'Hour', 'Minute', 'Weekday') })
      if ($untranslated.Count) {
        Write-Output ("WARN   {0,-34} StartCalendarInterval key(s) ignored: {1}" -f $task, ($untranslated -join ', '))
      }
      if ($it.ContainsKey('Day')) {
        # Day-of-month. Until 2026-08-06 there was no branch for it, so a
        # monthly plist fell through to -Daily: com.signaldeck.revalidation asks
        # for the 1st at 03:20 and the live task ran every night (LastRunTime
        # 08-06 03:20, NextRunTime 08-07 03:20).
        #
        # New-ScheduledTaskTrigger has no -Monthly, so the trigger is built as a
        # CIM instance. The property shapes are load-bearing and are NOT the ones
        # most examples use: MSFT_TaskMonthlyTrigger declares MonthOfYear
        # (singular) and a scalar UInt16 DaysOfMonth BITMASK -- day N is
        # 1 shl (N-1), confirmed against IMonthlyTrigger, which renders
        # DaysOfMonth=32768 as <Day>16</Day>. The copied-everywhere
        # MonthsOfYear/uint32[] form fails to bind. A UInt16 mask stops at 16.
        $day = [int](ConvertTo-Value $it['Day'])
        if ($day -lt 1 -or $day -gt 16) {
          Write-Output ("WARN   {0,-34} Day {1} is outside the 1-16 a UInt16 day mask can carry; no trigger registered for it" -f $task, $day)
          continue
        }
        $mt = New-CimInstance -ClassName MSFT_TaskMonthlyTrigger `
          -Namespace Root/Microsoft/Windows/TaskScheduler -ClientOnly -Property @{
            DaysOfMonth   = [uint16](1 -shl ($day - 1))
            MonthOfYear   = [uint16]4095   # every month
            StartBoundary = $at.ToString('yyyy-MM-ddTHH:mm:ss')
            Enabled       = $true
          }
        # -Trigger binds on PSTypeName, and a ClientOnly instance of a subclass
        # does not advertise the base type on its own.
        $mt.PSTypeNames.Insert(0, 'Microsoft.Management.Infrastructure.CimInstance#MSFT_TaskTrigger')
        $triggers += $mt
        $when += ('monthly day {0} {1:HH:mm}' -f $day, $at)
      } elseif ($it.ContainsKey('Weekday')) {
        $dow = [System.DayOfWeek]([int](ConvertTo-Value $it['Weekday']) % 7)
        $triggers += New-ScheduledTaskTrigger -Weekly -DaysOfWeek $dow -At $at
        $when += ('{0} {1:HH:mm}' -f $dow, $at)
      } else {
        $triggers += New-ScheduledTaskTrigger -Daily -At $at
        $when += ('daily {0:HH:mm}' -f $at)
      }
    }
  }
  if ($d.ContainsKey('StartInterval')) {
    $sec = [int](ConvertTo-Value $d['StartInterval'])
    $triggers += New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval ([TimeSpan]::FromSeconds($sec))
    $when += "every ${sec}s"
  }
  if (-not $triggers.Count) {
    $keep = $d.ContainsKey('KeepAlive') -and (ConvertTo-Value $d['KeepAlive'])
    $load = $d.ContainsKey('RunAtLoad') -and (ConvertTo-Value $d['RunAtLoad'])
    if ($keep -or $load) { $triggers += New-ScheduledTaskTrigger -AtLogOn; $when += 'at logon' }
  }
  if (-not $triggers.Count) {
    Write-Output ("SKIP   {0,-34} on-demand only (no schedule to translate)" -f $task)
    $skipped++; continue
  }

  $exists = $null -ne (Get-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue)
  $managed += $task
  if ($exists) { $updated++ } else { $created++ }

  if ($Install) {
    $action = New-ScheduledTaskAction -Execute $exeForTask -Argument $argLine -WorkingDirectory $repo
    # The 6-hour ExecutionTimeLimit is for BATCH jobs. The daemon is a long-lived
    # service -- the live "SignalDeck Daemon" task carries PT0S (unlimited) and was
    # created by another route -- so applying the batch cap to it would have Task
    # Scheduler terminate the daemon every six hours. Following this file's own
    # documented recovery path (`.\install-windows-tasks.ps1 -Install`) would
    # therefore have converted a healthy daemon into one that dies four times a
    # day, with the restart looking like an ordinary crash.
    $isService = $label -like 'com.signaldeck.daemon'
    $limit = if ($isService) { [TimeSpan]::Zero } else { [TimeSpan]::FromHours(6) }
    $set = New-ScheduledTaskSettingsSet -StartWhenAvailable -AllowStartIfOnBatteries `
      -DontStopIfGoingOnBatteries -ExecutionTimeLimit $limit
    # S4U, NOT the Interactive default. Register-ScheduledTask with -RunLevel
    # and no -Principal registers the task Interactive, which puts it inside the
    # console session and therefore reachable by console control events --
    # 0xC000013A, the kill signature ops/fix-task-principals.ps1 exists to end.
    # That whole fleet had already been converted to S4U; re-running THIS script
    # silently converted them back. Measured 2026-08-12: one -Install run took
    # the fleet from Interactive=0/S4U=14 to Interactive=11/S4U=5, and
    # check-task-health caught it immediately. Registering the principal
    # explicitly is what stops the recovery path from undoing the fix.
    # RUNLEVEL: Limited for the fleet, Highest for the two tasks that must
    # TERMINATE the daemon.
    #
    # Task Scheduler starts the daemon with an S4U token in session 0, and an
    # S4U logon gets its own logon-session SID. Another S4U task -- even one
    # running as the SAME user, which every task here does -- therefore cannot
    # open the daemon's process handle: measured 2026-09-13,
    # OpenProcess(PROCESS_TERMINATE) against signaldeckd returns win32 error 5,
    # ACCESS_DENIED. Highest gives the token SeDebugPrivilege, which is what
    # actually permits the kill.
    #
    # The consequence of leaving it Limited is not a failed kill, it is a
    # MISSING BACKUP: market-close.sh could not stop the daemon, so
    # signaldeck-backup-offline.sh's is-daemon-alive check refused to run and
    # the day's off-machine copy silently did not happen, while the task still
    # exited 0. Confirmed on 2026-09-12, with the newest GitHub backup two days
    # stale.
    #
    # Exactly the two labels whose scripts call sd_kill_hard, and no others --
    # elevation is granted where it is needed, not fleet-wide. LogonType stays
    # S4U for every task, which is the load-bearing part above and must not
    # change.
    $needsKill = @('com.signaldeck.market-close', 'com.signaldeck.daily-refresh')
    $runLevel = if ($needsKill -contains $label) { 'Highest' } else { 'Limited' }
    $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" `
      -LogonType S4U -RunLevel $runLevel
    Register-ScheduledTask -TaskName $task -Action $action -Trigger $triggers `
      -Settings $set -Principal $principal -Force | Out-Null
    # The monthly trigger is assembled by hand from a CIM class whose property
    # shapes are not documented alongside the cmdlet, and a wrong shape can
    # register quietly as something else -- the exact failure this branch exists
    # to end. Read it back rather than trust it.
    # Read the RunLevel back for the same reason the monthly trigger is read
    # back: a principal that did not take is indistinguishable from one that
    # did until the task next needs the privilege, which is at market close.
    $gotLevel = (Get-ScheduledTask -TaskName $task).Principal.RunLevel
    if ("$gotLevel" -ne $runLevel) {
        Write-Output ("FAIL   {0,-34} RunLevel is {1}, expected {2}" -f $task, $gotLevel, $runLevel)
    }
    if (($when -join ',') -like '*monthly*') {
      $kinds = @((Get-ScheduledTask -TaskName $task).Triggers.CimClass.CimClassName)
      if ($kinds -notcontains 'MSFT_TaskMonthlyTrigger') {
        Write-Output ("FAIL   {0,-34} monthly trigger did not survive registration (got: {1})" -f $task, ($kinds -join ', '))
      }
    }
    Write-Output ("{0} {1,-34} {2}" -f $(if ($exists) { 'UPDATE' } else { 'CREATE' }), $task, ($when -join ', '))
  } else {
    Write-Output ("{0,-21} {1,-34} {2}" -f $(if ($exists) { 'exists (would update)' } else { 'would create' }), $task, ($when -join ', '))
  }
}

if ($Remove) {
  foreach ($t in $managed) {
    if (Get-ScheduledTask -TaskName $t -ErrorAction SilentlyContinue) {
      Unregister-ScheduledTask -TaskName $t -Confirm:$false
      Write-Output "REMOVED $t"
    }
  }
  Write-Output "removed $($managed.Count) managed task(s)"
  exit 0
}

Write-Output ''
Write-Output ("{0}: {1} new, {2} existing, {3} skipped" -f `
  $(if ($Install) { 'installed' } else { 'would install' }), $created, $updated, $skipped)
if (-not $Install) { Write-Output 'Nothing was changed. Re-run with -Install to apply.' }
